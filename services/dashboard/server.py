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


CHAPTER_STEP_CHAIN = ["plan", "draft", "check", "commit", "review"]


def checkpoint_tail(nd: Path, n: int = 60) -> list[dict]:
    path = nd / "meta" / "checkpoints.jsonl"
    out = []
    try:
        with open(path, "rb") as f:
            f.seek(0, os.SEEK_END)
            f.seek(max(0, f.tell() - 32 * 1024))
            lines = f.read().decode("utf-8", errors="replace").splitlines()[-n:]
        for line in lines:
            try:
                e = json.loads(line)
            except json.JSONDecodeError:
                continue
            if isinstance(e, dict):
                out.append(e)
    except OSError:
        pass
    return out


def working_state(nd: Path, prog: dict) -> dict:
    """当前章进展：checkpoint 步骤链 + 草稿落盘事实 + 最近一次全局动作。"""
    entries = checkpoint_tail(nd)
    chapter_steps: dict[int, str] = {}
    last = entries[-1] if entries else {}
    for e in entries:
        scope = e.get("scope") or {}
        if scope.get("kind") == "chapter" and isinstance(scope.get("chapter"), int):
            chapter_steps[scope["chapter"]] = str(e.get("step") or "")
    cur = prog.get("current_chapter") or 0
    if not cur and chapter_steps:
        cur = max(chapter_steps)
    if not cur:
        cur = (len(prog.get("completed_chapters") or []) or 0) + 1
    step = chapter_steps.get(cur, "")
    if not step:
        # checkpoint 还没到章级：看 drafts 目录里的最新事实
        if (nd / "drafts" / f"{cur:02d}.md").exists():
            step = "draft"
        elif list((nd / "drafts").glob(f"{cur:02d}.plan*")) if (nd / "drafts").is_dir() else []:
            step = "plan"
    last_scope = last.get("scope") or {}
    return {
        "chapter": cur,
        "step": step,
        "chain": CHAPTER_STEP_CHAIN,
        "chain_pos": CHAPTER_STEP_CHAIN.index(step) if step in CHAPTER_STEP_CHAIN else -1,
        "last_step": str(last.get("step") or ""),
        "last_kind": str(last_scope.get("kind") or ""),
        "last_chapter": last_scope.get("chapter"),
        "last_at": str(last.get("occurred_at") or ""),
    }


def summarize_run(run: Path) -> dict:
    nd = novel_dir(run)
    prog = read_json(nd / "meta" / "progress.json") or {}
    pipe = read_json(nd / "meta" / "pipeline.json") or {}
    usage = read_json(nd / "meta" / "usage.json") or {}
    overall = usage.get("overall") or {}
    outline = read_json(nd / "outline.json") or []

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
        "chapters_planned": len(outline) if isinstance(outline, list) else 0,
        "chapters_completed": completed_count,
        "chapters_on_disk": len(files),
        "working": working_state(nd, prog),
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
    planned = []
    for ch in read_json(nd / "outline.json") or []:
        if isinstance(ch, dict):
            planned.append({
                "chapter": ch.get("chapter"),
                "title": ch.get("title") or "",
                "core_event": clip(ch.get("core_event"), 140),
            })
    base.update({
        "chapters": chapters,
        "planned": planned,
        "per_agent": per_agent,
        "deliveries": deliveries,
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


def setting_payload(run: Path) -> dict:
    nd = novel_dir(run)
    bw = read_json(nd / "book_world.json") or {}
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
            "emotion": {
                "trigger": clip(emo.get("trigger_event"), 160),
                "visible": clip(emo.get("visible_expression"), 140),
                "suppressed": clip(emo.get("suppressed_expression"), 140),
                "coping": clip(emo.get("coping_strategy"), 140),
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
    return {
        "tick": tick,
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
            m = re.match(r"^/api/novels/([^/]+)(?:/(setting|cast|plan|offscreen|growth))?$", path)
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
