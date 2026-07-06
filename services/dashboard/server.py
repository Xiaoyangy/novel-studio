#!/usr/bin/env python3
"""novel-studio 创作进度看板（零依赖，stdlib only）。

数据源统一为仓库根目录 data/runs/ 下的书目工程：每个 <runs>/<书名>/ 内含
output/novel/{meta,chapters,reviews,summaries,logs}。本服务只读、不写任何数据。

由 `novel-studio service start` 拉起：python3 server.py --host H --port P。
Go 侧健康检查依赖 /api/health 与 /api/novels 均返回 2xx。
"""
from __future__ import annotations

import argparse
import json
import os
import re
import time
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RUNS_DIR = Path(os.environ.get("NOVEL_STUDIO_RUNS_DIR", ROOT / "data" / "runs"))
STATIC_DIR = Path(__file__).resolve().parent / "static"

ACTIVE_WINDOW_SECONDS = 180  # 日志/进度文件 3 分钟内有更新即视为运行中
LOG_TAIL_LINES = 80


# ---------- 数据读取（全部防御式，缺文件返回空） ----------

def read_json(path: Path):
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except (OSError, json.JSONDecodeError):
        return None


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
    for p in (nd / "chapters").glob("[0-9][0-9].md"):
        try:
            out.append(int(p.stem))
        except ValueError:
            pass
    return sorted(out)


def review_state(nd: Path, ch: int):
    """返回 (verdict, gate, gate_warnings)。verdict 来自 reviews/NN.json；
    gate 依据 NN_ai_gate.json 的 rule_violations：含 error 级 → fail，否则 pass。"""
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
    return verdict, gate, warns


def chapter_title(nd: Path, ch: int) -> str:
    s = read_json(nd / "summaries" / f"{ch:02d}.json")
    if isinstance(s, dict):
        title = str(s.get("title", "")).strip()
        if title:
            return title
    try:
        with open(nd / "chapters" / f"{ch:02d}.md", encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if line:
                    return re.sub(r"^#+\s*", "", line)[:40]
    except OSError:
        pass
    return ""


def count_words(nd: Path, ch: int) -> int:
    try:
        text = (nd / "chapters" / f"{ch:02d}.md").read_text(encoding="utf-8")
        return len(re.sub(r"\s", "", text))
    except OSError:
        return 0


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


def summarize_run(run: Path) -> dict:
    nd = novel_dir(run)
    prog = read_json(nd / "meta" / "progress.json") or {}
    pipe = read_json(nd / "meta" / "pipeline.json") or {}
    usage = read_json(nd / "meta" / "usage.json") or {}
    overall = usage.get("overall") or {}

    completed = prog.get("completed_chapters") or []
    if isinstance(completed, int):
        completed_count = completed
    else:
        completed_count = len(completed)
    files = chapter_files(nd)

    updated = latest_mtime(
        nd / "meta" / "progress.json",
        nd / "logs" / "headless.log",
        nd / "meta" / "pipeline.json",
    )
    reviews_dir = nd / "reviews"
    accepted = 0
    gate_pass = 0
    if reviews_dir.is_dir():
        for ch in files:
            verdict, gate, _ = review_state(nd, ch)
            if verdict == "accept":
                accepted += 1
            if gate == "pass":
                gate_pass += 1

    word_counts = prog.get("chapter_word_counts") or {}
    return {
        "name": prog.get("novel_name") or run.name,
        "dir": run.name,
        "phase": prog.get("phase") or "unknown",
        "flow": prog.get("flow") or "",
        "generation_id": prog.get("generation_id") or "",
        "generation_mode": prog.get("generation_mode") or "",
        "volume": prog.get("current_volume") or 0,
        "arc": prog.get("current_arc") or 0,
        "current_chapter": prog.get("current_chapter") or 0,
        "chapters_total": prog.get("total_chapters") or 0,
        "chapters_completed": completed_count,
        "chapters_on_disk": len(files),
        "words_total": prog.get("total_word_count") or 0,
        "chapter_words": {str(k): v for k, v in sorted(word_counts.items(), key=lambda kv: int(kv[0]))},
        "cost_usd": round(float(overall.get("cost_usd") or 0.0), 4),
        "saved_usd": round(float(overall.get("saved_usd") or 0.0), 4),
        "tokens_in": overall.get("input") or 0,
        "tokens_out": overall.get("output") or 0,
        "pipeline_stages": pipe.get("stages") or [],
        "pipeline_completed": pipe.get("completed") or [],
        "reviews_accepted": accepted,
        "gate_passed": gate_pass,
        "updated_at": updated,
        "active": bool(updated and time.time() - updated < ACTIVE_WINDOW_SECONDS),
    }


def run_detail(run: Path) -> dict:
    nd = novel_dir(run)
    base = summarize_run(run)
    usage = read_json(nd / "meta" / "usage.json") or {}
    chapters = []
    for ch in chapter_files(nd):
        verdict, gate, warns = review_state(nd, ch)
        chapters.append({
            "n": ch,
            "title": chapter_title(nd, ch),
            "words": base["chapter_words"].get(str(ch)) or count_words(nd, ch),
            "verdict": verdict,
            "gate": gate,
            "gate_warnings": warns,
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
    base.update({
        "chapters": chapters,
        "per_agent": per_agent,
        "deliveries": deliveries,
        "log": log_tail(nd),
    })
    return base


# ---------- HTTP ----------

class Handler(BaseHTTPRequestHandler):
    server_version = "novel-studio-dashboard/2.0"

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
                return self._json({"ok": True, "runs_dir": str(RUNS_DIR), "time": time.time()})
            if path == "/api/novels":
                return self._json({"runs_dir": str(RUNS_DIR),
                                   "novels": [summarize_run(r) for r in list_runs()]})
            m = re.match(r"^/api/novels/([^/]+)$", path)
            if m:
                run = RUNS_DIR / m.group(1)
                if not is_run_dir(run) or run.parent != RUNS_DIR:
                    return self._json({"error": "not found"}, 404)
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
