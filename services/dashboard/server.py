#!/usr/bin/env python3
"""novel-studio 创作进度看板（零依赖，stdlib only）。

数据源统一为仓库根目录 data/runs/ 下的书目工程：每个 <runs>/<书名>/ 内含
output/novel/{meta,chapters,reviews,summaries,logs}。本服务只读、不写任何数据。

由 `novel-studio service start` 拉起：python3 server.py --host H --port P。
Go 侧健康检查依赖 /api/health 与 /api/novels 均返回 2xx。
"""
from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import re
import shlex
import subprocess
import time
import urllib.parse
from collections import OrderedDict
from datetime import datetime
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Lock

ROOT = Path(__file__).resolve().parents[2]
RUNS_DIR = Path(os.environ.get("NOVEL_STUDIO_RUNS_DIR", ROOT / "data" / "runs"))
STATIC_DIR = Path(__file__).resolve().parent / "static"
SERVICE_SCRIPT = str(Path(__file__).resolve())


def _dashboard_version() -> str:
    """Content hash of the running dashboard code, so `service start` can detect a
    stale instance serving from an old checkout and replace it instead of leaving
    the user pinned to outdated code that still answers /api/novels."""
    h = hashlib.sha256()
    for p in (Path(__file__).resolve(), STATIC_DIR / "index.html"):
        try:
            h.update(p.read_bytes())
        except OSError:
            h.update(b"\0")
    return h.hexdigest()[:16]


SERVICE_VERSION = _dashboard_version()

ACTIVE_WINDOW_SECONDS = 300  # 运行事件 5 分钟内有更新即视为执行中
LOG_TAIL_LINES = 80
RUNTIME_EVENT_LINES = 120
BODY_CACHE_ENTRIES = 2048
# Keep only derived metadata, never chapter text. Check file identity on every
# read so rewrites/atomic promotion become visible without a polling TTL.
_body_cache: OrderedDict = OrderedDict()
_body_cache_lock = Lock()


# ---------- 数据读取（全部防御式，缺文件返回空） ----------

def read_json(path: Path):
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except (OSError, json.JSONDecodeError):
        return None


def read_jsonl_tail(path: Path, lines: int = 80) -> list[dict]:
    out = []
    try:
        with open(path, "rb") as f:
            f.seek(0, os.SEEK_END)
            f.seek(max(0, f.tell() - 256 * 1024))
            raw = f.read().decode("utf-8", errors="replace").splitlines()[-lines:]
    except OSError:
        return out
    for line in raw:
        try:
            item = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(item, dict):
            out.append(item)
    return out


def timestamp(value) -> float:
    if isinstance(value, (int, float)):
        return float(value)
    if not value:
        return 0.0
    try:
        return datetime.fromisoformat(str(value).replace("Z", "+00:00")).timestamp()
    except (TypeError, ValueError):
        return 0.0


def iso_time(ts: float) -> str:
    if not ts:
        return ""
    return datetime.fromtimestamp(ts).astimezone().isoformat(timespec="seconds")


def int_value(value, default: int = 0) -> int:
    try:
        return int(value)
    except (TypeError, ValueError):
        return default


def novel_dir(run: Path) -> Path:
    return run / "output" / "novel"


def is_run_dir(run: Path) -> bool:
    return run.is_dir() and (novel_dir(run) / "meta").is_dir()


def list_runs() -> list[Path]:
    if not RUNS_DIR.is_dir():
        return []
    return sorted((p for p in RUNS_DIR.iterdir() if is_run_dir(p)), key=lambda p: p.name)


def latest_mtime(*paths: Path) -> float:
    ts = 0.0
    for p in paths:
        try:
            ts = max(ts, p.stat().st_mtime)
        except OSError:
            pass
    return ts


def chapter_files(nd: Path) -> list[int]:
    out = []
    for p in (nd / "chapters").glob("*.md"):
        if not re.fullmatch(r"[0-9]{2,}", p.stem):
            continue
        ch = int(p.stem)
        # Go's %02d specifies a minimum width, not a two-digit chapter limit.
        if ch > 0 and p.stem == f"{ch:02d}" and p.is_file():
            out.append(ch)
    return sorted(out)


def review_state(nd: Path, ch: int):
    """返回 (verdict, gate, gate_warnings, rewrite)。
    verdict 来自 reviews/NN.json；gate 依据 NN_ai_gate.json 的 rule_violations。
    rewrite 汇总返工状态：
      · rounds     —— 评审/返工圈数（NN.history.jsonl 行数，无则据备份/brief 推 1）
      · rewritten  —— 是否已重写过（存在 chapters/NN.md.pre-rewrite.md 备份）
      · pending    —— 是否待返工（最新 verdict=rewrite，或有 brief 但未见备份）
    """
    verdict, gate, warns = "", "", 0
    rv = read_json(nd / "reviews" / f"{ch:02d}.json")
    if isinstance(rv, dict):
        verdict = str(rv.get("verdict", ""))
    g = read_json(nd / "reviews" / f"{ch:02d}_ai_gate.json")
    if isinstance(g, dict):
        violations = g.get("rule_violations") or []
        sev = [str(v.get("severity", "")).lower() for v in violations if isinstance(v, dict)]
        warns = sev.count("warning")
        gate = "fail" if "error" in sev else "pass"
    rewritten = (nd / "chapters" / f"{ch:02d}.md.pre-rewrite.md").exists()
    has_brief = (nd / "reviews" / f"{ch:02d}_rewrite_brief.md").exists()
    rounds = 0
    try:
        with open(nd / "reviews" / f"{ch:02d}.history.jsonl", encoding="utf-8") as f:
            rounds = sum(1 for line in f if line.strip())
    except OSError:
        pass
    if not rounds and (rewritten or has_brief):
        rounds = 1
    pending = verdict == "rewrite" or (has_brief and not rewritten and verdict != "accept")
    rewrite = {"rounds": rounds, "rewritten": rewritten, "pending": pending, "has_brief": has_brief}
    return verdict, gate, warns, rewrite


def chapter_title(nd: Path, ch: int) -> str:
    s = read_json(nd / "summaries" / f"{ch:02d}.json")
    if isinstance(s, dict):
        title = str(s.get("title", "")).strip()
        if title:
            return title
    return _body_metadata(nd / "chapters" / f"{ch:02d}.md")[1]


def _file_identity(info: os.stat_result) -> tuple:
    # ctime also detects a same-size rewrite whose mtime was deliberately kept;
    # inode/device distinguish an atomically promoted replacement.
    return (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns)


def _body_metadata(path: Path) -> tuple[int, str, str]:
    """Return rune count, title and SHA from one file version, with bounded reuse."""
    try:
        identity = _file_identity(path.stat())
        with _body_cache_lock:
            cached = _body_cache.get(path)
            if cached is not None and cached[0] == identity:
                _body_cache.move_to_end(path)
                return cached[1]
        raw = path.read_bytes()
        # Avoid retaining a snapshot while a producer is replacing/writing it.
        unchanged = _file_identity(path.stat()) == identity
    except OSError:
        with _body_cache_lock:
            _body_cache.pop(path, None)
        return 0, "", ""
    text = raw.decode("utf-8", errors="replace")
    title = next((re.sub(r"^#+\s*", "", line.strip())[:40]
                  for line in text.splitlines() if line.strip()), "")
    metadata = (len(text), title, hashlib.sha256(raw).hexdigest())
    if unchanged:
        with _body_cache_lock:
            _body_cache[path] = (identity, metadata)
            _body_cache.move_to_end(path)
            while len(_body_cache) > BODY_CACHE_ENTRIES:
                _body_cache.popitem(last=False)
    return metadata


def count_words(nd: Path, ch: int) -> int:
    # 与 internal/domain.WordCount 一致：按 Unicode rune 计数，包含换行与标点。
    return _body_metadata(nd / "chapters" / f"{ch:02d}.md")[0]


def file_sha256(path: Path) -> str:
    return _body_metadata(path)[2]


def line_count(path: Path) -> int:
    try:
        with open(path, encoding="utf-8") as f:
            return sum(1 for line in f if line.strip())
    except OSError:
        return 0


def rag_index_summary(nd: Path) -> dict:
    """读取轻量 RAG 摘要，避免轮询时反复反序列化近百 MB 的 chunk 正文。"""
    json_path = nd / "meta" / "rag" / "index_state.json"
    md_path = nd / "meta" / "rag" / "index_state.md"
    if not json_path.is_file() and not md_path.is_file():
        return {"ready": False, "chunks": 0, "provider": "", "model": "", "store": "",
                "collection": "", "updated_at": "", "retrievals": 0, "craft_recalls": 0}
    try:
        md = md_path.read_text(encoding="utf-8")[:12000]
    except OSError:
        md = ""
    try:
        with open(json_path, encoding="utf-8") as f:
            prefix = f.read(8192)
    except OSError:
        prefix = ""
    try:
        with open(nd / "meta" / "rag" / "health.json", encoding="utf-8") as f:
            health = json.load(f)
    except (OSError, json.JSONDecodeError):
        health = {}

    def md_value(label: str) -> str:
        match = re.search(rf"^-\s*{re.escape(label)}[：:]\s*(.+?)\s*$", md, re.M)
        return match.group(1).strip() if match else ""

    def json_string(key: str) -> str:
        match = re.search(rf'"{re.escape(key)}"\s*:\s*"([^"\\]*(?:\\.[^"\\]*)*)"', prefix)
        if not match:
            return ""
        try:
            return json.loads('"' + match.group(1) + '"')
        except json.JSONDecodeError:
            return match.group(1)

    raw_chunks = md_value("Chunk 数")
    chunks = int(raw_chunks.replace(",", "")) if raw_chunks.replace(",", "").isdigit() else 0
    facets = {}
    for key, value in re.findall(r"^-\s*([^：:\n]+)[：:]\s*(\d+)\s*$", md, re.M):
        if key not in ("Chunk 数", "Chunk hash 数"):
            facets[key.strip()] = int(value)
    return {
        "ready": True,
        "chunks": chunks,
        "provider": json_string("embedding_provider"),
        "model": json_string("embedding_model"),
        "store": json_string("vector_store"),
        "collection": json_string("collection") or md_value("Collection"),
        "updated_at": md_value("更新时间") or iso_time(latest_mtime(json_path, md_path)),
        "retrievals": line_count(nd / "meta" / "rag" / "retrieval_trace.jsonl"),
        "craft_recalls": line_count(nd / "meta" / "rag" / "craft_recall_log.jsonl"),
        "facets": facets,
        "health": health.get("healthy"),
        "health_checked_at": health.get("checked_at", ""),
        "health_issues": health.get("issues", {}),
        "fact_chunks": health.get("fact_chunks", 0),
        "vector_points": health.get("vector_points", 0),
        "pending_chunks": health.get("pending_chunks", 0),
    }


def log_tail(nd: Path, lines: int = LOG_TAIL_LINES) -> list[str]:
    path = nd / "logs" / "headless.log"
    try:
        with open(path, "rb") as f:
            f.seek(0, os.SEEK_END)
            size = f.tell()
            f.seek(max(0, size - 64 * 1024))
            chunk = f.read().decode("utf-8", errors="replace")
        return chunk.splitlines()[-lines:]
    except OSError:
        return []


CHAPTER_STEP_CHAIN = ["simulate", "plan", "draft", "check", "commit", "review", "rewrite", "deliver"]
PIPELINE_EXECUTION_MODES = {"foundation", "outline_all", "preplan", "project_all", "render"}
PIPELINE_STAGE_BY_MODE = {
    "foundation": "foundation",
    "outline_all": "outline-all",
    "preplan": "preplan",
    "project_all": "project-all",
    "render": "render",
}
PLANNING_PIPELINE_STAGES = {"foundation", "outline-all", "preplan", "project-all", "seal"}
RAG_PROCESS_CACHE_SECONDS = 1.0
_rag_process_cache_at = 0.0
_rag_process_cache: list[dict] = []


def checkpoint_tail(nd: Path, n: int = 60) -> list[dict]:
    return read_jsonl_tail(nd / "meta" / "checkpoints.jsonl", n)


def normalize_step(value: str) -> str:
    raw = str(value or "").strip().lower().replace("_", "-")
    if not raw:
        return ""
    if raw in ("rag", "rag-build", "build-rag"):
        return "rag"
    if ("simulate-chapter-world" in raw or "chapter-world-simulation" in raw or
            "world-simulation" in raw or raw == "project-all"):
        return "simulate"
    if ("plan-structure" in raw or "plan-details" in raw or "plan-chapter" in raw or
            raw in ("plan", "preplan", "outline-all", "foundation")):
        return "plan"
    if "rewrite" in raw:
        return "rewrite"
    if "deepseek-ai-judge" in raw or "review" in raw or "ai-gate" in raw or "aigc" in raw:
        return "review"
    if "check-consistency" in raw or "consistency" in raw or "content-lint" in raw or raw == "check":
        return "check"
    if "commit" in raw:
        return "commit"
    if "deliver" in raw:
        return "deliver"
    if "draft" in raw or raw in ("write", "render") or "draft-chapter" in raw:
        return "draft"
    return ""


def process_alive(pid: int) -> bool:
    """以 signal 0 只读探测本机进程；无权限探测也代表进程仍存在。"""
    try:
        os.kill(pid, 0)
        return True
    except PermissionError:
        return True
    except (ProcessLookupError, OSError):
        return False


def elapsed_seconds(value: str) -> int:
    """解析 ps 的 [[dd-]hh:]mm:ss ETIME；异常值只影响开始时间展示。"""
    raw = str(value or "").strip()
    match = re.fullmatch(r"(?:(\d+)-)?(?:(\d+):)?(\d+):(\d+)", raw)
    if not match:
        return 0
    days, hours, minutes, seconds = (int(value or 0) for value in match.groups())
    return days * 86400 + hours * 3600 + minutes * 60 + seconds


def cli_flag_value(tokens: list[str], name: str) -> str:
    for index, token in enumerate(tokens):
        if token == name and index + 1 < len(tokens):
            return tokens[index + 1]
        prefix = name + "="
        if token.startswith(prefix):
            return token[len(prefix):]
    return ""


def normalize_rag_output_dir(raw: str) -> Path | None:
    value = str(raw or "").strip()
    if not value:
        return None
    path = Path(value).expanduser()
    if not path.is_absolute():
        path = ROOT / path
    path = Path(os.path.abspath(path))
    if path.name == "novel" and path.parent.name == "output":
        return path
    candidate = path / "output" / "novel"
    if candidate.is_dir():
        return candidate
    return path


def scan_rag_processes() -> list[dict]:
    """只读扫描本机 build-rag 进程，并把显式 --dir 归一到小说输出目录。"""
    try:
        result = subprocess.run(
            ["ps", "-ww", "-axo", "pid=,etime=,command="],
            check=True,
            capture_output=True,
            text=True,
            timeout=2,
        )
    except (OSError, subprocess.SubprocessError):
        return []
    now = time.time()
    activities = []
    for line in result.stdout.splitlines():
        match = re.match(r"^\s*(\d+)\s+(\S+)\s+(.+)$", line)
        if not match:
            continue
        pid, elapsed, command = int(match.group(1)), match.group(2), match.group(3)
        try:
            tokens = shlex.split(command)
        except ValueError:
            continue
        if "--build-rag" not in tokens:
            continue
        build_index = tokens.index("--build-rag")
        if not any("novel-studio" in Path(token).name for token in tokens[:build_index]):
            continue
        output_dir = normalize_rag_output_dir(cli_flag_value(tokens, "--dir"))
        if output_dir is None:
            # 没有显式 --dir 时无法可靠归属到某本书，宁可不误报其他工程。
            continue
        age = elapsed_seconds(elapsed)
        activities.append({
            "kind": "rag",
            "stage": "rag-build",
            "process_id": pid,
            "output_dir": str(output_dir),
            "started_at": iso_time(now - age),
            "started_timestamp": now - age,
            "observed_timestamp": now,
        })
    return activities


def active_rag_processes() -> list[dict]:
    global _rag_process_cache_at, _rag_process_cache
    now = time.monotonic()
    if now - _rag_process_cache_at >= RAG_PROCESS_CACHE_SECONDS:
        _rag_process_cache = scan_rag_processes()
        _rag_process_cache_at = now
    return _rag_process_cache


def rag_process_state(nd: Path) -> dict:
    target = str(Path(os.path.abspath(nd)))
    matches = [item for item in active_rag_processes() if item.get("output_dir") == target]
    if not matches:
        return {"active": False, "kind": "", "stage": "", "process_id": 0,
                "started_at": "", "started_timestamp": 0.0, "observed_timestamp": 0.0}
    return max(matches, key=lambda item: item.get("started_timestamp") or 0)


def pipeline_execution_state(nd: Path, now: float | None = None) -> dict:
    """读取并防御式校验 Go RuntimeStore 写入的 pipeline execution lease。"""
    state = {
        "valid": False,
        "active": False,
        "mode": "",
        "stage": "",
        "target_chapter": 0,
        "owner": "",
        "process_id": 0,
        "process_alive": None,
        "acquired_at": "",
        "acquired_timestamp": 0.0,
        "expires_at": "",
        "expires_timestamp": 0.0,
    }
    raw = read_json(nd / "meta" / "runtime" / "pipeline_execution.json")
    if not isinstance(raw, dict):
        return state

    mode = str(raw.get("mode") or "").strip()
    owner = str(raw.get("owner") or "").strip()
    target = raw.get("target_chapter")
    acquired_at = str(raw.get("acquired_at") or "")
    expires_at = str(raw.get("expires_at") or "")
    acquired_ts = timestamp(acquired_at)
    expires_ts = timestamp(expires_at)
    valid_target = isinstance(target, int) and not isinstance(target, bool) and target > 0
    valid = (
        raw.get("version") == 1 and
        mode in PIPELINE_EXECUTION_MODES and
        valid_target and
        bool(owner) and
        expires_ts > 0 and
        (mode != "render" or bool(str(raw.get("plan_digest") or "").strip()))
    )

    pid = raw.get("process_id")
    if not isinstance(pid, int) or isinstance(pid, bool) or pid <= 0:
        match = re.search(r"-pid(\d+)", owner)
        pid = int(match.group(1)) if match else 0
    alive = process_alive(pid) if pid > 0 else None
    now = time.time() if now is None else now
    # RuntimeStore 对没有 PID 的 legacy/custom owner 仍按租约保护；这里保持同一语义。
    active = valid and expires_ts > now and alive is not False
    state.update({
        "valid": valid,
        "active": active,
        "mode": mode if valid else "",
        "stage": PIPELINE_STAGE_BY_MODE.get(mode, "") if valid else "",
        "target_chapter": target if valid_target else 0,
        "owner": owner if valid else "",
        "process_id": pid,
        "process_alive": alive,
        "acquired_at": acquired_at if valid else "",
        "acquired_timestamp": acquired_ts if valid else 0.0,
        "expires_at": expires_at if valid else "",
        "expires_timestamp": expires_ts if valid else 0.0,
    })
    return state


def next_pipeline_stage(pipe: dict) -> str:
    stages = pipe.get("stages") if isinstance(pipe, dict) else []
    completed = pipe.get("completed") if isinstance(pipe, dict) else []
    if not isinstance(stages, list):
        return ""
    completed_set = {str(value) for value in completed} if isinstance(completed, list) else set()
    for value in stages:
        stage = str(value or "").strip()
        if stage and stage not in completed_set:
            return stage
    return ""


def runtime_events(nd: Path, n: int = RUNTIME_EVENT_LINES) -> list[dict]:
    events = []
    for item in read_jsonl_tail(nd / "meta" / "runtime" / "queue.jsonl", n):
        payload = item.get("payload") if isinstance(item.get("payload"), dict) else {}
        category = str(item.get("category") or payload.get("Category") or "")
        level = str(payload.get("Level") or item.get("level") or "").lower()
        failed = bool(payload.get("Failed")) or category.upper() == "ERROR" or level == "error"
        detail = str(payload.get("Detail") or item.get("detail") or "")
        summary = str(item.get("summary") or payload.get("Summary") or "")
        chapter_match = re.search(r"第\s*(\d+)\s*章|\bch(?:apter)?[-_ ]?(\d+)\b", summary + " " + detail, re.I)
        event_chapter = int(chapter_match.group(1) or chapter_match.group(2)) if chapter_match else None
        event_time = str(item.get("time") or payload.get("Time") or "")
        finished_at = str(item.get("finished_at") or payload.get("FinishedAt") or "")
        event_ts = timestamp(event_time)
        finished_ts = timestamp(finished_at)
        events.append({
            "seq": item.get("seq"),
            "time": event_time,
            "timestamp": event_ts,
            "finished_at": finished_at,
            "activity_timestamp": max(event_ts, finished_ts),
            "category": category,
            "agent": str(item.get("agent") or payload.get("Agent") or ""),
            "summary": clip(summary, 260),
            "detail": clip(detail, 480),
            "failed": failed,
            "step": normalize_step(summary or detail),
            "chapter": event_chapter,
        })
    # Direct pipeline stages need not construct a Host, so queue.jsonl can be
    # empty even when a durable stage failure exists. Read the numeric/status
    # ledger too; never infer success or failure from stale terminal prose.
    for item in read_jsonl_tail(nd / "meta" / "pipeline_timings.jsonl", n):
        if not isinstance(item, dict) or item.get("schema") != "pipeline-timing.v1" or item.get("scope") != "stage":
            continue
        stage, status = item.get("stage"), item.get("status")
        if not isinstance(stage, str) or not stage or status not in (
            "ok", "error", "verification_error", "checkpoint_error", "watchdog_progress_error",
        ):
            continue
        finished_at = item.get("finished_at")
        finished_ts = timestamp(finished_at)
        if not finished_ts or not math.isfinite(finished_ts):
            continue
        failed = status != "ok"
        events.append({
            "seq": None, "time": finished_at, "timestamp": finished_ts,
            "finished_at": finished_at, "activity_timestamp": finished_ts,
            "category": "PIPELINE", "agent": "pipeline", "stage": stage,
            "summary": clip(f"{stage} {'失败' if failed else '完成'}", 260),
            "detail": clip(str(item.get("error") or ""), 480),
            "failed": failed, "step": normalize_step(stage), "chapter": None,
        })
    events.sort(key=lambda e: (e.get("activity_timestamp") or 0, int_value(e.get("seq"))))
    return events[-n:]


def runtime_state(nd: Path, prog: dict) -> dict:
    events = runtime_events(nd)
    run = read_json(nd / "meta" / "run.json") or {}
    pipe = read_json(nd / "meta" / "pipeline.json") or {}
    execution = pipeline_execution_state(nd)
    rag_execution = rag_process_state(nd)

    def event_key(event: dict) -> tuple[float, int]:
        seq = event.get("seq")
        return event.get("activity_timestamp") or 0, seq if isinstance(seq, int) else 0

    last = max(events, key=event_key) if events else {}
    failures = [event for event in events if event.get("failed")]
    last_error = max(failures, key=event_key) if failures else None
    event_ts = max((e.get("activity_timestamp") or 0 for e in events), default=0)
    durable_ts = max(
        timestamp(pipe.get("updated_at")),
        latest_mtime(
            nd / "meta" / "checkpoints.jsonl",
            nd / "meta" / "progress.json",
            nd / "meta" / "pipeline.json",
        ),
    )
    log_ts = latest_mtime(nd / "logs" / "headless.log")
    execution_ts = (execution.get("acquired_timestamp") or 0) if execution.get("active") else 0
    rag_ts = (rag_execution.get("observed_timestamp") or 0) if rag_execution.get("active") else 0
    rag_started_ts = (rag_execution.get("started_timestamp") or 0) if rag_execution.get("active") else 0
    updated = max(event_ts, durable_ts, log_ts, execution_ts, rag_ts)
    age = max(0.0, time.time() - updated) if updated else None
    pending = prog.get("pending_rewrites") or []
    successful_event_ts = max(
        (event.get("activity_timestamp") or 0 for event in events if not event.get("failed")),
        default=0,
    )
    recovery_ts = max(durable_ts, successful_event_ts, execution_ts, rag_ts)
    error_ts = (last_error or {}).get("activity_timestamp") or 0
    current_error = bool(last_error) and error_ts >= recovery_ts
    if last_error and last_error.get("category") == "PIPELINE":
        # Touching progress/metadata is not evidence that a failed stage was
        # repaired. Require a subsequent successful event or a live retry.
        current_error = error_ts >= max(successful_event_ts, execution_ts, rag_ts)
    last_event_current = bool(last) and (last.get("activity_timestamp") or 0) >= max(
        durable_ts, execution_ts, rag_started_ts,
    )
    if execution.get("active"):
        status = "running"
    elif rag_execution.get("active"):
        status = "running"
    elif prog.get("phase") == "complete":
        status = "complete"
    elif current_error:
        status = "error"
    elif updated and age is not None and age < ACTIVE_WINDOW_SECONDS:
        status = "running"
    elif pending or prog.get("flow") == "rewriting":
        status = "attention"
    else:
        status = "idle"
    pipeline_stage = execution.get("stage") if execution.get("active") else ""
    if not pipeline_stage and rag_execution.get("active"):
        pipeline_stage = rag_execution.get("stage") or "rag-build"
    if not pipeline_stage and current_error:
        pipeline_stage = (last_error or {}).get("stage") or ""
    if status == "running" and not pipeline_stage and not last_event_current:
        pipeline_stage = next_pipeline_stage(pipe)
    recent = events[-16:]
    errors = failures[-8:]
    return {
        "status": status,
        "active": status == "running",
        "updated_at": updated,
        "updated_iso": iso_time(updated),
        "age_seconds": round(age, 1) if age is not None else None,
        "last_event": last,
        "last_event_current": last_event_current,
        "last_error": last_error,
        "last_error_recovered": bool(last_error) and not current_error,
        "recent_events": recent,
        "recent_errors": errors,
        "current_stage": pipeline_stage,
        "execution": execution,
        "activity": rag_execution if rag_execution.get("active") else {},
        "provider": run.get("provider") or "",
        "model": run.get("model") or "",
        "planning_tier": run.get("planning_tier") or "",
        "started_at": run.get("started_at") or "",
    }


def working_state(nd: Path, prog: dict, runtime=None) -> dict:
    """当前执行进展：优先返工/在途章，并将新 pipeline 细步骤归一到阶段链。"""
    entries = checkpoint_tail(nd)
    chapter_steps: dict[int, str] = {}
    last = entries[-1] if entries else {}
    for e in entries:
        scope = e.get("scope") or {}
        if scope.get("kind") == "chapter" and isinstance(scope.get("chapter"), int):
            normalized = normalize_step(e.get("step") or "")
            if normalized:
                chapter_steps[scope["chapter"]] = normalized
    pending = prog.get("pending_rewrites") or []
    if isinstance(pending, int):
        pending = [pending]
    runtime = runtime or runtime_state(nd, prog)
    execution = runtime.get("execution") or {}
    activity = runtime.get("activity") or {}
    cur = prog.get("in_progress_chapter") or 0
    if not cur and prog.get("flow") == "rewriting" and pending:
        cur = pending[0]
    if not cur:
        cur = prog.get("current_chapter") or 0
    if not cur and chapter_steps:
        cur = max(chapter_steps)
    if not cur and "current_chapter" not in prog:
        cur = (len(completed_chapters(prog.get("completed_chapters"))) or 0) + 1
    if execution.get("active") and execution.get("target_chapter"):
        cur = execution["target_chapter"]
    projection = runtime.get("planning_projection") or {}
    if projection.get("state") == "running" and execution.get("active") and execution.get("mode") == "project_all":
        cur = projection["chapter"]
    live = runtime.get("last_event") or {}
    if live and not runtime.get("last_event_current"):
        live = {}
    if live and not runtime.get("active") and live.get("chapter") != cur:
        live = {}
    pipeline_stage = runtime.get("current_stage") or ""
    step = live.get("step") or normalize_step(pipeline_stage) or chapter_steps.get(cur, "")
    if not step:
        # checkpoint 还没到章级：看 drafts 目录里的最新事实
        if (nd / "drafts" / f"{cur:02d}.draft.md").exists() or (nd / "drafts" / f"{cur:02d}.md").exists():
            step = "draft"
        elif list((nd / "drafts").glob(f"{cur:02d}.plan*")) if (nd / "drafts").is_dir() else []:
            step = "plan"
    last_scope = last.get("scope") or {}
    planning_execution = (
        execution.get("active") and execution.get("mode") != "render" or
        pipeline_stage in PLANNING_PIPELINE_STAGES and prog.get("phase") != "writing"
    )
    if activity.get("kind") == "rag":
        pipeline_summary = "RAG 重建"
    elif planning_execution:
        pipeline_summary = pipeline_stage
    else:
        pipeline_summary = f"{pipeline_stage}（第 {cur} 章）" if pipeline_stage and cur else pipeline_stage
    last_step = live.get("summary") or pipeline_summary or str(last.get("step") or "")
    last_at = (live.get("time") or execution.get("acquired_at") or
               (iso_time(runtime.get("updated_at") or 0) if pipeline_stage else "") or
               str(last.get("occurred_at") or ""))
    if activity.get("kind") == "rag":
        mode = "rag"
    elif planning_execution:
        mode = "planning"
    else:
        mode = "rewrite" if prog.get("flow") == "rewriting" or cur in pending else "write"
    display_chapter = 0 if planning_execution or activity.get("kind") == "rag" else cur
    return {
        "chapter": display_chapter,
        "target_chapter": cur if planning_execution else display_chapter,
        "cycle": (runtime.get("planning_progress") or {}).get("cycle", 0) if planning_execution else 0,
        "round": (runtime.get("planning_progress") or {}).get("round", 0) if planning_execution else 0,
        "next_chapter": prog.get("current_chapter") or 0,
        "mode": mode,
        "step": step,
        "chain": CHAPTER_STEP_CHAIN,
        "chain_pos": CHAPTER_STEP_CHAIN.index(step) if step in CHAPTER_STEP_CHAIN else -1,
        "last_step": last_step,
        "last_kind": "runtime" if live else "rag" if activity.get("kind") == "rag" else "pipeline" if pipeline_stage else str(last_scope.get("kind") or ""),
        "last_chapter": None if activity.get("kind") == "rag" or planning_execution else cur if pipeline_stage else last_scope.get("chapter"),
        "last_at": last_at,
        "last_agent": live.get("agent") or ("rag" if activity.get("kind") == "rag" else "pipeline" if pipeline_stage else ""),
        "last_failed": bool(live.get("failed")),
        "last_error": runtime.get("last_error"),
    }


def completed_chapters(raw) -> list[int]:
    if isinstance(raw, int):
        return list(range(1, max(0, raw) + 1))
    if not isinstance(raw, list):
        return []
    return sorted({n for n in raw if isinstance(n, int) and n > 0})


def current_arc_outline(nd: Path, prog: dict) -> dict:
    volumes = [value for value in (read_json(nd / "layered_outline.json") or []) if isinstance(value, dict)]
    if not volumes:
        return {"volume": 0, "arc": 0, "first_chapter": 0, "last_chapter": 0, "expected_chapters": 0}
    volume_index = int_value(prog.get("current_volume"))
    expanded_volumes = [value for value in volumes if isinstance(value.get("arcs"), list) and value.get("arcs")]
    volume = next((value for value in expanded_volumes if value.get("index") == volume_index),
                  expanded_volumes[0] if expanded_volumes else volumes[0])
    arcs = [value for value in (volume.get("arcs") or []) if isinstance(value, dict)]
    if not arcs:
        return {"volume": volume.get("index") or 0, "arc": 0,
                "first_chapter": 0, "last_chapter": 0, "expected_chapters": 0}
    arc_index = int_value(prog.get("current_arc"))
    arc = next((value for value in arcs if value.get("index") == arc_index), arcs[0])
    chapter_cursor = 0
    derived_first, derived_last = 0, 0
    for candidate_volume in volumes:
        for candidate_arc in (candidate_volume.get("arcs") or []):
            if not isinstance(candidate_arc, dict):
                continue
            candidate_chapters = candidate_arc.get("chapters") or []
            span = len(candidate_chapters) if isinstance(candidate_chapters, list) else 0
            span = span or int_value(candidate_arc.get("estimated_chapters"))
            if candidate_volume is volume and candidate_arc is arc:
                derived_first = chapter_cursor + 1 if span else 0
                derived_last = chapter_cursor + span if span else 0
            chapter_cursor += span
    chapter_numbers = []
    chapters = arc.get("chapters") or []
    if isinstance(chapters, list):
        for value in chapters:
            chapter = value.get("chapter") if isinstance(value, dict) else value
            if isinstance(chapter, int) and not isinstance(chapter, bool) and chapter > 0:
                chapter_numbers.append(chapter)
    expected = len(chapter_numbers) or int_value(arc.get("estimated_chapters"))
    expected = expected or max(0, derived_last - derived_first + 1)
    return {
        "volume": volume.get("index") or 0,
        "arc": arc.get("index") or 0,
        "first_chapter": min(chapter_numbers) if chapter_numbers else derived_first,
        "last_chapter": max(chapter_numbers) if chapter_numbers else derived_last,
        "expected_chapters": expected,
    }


def formal_planning_summary(nd: Path, prog: dict, runtime: dict) -> dict:
    """把冻结分层大纲与 project-all/seal 的正式弧计划证据分开计数。"""
    arc = current_arc_outline(nd, prog)
    candidates = []
    roots = (
        (nd / "meta" / "planning" / "v2" / ".building", "building"),
        (nd / "meta" / "planning" / "v2" / "generations", "sealed"),
    )
    for root, location_state in roots:
        for generation_path in root.glob("*/generation.json"):
            generation = read_json(generation_path)
            if not isinstance(generation, dict):
                continue
            if str(generation.get("status") or "") != location_state:
                continue
            generation_id = str(generation.get("generation_id") or "")
            generation_dir = generation_path.parent
            if location_state == "sealed" and not (generation_dir / "seal_receipt.json").is_file():
                continue
            lifecycle = nd / "meta" / "planning" / "v2" / "lifecycle"
            if (list((lifecycle / "archives" / generation_id).glob("*.json")) or
                    list((lifecycle / "invalidations" / generation_id).glob("*.json"))):
                continue
            first = int_value(generation.get("first_projected_chapter"))
            last = int_value(generation.get("last_projected_chapter"))
            if (arc["first_chapter"] and arc["last_chapter"] and
                    (last < arc["first_chapter"] or first > arc["last_chapter"])):
                continue
            bundle_count = len(list((generation_dir / "chapters").glob("*.bundle.json")))
            projected = max(bundle_count, int_value(generation.get("projected_chapter_count")))
            expected = int_value(generation.get("expected_chapter_count"))
            if location_state == "sealed":
                projected = max(projected, expected)
            updated = latest_mtime(generation_path, generation_dir / "seal_receipt.json")
            try:
                updated = max(updated, max(
                    (path.stat().st_mtime for path in (generation_dir / "chapters").glob("*.bundle.json")),
                    default=0.0,
                ))
            except OSError:
                pass
            candidates.append({
                "state": location_state,
                "generation_id": generation_id,
                "planned_chapters": projected,
                "expected_chapters": expected,
                "first_chapter": first,
                "last_chapter": last,
                "updated_at": updated,
            })

    selected = max(candidates, key=lambda value: value["updated_at"]) if candidates else None
    stage = str(runtime.get("current_stage") or "")
    if selected:
        state = selected["state"]
        planned = selected["planned_chapters"]
        expected = selected["expected_chapters"] or arc["expected_chapters"]
        first = selected["first_chapter"] or arc["first_chapter"]
        last = selected["last_chapter"] or arc["last_chapter"]
        generation_id = selected["generation_id"]
        updated = selected["updated_at"]
    else:
        state = "building" if stage == "project-all" else "sealing" if stage == "seal" else "not_started"
        planned = 0
        expected = arc["expected_chapters"]
        first, last = arc["first_chapter"], arc["last_chapter"]
        generation_id = ""
        updated = 0.0
    if state == "building" and stage == "seal":
        state = "sealing"
    labels = {
        "not_started": "未开始",
        "building": "推演规划中",
        "sealing": "封存中",
        "sealed": "已封存",
    }
    percent = round(min(100, planned / expected * 100), 2) if expected else 0
    return {
        "state": state,
        "label": labels[state],
        "generation_id": generation_id,
        "planned_chapters": planned,
        "expected_chapters": expected,
        "first_chapter": first,
        "last_chapter": last,
        "sealed": state == "sealed",
        "percent": percent,
        "updated_at": updated,
        "volume": arc["volume"],
        "arc": arc["arc"],
    }


def artifact_inventory(nd: Path) -> dict:
    specs = [
        ("世界基线", [nd / "book_world.json"]),
        ("世界法则", [nd / "world_rules.json"]),
        ("人物档案", [nd / "characters.json"]),
        ("分层大纲", [nd / "layered_outline.json"]),
        ("章节细纲", [nd / "outline.json"]),
        ("写作规则", [nd / "meta" / "user_rules.json"]),
        ("项目进度", [nd / "meta" / "project_progress.json"]),
        ("人物连续性", [nd / "meta" / "character_continuity.json"]),
        ("人物关系", [nd / "relationship_state.json", nd / "relationship_state.initial.json"]),
        ("伏笔台账", [nd / "foreshadow_ledger.json", nd / "foreshadow_ledger.initial.json"]),
        ("资源台账", [nd / "meta" / "resource_ledger.json", nd / "meta" / "initial_resource_ledger.json"]),
        ("离屏日程", [nd / "meta" / "offscreen_agenda.json"]),
        ("离屏世界基线 tick", [nd / "meta" / "world_tick.json"]),
        ("RAG 索引", [nd / "meta" / "rag" / "index_state.json"]),
    ]
    items = []
    for label, candidates in specs:
        path = next((p for p in candidates if p.is_file()), candidates[0])
        exists = path.is_file()
        try:
            stat = path.stat()
            updated = stat.st_mtime
            size = stat.st_size
        except OSError:
            updated, size = 0.0, 0
        items.append({
            "label": label,
            "path": str(path.relative_to(nd)),
            "exists": exists,
            "updated_at": updated,
            "size": size,
        })
    rag_info = rag_index_summary(nd)
    sediment = {
        "character_stage_files": len(list((nd / "meta" / "character_stage").glob("*.json"))),
        "side_journey_files": len(list((nd / "meta" / "side_character_journeys").glob("*.json"))),
        "world_delta_files": len(list((nd / "meta" / "chapter_world_deltas").glob("*.json"))),
        "world_events": line_count(nd / "meta" / "world_events.jsonl"),
        "delivery_snapshots": len(list((nd / "meta" / "delivery_snapshots").glob("*.json"))),
    }
    return {
        "ready": sum(1 for item in items if item["exists"]),
        "total": len(items),
        "items": items,
        "rag": rag_info,
        "sediment": sediment,
    }


def progress_health(nd: Path, prog: dict, files: list[int], actual_counts: dict[int, int]) -> dict:
    issues = []
    reported = completed_chapters(prog.get("completed_chapters"))
    reported_set, disk_set = set(reported), set(files)
    missing_files = sorted(reported_set - disk_set)
    untracked_files = sorted(disk_set - reported_set)
    if missing_files:
        issues.append({"level": "error", "code": "completed_missing_body",
                       "message": "进度台账标记完成，但正文缺失：" + "、".join(f"第 {n} 章" for n in missing_files)})
    if untracked_files:
        issues.append({"level": "warning", "code": "body_not_committed",
                       "message": "正文已落盘但未进入完成台账：" + "、".join(f"第 {n} 章" for n in untracked_files)})

    actual_total = sum(actual_counts.values())
    reported_total = int(prog.get("total_word_count") or 0)
    if files and actual_total != reported_total:
        issues.append({"level": "warning", "code": "word_total_mismatch",
                       "message": f"正文实算 {actual_total} 字，进度台账为 {reported_total} 字，相差 {actual_total - reported_total:+d}。"})
    reported_counts = prog.get("chapter_word_counts") or {}
    mismatched = []
    for ch, actual in actual_counts.items():
        reported_ch = reported_counts.get(str(ch), reported_counts.get(ch))
        if reported_ch is not None and int(reported_ch or 0) != actual:
            mismatched.append(f"第 {ch} 章 {actual}/{reported_ch}")
    if mismatched:
        issues.append({"level": "warning", "code": "chapter_word_mismatch",
                       "message": "章节字数实算/台账不一致：" + "；".join(mismatched[:8])})

    pending = prog.get("pending_rewrites") or []
    if isinstance(pending, int):
        pending = [pending]
    pending_missing = sorted(n for n in pending if isinstance(n, int) and n not in disk_set)
    if pending_missing:
        issues.append({"level": "error", "code": "rewrite_body_missing",
                       "message": "待返工章节没有可用正文：" + "、".join(f"第 {n} 章" for n in pending_missing)})

    total_chapters = int(prog.get("total_chapters") or 0)
    if total_chapters and max(files + reported + [0]) > total_chapters:
        issues.append({"level": "error", "code": "chapter_overflow",
                       "message": "已出现超出全书目标章数的章节。"})

    stale_reviews = []
    for ch in files:
        body_hash = file_sha256(nd / "chapters" / f"{ch:02d}.md")
        for suffix in (".json", "_ai_gate.json", "_deepseek_ai_judge.json"):
            review = read_json(nd / "reviews" / f"{ch:02d}{suffix}") or {}
            review_hash = str(review.get("body_sha256") or "")
            if review_hash and body_hash and review_hash != body_hash:
                stale_reviews.append(f"第 {ch} 章 {suffix.lstrip('_').replace('.json', '')}")
    if stale_reviews:
        issues.append({"level": "warning", "code": "stale_review",
                       "message": "评审对应的正文版本已过期：" + "、".join(stale_reviews[:8])})

    status = "error" if any(i["level"] == "error" for i in issues) else "warning" if issues else "ok"
    sources = []
    for label, path in (
        ("进度台账", nd / "meta" / "progress.json"),
        ("正文目录", nd / "chapters"),
        ("评审目录", nd / "reviews"),
        ("检查点", nd / "meta" / "checkpoints.jsonl"),
        ("运行队列", nd / "meta" / "runtime" / "queue.jsonl"),
    ):
        sources.append({"label": label, "updated_at": latest_mtime(path), "path": str(path.relative_to(nd))})
    verified = sorted(reported_set & disk_set)
    return {
        "status": status,
        "issues": issues,
        "verified_completed": len(verified),
        "reported_completed": len(reported),
        "actual_words": actual_total,
        "reported_words": reported_total,
        "sources": sources,
        "scanned_at": time.time(),
    }


def summarize_run(run: Path) -> dict:
    nd = novel_dir(run)
    prog = read_json(nd / "meta" / "progress.json") or {}
    pipe = read_json(nd / "meta" / "pipeline.json") or {}
    usage = read_json(nd / "meta" / "usage.json") or {}
    overall = usage.get("overall") or {}
    outline = read_json(nd / "outline.json") or []

    files = chapter_files(nd)
    actual_counts = {ch: count_words(nd, ch) for ch in files}
    runtime = runtime_state(nd, prog)
    formal_planning = formal_planning_summary(nd, prog, runtime)
    workspace, projection = current_character_planning_workspace(nd, formal_planning)
    if projection is not None:
        runtime["planning_projection"] = projection
    planning_progress = current_planning_watchdog(nd, pipe, runtime, workspace, projection)
    if planning_progress is not None:
        runtime["planning_progress"] = planning_progress
    health = progress_health(nd, prog, files, actual_counts)
    assets = artifact_inventory(nd)
    reviews_dir = nd / "reviews"
    accepted = 0
    reviewed = 0
    gate_pass = 0
    gated = 0
    rewritten_count = 0
    rewrite_pending = 0
    if reviews_dir.is_dir():
        for ch in files:
            verdict, gate, _, rw = review_state(nd, ch)
            if verdict:
                reviewed += 1
            if verdict == "accept":
                accepted += 1
            if gate:
                gated += 1
            if gate == "pass":
                gate_pass += 1
            if rw["rewritten"]:
                rewritten_count += 1
            if rw["pending"]:
                rewrite_pending += 1

    pending_rewrites = prog.get("pending_rewrites") or []
    if isinstance(pending_rewrites, int):
        pending_rewrites = [pending_rewrites]
    word_rules = ((read_json(nd / "meta" / "user_rules.json") or {}).get("structured") or {}).get("chapter_words") or {}
    completed_count = health["verified_completed"]
    total_chapters = int(prog.get("total_chapters") or 0)
    planned_count = len(outline) if isinstance(outline, list) else 0
    words_total = health["actual_words"] if files else health["reported_words"]
    return {
        "name": prog.get("novel_name") or run.name,
        "dir": run.name,
        "archived": run.name.startswith(("废弃-", "归档-", "archive-", "_archive")),
        "phase": prog.get("phase") or "unknown",
        "flow": prog.get("flow") or "",
        "generation_id": prog.get("generation_id") or "",
        "generation_mode": prog.get("generation_mode") or "",
        "volume": prog.get("current_volume") or 0,
        "arc": prog.get("current_arc") or 0,
        "current_chapter": prog.get("current_chapter") or 0,
        "in_progress_chapter": prog.get("in_progress_chapter"),
        "chapters_total": total_chapters,
        "chapters_outlined": planned_count,
        "chapters_planned": planned_count,
        "chapters_formally_planned": formal_planning["planned_chapters"],
        "formal_planning": formal_planning,
        "chapters_completed": completed_count,
        "chapters_reported_completed": health["reported_completed"],
        "chapters_on_disk": len(files),
        "working": working_state(nd, prog, runtime),
        "runtime": runtime,
        "health": health,
        "words_total": words_total,
        "words_reported": health["reported_words"],
        "chapter_words": {str(k): v for k, v in sorted(actual_counts.items())},
        "chapter_word_target": {"min": word_rules.get("min"), "max": word_rules.get("max")},
        "average_chapter_words": round(words_total / len(files)) if files else 0,
        "cost_usd": round(float(overall.get("cost_usd") or 0.0), 4),
        "saved_usd": round(float(overall.get("saved_usd") or 0.0), 4),
        "tokens_in": overall.get("input") or 0,
        "tokens_out": overall.get("output") or 0,
        "pipeline_stages": pipe.get("stages") or [],
        "pipeline_completed": pipe.get("completed") or [],
        "pipeline_updated_at": pipe.get("updated_at") or "",
        "reviews_accepted": accepted,
        "reviews_total": reviewed,
        "gate_passed": gate_pass,
        "gate_total": gated,
        "rewritten_count": rewritten_count,
        "rewrite_pending": max(rewrite_pending, len(pending_rewrites)),
        "pending_rewrites": pending_rewrites,
        "rewrite_reason": clip(prog.get("rewrite_reason"), 260),
        "progress_percent": round(completed_count / total_chapters * 100, 2) if total_chapters else 0,
        "outline_percent": round(planned_count / total_chapters * 100, 2) if total_chapters else 0,
        "outline_frozen": bool(total_chapters and planned_count == total_chapters),
        "plan_percent": round(planned_count / total_chapters * 100, 2) if total_chapters else 0,
        "formal_plan_percent": formal_planning["percent"],
        "quality_percent": round(accepted / len(files) * 100, 2) if files else 0,
        "assets_ready": assets["ready"],
        "assets_total": assets["total"],
        "rag_chunks": assets["rag"]["chunks"],
        "updated_at": runtime["updated_at"],
        "active": runtime["active"],
    }


def empty_character_usage() -> dict:
    return {"cost_usd": 0.0, "tokens_in": 0, "tokens_out": 0, "usage_calls": 0,
            "attempts": 0, "unpriced_calls": 0,
            "cost_sources": {"reported": 0, "estimated": 0, "unknown": 0},
            "usage_statuses": {"success": 0, "failed": 0, "canceled": 0, "unknown": 0},
            "_usage_models": set()}


def character_usage_cost(item: dict) -> tuple[float, str]:
    """Missing legacy prices are unknown; an explicit source can prove zero.

    An unknown aggregate may carry the priced subtotal of some responses.
    Preserve that amount without claiming its entire usage was priced.
    """
    source = str(item.get("cost_source") or "").strip().lower()
    raw_cost = item.get("cost_usd")
    try:
        amount = float(raw_cost) if raw_cost is not None and not isinstance(raw_cost, bool) else None
    except (TypeError, ValueError):
        amount = None
    valid = amount is not None and math.isfinite(amount) and amount >= 0
    if not valid:
        # cost_usd uses omitempty in the Go protocol, including reported zero.
        if "cost_usd" not in item and source in ("reported", "estimated"):
            return 0.0, source
        return 0.0, "unknown"
    if not source:
        return amount, "reported"
    return amount, source if source in ("reported", "estimated") else "unknown"


def accumulate_character_usage(summary: dict, item: dict):
    amount, source = character_usage_cost(item)
    unpriced = max(0, int_value(item.get("unpriced_calls")))
    if unpriced:
        source = "unknown"
    elif source == "unknown":
        # Legacy records identify an unpriced aggregate, but not its number
        # of underlying calls. Count at least one rather than assuming free.
        unpriced = 1
    summary["cost_usd"] += amount
    summary["tokens_in"] += max(0, int_value(item.get("input")))
    summary["tokens_out"] += max(0, int_value(item.get("output")))
    summary["usage_calls"] += 1
    summary["attempts"] += max(1, int_value(item.get("attempts")))
    summary["unpriced_calls"] += unpriced
    summary["cost_sources"][source] += 1
    status = item.get("status")
    summary["usage_statuses"][status if status in ("success", "failed", "canceled") else "unknown"] += 1
    models = item.get("models") if isinstance(item.get("models"), list) else []
    for identity in [item, *models]:
        if isinstance(identity, str):
            provider, separator, model = identity.partition("/")
            if not separator:
                provider, model = "", provider
            identity = {"provider": provider.strip(), "model": model.strip()}
        if not isinstance(identity, dict):
            continue
        provider = clip(identity.get("provider"), 100) if isinstance(identity.get("provider"), str) else ""
        model = clip(identity.get("model"), 100) if isinstance(identity.get("model"), str) else ""
        if model == "mixed" and models:
            continue
        if provider or model:
            summary["_usage_models"].add((provider, model))


def finalize_character_usage(summary: dict) -> dict:
    sources = summary["cost_sources"]
    summary["cost_usd"] = round(summary["cost_usd"], 8)
    summary["cost_complete"] = sources["unknown"] == 0
    summary["cost_source"] = "unknown" if sources["unknown"] else "estimated" if sources["estimated"] else "reported"
    summary["models"] = [{"provider": provider, "model": model}
                         for provider, model in sorted(summary.pop("_usage_models"))]
    return summary


def contained_regular_path(root: Path, path: Path) -> bool:
    """Only follow ordinary descendants of this explicit, already-bound root."""
    try:
        relative = path.relative_to(root)
        if root.is_symlink():
            return False
        current = root
        for part in relative.parts:
            if part in ("", ".", ".."):
                return False
            current = current / part
            if current.is_symlink():
                return False
        return (path.is_file() or path.is_dir()) and path.resolve().is_relative_to(root.resolve())
    except (OSError, ValueError, RuntimeError):
        return False


def current_character_planning_workspace(nd: Path, formal: dict) -> tuple[Path | None, dict | None]:
    """Resolve one current building generation; never search historical workspaces."""
    generation_id = str(formal.get("generation_id") or "")
    if formal.get("state") != "building" or not re.fullmatch(r"pg2_[A-Za-z0-9_-]+", generation_id):
        return None, None
    run = nd.parent.parent
    workspace = run / ".project-all" / generation_id / "output" / "novel"
    generation_dir = nd / "meta" / "planning" / "v2" / ".building" / generation_id

    def bound_json(path):
        return read_json(path) if contained_regular_path(run, path) else None

    generation = bound_json(generation_dir / "generation.json") or {}
    snapshot = bound_json(generation_dir / "source_snapshot.json") or {}
    cursor = bound_json(nd / "meta" / "planning" / "v2" / "projection_cursor.json") or {}
    manifest = bound_json(workspace / "meta" / "project_all_workspace_manifest.json") or {}
    if not all(isinstance(item, dict) for item in (generation, snapshot, cursor, manifest)):
        return None, None
    lifecycle = nd / "meta" / "planning" / "v2" / "lifecycle"
    if any((lifecycle / kind / generation_id).exists() for kind in ("archives", "invalidations")):
        return None, None
    if (generation.get("status") != "building" or generation.get("generation_id") != generation_id or
            cursor.get("generation_id") != generation_id or snapshot.get("generation_id") != generation_id or
            manifest.get("version") != "project-all-workspace.v3" or
            manifest.get("generation_id") != generation_id or manifest.get("isolated_writes") is not True or
            manifest.get("source_output") != str(nd) or manifest.get("workspace") != str(workspace) or
            manifest.get("base_chapter") != generation.get("base_canon_chapter") or
            manifest.get("base_chapter") != snapshot.get("base_canon_chapter")):
        return None, None
    for key in ("foundation_snapshot_root", "rag_snapshot_root"):
        digest = snapshot.get(key)
        if not isinstance(digest, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", digest) or manifest.get(key) != digest:
            return None, None
    if not contained_regular_path(run, workspace):
        return None, None
    base = generation.get("base_canon_chapter")
    first, last = int_value(generation.get("first_projected_chapter")), int_value(generation.get("last_projected_chapter"))
    if not isinstance(base, int) or isinstance(base, bool) or base < 0 or first <= base or last < first:
        return None, None
    live_execution = pipeline_execution_state(nd)
    shadow_execution = (pipeline_execution_state(workspace)
                        if contained_regular_path(run, workspace / "meta/runtime/pipeline_execution.json") else {})
    active = (live_execution.get("active") and shadow_execution.get("active") and
              live_execution.get("mode") == shadow_execution.get("mode") == "project_all" and
              live_execution.get("process_id") == shadow_execution.get("process_id") and
              live_execution.get("process_id", 0) > 0 and
              first <= int_value(shadow_execution.get("target_chapter")) <= last)
    return workspace, {
        "evidence_scope": "projected", "generation_id": generation_id,
        "state": "running" if active else "paused", "label": "当前规划投影",
        "first_chapter": first, "last_chapter": last,
        "chapter": int_value(shadow_execution.get("target_chapter")),
    }


PLANNING_COMMIT_PHASES = frozenset(("proposal_committed", "arbitration_committed", "cycle_committed",
                                   "readiness_committed", "bundle_committed"))
PLANNING_WATCHDOG_MAX_BYTES = 1024 * 1024  # Includes Go's bounded, private deduplication keys.


def watchdog_opaque_token(value: str) -> str:
    """Match pipelineWatchdogOpaqueToken without exposing arbitrary source text."""
    value = value.strip()
    if len(value.encode("utf-8")) <= 256 and re.fullmatch(r"[A-Za-z0-9._:/@+\\=\-]+", value):
        return value
    return "sha256:" + hashlib.sha256(value.encode("utf-8")).hexdigest() if value else ""


def pipeline_watchdog_path(nd: Path) -> Path:
    """Same external, output-root-bound control path as the Go publisher."""
    absolute = Path(os.path.abspath(nd))
    token = watchdog_opaque_token(absolute.name).removeprefix("sha256:")
    token = re.sub(r"[/:@+=]", "-", token)[:96] or "unknown"
    suffix = hashlib.sha256(str(absolute).encode("utf-8")).hexdigest()[:16]
    return absolute.parent / ".pipeline-runtime" / f"{token}-{suffix}" / "meta/runtime/pipeline_watchdog.json"


def current_planning_watchdog(nd: Path, pipe: dict, runtime: dict, workspace: Path | None,
                              projection: dict | None, now: float | None = None) -> dict | None:
    """Read one bounded, public control snapshot, never proposal/observation history.

    A heartbeat proves liveness only. The returned progress timestamp/sequence
    are copied from durable events; neither leases nor usage can advance them.
    Invalid/missing monitoring data has no business or filesystem side effects.
    """
    execution = runtime.get("execution") or {}
    if (workspace is None or not projection or projection.get("state") != "running" or
            not execution.get("active") or execution.get("mode") != "project_all" or
            runtime.get("current_stage") != "project-all"):
        return None
    run = nd.parent.parent
    path = pipeline_watchdog_path(nd)
    if not contained_regular_path(run, path) or not path.is_file():
        return None
    try:
        with path.open("rb") as source:
            encoded = source.read(PLANNING_WATCHDOG_MAX_BYTES + 1)
        if len(encoded) > PLANNING_WATCHDOG_MAX_BYTES:
            return None
        raw = json.loads(encoded)
        if not isinstance(raw, dict):
            return None
        identity = pipe.get("run_identity")
        if not isinstance(identity, str) or not identity.strip():
            return None
        if (raw.get("schema") != "pipeline-watchdog.v1" or raw.get("stage") != "project-all" or
                raw.get("run_identity") != watchdog_opaque_token(identity) or
                raw.get("invocation_id") != watchdog_opaque_token(identity.strip() + ":project-all") or
                raw.get("generation_id") != projection.get("generation_id") or
                raw.get("status") not in ("running", "stalled") or raw.get("stopped_at")):
            return None
        chapter, cycle, revision, seq = (raw.get(key, 0) for key in ("chapter", "cycle", "round", "progress_seq"))
        if (not all(isinstance(value, int) and not isinstance(value, bool) for value in (chapter, cycle, revision, seq)) or
                chapter != projection.get("chapter") or not 0 <= cycle <= 64 or not 0 <= revision <= 2 or seq < 0):
            return None
        phase, digest = raw.get("planning_phase"), raw.get("progress_artifact_digest", "")
        if (phase not in PLANNING_COMMIT_PHASES | {"context_bound"} or not isinstance(digest, str) or
                (digest and not re.fullmatch(r"sha256:[0-9a-f]{64}", digest)) or
                (phase in PLANNING_COMMIT_PHASES and (not digest or seq == 0))):
            return None
        # Invocation IDs intentionally survive same-run restart in Go. Require
        # a heartbeat observed after BOTH current leases, not an old PID's state.
        if not contained_regular_path(run, workspace / "meta/runtime/pipeline_execution.json"):
            return None
        shadow = pipeline_execution_state(workspace, now)
        if (not shadow.get("active") or shadow.get("mode") != "project_all" or
                shadow.get("process_id") != execution.get("process_id") or execution.get("process_id", 0) <= 0 or
                shadow.get("target_chapter") != chapter):
            return None
        times = {key: raw.get(key) for key in ("started_at", "heartbeat_at", "last_progress_at")}
        if not all(isinstance(value, str) and 0 < len(value) <= 64 for value in times.values()):
            return None
        start, heartbeat, progress = (timestamp(times[key]) for key in ("started_at", "heartbeat_at", "last_progress_at"))
        now = time.time() if now is None else now
        leases = [execution.get("acquired_timestamp", 0), shadow.get("acquired_timestamp", 0)]
        if (not all(math.isfinite(value) and value > 0 for value in (start, heartbeat, progress, *leases)) or
                not start <= progress <= heartbeat or heartbeat < max(leases) or not -5 <= now - heartbeat <= 90):
            return None
        last_kind = raw.get("last_progress_kind")
        return {
            "generation_id": projection["generation_id"], "chapter": chapter, "cycle": cycle, "round": revision,
            "planning_phase": phase, "status": raw["status"], "progress_artifact_digest": digest,
            "last_progress_kind": last_kind if last_kind in PLANNING_COMMIT_PHASES else "",
            "progress_seq": seq, "last_progress_at": times["last_progress_at"], "heartbeat_at": times["heartbeat_at"],
        }
    except (OSError, ValueError, TypeError, OverflowError, RecursionError):
        return None


def character_agent_payload(nd: Path, planning_workspace: Path | None = None, planning_projection: dict | None = None) -> dict:
    """Public dashboard projection of character-agent evidence.

    Observation packets, private memories and decision reasons never leave the
    server-side files. The UI receives only activation reasons, the submitted
    choice/action, the arbiter's public result, memory chapter and usage.
    """
    registry = read_json(nd / "meta" / "character_agents" / "registry.json") or {}
    rows = {}
    for entry in registry.get("entries") or []:
        if not isinstance(entry, dict) or not entry.get("agent_id"):
            continue
        rows[entry["agent_id"]] = {
            "agent_id": entry["agent_id"],
            "agent_name": entry.get("agent_name") or f"character_{entry['agent_id']}",
            "character": entry.get("character") or "",
            "tier": entry.get("tier") or "",
            "status": entry.get("status") or "sleeping",
            "last_activated_chapter": entry.get("last_activated_chapter") or 0,
            "memory_version": entry.get("memory_version") or 0,
            "memory_chapter": 0,
            "activation_reasons": [],
            "recent_decision": "",
            "recent_action": "",
            "arbitration_outcome": "",
            "arbitration_result": "",
            "conflict_rounds": 0,
            "conflict_count": 0,
            **empty_character_usage(),
            "_latest_chapter": -1,
        }
        memory = read_json(nd / "meta" / "character_agents" / "memory" / f"{entry['agent_id']}.json") or {}
        rows[entry["agent_id"]]["memory_chapter"] = memory.get("last_accepted_chapter") or 0

    seen_evidence = set()
    usage_records = {}
    arbiter_usage = empty_character_usage()
    readiness_usage = empty_character_usage()
    total_usage = empty_character_usage()
    projected_usage = empty_character_usage()
    current_generation = (planning_projection or {}).get("generation_id") or ""

    def ensure_row(agent_id, character="", tier=""):
        if not agent_id:
            return None
        if agent_id not in rows:
            rows[agent_id] = {
                "agent_id": agent_id, "character": character or "", "tier": tier or "",
                "agent_name": f"character_{agent_id}",
                "status": "sleeping", "last_activated_chapter": 0, "memory_version": 0,
                "memory_chapter": 0, "activation_reasons": [], "recent_decision": "",
                "recent_action": "", "arbitration_outcome": "", "arbitration_result": "",
                "conflict_rounds": 0, "conflict_count": 0,
                **empty_character_usage(), "_latest_chapter": -1,
            }
        return rows[agent_id]

    def add_usage(item):
        if not isinstance(item, dict):
            return
        agent_id = item.get("agent_id") or ""
        key = (item.get("generation_id"), item.get("role"), agent_id, item.get("chapter"), item.get("cycle") or 0, item.get("round"),
               item.get("input"), item.get("output"), item.get("cache_read") or 0, item.get("cache_write") or 0)
        if isinstance(item.get("usage_id"), str) and item["usage_id"].strip():
            # Failed/canceled retries share the same character/chapter/round,
            # but are distinct paid runs. New immutable usage IDs distinguish
            # them; only legacy records use the old shape-based fallback.
            key = ("usage_id", item["usage_id"])
        if not agent_id:
            return
        # A bundle and its usage ledger describe the same completed round.
        # Enriched pricing/model metadata must not count its tokens twice.
        def quality(record):
            _, source = character_usage_cost(record)
            return (record.get("cost_source") in ("reported", "estimated", "unknown"),
                    {"unknown": 0, "estimated": 1, "reported": 2}[source],
                    bool(record.get("model") or record.get("models")))
        previous = usage_records.get(key)
        if previous is None or quality(item) >= quality(previous):
            usage_records[key] = item

    def is_current(row, chapter, cycle, scope):
        return ((scope == "projected" and row.get("evidence_scope") != "projected")
                or (chapter, cycle) >= (row["_latest_chapter"], row.get("_latest_cycle", 0)))

    def ingest(evidence, scope="stored", cycle=0):
        if not isinstance(evidence, dict):
            return
        root = evidence.get("evidence_root") or ""
        if root and root in seen_evidence:
            return
        if root:
            seen_evidence.add(root)
        chapter = int(evidence.get("chapter") or 0)
        for entry in (evidence.get("registry") or {}).get("entries") or []:
            if isinstance(entry, dict):
                row = ensure_row(entry.get("agent_id"), entry.get("character"), entry.get("tier"))
                if row is not None and entry.get("agent_name"):
                    row["agent_name"] = entry["agent_name"]
        activation = evidence.get("activation") or {}
        activation_by_agent = {}
        for entry in activation.get("entries") or []:
            if not isinstance(entry, dict) or not entry.get("agent_id"):
                continue
            activation_by_agent[entry["agent_id"]] = entry
            row = ensure_row(entry["agent_id"], entry.get("character"), entry.get("tier"))
            if is_current(row, chapter, cycle, scope):
                row["_latest_chapter"], row["_latest_cycle"] = chapter, cycle
                row["activation_cycle"] = cycle
                row["status"] = entry.get("state") or row["status"]
                row["activation_reasons"] = [clip(x, 80) for x in (entry.get("reasons") or [])][:8]
                if entry.get("state") == "active":
                    row["last_activated_chapter"] = max(row["last_activated_chapter"], chapter)
        proposals = {}
        for proposal in evidence.get("proposals") or []:
            if not isinstance(proposal, dict) or not proposal.get("agent_id"):
                continue
            current = proposals.get(proposal["agent_id"])
            if current is None or int(proposal.get("round") or 0) > int(current.get("round") or 0):
                proposals[proposal["agent_id"]] = proposal
        for agent_id, proposal in proposals.items():
            row = ensure_row(agent_id, proposal.get("character") or "")
            if is_current(row, chapter, cycle, scope):
                row["_latest_chapter"] = chapter
                row["_latest_cycle"] = cycle
                row["decision_cycle"] = cycle
                row["evidence_scope"] = scope
                row["recent_decision"] = clip(proposal.get("decision"), 180)
                row["recent_action"] = clip(proposal.get("intended_action"), 180)
                row["arbitration_outcome"] = ""
                row["arbitration_result"] = ""
        arbitrations = [x for x in (evidence.get("arbitrations") or []) if isinstance(x, dict)]
        arbitrations.sort(key=lambda x: int(x.get("round") or 0))
        final = arbitrations[-1] if arbitrations else {}
        conflicts = [x for a in arbitrations for x in (a.get("conflicts") or []) if isinstance(x, dict)]
        for resolution in final.get("resolutions") or []:
            if not isinstance(resolution, dict) or not resolution.get("agent_id"):
                continue
            agent_id = resolution["agent_id"]
            row = ensure_row(agent_id, resolution.get("character") or "")
            proposal = proposals.get(agent_id) or {}
            if is_current(row, chapter, cycle, scope):
                row["_latest_chapter"] = chapter
                row["_latest_cycle"] = cycle
                row["decision_cycle"] = cycle
                row["evidence_scope"] = scope
                row["recent_decision"] = clip(proposal.get("decision"), 180)
                row["recent_action"] = clip(proposal.get("intended_action"), 180)
                row["arbitration_outcome"] = resolution.get("outcome") or ""
                row["arbitration_result"] = clip(resolution.get("immediate_result"), 220)
                row["conflict_rounds"] = max(0, int(final.get("round") or 1) - 1)
                row["conflict_count"] = sum(
                    1 for conflict in conflicts if agent_id in (conflict.get("affected_agent_ids") or [])
                )
        for item in evidence.get("usage") or []:
            add_usage(item)

    planning_root = nd / "meta" / "planning" / "v2"
    if planning_root.is_dir():
        for path in planning_root.rglob("*.bundle.json"):
            bundle = read_json(path) or {}
            ingest(bundle.get("character_agent_evidence"))
            activation_chapter = bundle.get("character_activation_evidence") or {}
            if isinstance(activation_chapter, dict):
                for cycle in activation_chapter.get("cycles") or []:
                    if isinstance(cycle, dict):
                        ingest(cycle.get("evidence"), cycle=int_value(cycle.get("index")))

    projected_root = nd / "meta" / "character_agents" / "projected"
    if projected_root.is_dir():
        for chapter_dir in projected_root.glob("*/chapters/*"):
            activation = read_json(chapter_dir / "activation.json")
            if not isinstance(activation, dict):
                continue
            arbitration_paths = sorted(chapter_dir.glob("arbitration-round-*.json"))
            arbitrations = [read_json(path) for path in arbitration_paths]
            proposals = []
            for path in sorted((chapter_dir / "proposals").glob("round-*/*.json")):
                proposal = read_json(path)
                if isinstance(proposal, dict):
                    proposals.append(proposal)
            ingest({
                "chapter": activation.get("chapter") or 0,
                "activation": activation,
                "proposals": proposals,
                "arbitrations": [x for x in arbitrations if isinstance(x, dict)],
            })
    for item in read_jsonl_tail(nd / "meta" / "character_agents" / "usage.jsonl", 10000):
        add_usage(item)

    if planning_workspace is not None and current_generation:
        # Only public projections are ingested. In particular, never load the
        # workspace's observations or memory: accepted memory stays live-owned.
        def projected_json(path):
            return read_json(path) if contained_regular_path(planning_workspace, path) else None

        projected_registry = projected_json(planning_workspace / "meta/character_agents/registry.json") or {}
        for entry in (projected_registry.get("entries") or []) if isinstance(projected_registry, dict) else []:
            if not isinstance(entry, dict):
                continue
            row = ensure_row(entry.get("agent_id"), entry.get("character"), entry.get("tier"))
            if row is not None and entry.get("agent_name"):
                row["agent_name"] = entry["agent_name"]
        chapters = planning_workspace / "meta/character_agents/projected" / current_generation / "chapters"
        chapter_dirs = sorted(chapters.glob("*")) if contained_regular_path(planning_workspace, chapters) else []
        for chapter_dir in chapter_dirs:
            activation = projected_json(chapter_dir / "activation.json")
            if not isinstance(activation, dict) or activation.get("generation_id") != current_generation:
                continue
            chapter = int_value(activation.get("chapter"))
            if not (planning_projection["first_chapter"] <= chapter <= planning_projection["last_chapter"]):
                continue
            def matching(path):
                value = projected_json(path)
                return (value if isinstance(value, dict) and value.get("generation_id") == current_generation
                        and int_value(value.get("chapter")) == chapter else None)
            proposals = [value for path in sorted((chapter_dir / "proposals").glob("round-*/*.json"))
                         if (value := matching(path)) is not None]
            arbitrations = [value for path in sorted(chapter_dir.glob("arbitration-round-*.json"))
                            if (value := matching(path)) is not None]
            ingest({"chapter": chapter, "activation": activation, "proposals": proposals,
                    "arbitrations": arbitrations}, "projected")
        usage_path = planning_workspace / "meta/character_agents/usage.jsonl"
        # Read only per-cycle public projection files, never input snapshots,
        # observations, private memories or readiness reasoning inputs.
        sessions = planning_workspace / "meta/character_agents/activation_sessions" / current_generation
        session_dirs = sorted(sessions.glob("*")) if contained_regular_path(planning_workspace, sessions) else []
        for session_dir in session_dirs:
            chapter = int_value(session_dir.name)
            if not (planning_projection["first_chapter"] <= chapter <= planning_projection["last_chapter"]):
                continue
            work = session_dir / "work"
            cycle_dirs = sorted(work.glob("*")) if contained_regular_path(planning_workspace, work) else []
            for cycle_dir in cycle_dirs:
                proof = cycle_dir / "proof"
                activation = projected_json(proof / "activation.json")
                if not isinstance(activation, dict) or activation.get("generation_id") != current_generation or int_value(activation.get("chapter")) != chapter:
                    continue
                def cycle_item(path):
                    value = projected_json(path)
                    return (value if isinstance(value, dict) and value.get("generation_id") == current_generation
                            and int_value(value.get("chapter")) == chapter else None)
                proposals = [value for path in sorted((proof / "proposals").glob("round-*/*.json"))
                             if (value := cycle_item(path)) is not None]
                arbitrations = [value for path in sorted(proof.glob("arbitration-round-*.json"))
                                if (value := cycle_item(path)) is not None]
                ingest({"chapter": chapter, "activation": activation, "proposals": proposals,
                        "arbitrations": arbitrations}, "projected", int_value(cycle_dir.name))
        if contained_regular_path(planning_workspace, usage_path):
            for item in read_jsonl_tail(usage_path, 10000):
                if item.get("generation_id") == current_generation:
                    add_usage(item)

    for item in usage_records.values():
        if item.get("role") == "world_arbiter" or item.get("agent_id") == "world_arbiter":
            row = arbiter_usage
        elif item.get("role") == "chapter_readiness" or item.get("agent_id") == "chapter_readiness":
            row = readiness_usage
        else:
            row = ensure_row(item["agent_id"], item.get("character") or "")
        accumulate_character_usage(row, item)
        accumulate_character_usage(total_usage, item)
        if current_generation and item.get("generation_id") == current_generation:
            accumulate_character_usage(projected_usage, item)

    public_rows = []
    for row in rows.values():
        row.pop("_latest_chapter", None)
        row.pop("_latest_cycle", None)
        finalize_character_usage(row)
        public_rows.append(row)
    public_rows.sort(key=lambda row: (0 if row["tier"] == "core" else 1, row["character"], row["agent_id"]))
    successor_public = None
    successor_pointer = read_json(nd / "meta" / "character_agents" / "successors" / "current.json") or {}
    parent_generation = successor_pointer.get("parent_generation_id") or ""
    successor_digest = successor_pointer.get("plan_digest") or ""
    if parent_generation and successor_digest.startswith("sha256:"):
        successor = read_json(
            nd / "meta" / "character_agents" / "successors" / parent_generation /
            f"{successor_digest.removeprefix('sha256:')}.json"
        ) or {}
        if successor.get("digest") == successor_digest:
            successor_public = {
                "parent_generation_id": parent_generation,
                "plan_digest": successor_digest,
                "trigger_chapter": successor.get("trigger_chapter") or 0,
                "hard_contract_conflicts": [clip(x, 180) for x in (successor.get("hard_contract_conflicts") or [])],
                "architect_summary": clip(successor.get("architect_summary"), 240),
            }
    return {
        "version": registry.get("version") or "",
        "registry_root": registry.get("registry_root") or "",
        "characters": public_rows,
        "world_arbiter_usage": finalize_character_usage(arbiter_usage),
        "chapter_readiness_usage": finalize_character_usage(readiness_usage),
        "usage_summary": finalize_character_usage(total_usage),
        "successor_generation": successor_public,
        "planning_projection": ({**planning_projection, "usage_summary": finalize_character_usage(projected_usage)}
                                if planning_projection else None),
    }


def run_detail(run: Path) -> dict:
    nd = novel_dir(run)
    base = summarize_run(run)
    planning_workspace, planning_projection = current_character_planning_workspace(nd, base["formal_planning"])
    usage = read_json(nd / "meta" / "usage.json") or {}
    assets = artifact_inventory(nd)
    chapters = []
    for ch in chapter_files(nd):
        verdict, gate, warns, rw = review_state(nd, ch)
        chapters.append({
            "n": ch,
            "title": chapter_title(nd, ch),
            "words": base["chapter_words"].get(str(ch)) or count_words(nd, ch),
            "verdict": verdict,
            "gate": gate,
            "gate_warnings": warns,
            "rewritten": rw["rewritten"],
            "rewrite_pending": rw["pending"],
            "rewrite_rounds": rw["rounds"],
        })
    per_agent = []
    for name, row in (usage.get("per_agent") or {}).items():
        if isinstance(row, dict):
            per_agent.append({
                "agent": name,
                "input": row.get("input") or 0,
                "output": row.get("output") or 0,
                "cost_usd": round(float(row.get("cost_usd") or 0.0), 4),
            })
    per_agent.sort(key=lambda r: -r["cost_usd"])
    deliveries = 0
    dl = nd / "meta" / "delivery_log.jsonl"
    try:
        with open(dl, encoding="utf-8") as f:
            deliveries = sum(1 for line in f if line.strip())
    except OSError:
        pass
    planned = []
    for ch in read_json(nd / "outline.json") or []:
        if isinstance(ch, dict):
            planned.append({
                "chapter": ch.get("chapter"),
                "title": ch.get("title") or "",
                "core_event": clip(ch.get("core_event"), 140),
            })
    position = {}
    project_progress = read_json(nd / "meta" / "project_progress.json") or {}
    for item in project_progress.get("outline_status") or []:
        if isinstance(item, dict) and item.get("status") == "current":
            position = {
                "volume": item.get("volume"),
                "volume_title": item.get("volume_title") or "",
                "arc": item.get("arc"),
                "arc_title": item.get("arc_title") or "",
                "arc_goal": clip(item.get("goal"), 260),
                "start_chapter": item.get("start_chapter"),
                "end_chapter": item.get("end_chapter"),
                "arc_completed": item.get("completed_chapters") or 0,
                "arc_total": item.get("total_chapters") or item.get("estimated_chapters") or 0,
            }
            break
    base.update({
        "chapters": chapters,
        "planned": planned,
        "per_agent": per_agent,
        "deliveries": deliveries,
        "position": position,
        "assets": assets,
        "character_agents": character_agent_payload(nd, planning_workspace, planning_projection),
        "log": log_tail(nd),
    })
    return base


# ---------- 详情页签数据（设定 / 人物 / 计划 / 离屏世界） ----------

def read_json_variants(nd: Path, *rels: str):
    """按顺序读第一个存在的 JSON（如 relationship_state.json → .initial.json）。"""
    for rel in rels:
        data = read_json(nd / rel)
        if data is not None:
            return data
    return None


def text_head(path: Path, limit: int = 2000) -> str:
    try:
        text = path.read_text(encoding="utf-8").strip()
    except OSError:
        return ""
    return text[:limit] + ("\n…（截断）" if len(text) > limit else "")


def clip(s, n: int = 200) -> str:
    s = str(s or "").strip()
    return s[:n] + ("…" if len(s) > n else "")


def text_list(value, limit: int = 20, width: int = 220) -> list[str]:
    if isinstance(value, list):
        return [clip(x, width) for x in value if str(x or "").strip()][:limit]
    if str(value or "").strip():
        return [clip(value, width)]
    return []


def setting_payload(run: Path) -> dict:
    nd = novel_dir(run)
    bw = read_json(nd / "book_world.json") or {}
    place_details = []
    for p in bw.get("places") or []:
        if not isinstance(p, dict):
            continue
        place_details.append({
            "id": p.get("id") or "",
            "name": p.get("name") or "",
            "kind": p.get("kind") or "",
            "description": clip(p.get("description"), 260),
            "rules": [clip(x, 140) for x in (p.get("rules") or [])][:6],
            "factions": (p.get("factions") or [])[:8],
            "tags": (p.get("tags") or [])[:8],
        })
    route_details = []
    for r in bw.get("routes") or []:
        if not isinstance(r, dict):
            continue
        route_details.append({
            "from": r.get("from") or "",
            "to": r.get("to") or "",
            "description": clip(r.get("description"), 220),
            "risk": clip(r.get("risk"), 180),
            "travel_days": r.get("travel_days"),
        })
    factions = []
    for f in bw.get("factions") or []:
        if not isinstance(f, dict):
            continue
        factions.append({
            "name": f.get("name") or f.get("id") or "",
            "stance": f.get("stance") or "",
            "goal": clip(f.get("goal"), 160),
            "internal_tension": clip(f.get("internal_tension"), 140),
            "clock": f.get("clock") or {},
        })
    rules = []
    for r in read_json(nd / "world_rules.json") or []:
        if isinstance(r, dict):
            rules.append({
                "category": r.get("category") or "",
                "rule": clip(r.get("rule"), 220),
                "boundary": clip(r.get("boundary"), 180),
                "visibility": r.get("visibility") or "",
            })
    axioms = read_json(nd / "meta" / "physics_axioms.json") or {}
    timeline = []
    for ev in (read_json(nd / "timeline.json") or [])[-80:]:
        if isinstance(ev, dict):
            timeline.append({
                "chapter": ev.get("chapter"),
                "time": clip(ev.get("time"), 60),
                "event": clip(ev.get("event"), 200),
                "characters": (ev.get("characters") or [])[:6],
            })
    return {
        "premise": text_head(nd / "premise.md", 12000),
        "world_md": text_head(nd / "book_world.md", 12000),
        "prompt": text_head(run / "prompt.md", 1200),
        "world_name": bw.get("name") or "",
        "world_summary": clip(bw.get("summary"), 400),
        "places": [clip((p or {}).get("name"), 30) for p in (bw.get("places") or []) if isinstance(p, dict)][:40],
        "routes": len(bw.get("routes") or []),
        "place_details": place_details,
        "route_details": route_details,
        "map_notes": text_list(bw.get("map_notes")),
        "world_pillars": text_list(bw.get("world_pillars")),
        "vision_pillars": text_list(bw.get("vision_pillars")),
        "factions": factions,
        "world_rules": rules,
        "physics_axioms": [clip(n, 200) for n in (axioms.get("notes") or [])][:20],
        "story_calendar": read_json(nd / "meta" / "story_calendar.json") or {},
        "timeline": timeline,
    }


TIER_GROUPS = {"core": "core", "protagonist": "core", "important": "important",
               "supporting": "important", "secondary": "minor", "background": "minor"}


def cast_payload(run: Path) -> dict:
    nd = novel_dir(run)
    # 零章人物动态：目标 / 压力 / 情绪评价 / 可能行动 / 能力阶段 / 知识账本
    dynamics = {}
    dyn = read_json(nd / "meta" / "initial_character_dynamics.json") or {}
    for item in dyn.get("characters") or []:
        if isinstance(item, dict) and item.get("character"):
            dynamics[item["character"]] = item
    latest_stage = {}
    for path in sorted((nd / "meta" / "character_stage").glob("*.json"))[-8:]:
        records = read_json(path) or []
        if isinstance(records, dict):
            records = records.get("characters") or records.get("records") or []
        for record in records:
            if isinstance(record, dict) and record.get("character"):
                latest_stage[record["character"]] = record
    chars = []
    for c in read_json(nd / "characters.json") or []:
        if not isinstance(c, dict):
            continue
        name = c.get("name") or ""
        psych = c.get("psych") or {}
        dna = psych.get("dna") or {}
        d = dynamics.get(name) or {}
        emo = d.get("emotion_appraisal") or {}
        arcax = d.get("arc_axis") or {}
        know = d.get("knowledge_ledger") or {}
        frame = d.get("decision_frame") or {}
        stage = latest_stage.get(name) or {}
        chars.append({
            "name": name,
            "role": c.get("role") or "",
            "tier": c.get("tier") or "",
            "group": TIER_GROUPS.get(str(c.get("tier") or "").lower(), "minor"),
            "traits": [clip(t, 20) for t in (c.get("traits") or [])][:6],
            "description": clip(c.get("description"), 220),
            "arc": clip(c.get("arc"), 160),
            "big_five": psych.get("big_five") or {},
            "attachment": ((psych.get("attachment") or {}).get("style")) or "",
            "dna": {
                "exposed": [clip(x, 60) for x in (dna.get("exposed") or [])][:4],
                "hidden": [clip(x, 60) for x in (dna.get("hidden") or [])][:4],
                "latent": [clip(x, 60) for x in (dna.get("latent") or [])][:4],
            },
            "state": {
                "goal": clip(d.get("current_goal"), 180),
                "pressure": clip(d.get("pressure"), 200),
                "likely_action": clip(d.get("likely_action"), 200),
                "competence": clip(d.get("competence_stage"), 120),
                "want": clip(arcax.get("want"), 200),
                "known": len(know.get("known_facts") or []),
                "unknown": len(know.get("unknown_facts") or []),
            },
            "stage": {
                "chapter": stage.get("chapter"),
                "time": clip(stage.get("time"), 90),
                "location": clip(stage.get("location"), 100),
                "status": clip(stage.get("status"), 100),
                "action": clip(stage.get("current_action"), 220),
                "decision": clip(stage.get("decision"), 220),
                "personality_delta": clip(stage.get("personality_delta"), 180),
                "death_state": clip(stage.get("death_state"), 100),
                "next_potential": clip(stage.get("next_potential"), 180),
            },
            "knowledge": {
                "known": text_list(know.get("known_facts"), 8, 180),
                "unknown": text_list(know.get("unknown_facts"), 8, 180),
                "suspicions": text_list(know.get("suspicions"), 6, 180),
                "false_beliefs": text_list(know.get("false_beliefs"), 6, 180),
                "forbidden": text_list(know.get("forbidden_knowledge"), 6, 180),
            },
            "decision_frame": {
                "rule": clip(frame.get("decision_rule"), 220),
                "tradeoff": clip(frame.get("tradeoff"), 200),
                "cost": clip(frame.get("cost_paid"), 180),
                "risk": clip(frame.get("risk_accepted"), 180),
                "evidence": clip(frame.get("minimum_evidence_required"), 200),
                "options": text_list(frame.get("available_options"), 8, 140),
                "rejected": text_list(frame.get("rejected_options"), 8, 140),
            },
            "constraints": {
                "resources": text_list(d.get("resources"), 8, 160),
                "secrets": text_list(d.get("secrets"), 8, 160),
                "misbeliefs": text_list(d.get("misbeliefs"), 8, 160),
                "skill_limits": text_list(d.get("skill_limits"), 8, 160),
                "plausible_mistakes": text_list(d.get("plausible_mistakes"), 8, 160),
                "correction_triggers": text_list(d.get("correction_triggers"), 8, 160),
            },
            "emotion": {
                "trigger": clip(emo.get("trigger_event"), 160),
                "visible": clip(emo.get("visible_expression"), 140),
                "suppressed": clip(emo.get("suppressed_expression"), 140),
                "coping": clip(emo.get("coping_strategy"), 140),
            },
            "arc_axis": {
                "want": clip(arcax.get("want"), 200),
                "need": clip(arcax.get("need"), 200),
                "lie": clip(arcax.get("core_lie"), 180),
                "stage": clip(arcax.get("arc_stage"), 120),
                "growth_signal": clip(arcax.get("growth_signal"), 180),
                "regression_signal": clip(arcax.get("regression_signal"), 180),
            },
        })
    # 群众：crowd_life NPC + 配角名册
    crowd = []
    seen = {c["name"] for c in chars}
    cl = read_json(nd / "meta" / "crowd_life.json") or {}
    for npc in cl.get("npcs") or []:
        if isinstance(npc, dict):
            nm = npc.get("npc_id") or ""
            crowd.append({"name": nm, "note": clip("；".join(npc.get("goals") or []), 160),
                          "source": "crowd_life", "named": nm in seen})
    ledger = read_json(nd / "meta" / "cast_ledger.json")
    if isinstance(ledger, dict):
        for nm, v in ledger.items():
            if nm in ("version",) or nm in {c["name"] for c in crowd}:
                continue
            note = ""
            if isinstance(v, dict):
                note = clip(v.get("role") or v.get("note") or v.get("last_seen") or "", 120)
            crowd.append({"name": str(nm), "note": note, "source": "cast_ledger", "named": False})
    rs = read_json_variants(nd, "relationship_state.json", "relationship_state.initial.json") or {}
    relations = []
    if isinstance(rs, list):  # 旧版扁平形态：[{character_a, character_b, relation, chapter}]
        for r in rs[:80]:
            if isinstance(r, dict):
                relations.append({
                    "owner": r.get("character_a") or "",
                    "counterpart": r.get("character_b") or "",
                    "trust": None,
                    "alliance": clip(r.get("relation"), 120),
                    "promise": "", "debt": "",
                    "leverage": f"第 {r.get('chapter')} 章" if r.get("chapter") else "",
                })
        return {"characters": chars, "crowd": crowd, "relationship_scope": "",
                "relationship_chapter": None, "relations": relations}
    contracts = rs.get("contracts") or []
    if isinstance(contracts, dict):  # {owner: [contract...]} 与 [contract...] 双形态兼容
        pairs = [(owner, c) for owner, lst in contracts.items() for c in (lst or []) if isinstance(c, dict)]
    else:
        pairs = [(c.get("name") or c.get("owner") or "", c) for c in contracts if isinstance(c, dict)]
    for owner, c in pairs[:80]:
        relations.append({
            "owner": owner,
            "counterpart": c.get("counterpart") or "",
            "trust": c.get("trust"),
            "alliance": c.get("alliance_status") or "",
            "promise": clip(c.get("promise"), 120),
            "debt": clip(c.get("debt"), 120),
            "leverage": clip(c.get("leverage"), 120),
        })
    return {
        "characters": chars,
        "crowd": crowd,
        "relationship_scope": rs.get("scope") or "",
        "relationship_chapter": rs.get("chapter"),
        "relations": relations,
    }


def plan_payload(run: Path) -> dict:
    nd = novel_dir(run)
    volumes = []
    for v in read_json(nd / "layered_outline.json") or []:
        if not isinstance(v, dict):
            continue
        volumes.append({
            "index": v.get("index"),
            "title": v.get("title") or "",
            "theme": clip(v.get("theme"), 160),
            "arcs": [{
                "index": a.get("index"),
                "title": a.get("title") or "",
                "goal": clip(a.get("goal"), 160),
                "chapters": len(a.get("chapters") or []) if isinstance(a.get("chapters"), list) else a.get("chapters"),
            } for a in (v.get("arcs") or []) if isinstance(a, dict)],
        })
    outline = []
    for ch in read_json(nd / "outline.json") or []:
        if isinstance(ch, dict):
            outline.append({
                "chapter": ch.get("chapter"),
                "title": ch.get("title") or "",
                "core_event": clip(ch.get("core_event"), 180),
                "hook": clip(ch.get("hook"), 120),
            })
    ledger = read_json(nd / "meta" / "chapter_progress.json") or {}
    np_raw = ledger.get("next_plan") or {}
    next_plan = {
        "chapter": np_raw.get("chapter"),
        "title": np_raw.get("title") or "",
        "position": np_raw.get("position") or "",
        "core_event": clip(np_raw.get("core_event"), 400),
        "hook": clip(np_raw.get("hook"), 220),
        "required_beats": [clip(b, 160) for b in (np_raw.get("required_beats") or [])][:10],
    } if np_raw else None
    fs = read_json_variants(nd, "foreshadow_ledger.json", "foreshadow_ledger.initial.json") or {}
    seeds = []
    raw_seeds = fs if isinstance(fs, list) else (fs.get("seeds") or fs.get("items") or fs.get("foreshadows") or [])
    for s in raw_seeds:
        if isinstance(s, dict):
            planted = s.get("planted_at") or s.get("source_chapter")
            seeds.append({
                "id": s.get("id") or "",
                "description": clip(s.get("description") or s.get("content"), 180),
                "status": s.get("status") or "",
                "target_chapter": s.get("target_chapter") or s.get("payoff_chapter"),
                "horizon": s.get("payoff_horizon") or (f"埋于第 {planted} 章" if planted else ""),
            })
    timeline = []
    for ev in (read_json(nd / "timeline.json") or [])[-14:]:
        if isinstance(ev, dict):
            timeline.append({
                "chapter": ev.get("chapter"),
                "time": clip(ev.get("time"), 40),
                "event": clip(ev.get("event"), 160),
            })
    return {"volumes": volumes, "outline": outline, "next_plan": next_plan,
            "foreshadows": seeds, "timeline": timeline}


def offscreen_payload(run: Path) -> dict:
    nd = novel_dir(run)
    tick = read_json(nd / "meta" / "world_tick.json") or {}
    agendas = []
    ag = read_json(nd / "meta" / "offscreen_agenda.json") or {}
    for a in ag.get("agendas") or []:
        if isinstance(a, dict):
            agendas.append({
                "name": a.get("name") or "",
                "tier": a.get("tier") or "",
                "goal": clip(a.get("current_goal"), 180),
                "status": a.get("status") or "",
            })
    mood = read_json(nd / "meta" / "social_mood.json") or {}
    events = []
    try:
        with open(nd / "meta" / "world_events.jsonl", encoding="utf-8") as f:
            lines = f.readlines()[-50:]
        for line in lines:
            try:
                e = json.loads(line)
            except json.JSONDecodeError:
                continue
            events.append({
                "chapter": e.get("chapter"),
                "summary": clip(e.get("summary"), 200),
                "actors": (e.get("actors") or [])[:5],
                "visibility_chapter": e.get("visibility_chapter"),
                "visibility_path": e.get("visibility_path") or "",
                "tier": e.get("tier") or "",
                "foreshadow": bool(e.get("foreshadow_candidate")),
            })
    except OSError:
        pass
    tiers = read_json(nd / "meta" / "simulation_tiers.json") or {}
    tier_counts: dict[str, int] = {}
    for a in tiers.get("assignments") or []:
        if isinstance(a, dict):
            t = a.get("tier") or "unknown"
            tier_counts[t] = tier_counts.get(t, 0) + 1
    info = read_json(nd / "meta" / "info_graph.json") or {}
    readiness = read_json(nd / "meta" / "first_chapter_generation_readiness.json") or {}
    bw = read_json(nd / "book_world.json") or {}
    clocks = [{
        "name": f.get("name") or "",
        "clock": f.get("clock") or {},
    } for f in (bw.get("factions") or []) if isinstance(f, dict) and f.get("clock")]
    journeys = []
    for path in sorted((nd / "meta" / "side_character_journeys").glob("*.json"))[-8:]:
        records = read_json(path) or []
        if isinstance(records, dict):
            records = records.get("records") or records.get("characters") or []
        for item in records:
            if not isinstance(item, dict):
                continue
            journeys.append({
                "chapter": item.get("chapter"),
                "character": item.get("character") or "",
                "time": clip(item.get("time"), 80),
                "location": clip(item.get("location"), 100),
                "status": clip(item.get("status"), 100),
                "action": clip(item.get("current_action"), 220),
                "pressure": clip(item.get("pressure"), 180),
                "decision": clip(item.get("decision"), 200),
                "mistake": clip(item.get("mistake_or_misbelief"), 180),
                "knowledge_boundary": clip(item.get("knowledge_boundary"), 220),
                "transport": clip(item.get("transport"), 120),
                "travel_time": clip(item.get("travel_time"), 100),
                "meeting_constraint": clip(item.get("meeting_constraint"), 180),
                "personality_delta": clip(item.get("personality_delta"), 180),
                "death_state": clip(item.get("death_state"), 100),
                "protagonist_notice": clip(item.get("protagonist_notice"), 180),
                "next_potential": clip(item.get("next_potential"), 180),
                "visible_in_chapter": bool(item.get("visible_in_chapter")),
            })
    journeys = journeys[-60:]
    world_deltas = []
    for path in sorted((nd / "meta" / "chapter_world_deltas").glob("*.json"))[-8:]:
        item = read_json(path) or {}
        if not isinstance(item, dict):
            continue
        changes = item.get("world_deltas") or []
        world_deltas.append({
            "chapter": item.get("chapter"),
            "summary": clip(item.get("summary"), 300),
            "character_count": len(item.get("character_deltas") or []),
            "change_count": len(changes),
            "changes": [{
                "kind": c.get("kind") or "",
                "entity": clip(c.get("entity"), 100),
                "change": clip(c.get("change"), 220),
                "visible": bool(c.get("visible_to_protagonist")),
            } for c in changes[-12:] if isinstance(c, dict)],
        })
    # world_tick 是「离屏世界基线」tick，Architect 只在弧/卷边界 save_world_tick 时
    # 推进；它不是逐章推演游标。逐章世界推演落在 chapter_simulations /
    # chapter_world_deltas，单独统计，避免把「单弧完结、tick 停在第0章」误读成推演没做。
    prog = read_json(nd / "meta" / "progress.json") or read_json(nd / "meta" / "project_progress.json") or {}
    completed_ch = len(prog.get("completed_chapters") or [])
    sim_chapters = len(list((nd / "meta" / "chapter_simulations").glob("*.json")))
    delta_chapters = len(list((nd / "meta" / "chapter_world_deltas").glob("*.json")))
    return {
        "tick": tick,
        "simulation_coverage": {
            "per_chapter_simulated": sim_chapters,
            "per_chapter_deltas": delta_chapters,
            "completed_chapters": completed_ch,
        },
        "agendas": agendas,
        "social_mood": {
            "mood": clip(mood.get("mood"), 160),
            "intensity": mood.get("intensity"),
            "rumors": [clip(r, 140) for r in (mood.get("rumors") or [])][:8],
        },
        "events": events,
        "tier_counts": tier_counts,
        "info_nodes": len(info.get("nodes") or []),
        "readiness": {"ready": readiness.get("ready"), "generated_at": readiness.get("generated_at") or ""},
        "clocks": clocks,
        "journeys": journeys,
        "world_deltas": world_deltas,
    }


def growth_payload(run: Path) -> dict:
    """人物成长轨迹 + 决策流。成长来自 character_continuity（出场时间线 / 弧向 /
    当前事实）合入 long_arc_character_plan（长弧规划）；决策来自 state_changes
    （章号 / 人物 / 字段 old→new + 理由）。"""
    nd = novel_dir(run)
    prog = read_json(nd / "meta" / "progress.json") or {}
    total_ch = prog.get("total_chapters") or 0
    cur_ch = prog.get("current_chapter") or (len(prog.get("completed_chapters") or []) or 0)

    # 长弧规划按名字索引
    long_arc = {}
    lap = read_json(nd / "meta" / "long_arc_character_plan.json") or {}
    for e in lap.get("entries") or []:
        if isinstance(e, dict) and e.get("name"):
            long_arc[e["name"]] = {
                "first_three_volumes": clip(e.get("first_three_volumes"), 240),
                "later_macro": clip(e.get("later_macro"), 200),
            }

    characters = []
    cc = read_json(nd / "meta" / "character_continuity.json") or {}
    for e in cc.get("entries") or []:
        if not isinstance(e, dict):
            continue
        name = e.get("name") or ""
        facts = e.get("current_facts") or []
        characters.append({
            "name": name,
            "role": e.get("role") or "",
            "tier": e.get("tier") or "",
            "group": TIER_GROUPS.get(str(e.get("tier") or "").lower(), "minor"),
            "first_seen": e.get("first_seen_chapter"),
            "last_seen": e.get("last_seen_chapter"),
            "appearance_chapters": [c for c in (e.get("appearance_chapters") or []) if isinstance(c, int)],
            "appearance_count": e.get("appearance_count") or len(e.get("appearance_chapters") or []),
            "arc_direction": clip(e.get("arc_direction"), 400),
            "current_facts": [clip(f, 180) for f in facts][:5],
            "return_plan": (e.get("return_plan") or {}).get("return_priority") or "",
            "next_use_chapter": (e.get("return_plan") or {}).get("suggested_chapter"),
            "long_arc": long_arc.get(name) or {},
        })
    # 主角圈优先、出场多者在前
    order = {"core": 0, "important": 1, "minor": 2}
    characters.sort(key=lambda c: (order.get(c["group"], 3), -c["appearance_count"]))

    decisions = []
    for s in read_json(nd / "meta" / "state_changes.json") or []:
        if isinstance(s, dict):
            decisions.append({
                "chapter": s.get("chapter"),
                "entity": s.get("entity") or "",
                "field": clip(s.get("field"), 60),
                "old": clip(s.get("old_value"), 160),
                "new": clip(s.get("new_value"), 160),
                "reason": clip(s.get("reason"), 220),
            })
    decisions.sort(key=lambda d: (d["chapter"] or 0))
    # 决策按人物聚合计数（供前端筛选/热度）
    by_entity: dict[str, int] = {}
    for d in decisions:
        by_entity[d["entity"]] = by_entity.get(d["entity"], 0) + 1

    return {
        "total_chapters": total_ch,
        "current_chapter": cur_ch,
        "characters": characters,
        "decisions": decisions,
        "decision_counts": by_entity,
    }


def quality_payload(run: Path) -> dict:
    nd = novel_dir(run)
    rows = []
    for ch in chapter_files(nd):
        body_hash = file_sha256(nd / "chapters" / f"{ch:02d}.md")
        review = read_json(nd / "reviews" / f"{ch:02d}.json") or {}
        gate = read_json(nd / "reviews" / f"{ch:02d}_ai_gate.json") or {}
        judge = read_json(nd / "reviews" / f"{ch:02d}_deepseek_ai_judge.json") or {}
        metrics = read_json(nd / "meta" / "chapter_metrics" / f"{ch:02d}.json") or {}
        violations = gate.get("rule_violations") or []
        errors = sum(1 for v in violations if isinstance(v, dict) and str(v.get("severity") or "").lower() == "error")
        warnings = sum(1 for v in violations if isinstance(v, dict) and str(v.get("severity") or "").lower() == "warning")
        aigc = gate.get("aigc_report") or {}
        known_hashes = [str(x.get("body_sha256") or "") for x in (review, gate, judge) if isinstance(x, dict)]
        known_hashes = [h for h in known_hashes if h]
        freshness = "unknown" if not known_hashes else "fresh" if all(h == body_hash for h in known_hashes) else "stale"
        dimensions = []
        for d in review.get("dimensions") or []:
            if isinstance(d, dict):
                dimensions.append({
                    "name": d.get("dimension") or "",
                    "score": d.get("score"),
                    "verdict": d.get("verdict") or "",
                    "comment": clip(d.get("comment"), 240),
                })
        rows.append({
            "chapter": ch,
            "title": chapter_title(nd, ch),
            "review_verdict": review.get("verdict") or "",
            "contract_status": review.get("contract_status") or "",
            "review_summary": clip(review.get("summary"), 320),
            "dimensions": dimensions,
            "gate": "fail" if errors else "pass" if gate else "",
            "gate_errors": errors,
            "gate_warnings": warnings,
            "violations": [{
                "severity": v.get("severity") or "",
                "type": v.get("type") or "",
                "description": clip(v.get("description") or v.get("evidence"), 260),
            } for v in violations[:16] if isinstance(v, dict)],
            "aigc_percent": aigc.get("aigc_percent"),
            "aigc_risk": aigc.get("risk_label") or "",
            "aigc_confidence": aigc.get("confidence") or "",
            "judge_verdict": judge.get("verdict") or "",
            "judge_risk": judge.get("risk_level") or "",
            "judge_probability": judge.get("ai_probability_percent"),
            "judge_confidence": judge.get("confidence") or "",
            "judge_model": judge.get("model") or "",
            "judge_summary": clip(judge.get("summary"), 280),
            "freshness": freshness,
            "metrics": {
                "ai_voice_score": metrics.get("ai_voice_score"),
                "reader_experience_score": metrics.get("reader_experience_score"),
                "revision_round": metrics.get("revision_round"),
                "protagonist_waver": metrics.get("protagonist_waver"),
                "dialogue_ratio": metrics.get("dialogue_ratio"),
                "figurative_density": metrics.get("figurative_density"),
                "paragraph_count": metrics.get("paragraph_count"),
                "sentence_count": metrics.get("sentence_count"),
            },
        })
    values = [r["aigc_percent"] for r in rows if isinstance(r.get("aigc_percent"), (int, float))]
    return {
        "chapters": rows,
        "accepted": sum(1 for r in rows if r["review_verdict"] == "accept"),
        "rewrite": sum(1 for r in rows if r["review_verdict"] == "rewrite"),
        "gate_passed": sum(1 for r in rows if r["gate"] == "pass"),
        "gate_failed": sum(1 for r in rows if r["gate"] == "fail"),
        "stale": sum(1 for r in rows if r["freshness"] == "stale"),
        "average_aigc_percent": round(sum(values) / len(values), 2) if values else None,
    }


# ---------- HTTP ----------

class Handler(BaseHTTPRequestHandler):
    server_version = "novel-studio-dashboard/3.0"

    def log_message(self, fmt, *args):  # 静默访问日志
        pass

    def _send(self, code: int, body: bytes, ctype: str):
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(body)

    def _json(self, obj, code: int = 200):
        self._send(code, json.dumps(obj, ensure_ascii=False).encode("utf-8"),
                   "application/json; charset=utf-8")

    def do_GET(self):
        path = urllib.parse.unquote(self.path.split("?", 1)[0])
        try:
            if path in ("/", "/index.html"):
                page = (STATIC_DIR / "index.html").read_bytes()
                return self._send(200, page, "text/html; charset=utf-8")
            if path == "/api/health":
                return self._json({"ok": True, "runs_dir": str(RUNS_DIR), "time": time.time(),
                                   "version": SERVICE_VERSION, "script": SERVICE_SCRIPT})
            if path == "/api/novels":
                return self._json({"runs_dir": str(RUNS_DIR),
                                   "novels": [summarize_run(r) for r in list_runs()]})
            m = re.match(r"^/api/novels/([^/]+)(?:/(setting|cast|plan|offscreen|growth|quality))?$", path)
            if m:
                run = RUNS_DIR / m.group(1)
                if not is_run_dir(run) or run.parent != RUNS_DIR:
                    return self._json({"error": "not found"}, 404)
                section = m.group(2)
                if section == "setting":
                    return self._json(setting_payload(run))
                if section == "cast":
                    return self._json(cast_payload(run))
                if section == "plan":
                    return self._json(plan_payload(run))
                if section == "offscreen":
                    return self._json(offscreen_payload(run))
                if section == "growth":
                    return self._json(growth_payload(run))
                if section == "quality":
                    return self._json(quality_payload(run))
                return self._json(run_detail(run))
            return self._json({"error": "not found"}, 404)
        except BrokenPipeError:
            pass
        except Exception as exc:  # 看板永不 500 裸奔，返回结构化错误
            return self._json({"error": str(exc)}, 500)


def main() -> None:
    ap = argparse.ArgumentParser(description="novel-studio progress dashboard")
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--port", type=int, default=8765)
    args = ap.parse_args()
    httpd = ThreadingHTTPServer((args.host, args.port), Handler)
    print(f"[dashboard] http://{args.host}:{args.port}  runs={RUNS_DIR}", flush=True)
    httpd.serve_forever()


if __name__ == "__main__":
    main()
