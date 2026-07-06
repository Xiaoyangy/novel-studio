#!/usr/bin/env python3
import argparse
import json
import re
from datetime import datetime
from pathlib import Path


STAGES = [
    "intake",
    "route",
    "workspace",
    "story-bible",
    "title-package",
    "outline",
    "draft-chapters",
    "merge",
    "de-ai-pass",
    "typo-first",
    "audit",
    "revise-until-5",
    "typo-second",
    "image-package",
    "final-gate",
]

MAX_PARALLEL_AGENTS = 5
BATCH_AGENT_STATUSES = {"pending", "running", "done", "blocked"}


def slugify(value):
    value = re.sub(r"[\\/:*?\"<>|]+", "_", value.strip())
    value = re.sub(r"\s+", "_", value)
    return value or "untitled"


def now():
    return datetime.now().isoformat(timespec="seconds")


def load_state(path):
    with open(path, "r", encoding="utf-8") as f:
        return json.load(f)


def save_state(path, state):
    state["updated_at"] = now()
    with open(path, "w", encoding="utf-8") as f:
        json.dump(state, f, ensure_ascii=False, indent=2)
        f.write("\n")


def validate_max_parallel(value):
    if value < 1:
        raise SystemExit("--max-parallel-agents must be at least 1")
    if value > MAX_PARALLEL_AGENTS:
        raise SystemExit(f"--max-parallel-agents cannot exceed {MAX_PARALLEL_AGENTS}")
    return value


def init(args):
    title_slug = slugify(args.title)
    root = Path(args.root) / title_slug
    root.mkdir(parents=True, exist_ok=True)
    state_path = root / "flow_state.json"
    if state_path.exists() and not args.force:
        print(state_path)
        return
    state = {
        "title": args.title,
        "direction": args.direction,
        "selected_skills": [s for s in args.selected_skill.split(",") if s],
        "book_dir": args.book_dir,
        "state_dir": str(root),
        "status": "in_progress",
        "current_stage": "intake",
        "created_at": now(),
        "updated_at": now(),
        "assumptions": [],
        "stages": {stage: "pending" for stage in STAGES},
        "chapters": {},
        "artifacts": {},
    }
    state["stages"]["intake"] = "in_progress"
    save_state(state_path, state)
    print(state_path)


def show(args):
    state = load_state(args.state)
    print(json.dumps(state, ensure_ascii=False, indent=2))


def set_stage(args):
    state = load_state(args.state)
    if args.stage not in STAGES:
        raise SystemExit(f"unknown stage: {args.stage}")
    state["stages"][args.stage] = args.status
    state["current_stage"] = next(
        (stage for stage in STAGES if state["stages"].get(stage) in {"pending", "in_progress"}),
        "done",
    )
    if state["current_stage"] == "done":
        state["status"] = "done"
    save_state(args.state, state)
    print(state["current_stage"])


def complete(args):
    args.status = "done"
    set_stage(args)


def chapter(args):
    state = load_state(args.state)
    key = str(args.chapter).zfill(2)
    item = state["chapters"].setdefault(key, {})
    item["status"] = args.status
    if args.path:
        item["path"] = args.path
    if args.chars is not None:
        item["chars"] = args.chars
    save_state(args.state, state)
    print(json.dumps(state["chapters"][key], ensure_ascii=False))


def artifact(args):
    state = load_state(args.state)
    state["artifacts"][args.name] = args.path
    save_state(args.state, state)
    print(args.path)


def child_terminal_status(child_state):
    if child_state.get("status") == "done" or child_state.get("current_stage") == "done":
        return "done"
    if child_state.get("status") == "blocked":
        return "blocked"
    if any(status == "blocked" for status in child_state.get("stages", {}).values()):
        return "blocked"
    return ""


def refresh_batch_book(book):
    state_path = book.get("state_path")
    if not state_path:
        return
    try:
        child = load_state(state_path)
    except FileNotFoundError:
        book["agent_status"] = "blocked"
        book["block_reason"] = f"state file not found: {state_path}"
        return
    book["title"] = child.get("title", book.get("title", ""))
    book["direction"] = child.get("direction", book.get("direction", ""))
    book["book_dir"] = child.get("book_dir", book.get("book_dir", ""))
    book["current_stage"] = child.get("current_stage", book.get("current_stage", ""))
    book["flow_status"] = child.get("status", book.get("flow_status", ""))
    book["flow_updated_at"] = child.get("updated_at", book.get("flow_updated_at", ""))
    terminal = child_terminal_status(child)
    if terminal:
        book["agent_status"] = terminal
        book.pop("agent_slot", None)


def rebalance_batch_slots(state):
    max_parallel = validate_max_parallel(int(state.get("max_parallel_agents", MAX_PARALLEL_AGENTS)))
    books = state.get("books", [])
    used_slots = set()
    running = []
    for book in books:
        if book.get("agent_status") != "running":
            book.pop("agent_slot", None)
            continue
        slot = book.get("agent_slot")
        if not isinstance(slot, int) or slot < 1 or slot > max_parallel or slot in used_slots:
            book.pop("agent_slot", None)
        else:
            used_slots.add(slot)
        running.append(book)

    available_slots = [slot for slot in range(1, max_parallel + 1) if slot not in used_slots]
    for book in running:
        if "agent_slot" not in book and available_slots:
            book["agent_slot"] = available_slots.pop(0)
            used_slots.add(book["agent_slot"])

    running_count = sum(1 for book in books if book.get("agent_status") == "running")
    available_slots = [slot for slot in range(1, max_parallel + 1) if slot not in used_slots]
    for book in books:
        if running_count >= max_parallel:
            break
        if book.get("agent_status") == "pending":
            book["agent_status"] = "running"
            book["agent_slot"] = available_slots.pop(0)
            used_slots.add(book["agent_slot"])
            running_count += 1


def sync_batch(state):
    for book in state.get("books", []):
        if book.get("agent_status") in {"running", "done", "blocked"}:
            refresh_batch_book(book)
    rebalance_batch_slots(state)
    statuses = [book.get("agent_status") for book in state.get("books", [])]
    if statuses and all(status == "done" for status in statuses):
        state["status"] = "done"
    elif any(status in {"pending", "running"} for status in statuses):
        state["status"] = "in_progress"
    elif any(status == "blocked" for status in statuses):
        state["status"] = "blocked"
    else:
        state["status"] = "in_progress"
    state["running_agents"] = sum(1 for status in statuses if status == "running")
    state["pending_books"] = sum(1 for status in statuses if status == "pending")
    state["done_books"] = sum(1 for status in statuses if status == "done")
    state["blocked_books"] = sum(1 for status in statuses if status == "blocked")


def batch_init(args):
    max_parallel = validate_max_parallel(args.max_parallel_agents)
    batch_slug = slugify(args.batch_title)
    root = Path(args.root) / batch_slug
    root.mkdir(parents=True, exist_ok=True)
    state_path = root / "batch_state.json"
    if state_path.exists() and not args.force:
        print(state_path)
        return
    books = []
    for index, child_state_path in enumerate(args.state, start=1):
        child = load_state(child_state_path)
        books.append(
            {
                "index": index,
                "title": child.get("title", f"untitled-{index}"),
                "direction": child.get("direction", ""),
                "book_dir": child.get("book_dir", ""),
                "state_path": child_state_path,
                "current_stage": child.get("current_stage", ""),
                "flow_status": child.get("status", ""),
                "flow_updated_at": child.get("updated_at", ""),
                "agent_status": "pending",
            }
        )
    state = {
        "batch_title": args.batch_title,
        "state_dir": str(root),
        "status": "in_progress",
        "max_parallel_agents": max_parallel,
        "created_at": now(),
        "updated_at": now(),
        "books": books,
    }
    sync_batch(state)
    save_state(state_path, state)
    print(state_path)


def batch_show(args):
    state = load_state(args.state)
    print(json.dumps(state, ensure_ascii=False, indent=2))


def batch_sync(args):
    state = load_state(args.state)
    sync_batch(state)
    save_state(args.state, state)
    print(json.dumps(state, ensure_ascii=False, indent=2))


def batch_book(args):
    state = load_state(args.state)
    if args.status not in BATCH_AGENT_STATUSES:
        raise SystemExit(f"unknown agent status: {args.status}")
    matches = []
    for book in state.get("books", []):
        if args.book_title and book.get("title") == args.book_title:
            matches.append(book)
        if args.book_state and book.get("state_path") == args.book_state:
            matches.append(book)
    matches = list({id(book): book for book in matches}.values())
    if not matches:
        raise SystemExit("book not found")
    if len(matches) > 1:
        raise SystemExit("multiple books matched; use --book-state for an exact match")
    book = matches[0]
    book["agent_status"] = args.status
    if args.status != "running":
        book.pop("agent_slot", None)
    elif args.agent_slot is not None:
        max_parallel = validate_max_parallel(int(state.get("max_parallel_agents", MAX_PARALLEL_AGENTS)))
        if args.agent_slot < 1 or args.agent_slot > max_parallel:
            raise SystemExit(f"--agent-slot must be between 1 and {max_parallel}")
        book["agent_slot"] = args.agent_slot
    sync_batch(state)
    save_state(args.state, state)
    print(json.dumps(book, ensure_ascii=False))


def main():
    parser = argparse.ArgumentParser(description="Manage Fanqie writing flow state.")
    sub = parser.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("init")
    p.add_argument("--title", required=True)
    p.add_argument("--direction", required=True)
    p.add_argument("--selected-skill", required=True)
    p.add_argument("--book-dir", required=True)
    p.add_argument("--root", default="data/generated-output/writing_flows")
    p.add_argument("--force", action="store_true")
    p.set_defaults(func=init)

    p = sub.add_parser("show")
    p.add_argument("--state", required=True)
    p.set_defaults(func=show)

    p = sub.add_parser("stage")
    p.add_argument("--state", required=True)
    p.add_argument("--stage", required=True)
    p.add_argument("--status", choices=["pending", "in_progress", "done", "blocked"], required=True)
    p.set_defaults(func=set_stage)

    p = sub.add_parser("complete")
    p.add_argument("--state", required=True)
    p.add_argument("--stage", required=True)
    p.set_defaults(func=complete)

    p = sub.add_parser("chapter")
    p.add_argument("--state", required=True)
    p.add_argument("--chapter", type=int, required=True)
    p.add_argument("--status", choices=["pending", "in_progress", "done", "blocked"], required=True)
    p.add_argument("--path")
    p.add_argument("--chars", type=int)
    p.set_defaults(func=chapter)

    p = sub.add_parser("artifact")
    p.add_argument("--state", required=True)
    p.add_argument("--name", required=True)
    p.add_argument("--path", required=True)
    p.set_defaults(func=artifact)

    p = sub.add_parser("batch-init")
    p.add_argument("--batch-title", required=True)
    p.add_argument("--state", action="append", required=True)
    p.add_argument("--root", default="data/generated-output/writing_flows/_batches")
    p.add_argument("--max-parallel-agents", type=int, default=MAX_PARALLEL_AGENTS)
    p.add_argument("--force", action="store_true")
    p.set_defaults(func=batch_init)

    p = sub.add_parser("batch-show")
    p.add_argument("--state", required=True)
    p.set_defaults(func=batch_show)

    p = sub.add_parser("batch-sync")
    p.add_argument("--state", required=True)
    p.set_defaults(func=batch_sync)

    p = sub.add_parser("batch-book")
    p.add_argument("--state", required=True)
    p.add_argument("--book-title")
    p.add_argument("--book-state")
    p.add_argument("--status", choices=sorted(BATCH_AGENT_STATUSES), required=True)
    p.add_argument("--agent-slot", type=int)
    p.set_defaults(func=batch_book)

    args = parser.parse_args()
    if getattr(args, "cmd", "") == "batch-book" and not args.book_title and not args.book_state:
        raise SystemExit("batch-book requires --book-title or --book-state")
    args.func(args)


if __name__ == "__main__":
    main()
