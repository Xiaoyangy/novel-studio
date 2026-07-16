#!/usr/bin/env python3
"""Register a human-triggered external AIGC result for one exact payload.

This command never submits prose to a detector. It records a result only after
the caller names the score scale and proves which bytes were submitted.
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
    current hard-gate result unless those bytes are identical to
    chapters/NN.md. This prevents newline conversion, copied plain text, or a
    stale export from being registered against the canonical chapter hash.
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


def _identity(detector: object, mode: object) -> tuple[str, str]:
    return str(detector or "").strip().casefold(), str(mode or "").strip().casefold()


def _load_json_object(path: Path, label: str) -> dict:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError as exc:
        raise ValueError(f"{label} does not exist: {path}") from exc
    except (OSError, TypeError, ValueError) as exc:
        raise ValueError(f"{label} is unreadable or invalid JSON: {path}") from exc
    if not isinstance(value, dict):
        raise ValueError(f"{label} must be a JSON object: {path}")
    return value


def _registered_marker_identities(marker: dict) -> set[tuple[str, str]]:
    raw_retests = marker.get("required_external_retests", [])
    if raw_retests is None:
        raw_retests = []
    if not isinstance(raw_retests, list):
        raise ValueError("draft retest marker required_external_retests must be a list")

    identities: set[tuple[str, str]] = set()
    for item in raw_retests:
        if not isinstance(item, dict):
            raise ValueError("draft retest marker contains a malformed detector/mode identity")
        identity = _identity(item.get("detector"), item.get("mode"))
        if not all(identity):
            raise ValueError("draft retest marker contains an empty detector/mode identity")
        identities.add(identity)

    legacy_detector = str(marker.get("required_detector") or "").strip()
    legacy_mode = str(marker.get("required_mode") or "").strip()
    if bool(legacy_detector) != bool(legacy_mode):
        raise ValueError("draft retest marker has an incomplete required_detector/required_mode pair")
    if legacy_detector:
        identities.add(_identity(legacy_detector, legacy_mode))

    named = (
        str(marker.get("evaluator") or "").strip() == "registered_external_detector"
        or bool(raw_retests)
    )
    if not named:
        raise ValueError("draft retest marker is not a named external-detector contract")
    return identities


def _matching_chapter_scope(row: dict, chapter: int) -> bool:
    scope = row.get("scope")
    return (
        isinstance(scope, dict)
        and scope.get("kind") == "chapter"
        and scope.get("chapter") == chapter
    )


def _require_current_draft_checkpoint(project: Path, chapter: int, body_sha256: str) -> None:
    """Prove these bytes came from a completed whole-draft write.

    A whole render appends ``draft`` after its write saga; one permitted polish
    after an approved external hash appends ``edit``. Both bind the raw-file
    digest. A later structural-block checkpoint means the same candidate is
    still in the bounded rerender phase and is not ready for a platform retest.
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
    structural_events: list[int] = []
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
        if row.get("step") == "draft-structural-block":
            structural_events.append(seq)

    if not prose_events:
        raise ValueError("draft candidate has no completed prose checkpoint")
    latest_seq, latest = max(prose_events, key=lambda item: item[0])
    expected_digest = "sha256:" + body_sha256
    if latest.get("step") not in {"draft", "edit"} or latest.get("digest") != expected_digest:
        raise ValueError(
            "draft candidate is not bound to the latest completed draft/edit checkpoint: "
            f"expected={expected_digest} latest_step={latest.get('step')!r} "
            f"latest_digest={latest.get('digest')!r}"
        )
    if any(seq > latest_seq for seq in structural_events):
        raise ValueError(
            "draft candidate still has a current structural-block checkpoint; "
            "finish bounded local rerender/replan before external registration"
        )


def _normalized_logged_score(row: dict, line_number: int) -> float:
    try:
        score = float(row["score"])
    except (KeyError, TypeError, ValueError) as exc:
        raise ValueError(f"external detection log line {line_number} has an invalid score") from exc
    scale = str(row.get("score_scale") or "").strip().lower()
    if scale:
        try:
            percent = normalize_score(score, scale)
        except ValueError as exc:
            raise ValueError(f"external detection log line {line_number} has an invalid score") from exc
    else:
        percent = score * 100 if 0 <= score <= 1 else score
    if not 0 <= percent <= 100:
        raise ValueError(f"external detection log line {line_number} has an invalid normalized score")
    if "score_percent" in row:
        try:
            stored_percent = float(row["score_percent"])
        except (TypeError, ValueError) as exc:
            raise ValueError(f"external detection log line {line_number} has an invalid score_percent") from exc
        if abs(stored_percent - percent) > 0.0001:
            raise ValueError(f"external detection log line {line_number} has inconsistent score_percent")
    verdict = str(row.get("verdict") or "").strip().lower()
    if verdict not in {"ai_like", "human_like", "mixed"}:
        raise ValueError(f"external detection log line {line_number} has an invalid verdict")
    if verdict == "human_like" and percent >= 4:
        raise ValueError(f"external detection log line {line_number} has a contradictory human_like verdict")
    if verdict == "ai_like" and percent < 4:
        raise ValueError(f"external detection log line {line_number} has a contradictory ai_like verdict")
    return percent


def _latest_current_detection_rows(
    project: Path, chapter: int, body_sha256: str
) -> dict[tuple[str, str], tuple[dict, float]]:
    path = project / "meta" / "external_detection_log.jsonl"
    if not path.exists():
        return {}
    latest: dict[tuple[str, str], tuple[dict, float]] = {}
    for line_number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
        if not line.strip():
            continue
        try:
            row = json.loads(line)
        except (TypeError, ValueError) as exc:
            raise ValueError(f"external detection log line {line_number} is invalid JSON") from exc
        if not isinstance(row, dict):
            raise ValueError(f"external detection log line {line_number} must be a JSON object")
        if row.get("chapter") != chapter or str(row.get("body_sha256") or "").strip().lower() != body_sha256:
            continue
        identity = _identity(row.get("detector"), row.get("mode"))
        if not all(identity):
            raise ValueError(f"external detection log line {line_number} has empty detector/mode")
        latest[identity] = (row, _normalized_logged_score(row, line_number))
    return latest


def require_pending_draft_payload(
    project: Path,
    chapter: int,
    detector: str,
    mode: str,
    payload_path: Path,
    payload: bytes,
) -> tuple[Path, str]:
    draft_path = (project / "drafts" / f"{chapter:02d}.draft.md").resolve()
    if payload_path.resolve() != draft_path:
        raise ValueError(
            "a pending-draft result must use the actual drafts/NN.draft.md path; "
            "arbitrary copies cannot become hard-gate evidence"
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

    marker_path = project / "reviews" / "drafts" / f"{chapter:02d}_full_rerender_required.json"
    marker = _load_json_object(marker_path, "named draft retest marker")
    if marker.get("chapter") != chapter:
        raise ValueError("draft retest marker chapter does not match --chapter")
    identities = _registered_marker_identities(marker)
    wanted = _identity(detector, mode)
    if wanted not in identities:
        labels = ", ".join(f"{item[0]}/{item[1]}" for item in sorted(identities)) or "none"
        raise ValueError(
            "draft retest marker does not require this detector/mode: "
            f"requested={detector}/{mode} required={labels}"
        )
    revision_plan = marker.get("revision_plan")
    if (
        marker.get("advice_complete") is not True
        or not isinstance(revision_plan, list)
        or not revision_plan
    ):
        raise ValueError("draft retest marker advice is incomplete; gate is not rejudge_pending")
    evaluated = str(marker.get("evaluated_body_sha256") or "").strip().lower()
    if not _valid_sha256(evaluated):
        raise ValueError("draft retest marker has an invalid evaluated_body_sha256")
    if evaluated == body_sha256:
        raise ValueError(
            "draft marker still evaluates the current hash; gate is rerender_authorized, not rejudge_pending"
        )

    final_path = project / "chapters" / f"{chapter:02d}.md"
    final_sha256 = sha256_bytes(final_path.read_bytes()) if final_path.is_file() else ""
    initial = str(marker.get("initial_draft_body_sha256") or "").strip().lower()
    if initial and not _valid_sha256(initial):
        raise ValueError("draft retest marker has an invalid initial_draft_body_sha256")
    final_rows = (
        _latest_current_detection_rows(project, chapter, final_sha256)
        if final_sha256 and final_sha256 != body_sha256
        else {}
    )
    final_blocking = any(score >= 4 for _, score in final_rows.values())
    if initial == body_sha256 and (final_sha256 == evaluated or final_blocking):
        raise ValueError(
            "draft is the retained pre-rerender bridge; gate is rerender_authorized, not rejudge_pending"
        )
    if not initial and final_blocking:
        raise ValueError(
            "formal chapter has a blocking registered result and the marker has no initial draft hash; "
            "Inspect would treat this draft as the retained rerender bridge"
        )
    if final_sha256 and final_sha256 == body_sha256:
        raise ValueError("draft is byte-identical to the formal chapter, not a new pending candidate")

    _require_current_draft_checkpoint(project, chapter, body_sha256)
    current_rows = _latest_current_detection_rows(project, chapter, body_sha256)
    if wanted in current_rows:
        raise ValueError(
            "this detector/mode already has a registered result for the current draft hash; "
            "the identity is not pending"
        )
    blocking = [
        f"{identity[0]}/{identity[1]}={score:.2f}%"
        for identity, (_, score) in current_rows.items()
        if score >= 4
    ]
    if blocking:
        raise ValueError(
            "current draft already has a blocking registered result and is rerender_authorized, "
            "not rejudge_pending: " + ", ".join(sorted(blocking))
        )
    return draft_path, body_sha256


def require_registration_payload(
    project: Path,
    chapter: int,
    detector: str,
    mode: str,
    payload_path: Path,
    payload: bytes,
) -> tuple[Path, str, str]:
    """Resolve the only two subjects permitted to enter the hard gate."""
    draft_path = (project / "drafts" / f"{chapter:02d}.draft.md").resolve()
    if payload_path.resolve() == draft_path:
        subject, digest = require_pending_draft_payload(
            project, chapter, detector, mode, payload_path, payload
        )
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
    identity_keys = (
        "chapter", "detector", "mode", "score_percent", "verdict",
        "body_sha256", "payload_path", "evidence_sha256",
    )
    for line_number, line in enumerate(log_path.read_text(encoding="utf-8").splitlines(), start=1):
        if not line.strip():
            continue
        try:
            existing = json.loads(line)
        except (TypeError, ValueError) as exc:
            raise ValueError(f"external detection log line {line_number} is invalid JSON") from exc
        if all(existing.get(key, "") == row.get(key, "") for key in identity_keys):
            return existing
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
            project, args.chapter, detector, mode, payload_path, payload
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
        "source": "human_registered_external_detector",
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
