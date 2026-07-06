#!/usr/bin/env python3
"""Validate skill context manifests and exported context files."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Iterable


ROOT = Path(__file__).resolve().parents[1]
SKILLS = ROOT / "skills"
SHARED_FILE_GROUPS = ROOT / "scripts" / "shared_skill_files.json"
DOC_DRIFT_RULES = {
    "docs/architecture.md": [
        "../agentcore",
        "../litellm",
        "go.work 兄弟目录",
    ],
    "docs/context-management.md": [
        "../agentcore/context",
    ],
}
PIPELINE_ADAPTER_GUARDS = {
    "skills/story-long-write/SKILL.md": [
        "最高优先级：novel-studio 强制 pipeline",
        "禁止直接生成、续写或改写正文",
        "novel-studio --pipeline",
    ],
    "skills/story-short-write/SKILL.md": [
        "最高优先级：novel-studio 强制 pipeline",
        "禁止直接生成、续写或改写正文",
        "novel-studio --pipeline",
    ],
    "skills/story-douban-long-write/SKILL.md": [
        "最高优先级：novel-studio 强制 pipeline",
        "禁止直接生成、续写或改写正文",
        "novel-studio --pipeline",
    ],
    "skills/story/SKILL.md": [
        "原生写作请求必须路由到 novel-studio pipeline",
        "即使用户显式点名",
        "禁止直接生成、续写或改写正文",
        "novel-studio --pipeline",
    ],
}
DIRECT_WRITE_ROUTE_RE = re.compile(
    r"\|[^|\n]*\|\s*story-(?:long|short)-write(?:\s*/\s*story-(?:long|short)-write)?\s*\|"
)
DIRECT_WRITE_PROMPT_RE = re.compile(
    r"提示用户「拆解完成，可调用 `/story-short-write` 写下一篇」"
)

LOCAL_REF_RE = re.compile(
    r"(?<![A-Za-z0-9_/-])((?:references|scripts)/[^`'\"\\s)\\]]+)"
)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--skip-export",
        action="store_true",
        help="skip novel-studio skills export validation",
    )
    args = parser.parse_args()

    errors: list[str] = []
    skill_dirs = sorted(
        path for path in SKILLS.iterdir() if path.is_dir() and (path / "SKILL.md").exists()
    )

    if not (SKILLS / "CONTEXT_PROTOCOL.md").exists():
        errors.append("missing skills/CONTEXT_PROTOCOL.md")

    for skill_dir in skill_dirs:
        errors.extend(validate_skill(skill_dir))

    errors.extend(validate_shared_skill_files())
    errors.extend(validate_doc_drift())
    errors.extend(validate_pipeline_adapter_guards())

    if not args.skip_export:
        errors.extend(validate_export(skill_dirs))

    if errors:
        for error in errors:
            print(f"ERROR: {error}", file=sys.stderr)
        return 1
    print(f"validated {len(skill_dirs)} skill context manifests")
    return 0


def validate_skill(skill_dir: Path) -> list[str]:
    errors: list[str] = []
    name = skill_dir.name
    context_md = skill_dir / "CONTEXT.md"
    context_json = skill_dir / "context.json"
    skill_md = skill_dir / "SKILL.md"

    if not context_md.exists():
        errors.append(f"{name}: missing CONTEXT.md")
    if not context_json.exists():
        errors.append(f"{name}: missing context.json")
        return errors

    try:
        manifest = json.loads(context_json.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        errors.append(f"{name}: invalid context.json: {exc}")
        return errors

    if manifest.get("skill") != name:
        errors.append(f"{name}: context.json skill must be {name!r}")
    if manifest.get("entrypoint") != "SKILL.md":
        errors.append(f"{name}: entrypoint must be SKILL.md")

    always_read = manifest.get("always_read")
    if not isinstance(always_read, list):
        errors.append(f"{name}: always_read must be a list")
        always_read = []
    for required in ["SKILL.md", "CONTEXT.md", "context.json"]:
        if required not in always_read:
            errors.append(f"{name}: always_read missing {required}")

    listed_paths = set()
    for key in ["always_read", "required_files", "state_files", "output_contract", "compaction_resume"]:
        value = manifest.get(key, [])
        if not isinstance(value, list):
            errors.append(f"{name}: {key} must be a list")
            continue
        if key in {"always_read", "required_files"}:
            listed_paths.update(path for path in value if isinstance(path, str))

    conditional = manifest.get("conditional_files", [])
    if not isinstance(conditional, list):
        errors.append(f"{name}: conditional_files must be a list")
        conditional = []
    for idx, entry in enumerate(conditional):
        if not isinstance(entry, dict):
            errors.append(f"{name}: conditional_files[{idx}] must be an object")
            continue
        if not entry.get("when"):
            errors.append(f"{name}: conditional_files[{idx}] missing when")
        paths = entry.get("paths", [])
        if not isinstance(paths, list):
            errors.append(f"{name}: conditional_files[{idx}].paths must be a list")
            continue
        listed_paths.update(path for path in paths if isinstance(path, str))

    for rel in sorted(path for path in listed_paths if is_real_path(path)):
        target = (skill_dir / rel).resolve()
        if not target.exists():
            errors.append(f"{name}: listed path does not exist: {rel}")

    local_refs = find_existing_local_refs(skill_md, skill_dir)
    missing_from_manifest = sorted(local_refs - listed_paths)
    for rel in missing_from_manifest:
        errors.append(f"{name}: SKILL.md references {rel} but context.json does not list it")

    return errors


def validate_doc_drift() -> list[str]:
    errors: list[str] = []
    for rel, forbidden in DOC_DRIFT_RULES.items():
        target = ROOT / rel
        if not target.exists():
            errors.append(f"missing {rel}")
            continue
        text = target.read_text(encoding="utf-8")
        for snippet in forbidden:
            if snippet in text:
                errors.append(
                    f"{rel}: outdated dependency wording {snippet!r}; agentcore is a go.mod module dependency"
                )
    return errors


def validate_pipeline_adapter_guards() -> list[str]:
    errors: list[str] = []
    for rel, required_snippets in PIPELINE_ADAPTER_GUARDS.items():
        target = ROOT / rel
        if not target.exists():
            errors.append(f"missing {rel}")
            continue
        text = target.read_text(encoding="utf-8")
        for snippet in required_snippets:
            if snippet not in text:
                errors.append(f"{rel}: missing pipeline guard snippet {snippet!r}")

    for target in sorted(SKILLS.rglob("*.md")):
        rel = target.relative_to(ROOT)
        text = target.read_text(encoding="utf-8")
        if DIRECT_WRITE_ROUTE_RE.search(text):
            errors.append(f"{rel}: direct story-* write route must point to novel-studio --pipeline")
        if DIRECT_WRITE_PROMPT_RE.search(text):
            errors.append(f"{rel}: direct story-short-write prompt must point to novel-studio --pipeline")
    return errors


def validate_shared_skill_files() -> list[str]:
    errors: list[str] = []
    if not SHARED_FILE_GROUPS.exists():
        return [f"missing {SHARED_FILE_GROUPS.relative_to(ROOT)}"]

    try:
        payload = json.loads(SHARED_FILE_GROUPS.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        return [f"{SHARED_FILE_GROUPS.relative_to(ROOT)}: invalid JSON: {exc}"]

    groups = payload.get("groups") if isinstance(payload, dict) else payload
    if not isinstance(groups, list):
        return [f"{SHARED_FILE_GROUPS.relative_to(ROOT)}: groups must be a list"]

    seen_names: set[str] = set()
    for idx, group in enumerate(groups):
        if not isinstance(group, dict):
            errors.append(f"shared_files[{idx}]: must be an object")
            continue

        name = group.get("name")
        if not isinstance(name, str) or not name:
            errors.append(f"shared_files[{idx}]: missing name")
            name = f"#{idx}"
        elif name in seen_names:
            errors.append(f"shared_files[{idx}]: duplicate name {name!r}")
        seen_names.add(name)

        paths = group.get("paths")
        if not isinstance(paths, list) or not all(isinstance(path, str) for path in paths):
            errors.append(f"shared_files[{name}]: paths must be a list of strings")
            continue
        if len(paths) < 2:
            errors.append(f"shared_files[{name}]: must list at least two paths")
            continue

        canonical = group.get("canonical", paths[0])
        if not isinstance(canonical, str) or not canonical:
            errors.append(f"shared_files[{name}]: canonical must be a string")
            continue
        if canonical not in paths:
            errors.append(f"shared_files[{name}]: canonical must be listed in paths")
            continue

        canonical_digest: str | None = None
        digests: dict[str, str] = {}
        for rel in paths:
            target = resolve_shared_file(name, rel, errors)
            if target is None:
                continue
            digest = hashlib.sha256(target.read_bytes()).hexdigest()
            digests[rel] = digest
            if rel == canonical:
                canonical_digest = digest

        if canonical_digest is None:
            continue
        for rel, digest in sorted(digests.items()):
            if digest != canonical_digest:
                errors.append(
                    f"shared_files[{name}]: {rel} drifted from {canonical} "
                    f"({digest[:12]} != {canonical_digest[:12]})"
                )

    return errors


def resolve_shared_file(group_name: str, rel: str, errors: list[str]) -> Path | None:
    if Path(rel).is_absolute() or ".." in Path(rel).parts:
        errors.append(f"shared_files[{group_name}]: invalid relative path {rel!r}")
        return None
    target = (ROOT / rel).resolve()
    try:
        target.relative_to(ROOT)
    except ValueError:
        errors.append(f"shared_files[{group_name}]: path escapes project root: {rel}")
        return None
    if Path(ROOT / rel).is_symlink():
        errors.append(f"shared_files[{group_name}]: shared path must be a real file, not symlink: {rel}")
        return None
    if not target.exists():
        errors.append(f"shared_files[{group_name}]: missing file: {rel}")
        return None
    if not target.is_file():
        errors.append(f"shared_files[{group_name}]: not a file: {rel}")
        return None
    return target


def is_real_path(value: str) -> bool:
    if not value or value.startswith("<"):
        return False
    if any(marker in value for marker in ["{", "}", "*"]):
        return False
    if value.startswith("data/") or value.startswith(".skill-context/"):
        return False
    if value.startswith("output/") or value.startswith("deconstruction-library/"):
        return False
    return "/" in value or value.endswith((".md", ".json", ".py", ".js", ".yaml", ".toml", ".ts", ".sh"))


def find_existing_local_refs(skill_md: Path, skill_dir: Path) -> set[str]:
    raw = skill_md.read_text(encoding="utf-8")
    refs: set[str] = set()
    for match in LOCAL_REF_RE.finditer(raw):
        rel = clean_ref(match.group(1))
        if not rel or any(marker in rel for marker in ["{", "}", "*"]):
            continue
        if (skill_dir / rel).exists():
            refs.add(rel)
    return refs


def clean_ref(value: str) -> str:
    return value.rstrip(".,;:，。；：、）)]")


def validate_export(skill_dirs: Iterable[Path]) -> list[str]:
    errors: list[str] = []
    with tempfile.TemporaryDirectory(prefix="novel-studio-skills-") as tmp:
        dest = Path(tmp)
        cmd = ["go", "run", "./cmd/novel-studio", "skills", "export", "--to", str(dest)]
        proc = subprocess.run(cmd, cwd=ROOT, text=True, capture_output=True, check=False)
        if proc.returncode != 0:
            errors.append(f"skills export failed: {proc.stderr.strip() or proc.stdout.strip()}")
            return errors
        if not (dest / "CONTEXT_PROTOCOL.md").exists():
            errors.append("export: missing CONTEXT_PROTOCOL.md")
        for skill_dir in skill_dirs:
            for rel in ["SKILL.md", "CONTEXT.md", "context.json"]:
                exported = dest / skill_dir.name / rel
                if not exported.exists():
                    errors.append(f"export: missing {skill_dir.name}/{rel}")
        for rel in [
            "review/scripts/text_signals.py",
            "review/references/signals-zh.md",
            "scripts/typo_scan.py",
        ]:
            if not (dest / rel).exists():
                errors.append(f"export: missing compatibility file {rel}")
        review_context = dest / "review" / "context.json"
        if review_context.exists():
            try:
                manifest = json.loads(review_context.read_text(encoding="utf-8"))
            except json.JSONDecodeError as exc:
                errors.append(f"export: invalid review/context.json: {exc}")
            else:
                for rel in exported_manifest_paths(manifest):
                    if is_real_path(rel) and not (dest / "review" / rel).exists():
                        errors.append(f"export: review/context.json path missing: {rel}")
    errors.extend(validate_context_cli(skill_dirs))
    return errors


def validate_context_cli(skill_dirs: Iterable[Path]) -> list[str]:
    errors: list[str] = []
    skill_dirs = list(skill_dirs)
    cmd = ["go", "run", "./cmd/novel-studio", "skills", "context", "--all", "--json"]
    proc = subprocess.run(cmd, cwd=ROOT, text=True, capture_output=True, check=False)
    if proc.returncode != 0:
        return [f"skills context --all failed: {proc.stderr.strip() or proc.stdout.strip()}"]
    try:
        payload = json.loads(proc.stdout)
    except json.JSONDecodeError as exc:
        return [f"skills context --all invalid JSON: {exc}"]
    if not isinstance(payload, list):
        return ["skills context --all JSON must be a list"]
    by_skill = {
        item.get("manifest", {}).get("skill"): item
        for item in payload
        if isinstance(item, dict)
    }
    for skill_dir in skill_dirs:
        item = by_skill.get(skill_dir.name)
        if item is None:
            errors.append(f"skills context --all missing {skill_dir.name}")
            continue
        manifest = item.get("manifest", {})
        read_order = item.get("read_order", [])
        if manifest.get("skill") != skill_dir.name:
            errors.append(f"skills context {skill_dir.name}: manifest skill mismatch")
        for rel in [
            "skills/CONTEXT_PROTOCOL.md",
            f"skills/{skill_dir.name}/SKILL.md",
            f"skills/{skill_dir.name}/CONTEXT.md",
            f"skills/{skill_dir.name}/context.json",
        ]:
            if rel not in read_order:
                errors.append(f"skills context {skill_dir.name}: read_order missing {rel}")
    errors.extend(validate_context_content_cli())
    return errors


def validate_context_content_cli() -> list[str]:
    errors: list[str] = []
    cases = {
        "story-long-write": [
            "skills/CONTEXT_PROTOCOL.md",
            "skills/story-long-write/SKILL.md",
            "skills/story-long-write/references/human-feel-craft.md",
        ],
        "review": [
            "skills/review/SKILL.md",
            "quality/audit/README.md",
            "quality/audit/scripts/aigc_value.py",
        ],
    }
    for skill, expected_paths in cases.items():
        cmd = [
            "go",
            "run",
            "./cmd/novel-studio",
            "skills",
            "context",
            skill,
            "--content",
            "--include-conditional",
            "--json",
        ]
        proc = subprocess.run(cmd, cwd=ROOT, text=True, capture_output=True, check=False)
        if proc.returncode != 0:
            errors.append(
                f"skills context {skill} --content failed: {proc.stderr.strip() or proc.stdout.strip()}"
            )
            continue
        try:
            payload = json.loads(proc.stdout)
        except json.JSONDecodeError as exc:
            errors.append(f"skills context {skill} --content invalid JSON: {exc}")
            continue
        files = payload.get("files", [])
        if not isinstance(files, list):
            errors.append(f"skills context {skill} --content files must be a list")
            continue
        by_path = {
            item.get("path"): item
            for item in files
            if isinstance(item, dict)
        }
        for path in expected_paths:
            item = by_path.get(path)
            if item is None:
                errors.append(f"skills context {skill} --content missing {path}")
                continue
            content = item.get("content")
            if not isinstance(content, str) or not content.strip():
                errors.append(f"skills context {skill} --content empty content for {path}")
    errors.extend(validate_context_state_dir_cli())
    return errors


def validate_context_state_dir_cli() -> list[str]:
    errors: list[str] = []
    with tempfile.TemporaryDirectory(prefix="novel-studio-skill-state-") as tmp:
        root = Path(tmp)
        state_dir = root / ".skill-context"
        tracking_dir = root / "追踪"
        state_dir.mkdir(parents=True)
        tracking_dir.mkdir(parents=True)
        (state_dir / "story-long-write.md").write_text(
            "# story-long-write execution context\n- stage: validation\n",
            encoding="utf-8",
        )
        (tracking_dir / "上下文.md").write_text(
            "# 上下文\n- chapter: 4\n",
            encoding="utf-8",
        )
        cmd = [
            "go",
            "run",
            "./cmd/novel-studio",
            "skills",
            "context",
            "story-long-write",
            "--content",
            "--state-dir",
            str(root),
            "--json",
        ]
        proc = subprocess.run(cmd, cwd=ROOT, text=True, capture_output=True, check=False)
        if proc.returncode != 0:
            return [
                f"skills context story-long-write --state-dir failed: {proc.stderr.strip() or proc.stdout.strip()}"
            ]
        try:
            payload = json.loads(proc.stdout)
        except json.JSONDecodeError as exc:
            return [f"skills context story-long-write --state-dir invalid JSON: {exc}"]
        files = payload.get("files", [])
        if not isinstance(files, list):
            return ["skills context story-long-write --state-dir files must be a list"]
        by_path = {
            item.get("path"): item
            for item in files
            if isinstance(item, dict)
        }
        for path in [".skill-context/story-long-write.md", "追踪/上下文.md"]:
            item = by_path.get(path)
            if item is None:
                errors.append(f"skills context story-long-write --state-dir missing {path}")
                continue
            if item.get("state") is not True:
                errors.append(f"skills context story-long-write --state-dir did not mark {path} as state")
            if not item.get("source_path"):
                errors.append(f"skills context story-long-write --state-dir missing source_path for {path}")
            content = item.get("content")
            if not isinstance(content, str) or not content.strip():
                errors.append(f"skills context story-long-write --state-dir empty content for {path}")
        missing = payload.get("missing_state_files", [])
        if "_progress.md" not in missing:
            errors.append(
                f"skills context story-long-write --state-dir should report missing _progress.md, got {missing!r}"
            )
    return errors


def exported_manifest_paths(manifest: dict) -> set[str]:
    paths: set[str] = set()
    for key in ["always_read", "required_files"]:
        value = manifest.get(key, [])
        if isinstance(value, list):
            paths.update(path for path in value if isinstance(path, str))
    conditional = manifest.get("conditional_files", [])
    if isinstance(conditional, list):
        for entry in conditional:
            if isinstance(entry, dict) and isinstance(entry.get("paths"), list):
                paths.update(path for path in entry["paths"] if isinstance(path, str))
    return paths


if __name__ == "__main__":
    raise SystemExit(main())
