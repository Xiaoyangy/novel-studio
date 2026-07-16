#!/usr/bin/env python3
"""Register a user-reported external AIGC sampling result for one exact payload.

This command never submits prose to a detector. It records a result only after
the user reports it, the caller names the score scale, and the exact sampled
bytes are proven. Browser/automated detector sources are intentionally absent.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import re
from pathlib import Path


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def normalize_score(score: float, scale: str) -> float:
    if scale == "probability":
        if not 0 <= score <= 1:
            raise ValueError("probability score must be within [0,1]")
        return score * 100
    if scale == "percent":
        if not 0 <= score <= 100:
            raise ValueError("percent score must be within [0,100]")
        return score
    raise ValueError(f"unsupported score scale: {scale}")


def resolve_payload(project: Path, chapter: int, payload_file: str) -> Path:
    if payload_file:
        candidate = Path(payload_file).expanduser()
        if not candidate.is_absolute():
            candidate = project / candidate
    else:
        candidate = project / "chapters" / f"{chapter:02d}.md"
    candidate = candidate.resolve()
    if not candidate.is_file():
        raise FileNotFoundError(f"external detector payload does not exist: {candidate}")
    return candidate


def canonical_chapter_path(project: Path, chapter: int) -> Path:
    path = (project / "chapters" / f"{chapter:02d}.md").resolve()
    if not path.is_file():
        raise FileNotFoundError(f"canonical chapter does not exist: {path}")
    return path


def require_canonical_payload(project: Path, chapter: int, payload: bytes) -> tuple[Path, str]:
    """Bind registration to the exact committed chapter bytes.

    A detector upload may be staged in another file, but it cannot become a
    sampling event for the canonical chapter unless those bytes are identical
    to chapters/NN.md. This prevents newline conversion, copied plain text, or
    a stale export from being attributed to the wrong chapter hash.
    """
    canonical_path = canonical_chapter_path(project, chapter)
    canonical = canonical_path.read_bytes()
    if not canonical.strip():
        raise ValueError(f"canonical chapter is empty: {canonical_path}")
    canonical_sha256 = sha256_bytes(canonical)
    payload_sha256 = sha256_bytes(payload)
    if payload_sha256 != canonical_sha256:
        raise ValueError(
            "external detector payload is not byte-identical to the canonical chapter: "
            f"payload_sha256={payload_sha256} canonical_sha256={canonical_sha256} "
            f"canonical={canonical_path}"
        )
    return canonical_path, canonical_sha256


def _valid_sha256(value: object) -> bool:
    return (
        isinstance(value, str)
        and re.fullmatch(r"[0-9a-fA-F]{64}", value.strip()) is not None
    )


def _matching_chapter_scope(row: dict, chapter: int) -> bool:
    scope = row.get("scope")
    return (
        isinstance(scope, dict)
        and scope.get("kind") == "chapter"
        and scope.get("chapter") == chapter
    )


def _require_current_draft_checkpoint(project: Path, chapter: int, body_sha256: str) -> None:
    """Prove these bytes came from the latest completed whole-draft write.

    A whole render appends ``draft`` after its write saga; one permitted polish
    after an approved external hash appends ``edit``. Both bind the raw-file
    digest. Sampling may record a structurally blocked draft too: the result is
    evidence and never overrides local/DeepSeek/consistency gates.
    """
    path = project / "meta" / "checkpoints.jsonl"
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except FileNotFoundError as exc:
        raise ValueError(f"draft candidate has no checkpoint journal: {path}") from exc
    except OSError as exc:
        raise ValueError(f"draft candidate checkpoint journal is unreadable: {path}") from exc

    artifact = f"drafts/{chapter:02d}.draft.md"
    prose_events: list[tuple[int, dict]] = []
    for line_number, line in enumerate(lines, start=1):
        if not line.strip():
            continue
        try:
            row = json.loads(line)
        except (TypeError, ValueError) as exc:
            raise ValueError(f"checkpoint journal line {line_number} is invalid JSON") from exc
        if not isinstance(row, dict) or not _matching_chapter_scope(row, chapter):
            continue
        seq = row.get("seq")
        if not isinstance(seq, int) or isinstance(seq, bool):
            raise ValueError(f"checkpoint journal line {line_number} has an invalid seq")
        if row.get("step") in {"draft", "edit"} and row.get("artifact") == artifact:
            prose_events.append((seq, row))

    if not prose_events:
        raise ValueError("draft candidate has no completed prose checkpoint")
    _, latest = max(prose_events, key=lambda item: item[0])
    expected_digest = "sha256:" + body_sha256
    if latest.get("step") not in {"draft", "edit"} or latest.get("digest") != expected_digest:
        raise ValueError(
            "draft candidate is not bound to the latest completed draft/edit checkpoint: "
            f"expected={expected_digest} latest_step={latest.get('step')!r} "
            f"latest_digest={latest.get('digest')!r}"
        )


def require_pending_draft_payload(
    project: Path,
    chapter: int,
    payload_path: Path,
    payload: bytes,
) -> tuple[Path, str]:
    draft_path = (project / "drafts" / f"{chapter:02d}.draft.md").resolve()
    if payload_path.resolve() != draft_path:
        raise ValueError(
            "a pending-draft result must use the actual drafts/NN.draft.md path; "
            "arbitrary copies cannot become exact sampling evidence"
        )
    if not draft_path.is_file():
        raise ValueError(f"pending draft does not exist: {draft_path}")
    current = draft_path.read_bytes()
    if payload != current:
        raise ValueError("external detector payload is not byte-identical to the current draft")
    body_sha256 = sha256_bytes(current)

    intent = project / "drafts" / f"{chapter:02d}.draft_write_intent.json"
    if intent.exists():
        raise ValueError(
            "draft write intent is still pending; recover/finish the write saga before external registration"
        )

    _require_current_draft_checkpoint(project, chapter, body_sha256)
    return draft_path, body_sha256


def require_registration_payload(
    project: Path,
    chapter: int,
    payload_path: Path,
    payload: bytes,
) -> tuple[Path, str, str]:
    """Resolve the only two subjects permitted as exact sampling evidence."""
    draft_path = (project / "drafts" / f"{chapter:02d}.draft.md").resolve()
    if payload_path.resolve() == draft_path:
        subject, digest = require_pending_draft_payload(project, chapter, payload_path, payload)
        return subject, digest, "pending_draft"
    subject, digest = require_canonical_payload(project, chapter, payload)
    return subject, digest, "canonical_chapter"


def display_path(path: Path, project: Path) -> str:
    try:
        return path.relative_to(project.resolve()).as_posix()
    except ValueError:
        return str(path)


def existing_equivalent(log_path: Path, row: dict) -> dict | None:
    if not log_path.exists():
        return None
    event_identity_keys = ("chapter", "detector", "mode", "body_sha256")
    equivalence_keys = (
        "chapter", "detector", "mode", "score_percent", "verdict",
        "body_sha256", "payload_path", "evidence_sha256", "result_source",
    )
    latest_identity_event = None
    for line_number, line in enumerate(log_path.read_text(encoding="utf-8").splitlines(), start=1):
        if not line.strip():
            continue
        try:
            existing = json.loads(line)
        except (TypeError, ValueError) as exc:
            raise ValueError(f"external detection log line {line_number} is invalid JSON") from exc
        if all(
            existing.get(key, "") == row.get(key, "")
            for key in event_identity_keys
        ):
            latest_identity_event = existing
    if latest_identity_event is not None and all(
        latest_identity_event.get(key, "") == row.get(key, "")
        for key in equivalence_keys
    ):
        return latest_identity_event
    return None


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--project", required=True, help="output/novel directory")
    parser.add_argument("--chapter", type=int, required=True)
    parser.add_argument("--detector", required=True)
    parser.add_argument("--mode", required=True)
    parser.add_argument("--score", type=float, required=True)
    parser.add_argument("--score-scale", choices=("probability", "percent"), required=True)
    parser.add_argument("--verdict", choices=("ai_like", "human_like", "mixed"), required=True)
    parser.add_argument(
        "--result-source", choices=("user_reported", "manual"), required=True,
        help="attest that the score came from the user's manual sampling, never browser automation",
    )
    parser.add_argument("--payload-file", default="", help="exact submitted payload; default chapters/NN.md")
    parser.add_argument("--expected-sha256", required=True, help="pre-submission payload SHA-256")
    parser.add_argument("--evidence", default="", help="optional screenshot/report file")
    parser.add_argument("--note", default="")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if args.chapter <= 0:
        raise SystemExit("--chapter must be > 0")
    detector = args.detector.strip()
    mode = args.mode.strip()
    if not detector or not mode:
        raise SystemExit("--detector and --mode must be non-empty after trimming")
    project = Path(args.project).expanduser().resolve()
    if not project.is_dir():
        raise SystemExit(f"project directory does not exist: {project}")
    payload_path = resolve_payload(project, args.chapter, args.payload_file)
    payload = payload_path.read_bytes()
    if not payload.strip():
        raise SystemExit(f"external detector payload is empty: {payload_path}")
    try:
        _, body_sha256, payload_kind = require_registration_payload(
            project, args.chapter, payload_path, payload
        )
    except (FileNotFoundError, ValueError) as exc:
        raise SystemExit(str(exc)) from exc
    expected = args.expected_sha256.strip().lower()
    if re.fullmatch(r"[0-9a-f]{64}", expected) is None:
        raise SystemExit("--expected-sha256 must be exactly 64 hexadecimal characters")
    if expected != body_sha256:
        raise SystemExit(
            f"payload SHA mismatch: expected={expected} current={body_sha256}; "
            "freeze/re-submit the exact bytes before registering"
        )
    try:
        score_percent = normalize_score(args.score, args.score_scale)
    except ValueError as exc:
        raise SystemExit(str(exc)) from exc
    if args.verdict == "human_like" and score_percent >= 4:
        raise SystemExit("human_like verdict contradicts a score at or above the strict 4% gate")
    if args.verdict == "ai_like" and score_percent < 4:
        raise SystemExit("ai_like verdict contradicts a score below the strict 4% gate")

    evidence_path = ""
    evidence_sha256 = ""
    if args.evidence:
        evidence = Path(args.evidence).expanduser().resolve()
        if not evidence.is_file():
            raise SystemExit(f"evidence file does not exist: {evidence}")
        evidence_path = display_path(evidence, project)
        evidence_sha256 = sha256_bytes(evidence.read_bytes())

    row = {
        "chapter": args.chapter,
        "detector": detector,
        "mode": mode,
        "score": args.score,
        "score_scale": args.score_scale,
        "score_percent": score_percent,
        "verdict": args.verdict,
        "note": args.note.strip(),
        "body_sha256": body_sha256,
        "payload_path": display_path(payload_path, project),
        "payload_kind": payload_kind,
        "evidence_path": evidence_path,
        "evidence_sha256": evidence_sha256,
        "source": "user_reported_external_detector",
        "result_source": args.result_source,
        "registration_schema": 2,
        "checked_at": dt.datetime.now().astimezone().isoformat(timespec="seconds"),
    }
    log_path = project / "meta" / "external_detection_log.jsonl"
    log_path.parent.mkdir(parents=True, exist_ok=True)
    try:
        duplicate = existing_equivalent(log_path, row)
    except ValueError as exc:
        raise SystemExit(str(exc)) from exc
    if duplicate is not None:
        print("already registered:", json.dumps(duplicate, ensure_ascii=False, sort_keys=True))
        return 0
    encoded = (json.dumps(row, ensure_ascii=False, sort_keys=True) + "\n").encode("utf-8")
    fd = os.open(log_path, os.O_APPEND | os.O_CREAT | os.O_WRONLY, 0o644)
    try:
        offset = 0
        while offset < len(encoded):
            offset += os.write(fd, encoded[offset:])
        os.fsync(fd)
    finally:
        os.close(fd)
    print("registered:", json.dumps(row, ensure_ascii=False, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
