#!/usr/bin/env python3
from __future__ import annotations

import json
from datetime import datetime
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
OUTPUT_ROOT = ROOT / "data" / "generated-output"
REFERENCE_ROOT = ROOT / "data" / "reference-library"
SERVICE_PROJECTS_ROOT = OUTPUT_ROOT / "short_story_service" / "projects"


def count_files(root: Path) -> int:
    if not root.exists():
        return 0
    return sum(1 for p in root.rglob("*") if p.is_file())


def size_bytes(root: Path) -> int:
    if not root.exists():
        return 0
    return sum(p.stat().st_size for p in root.rglob("*") if p.is_file())


def human_size(n: int) -> str:
    units = ["B", "KB", "MB", "GB"]
    value = float(n)
    for unit in units:
        if value < 1024 or unit == units[-1]:
            return f"{value:.1f}{unit}" if unit != "B" else f"{int(value)}B"
        value /= 1024
    return f"{n}B"


def child_dirs(root: Path) -> list[Path]:
    if not root.exists():
        return []
    return sorted([p for p in root.iterdir() if p.is_dir()], key=lambda p: p.name)


def child_files(root: Path) -> list[Path]:
    if not root.exists():
        return []
    return sorted([p for p in root.iterdir() if p.is_file()], key=lambda p: p.name)


def md_link(path: Path, from_dir: Path | None = None) -> str:
    rel = path.relative_to(ROOT)
    if from_dir is None:
        target = rel.as_posix()
    else:
        target = Path("..", rel).as_posix()
    return f"[{rel.as_posix()}]({target})"


def summarize_root(root: Path) -> list[str]:
    lines: list[str] = []
    for d in child_dirs(root):
        lines.append(
            f"- `{d.name}/`: {count_files(d)} files, {human_size(size_bytes(d))}"
        )
    for f in child_files(root):
        if f.name == "INDEX.md":
            continue
        lines.append(f"- `{f.name}`: {human_size(f.stat().st_size)}")
    return lines


def short_story_projects() -> list[dict[str, object]]:
    projects: list[dict[str, object]] = []
    for d in child_dirs(SERVICE_PROJECTS_ROOT):
        meta = d / "project.json"
        title = d.name
        status = ""
        current_stage = ""
        if meta.exists():
            try:
                data = json.loads(meta.read_text(encoding="utf-8"))
                title = data.get("title") or data.get("name") or title
                status = data.get("lifecycle_status") or data.get("agent_status") or ""
                current_stage = data.get("current_stage") or ""
            except Exception:
                status = "project.json unreadable"
        projects.append(
            {
                "id": d.name,
                "title": title,
                "status": status,
                "current_stage": current_stage,
                "files": count_files(d),
            }
        )
    return projects


def write_output_index() -> None:
    root = OUTPUT_ROOT
    root.mkdir(parents=True, exist_ok=True)
    lines = [
        "# generated-output 数据索引",
        "",
        "本目录是历史短篇产出、短篇服务项目、工作流状态和交付包的规范存储位置。新产出优先写入 `data/generated-output/` 下的对应题材目录，长篇项目仍写入 `output/novel/`。",
        "",
        "## 顶层目录",
        "",
    ]
    lines.extend(summarize_root(root) or ["- 暂无数据"])
    projects = short_story_projects()
    lines.extend(["", "## short_story_service 项目", ""])
    if projects:
        lines.append("| project_id | title | status | stage | files |")
        lines.append("|---|---|---|---|---:|")
        for p in projects:
            lines.append(
                f"| `{p['id']}` | {p['title']} | {p['status']} | {p['current_stage']} | {p['files']} |"
            )
    else:
        lines.append("- 暂无服务项目")
    lines.append("")
    root.joinpath("INDEX.md").write_text("\n".join(lines), encoding="utf-8")


def write_reference_index() -> None:
    root = REFERENCE_ROOT
    root.mkdir(parents=True, exist_ok=True)
    lines = [
        "# reference-library 参考库索引",
        "",
        "本目录是历史参考库的规范存储位置，保存参考书、题材样本、摘要和写作技巧源材料。生成正文时不得照抄参考正文，只能提炼结构、节奏、钩子、人物关系和写法规律。",
        "",
        "## 顶层目录",
        "",
    ]
    lines.extend(summarize_root(root) or ["- 暂无数据"])
    lines.append("")
    root.joinpath("INDEX.md").write_text("\n".join(lines), encoding="utf-8")


def write_inventory() -> None:
    docs = ROOT / "docs"
    docs.mkdir(parents=True, exist_ok=True)
    now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    lines = [
        "# 工程能力清单",
        "",
        f"生成时间：{now}",
        "",
        "## 工程能力总览",
        "",
        "- `services/short-story-dashboard/`：短篇项目服务与 HTML 进度看板；使用 `novel-studio service start` 启动。",
        "- `data/generated-output/`：历史短篇正文、服务项目、工作流状态、配图方案和审核报告。",
        "- `data/reference-library/`：题材参考库、写作技巧源材料和拆书样本。",
        "- `quality/audit/`：本地 AIGC / AI 味 / 重复 / 内容逻辑 / 错别字审核脚本与参考。",
        "- `skills/`：novel-studio 原生命令、story 工具箱、审核等 skill 的唯一源目录，可通过 `novel-studio skills export --to <dir>` 导出。",
        "- `assets/references/`：运行时通用写作技巧摘要、人工感标尺、生产链路、去 AI 味规则和通用规划资料。",
    ]
    lines.extend(
        [
            "",
            "## 统一规划口径",
            "",
            "- 长篇和短篇共用 `novel-studio` 规划逻辑。",
            "- 默认单章字数预算为 2100-3000 字；用户或本书规则覆盖时，以覆盖值为准。",
            "- 用户给预期总字数时，先按当前单章预算反推大致章数，再设计卷弧与章节承载量。",
            "- 字数是节奏预算，不为卡点牺牲必要剧情、人物选择、铺垫、读者读感或章节钩子。",
            "",
            "## 快速命令",
            "",
            "```bash",
            "go run ./cmd/novel-studio service start",
            "go run ./cmd/novel-studio service status",
            "go run ./cmd/novel-studio skills list",
            "python3 scripts/index_workspace_assets.py",
            "```",
            "",
            "## 数据索引",
            "",
            f"- {md_link(OUTPUT_ROOT / 'INDEX.md', docs)}",
            f"- {md_link(REFERENCE_ROOT / 'INDEX.md', docs)}",
            "",
        ]
    )
    docs.joinpath("capability-inventory.md").write_text("\n".join(lines), encoding="utf-8")


def main() -> None:
    write_output_index()
    write_reference_index()
    write_inventory()
    print("wrote data/generated-output/INDEX.md, data/reference-library/INDEX.md, docs/capability-inventory.md")


if __name__ == "__main__":
    main()
