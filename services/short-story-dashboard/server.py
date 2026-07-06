#!/usr/bin/env python3
from __future__ import annotations

import argparse
import html
import hashlib
import json
import math
import mimetypes
import os
import re
import subprocess
import sys
import time
from datetime import datetime
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Optional
from urllib.parse import parse_qs, unquote, urlparse


ROOT = Path(__file__).resolve().parent
STATIC_DIR = ROOT / "static"


def find_workspace_root(start: Path) -> Path:
    for path in (start, *start.parents):
        if (path / "go.mod").exists():
            return path
    return start.parent


def env_path(name: str, default: Path) -> Path:
    value = os.environ.get(name)
    return Path(value).expanduser().resolve() if value else default.resolve()


def first_existing(*paths: Path) -> Path:
    for path in paths:
        if path.exists():
            return path.resolve()
    return paths[0].resolve()


WORKSPACE = find_workspace_root(ROOT)
OUTPUT_ROOT = env_path("NOVEL_STUDIO_OUTPUT_ROOT", WORKSPACE / "data" / "generated-output")
DATA_ROOT = env_path("NOVEL_STUDIO_SHORT_STORY_DATA", OUTPUT_ROOT / "short_story_service" / "projects")
DEFAULT_NOVEL_DIR = env_path("NOVEL_STUDIO_NOVEL_DIR", WORKSPACE / "output" / "novel")
MATERIAL_READ_LIMIT = 500_000
MAX_PARALLEL_AGENTS = 5
REVIEW_SCRIPTS_DIR = first_existing(
    env_path("NOVEL_STUDIO_AUDIT_SCRIPTS", WORKSPACE / "quality" / "audit" / "scripts"),
    WORKSPACE / "skills" / "review" / "scripts",
)
try:
    AUDIT_SCRIPTS_DISPLAY = REVIEW_SCRIPTS_DIR.relative_to(WORKSPACE).as_posix()
except ValueError:
    AUDIT_SCRIPTS_DISPLAY = str(REVIEW_SCRIPTS_DIR)


def audit_command(script: str, target: str) -> str:
    target_part = target if target.startswith("<") else f'"{target}"'
    return f"python3 {AUDIT_SCRIPTS_DISPLAY}/{script} {target_part}"

if str(REVIEW_SCRIPTS_DIR) not in sys.path:
    sys.path.insert(0, str(REVIEW_SCRIPTS_DIR))
try:
    from aigc_value import analyze_text as analyze_local_aigc_text
except Exception:
    analyze_local_aigc_text = None
try:
    from content_lint import (
        awkward_style_issues,
        count_mismatch_issues,
        punctuation_emotion_issues,
        semantic_clarity_issues,
    )
except Exception:
    awkward_style_issues = None
    count_mismatch_issues = None
    punctuation_emotion_issues = None
    semantic_clarity_issues = None

UNIFIED_AUDIT_ENGINE = "codex-local-aigc-v3"

STAGES = [
    {
        "id": "intake",
        "label": "题材录入",
        "goal": "确认短篇题材、书名、核心设定、人设、钩子和硬约束。",
        "artifact": "输入设定.md",
    },
    {
        "id": "bible",
        "label": "统一设计包",
        "goal": "产出与长篇同名的设计交付物：前提、人物、世界规则、时间线、关系、伏笔、指南针和大纲；短篇只压缩粒度，不减少文件。",
        "artifact": "故事圣经.md",
    },
    {
        "id": "draft",
        "label": "逐章正文",
        "goal": "一章一章写，每章写完立即审核；审核达标后才解锁下一章。",
        "artifact": "正文草稿.md",
    },
    {
        "id": "merge",
        "label": "终版合并",
        "goal": "只合并已审核达标的分章内容，形成单一正文文件。",
        "artifact": "正文.md",
    },
    {
        "id": "deai",
        "label": "降 AI 味",
        "goal": "按章节审核结果处理模板感、整齐感、抽象心理、解释性旁白和可替换细节。",
        "artifact": "降AI味记录.md",
    },
    {
        "id": "audit",
        "label": "审核",
        "goal": "复核所有章节均已逐章审核达标，生成整书质量报告。",
        "artifact": "审核报告.md",
    },
    {
        "id": "image",
        "label": "配图生成",
        "goal": "根据终版正文生成封面图、关键场景图和可直接用于图片模型的提示词，并记录图片文件路径。",
        "artifact": "图片生成方案.md",
    },
    {
        "id": "final",
        "label": "最终交付",
        "goal": "确认所有章节审核达标、终版正文合并、配图方案完成并再次审核通过后交付。",
        "artifact": "交付清单.md",
    },
]

ARTIFACTS = [
    "输入设定.md",
    "故事圣经.md",
    "premise.md",
    "characters.json",
    "characters.md",
    "world_rules.json",
    "world_rules.md",
    "book_world.json",
    "book_world.md",
    "outline.json",
    "outline.md",
    "layered_outline.json",
    "layered_outline.md",
    "timeline.json",
    "timeline.md",
    "relationship_state.json",
    "relationship_state.md",
    "foreshadow_ledger.json",
    "foreshadow_ledger.md",
    "compass.json",
    "compass.md",
    "正文草稿.md",
    "正文.md",
    "降AI味记录.md",
    "审核报告.md",
    "图片生成方案.md",
    "交付清单.md",
]

DOCUMENT_TYPES = {
    "intake": {"label": "题材设定", "order": 1},
    "planning": {"label": "结构规划", "order": 2},
    "chapter": {"label": "章节正文", "order": 3},
    "novel": {"label": "整书正文", "order": 4},
    "quality": {"label": "质检回改", "order": 5},
    "image": {"label": "图片方案", "order": 6},
    "delivery": {"label": "交付文件", "order": 7},
}

ARTIFACT_META = {
    "输入设定.md": {"type": "intake", "target": 200},
    "故事圣经.md": {"type": "planning", "target": 1200},
    "premise.md": {"type": "planning", "target": 500},
    "characters.json": {"type": "planning", "target": 200},
    "characters.md": {"type": "planning", "target": 500},
    "world_rules.json": {"type": "planning", "target": 120},
    "world_rules.md": {"type": "planning", "target": 300},
    "book_world.json": {"type": "planning", "target": 120},
    "book_world.md": {"type": "planning", "target": 300},
    "outline.json": {"type": "planning", "target": 300},
    "outline.md": {"type": "planning", "target": 600},
    "layered_outline.json": {"type": "planning", "target": 200},
    "layered_outline.md": {"type": "planning", "target": 400},
    "timeline.json": {"type": "planning", "target": 120},
    "timeline.md": {"type": "planning", "target": 300},
    "relationship_state.json": {"type": "planning", "target": 120},
    "relationship_state.md": {"type": "planning", "target": 240},
    "foreshadow_ledger.json": {"type": "planning", "target": 120},
    "foreshadow_ledger.md": {"type": "planning", "target": 240},
    "compass.json": {"type": "planning", "target": 120},
    "compass.md": {"type": "planning", "target": 240},
    "正文草稿.md": {"type": "novel", "target": "book_min"},
    "正文.md": {"type": "novel", "target": "book_min"},
    "降AI味记录.md": {"type": "quality", "target": 300},
    "审核报告.md": {"type": "quality", "target": 300},
    "图片生成方案.md": {"type": "image", "target": 600},
    "交付清单.md": {"type": "delivery", "target": 120},
}

DESIGN_ARTIFACTS = [
    "premise.md",
    "characters.json",
    "characters.md",
    "world_rules.json",
    "world_rules.md",
    "book_world.json",
    "book_world.md",
    "outline.json",
    "outline.md",
    "layered_outline.json",
    "layered_outline.md",
    "timeline.json",
    "timeline.md",
    "relationship_state.json",
    "relationship_state.md",
    "foreshadow_ledger.json",
    "foreshadow_ledger.md",
    "compass.json",
    "compass.md",
]

DESIGN_JSON_ARRAYS = {
    "characters.json",
    "world_rules.json",
    "outline.json",
    "layered_outline.json",
    "timeline.json",
    "relationship_state.json",
    "foreshadow_ledger.json",
}

DIRECTION_SKILLS = {
    "shuangnanzhu": ["fanqie-novel-template", "fanqie-shuangnanzhu-short", "review"],
    "baihe": ["fanqie-novel-template", "fanqie-baihe-short", "review"],
    "shuangwen": ["fanqie-novel-template", "fanqie-shuangwen-short", "review"],
    "custom": ["fanqie-novel-template", "review"],
}

DIRECTION_LABELS = {
    "shuangnanzhu": "双男主",
    "baihe": "百合 / GL",
    "shuangwen": "反转爽文",
    "custom": "自定义短篇",
}

CHAPTER_STATUSES = {"locked", "writing", "auditing", "revising", "passed"}

WORK_TAG_CATALOG = {
    "类型": [
        "言情",
        "现实情感",
        "悬疑",
        "惊悚",
        "科幻",
        "武侠",
        "权谋",
        "玄幻奇幻",
        "民间奇闻",
        "同人",
        "演义",
        "脑洞",
        "纯爱",
        "电竞",
        "ABO",
        "世情",
        "婚恋",
    ],
    "角色": [
        "姐弟恋",
        "白月光",
        "大女主",
        "女配",
        "替身",
        "病娇",
        "青梅竹马",
        "豪门霸总",
        "女神",
        "女王",
        "女婿",
        "草根",
        "江湖兄弟",
        "风水道士",
        "医生",
        "警察",
        "律师",
        "法医",
        "真假千金",
        "动物主角",
        "学霸",
        "校霸",
        "双男主",
        "双女主",
        "赘婿",
        "无女主",
        "哥哥",
        "小叔",
        "闺蜜",
        "轻熟男女",
        "银发",
        "忠犬",
        "室友",
        "作精",
        "绿茶",
        "萌宝",
        "兽人",
        "魅魔",
        "女兄弟",
        "父母",
    ],
    "情节": [
        "先婚后爱",
        "追妻火葬场",
        "破镜重圆",
        "出轨",
        "婚姻",
        "家庭",
        "校园",
        "职场",
        "娱乐圈",
        "复仇",
        "重生",
        "穿越",
        "犯罪",
        "无限流",
        "丧尸",
        "太空歌剧",
        "赛博朋克",
        "文明伦理",
        "时间旅行",
        "平行时空",
        "人工智能",
        "幻想爱情",
        "近未来",
        "远未来",
        "争霸",
        "游戏",
        "探险",
        "艳遇",
        "宫斗宅斗",
        "西游",
        "仙侠",
        "救赎",
        "金手指",
        "超能力",
        "克苏鲁",
        "系统",
        "穿书",
        "玄学",
        "友情",
        "规则怪谈",
        "江湖",
        "团宠",
        "养成",
        "囤物资",
        "HE",
        "BE",
        "剑道",
        "女性成长",
        "女性互助",
        "原生家庭",
        "追夫火葬场",
        "三国",
        "直播",
        "弹幕",
        "久别重逢",
        "死人文学",
        "养崽",
        "亲情火葬场",
        "虐渣",
        "网恋",
        "星际",
        "带球跑",
        "雄竞",
        "反套路",
    ],
    "情绪": [
        "甜宠",
        "虐恋",
        "暗恋",
        "先虐后甜",
        "沙雕",
        "爽文",
        "反转",
        "逆袭",
        "励志",
        "烧脑",
        "热血",
        "求生",
        "打脸",
        "多视角反转",
        "治愈",
    ],
}

REQUIRED_ROLE_TAG = {
    "baihe": "双女主",
    "shuangnanzhu": "双男主",
}


def now() -> str:
    return datetime.now().isoformat(timespec="seconds")


def compact_text(value: str, limit: int = 48) -> str:
    text = re.sub(r"\s+", " ", str(value or "")).strip()
    if len(text) <= limit:
        return text
    return f"{text[:limit]}..."


def work_tag_catalog_text() -> str:
    return "\n".join(
        f"- {category}：" + "、".join(tags)
        for category, tags in WORK_TAG_CATALOG.items()
    )


def required_role_tag(project: dict) -> str:
    return REQUIRED_ROLE_TAG.get(project.get("direction", ""), "")


def selected_work_tags_text(project: dict) -> str:
    fields = project.get("fields", {})
    value = str(fields.get("work_tags", "")).strip()
    if value:
        return value
    role_tag = required_role_tag(project)
    role_note = f"；角色栏必须包含「{role_tag}」" if role_tag else ""
    return f"待生成：从作品标签词表中选择类型 / 角色 / 情节 / 情绪{role_note}"


def protagonist_profile_text(project: dict) -> str:
    fields = project.get("fields", {})
    value = str(fields.get("protagonist_profile", "")).strip()
    if value:
        return value
    character_a = compact_text(fields.get("character_a", ""))
    character_b = compact_text(fields.get("character_b", ""))
    if character_a and character_b:
        return f"{character_a} VS {character_b}"
    return "待生成：按「人物 A 核心身份 / 处境 VS 人物 B 核心身份 / 性格 / 功能」格式补齐"


def slugify(value: str) -> str:
    value = value.strip() or "untitled"
    value = re.sub(r"[\\/:*?\"<>|]+", "_", value)
    value = re.sub(r"\s+", "_", value)
    return value[:80] or "untitled"


def parse_int(value, default: int, minimum: int = 1, maximum: int = 200) -> int:
    match = re.search(r"\d+", str(value or ""))
    if not match:
        return default
    number = int(match.group(0))
    return max(minimum, min(maximum, number))


def project_dir(project_id: str) -> Path:
    return DATA_ROOT / project_id


def project_json(project_id: str) -> Path:
    return project_dir(project_id) / "project.json"


def artifact_path(project_id: str, name: str) -> Path:
    safe = Path(name).name
    if safe not in ARTIFACTS:
        raise ValueError("unknown artifact")
    return project_dir(project_id) / safe


def empty_json_artifact(name: str):
    if name in DESIGN_JSON_ARRAYS:
        return []
    if name == "book_world.json":
        return {"version": 1, "name": "", "summary": "", "places": [], "routes": [], "factions": [], "map_notes": []}
    if name == "compass.json":
        return {"ending_direction": "", "open_threads": [], "estimated_scale": "", "last_updated": 0}
    return {}


def empty_markdown_artifact(name: str) -> str:
    title = name.removesuffix(".md").removesuffix(".json")
    if name == "故事圣经.md":
        return "# 故事圣经\n\n## 统一设计包索引\n\n- premise.md\n- characters.json / characters.md\n- world_rules.json / world_rules.md\n- book_world.json / book_world.md\n- outline.json / outline.md\n- layered_outline.json / layered_outline.md\n- timeline.json / timeline.md\n- relationship_state.json / relationship_state.md\n- foreshadow_ledger.json / foreshadow_ledger.md\n- compass.json / compass.md\n\n"
    return f"# {title}\n\n"


def empty_artifact_content(name: str) -> str:
    if name.endswith(".json"):
        return json.dumps(empty_json_artifact(name), ensure_ascii=False, indent=2) + "\n"
    return empty_markdown_artifact(name)


def design_artifact_missing(project: dict) -> list[str]:
    missing = []
    for name in DESIGN_ARTIFACTS:
        path = artifact_path(project["id"], name)
        if not path.exists():
            missing.append(name)
            continue
        text = path.read_text(encoding="utf-8")
        if name.endswith(".json"):
            try:
                data = json.loads(text or "null")
            except json.JSONDecodeError:
                missing.append(f"{name} 非法 JSON")
                continue
            if data in (None, [], {}):
                missing.append(name)
                continue
            if name == "compass.json" and not str(data.get("ending_direction", "")).strip():
                missing.append("compass.json 缺 ending_direction")
            if name == "book_world.json" and not (str(data.get("summary", "")).strip() or data.get("places") or data.get("factions")):
                missing.append("book_world.json 缺世界摘要/地点/势力")
        elif text_stats(path)["content_non_space_chars"] == 0:
            missing.append(name)
    return missing


def novel_dir_name(title: str) -> str:
    return slugify(title)


def novel_dir(project: dict) -> Path:
    return project_dir(project["id"]) / project.get("novel_dir_name", novel_dir_name(project["title"]))


def images_dir(project: dict) -> Path:
    return project_dir(project["id"]) / "images"


def chapter_file_name(number: int) -> str:
    return f"第{number:02d}章.md"


def chapter_audit_file_name(number: int) -> str:
    return f"第{number:02d}章-审核.md"


def chapter_path(project: dict, number: int) -> Path:
    return novel_dir(project) / chapter_file_name(number)


def chapter_audit_path(project: dict, number: int) -> Path:
    return novel_dir(project) / "_audit" / chapter_audit_file_name(number)


def reviews_dir(project: dict) -> Path:
    return novel_dir(project) / "reviews"


def reviews_ai_dir(project: dict) -> Path:
    return novel_dir(project) / "reviews_ai"


def chapter_ai_review_json_path(project: dict, number: int) -> Path:
    return reviews_dir(project) / f"{number:02d}_ai_gate.json"


def chapter_ai_review_md_path(project: dict, number: int) -> Path:
    return reviews_dir(project) / f"{number:02d}.md"


def legacy_chapter_ai_review_json_path(project: dict, number: int) -> Path:
    return reviews_ai_dir(project) / f"{number:02d}.json"


def legacy_chapter_ai_review_md_path(project: dict, number: int) -> Path:
    return reviews_ai_dir(project) / f"第{number:03d}章_AI味审核.md"


def artifact_meta(name: str) -> dict:
    meta = ARTIFACT_META.get(name, {"type": "planning", "target": 300})
    doc_type = DOCUMENT_TYPES.get(meta["type"], DOCUMENT_TYPES["planning"])
    return {
        "type": meta["type"],
        "type_label": doc_type["label"],
        "type_order": doc_type["order"],
        "target": meta["target"],
    }


def read_json(path: Path) -> dict:
    with path.open("r", encoding="utf-8") as f:
        return json.load(f)


def write_json(path: Path, data: dict) -> None:
    data["updated_at"] = now()
    with path.open("w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
        f.write("\n")


def sync_project_lifecycle(project: dict) -> bool:
    changed = False
    if not project.get("created_at"):
        project["created_at"] = now()
        changed = True
    started_at = project.get("created_at", "")
    is_done = project.get("current_stage") == "done"
    if is_done and not project.get("completed_at"):
        project["completed_at"] = now()
        changed = True
    if not is_done and project.get("completed_at"):
        project.pop("completed_at", None)
        changed = True
    next_values = {
        "started_at": started_at,
        "ended_at": project.get("completed_at", "") if is_done else "",
        "lifecycle_status": "done" if is_done else "running",
    }
    for key, value in next_values.items():
        if project.get(key) != value:
            project[key] = value
            changed = True
    return changed


def load_project(project_id: str, persist: bool = True) -> dict:
    return ensure_project_structure(read_json(project_json(project_id)), persist=persist)


def infer_direction(payload: dict) -> str:
    text = "\n".join(str(payload.get(k, "")) for k in payload)
    lowered = text.lower()
    if any(token in text for token in ["百合", "GL", "双女主", "女女", "她她"]):
        return "baihe"
    if any(token in text for token in ["双男主", "男男", "他他", "影帝", "顶流", "组CP", "CP营业", "恋综"]):
        return "shuangnanzhu"
    if any(token in text for token in ["爽文", "反转", "打脸", "复仇", "维权", "离婚", "背叛", "重生"]):
        return "shuangwen"
    if any(token in lowered for token in ["cp", "idol", "actor"]):
        return "shuangnanzhu"
    return "custom"


def make_input_markdown(project: dict) -> str:
    fields = project["fields"]
    lines = [
        f"# {project['title']}",
        "",
        f"- 方向：{DIRECTION_LABELS.get(project['direction'], project['direction'])}",
        f"- 目标字符数：{fields.get('target_chars', '25000-28000')}",
        f"- 计划章数：{fields.get('chapter_count', '10')}",
        f"- AI 味目标：≤ {fields.get('ai_taste_target', '5')}%",
        f"- 外部AIGC检测值：{fields.get('external_aigc_score') or '未提供'}",
        f"- 作品标签：{selected_work_tags_text(project)}",
        f"- 主角人设：{protagonist_profile_text(project)}",
        "",
        "## 核心设定",
        fields.get("premise", "").strip(),
        "",
        "## 人物 A",
        fields.get("character_a", "").strip(),
        "",
        "## 人物 B",
        fields.get("character_b", "").strip(),
        "",
        "## 开篇钩子",
        fields.get("hook", "").strip(),
        "",
        "## 风格与硬约束",
        fields.get("style_notes", "").strip(),
        "",
        "## 标签",
        fields.get("tags", "").strip(),
        "",
        "## 作品标签词表",
        work_tag_catalog_text(),
        "",
    ]
    return "\n".join(lines)


def make_empty_chapter(project: dict, number: int) -> str:
    return "\n".join(
        [
            f"# 第{number:02d}章",
            "",
        ]
    )


def make_empty_chapter_audit(project: dict, number: int) -> str:
    return "\n".join(
        [
            f"# 第{number:02d}章审核",
            "",
            f"- 项目：{project['title']}",
            "- AI创作度：",
            "- 六维合计：",
            "- 段落重复：",
            "- 审核结论：",
            "",
        ]
    )


def make_agent(project: dict, index: int = 1, status: str = "ready", batch_order: Optional[int] = None) -> dict:
    lane = max(1, min(MAX_PARALLEL_AGENTS, index))
    return {
        "id": f"novel-agent-{lane:02d}",
        "lane": lane,
        "batch_order": batch_order,
        "status": status,
        "max_parallel_agents": MAX_PARALLEL_AGENTS,
        "scope": "single_novel",
        "context_policy": "单小说、单章节、单审核上下文；多书批量时 Codex 不会自动开 subagent，必须明确 spawn N agents / 并行拆给 N 个 agent，最多 5 个 agent 并行，剩余项目排队。",
    }


def ensure_project_structure(project: dict, persist: bool = False) -> dict:
    changed = False
    pdir = project_dir(project["id"])
    pdir.mkdir(parents=True, exist_ok=True)

    if project.get("output_root") != str(OUTPUT_ROOT):
        project["output_root"] = str(OUTPUT_ROOT)
        changed = True
    if not project.get("novel_dir_name"):
        project["novel_dir_name"] = novel_dir_name(project["title"])
        changed = True
    if project.get("book_dir") != str(pdir):
        project["book_dir"] = str(pdir)
        changed = True
    if project.get("chapter_dir") != str(novel_dir(project)):
        project["chapter_dir"] = str(novel_dir(project))
        changed = True
    images_dir(project).mkdir(parents=True, exist_ok=True)
    if project.get("image_dir") != str(images_dir(project)):
        project["image_dir"] = str(images_dir(project))
        changed = True
    if not project.get("agent"):
        project["agent"] = make_agent(project, 1)
        changed = True
    if sync_project_lifecycle(project):
        changed = True

    fields = project.setdefault("fields", {})
    if "external_aigc_score" not in fields:
        fields["external_aigc_score"] = ""
        changed = True

    stages = project.setdefault("stages", {})
    for stage in STAGES:
        if stage["id"] not in stages:
            stages[stage["id"]] = {"status": "pending", "note": ""}
            changed = True
    artifacts = project.setdefault("artifacts", {})
    for name in ARTIFACTS:
        path = pdir / name
        if artifacts.get(name) != str(path):
            artifacts[name] = str(path)
            changed = True
        if not path.exists():
            path.write_text(empty_artifact_content(name), encoding="utf-8")
            changed = True

    count = parse_int(project.get("fields", {}).get("chapter_count", "10"), 10, 1, 80)
    chapters = project.get("chapters") or []
    by_number = {int(item.get("number", 0)): item for item in chapters if item.get("number")}
    normalized = []
    novel_dir(project).mkdir(parents=True, exist_ok=True)
    (novel_dir(project) / "_audit").mkdir(parents=True, exist_ok=True)
    reviews_dir(project).mkdir(parents=True, exist_ok=True)
    for number in range(1, count + 1):
        chapter = by_number.get(number, {})
        status = chapter.get("status")
        if status not in CHAPTER_STATUSES:
            status = "writing" if number == 1 else "locked"
        chapter_file = f"{project['novel_dir_name']}/{chapter_file_name(number)}"
        audit_file = f"{project['novel_dir_name']}/_audit/{chapter_audit_file_name(number)}"
        normalized.append(
            {
                "number": number,
                "title": chapter.get("title") or f"第{number:02d}章",
                "status": status,
                "file": chapter_file,
                "audit_file": audit_file,
                "updated_at": chapter.get("updated_at", ""),
            }
        )
        cpath = chapter_path(project, number)
        apath = chapter_audit_path(project, number)
        if not cpath.exists():
            cpath.write_text(make_empty_chapter(project, number), encoding="utf-8")
            changed = True
        if not apath.exists():
            apath.write_text(make_empty_chapter_audit(project, number), encoding="utf-8")
            changed = True
    if chapters != normalized:
        project["chapters"] = normalized
        changed = True
    if persist and changed:
        write_json(project_json(project["id"]), project)
    return project


def create_project(payload: dict) -> dict:
    title = payload.get("title", "").strip() or "未命名短篇"
    direction = payload.get("direction") or infer_direction(payload)
    if direction not in DIRECTION_LABELS:
        direction = "custom"
    created = datetime.now().strftime("%Y%m%d-%H%M%S")
    base_project_id = f"{created}-{slugify(title)}"
    project_id = base_project_id
    suffix = 2
    while project_dir(project_id).exists():
        project_id = f"{base_project_id}-{suffix}"
        suffix += 1
    pdir = project_dir(project_id)
    pdir.mkdir(parents=True, exist_ok=True)
    agent_index = parse_int(payload.get("agent_index", "1"), 1, 1, MAX_PARALLEL_AGENTS)
    agent_status = payload.get("agent_status", "ready")
    if agent_status not in {"ready", "running", "pending", "blocked", "done"}:
        agent_status = "ready"
    batch_order = payload.get("batch_order")
    batch_order = parse_int(batch_order, agent_index, 1, 999) if batch_order else None

    fields = {
        "premise": payload.get("premise", ""),
        "character_a": payload.get("character_a", ""),
        "character_b": payload.get("character_b", ""),
        "hook": payload.get("hook", ""),
        "style_notes": payload.get("style_notes", ""),
        "tags": payload.get("tags", ""),
        "work_tags": payload.get("work_tags", ""),
        "protagonist_profile": payload.get("protagonist_profile", ""),
        "target_chars": payload.get("target_chars", "25000-28000"),
        "chapter_count": payload.get("chapter_count", "10"),
        "ai_taste_target": payload.get("ai_taste_target", "5"),
        "external_aigc_score": payload.get("external_aigc_score", payload.get("aigc_score", "")),
    }
    stages = {stage["id"]: {"status": "pending", "note": ""} for stage in STAGES}
    stages["intake"]["status"] = "done"
    stages["bible"]["status"] = "in_progress"
    project = {
        "id": project_id,
        "title": title,
        "direction": direction,
        "direction_label": DIRECTION_LABELS[direction],
        "selected_skills": DIRECTION_SKILLS[direction],
        "fields": fields,
        "stages": stages,
        "current_stage": "bible",
        "created_at": now(),
        "updated_at": now(),
        "started_at": now(),
        "ended_at": "",
        "lifecycle_status": "running",
        "output_root": str(OUTPUT_ROOT),
        "novel_dir_name": novel_dir_name(title),
        "chapter_dir": str(pdir / novel_dir_name(title)),
        "image_dir": str(pdir / "images"),
        "agent": make_agent({"id": project_id, "title": title}, agent_index, agent_status, batch_order),
        "book_dir": str(pdir),
        "artifacts": {name: str(pdir / name) for name in ARTIFACTS},
        "events": [
            {"time": now(), "text": "项目已创建，进入统一设计包阶段。"},
        ],
    }
    write_json(project_json(project_id), project)
    for name in ARTIFACTS:
        path = pdir / name
        if not path.exists():
            path.write_text(empty_artifact_content(name), encoding="utf-8")
    (pdir / "输入设定.md").write_text(make_input_markdown(project), encoding="utf-8")
    project = ensure_project_structure(project, persist=True)
    return project


def split_topic_blocks(text: str) -> list[str]:
    normalized = (text or "").replace("\r\n", "\n").strip()
    if not normalized:
        return []
    blocks = [block.strip() for block in re.split(r"\n\s*\n+", normalized) if block.strip()]
    if len(blocks) == 1:
        numbered = re.split(r"\n(?=\s*\d+[.、]\s*《?[^。\n]+)", normalized)
        blocks = [block.strip() for block in numbered if block.strip()]
    return blocks


def topic_payload_from_block(block: str, defaults: dict, index: int) -> dict:
    title_match = re.search(r"《([^》]+)》", block)
    if title_match:
        title = title_match.group(1).strip()
    else:
        first_line = next((line.strip() for line in block.splitlines() if line.strip()), f"未命名短篇{index}")
        title = re.sub(r"^\s*\d+[.、]\s*", "", first_line).strip(" -")
    payload = dict(defaults)
    payload.update(
        {
            "title": title or f"未命名短篇{index}",
            "premise": block,
            "agent_index": ((index - 1) % MAX_PARALLEL_AGENTS) + 1,
            "agent_status": "running" if index <= MAX_PARALLEL_AGENTS else "pending",
            "batch_order": index,
        }
    )
    if not payload.get("direction"):
        payload["direction"] = infer_direction(payload)
    return payload


def create_batch_projects(payload: dict) -> dict:
    blocks = split_topic_blocks(payload.get("topics", ""))
    if not blocks:
        raise ValueError("topics is empty")
    defaults = {
        "direction": payload.get("direction", ""),
        "target_chars": payload.get("target_chars", "25000-28000"),
        "chapter_count": payload.get("chapter_count", "10"),
        "ai_taste_target": payload.get("ai_taste_target", "5"),
        "external_aigc_score": payload.get("external_aigc_score", payload.get("aigc_score", "")),
        "style_notes": payload.get("style_notes", ""),
        "tags": payload.get("tags", ""),
        "work_tags": payload.get("work_tags", ""),
        "protagonist_profile": payload.get("protagonist_profile", ""),
    }
    projects = []
    for index, block in enumerate(blocks, start=1):
        projects.append(create_project(topic_payload_from_block(block, defaults, index)))
    return {
        "projects": projects,
        "agent_count": min(len(projects), MAX_PARALLEL_AGENTS),
        "pending_count": max(0, len(projects) - MAX_PARALLEL_AGENTS),
        "max_parallel_agents": MAX_PARALLEL_AGENTS,
        "context_policy": "每个题材创建独立项目；Codex 不会自动开 subagent，调度时必须明确 spawn N agents / 并行拆给 N 个 agent；最多 5 个 agent lane 并行写作，超出的项目保持 pending。",
    }


def list_projects() -> list[dict]:
    DATA_ROOT.mkdir(parents=True, exist_ok=True)
    projects = []
    for path in DATA_ROOT.glob("*/project.json"):
        try:
            data = ensure_project_structure(read_json(path), persist=True)
        except (OSError, json.JSONDecodeError):
            continue
        current_stage_id = data.get("current_stage", "unknown")
        current_stage = next((stage for stage in STAGES if stage["id"] == current_stage_id), None)
        stage_status = data.get("stages", {}).get(current_stage_id, {}).get("status", "")
        done_stages = sum(1 for item in data.get("stages", {}).values() if item.get("status") == "done")
        projects.append(
            {
                "id": data["id"],
                "title": data["title"],
                "direction": data["direction"],
                "direction_label": data.get("direction_label", data["direction"]),
                "current_stage": current_stage_id,
                "current_stage_label": current_stage["label"] if current_stage else current_stage_id,
                "current_stage_status": stage_status,
                "done_stages": done_stages,
                "total_stages": len(STAGES),
                "agent_id": data.get("agent", {}).get("id", ""),
                "agent_status": data.get("agent", {}).get("status", ""),
                "chapter_dir": data.get("chapter_dir", ""),
                "started_at": data.get("started_at", data.get("created_at", "")),
                "ended_at": data.get("ended_at", ""),
                "lifecycle_status": data.get("lifecycle_status", "running"),
                "updated_at": data.get("updated_at", ""),
            }
        )
    projects.sort(key=lambda item: item["updated_at"], reverse=True)
    return projects


def update_current_stage(project: dict) -> None:
    for stage in STAGES:
        status = project["stages"][stage["id"]]["status"]
        if status in {"pending", "in_progress"}:
            project["current_stage"] = stage["id"]
            sync_project_lifecycle(project)
            return
    project["current_stage"] = "done"
    sync_project_lifecycle(project)


def stage_prompt(project: dict, stage_id: str) -> str:
    stage = next((item for item in STAGES if item["id"] == stage_id), STAGES[0])
    fields = project["fields"]
    skills = ", ".join(project["selected_skills"])
    extra = ""
    if stage_id == "bible":
        extra = f"""
本阶段是统一设计包阶段。长篇和短篇必须交付同名设计文件；区别只在粒度和复杂度：
- 短篇：单一核心冲突、2-3 个关键人物、一条主时间线、少量伏笔且必须在 8-12 章内回收；`layered_outline` 使用 1 卷 1 弧压缩结构。
- 长篇：同一批文件会扩展为多卷/多弧、多线并行、支线和阶段失败；可滚动展开后续弧，但当前可写弧必须细到章节。
- 设计判断依据：外部资料显示，短篇和长篇共享角色/冲突/主题/开端-中段-结尾等核心元素；短篇必须快速聚焦单一效果，长篇需要更多转折、支线、尝试失败、人物发展和节奏波峰波谷。

必须创建或更新这些设计交付物，文件名必须一致：
1. `premise.md`：书名、题材、目标读者、核心情绪、一句话梗概、终局承诺、写作禁区。
2. `characters.json` / `characters.md`：角色档案。短篇只保留核心/重要人物；长篇可分 core / important / secondary / decorative。
3. `world_rules.json` / `world_rules.md`：世界/背景硬规则、边界和不可违反条件；现实题材也要写社会/行业/家庭规则。
4. `book_world.json` / `book_world.md`：地点、路线、势力/组织、可反复使用的场景资产；短篇只写会实际出现的地点和势力。
5. `outline.json` / `outline.md`：扁平逐章大纲，每章都要有 core_event、hook、scenes。
6. `layered_outline.json` / `layered_outline.md`：分层大纲。短篇用 1 卷 1 弧承载全部章节；长篇用多卷/多弧或首弧详纲+后续骨架。
7. `timeline.json` / `timeline.md`：设计期时间线，包含关键过去事件、开篇触发、逐章事件、回收点；短篇不得堆无用前史。
8. `relationship_state.json` / `relationship_state.md`：人物关系初始态和预计变化节点。
9. `foreshadow_ledger.json` / `foreshadow_ledger.md`：伏笔、埋设章、推进章、回收章和状态；短篇伏笔必须短链闭合。
10. `compass.json` / `compass.md`：终局方向指南针。短篇写最终反转/情绪落点；长篇写终局方向、开放长线和估算规模。
11. `故事圣经.md`：只做可读总览和索引，不能替代上述结构化文件。

JSON 结构必须兼容长篇 store：
- `characters.json`：`[{{"name":"","aliases":[],"role":"","description":"","arc":"","traits":[],"tier":"core"}}]`
- `world_rules.json`：`[{{"category":"society","rule":"","boundary":""}}]`
- `outline.json`：`[{{"chapter":1,"title":"","core_event":"","hook":"","scenes":[]}}]`
- `layered_outline.json`：`[{{"index":1,"title":"第一卷","theme":"","arcs":[{{"index":1,"title":"第一弧","goal":"","chapters":[...]}}]}}]`
- `timeline.json`：`[{{"chapter":1,"time":"","event":"","characters":[]}}]`
- `relationship_state.json`：`[{{"character_a":"","character_b":"","relation":"","chapter":1}}]`
- `foreshadow_ledger.json`：`[{{"id":"F01","description":"","planted_at":1,"status":"planted","resolved_at":0}}]`
- `book_world.json`：`{{"version":1,"name":"","summary":"","places":[],"routes":[],"factions":[],"map_notes":[]}}`
- `compass.json`：`{{"ending_direction":"","open_threads":[],"estimated_scale":"","last_updated":0}}`

设计验收硬门槛：
1. 上述文件不能只写标题或空数组。
2. `outline.json` 的章数必须等于计划章数 `{fields.get('chapter_count', '10')}`，每章有核心事件、场景序列和钩子。
3. `timeline.json` 至少覆盖开篇触发、中点转折、终局回收；短篇最好逐章覆盖。
4. 伏笔、关系、时间线必须能互相对应：有伏笔就有埋设/回收章，有关系变化就能在 outline/timeline 找到事件。
5. 进入逐章正文前，必须先把 `故事圣经.md` 作为索引更新，列出所有设计文件路径和设计结论。
"""
    elif stage_id == "deai":
        extra = f"""
本阶段必须读取 `正文.md`、分章审核和 `故事圣经.md`，先定位 AI 味强信号，再做回改记录。不要泛泛写“已优化”。

必须执行/引用的本地检查：
1. `{audit_command('aigc_value.py', '<正文路径>')} --target {fields.get('ai_taste_target', '5')}`。
2. `{audit_command('text_signals.py', '<正文路径>')}`；若用户提供腾讯朱雀/AIGC 值，追加 `--external-aigc <值>`。
3. `{audit_command('paragraph_dup.py', '<正文路径>')}`。
4. `{audit_command('content_lint.py', '<正文路径>')}`。
5. `{audit_command('typo_scan.py', '<正文路径>')}`。

必须逐章读取 `reviews/NN.md` 和 `reviews/NN_ai_gate.json`；任一章节机械门禁未通过，不得写“已完成降 AI 味”。

`降AI味记录.md` 必须包含：
1. 自研AIGC值：0.xxxx / x%；引擎 `codex-local-aigc-v3`；是否过闸。
2. 朱雀四维代理分：突发性 / 困惑度代理 / 结构指纹 / 跨段一致性，逐项写分数、最高风险原因和回改动作。
3. 近年检测器代理层：弱语言模型一致性 / 局部熵TTR / 风格计量 / 语义平滑，逐项写分数、stats/signals 和回改动作。
4. 本地AI味风险分：x/100；综合AI味风险分：x/100。
5. 腾讯朱雀AIGC值：原始值 / 换算百分比 / 是否过闸；未提供则写“未提供”，不得编造。
6. AI 味强信号清单：句长 CV、句长标准差、套路措辞密度、解释归纳腔、弱模型概率过稳、滑窗熵/TTR 过稳、段落/句子重复。
7. 逐章回改清单：每章至少列“删除的模板句、改成的具体动作/物件/口癖/误会细节”。
8. 保留的人味信号：不追求全篇圆滑，允许短句、打断、脏尾巴和角色偏见。
9. 下一轮复审命令与目标：自研AIGC值 ≤ {fields.get('ai_taste_target', '5')}%，AI 创作度 ≤ {fields.get('ai_taste_target', '5')}%，六维合计 ≤ 1，段落级重复为 0，本地AI味风险分 ≤ 35/100。
"""
    elif stage_id == "audit":
        extra = f"""
本阶段必须对 `正文.md` 终版正文进行复审，确认正文最上方已包含作品标签和主角人设；审核报告保存到 `审核报告.md`。

必须运行并在报告中引用：
1. `{audit_command('aigc_value.py', '<正文路径>')} --target {fields.get('ai_taste_target', '5')}`。
2. `{audit_command('text_signals.py', '<正文路径>')}`；若用户提供腾讯朱雀/AIGC 值，追加 `--external-aigc <值>`。
3. `{audit_command('paragraph_dup.py', '<正文路径>')}`。
4. `{audit_command('content_lint.py', '<正文路径>')}`。
5. `{audit_command('typo_scan.py', '<正文路径>')}`。

终版审核前必须确认所有分章均已有 `reviews/NN_ai_gate.json` 和 `reviews/NN.md`，且机械门禁通过；短篇只是在全部章节过审后额外合并 `正文.md`。

审核报告必须逐项写明：
1. 自研AIGC值：0.xxxx / x%；引擎 `codex-local-aigc-v3`；是否过闸。
2. 朱雀四维代理分：突发性 / 困惑度代理 / 结构指纹 / 跨段一致性；每项必须写分数和 stats/signals 证据。
3. 近年检测器代理层：弱语言模型一致性 / 局部熵TTR / 风格计量 / 语义平滑；每项必须写分数和 stats/signals 证据。
4. AI创作度：x%。
5. 本地AI味风险分：x/100；综合AI味风险分：x/100。
6. 腾讯朱雀AIGC值：原始值 / 换算百分比 / 是否过闸；未提供则写“未提供”，不得编造。例：0.8252 必须换算为 82.52%，按硬闸判为不通过。
7. 六维合计：x，并列出六维明细。
8. 段落重复：x；同时列出完全重复段落、高度相似段落、重复长句数量。
9. AI 味强信号：至少列 3 条原文证据；若无强信号，写明反证。
10. 分章 `reviews/` 机械门禁汇总：逐章写通过 / 不通过。
11. 审核结论：通过 / 不通过。任一分章机械门禁未过时，终版审核结论必须是不通过。
"""
    elif stage_id == "image":
        extra = f"""
本阶段必须读取 `正文.md`、`故事圣经.md` 和 `审核报告.md`，根据终版内容生成图片交付包，保存到 `图片生成方案.md`。

必须输出：
1. 封面图方案：画面主体、人物造型、情绪、构图、光色、文字禁用说明、正向提示词、负向提示词、建议文件名。
2. 关键场景图方案：至少 3 张，分别对应开篇钩子、中段反转/关系爆点、结尾情绪落点；每张都要给出章节来源、画面描述、正向提示词、负向提示词、建议文件名。
3. 角色一致性说明：主角 A / 主角 B 的外观、服装、标志物、不可变特征。
4. 图片保存路径：优先使用 `{output_relative_path(images_dir(project))}/cover.png`、`{output_relative_path(images_dir(project))}/scene_01.png` 等路径。
5. 服务会同步维护 `{output_relative_path(images_dir(project) / 'image_jobs.json')}` 和 `images/*.prompt.txt`；如果配置 `NOVEL_STUDIO_IMAGE_GENERATOR_CMD`，必须实际生成图片并把文件路径写入方案；如果不能调用图片模型，必须写明“待生成”，但提示词要完整到可直接复制生成。
"""
    elif stage_id == "final":
        extra = "\n最终交付前必须确认：所有章节已达标、`正文.md` 已合并且字符达标、正文最上方有作品标签和主角人设、`审核报告.md` 晚于 `正文.md` 并通过终版复审、`图片生成方案.md` 已根据最新正文完成并列出图片提示词/图片路径。"
    artifact_requirement = f"内容必须可直接保存到 `{stage['artifact']}`。"
    if stage_id == "bible":
        artifact_requirement = "必须同步更新统一设计包全部文件；`故事圣经.md` 只做可读总览和文件索引。"
    return f"""你现在只处理番茄短篇小说生成项目的【{stage['label']}】阶段。

项目：{project['title']}
方向：{project['direction_label']}
已选技能：{skills}
目标：{stage['goal']}
AI 味硬闸门：终版自研AIGC值 ≤ {fields.get('ai_taste_target', '5')}%，AI 创作度 ≤ {fields.get('ai_taste_target', '5')}%，六维合计 ≤ 1，段落级重复为 0。
外部 AIGC 硬闸门：腾讯朱雀/AIGC 值若以 0-1 小数提供，必须换算为百分比；高于目标值即不通过。例：0.8252 = 82.52%，必须回到降 AI 味阶段。
本地 AI 味风险闸门：`text_signals.py` 输出的本地AI味风险分 > 35/100 时，不得判通过。

题材信息：
- 核心设定：{fields.get('premise', '').strip()}
- 人物 A：{fields.get('character_a', '').strip()}
- 人物 B：{fields.get('character_b', '').strip()}
- 开篇钩子：{fields.get('hook', '').strip()}
- 风格与约束：{fields.get('style_notes', '').strip()}
- 用户补充话题标签：{fields.get('tags', '').strip()}
- 外部AIGC检测值：{fields.get('external_aigc_score') or '未提供'}
- 作品标签：{selected_work_tags_text(project)}
- 主角人设：{protagonist_profile_text(project)}
- 目标字符数：{fields.get('target_chars', '25000-28000')}
- 计划章数：{fields.get('chapter_count', '10')}

正文头部硬要求：
1. 第 1 章和终版 `正文.md` 的文章最上方必须按顺序写：书名、作品标签、主角人设、金句、简介、话题标签，然后再进入第一章正文。
2. 作品标签必须从下列词表选择；类型 1-3 个、角色 2-5 个、情节 2-5 个、情绪 1-3 个。
3. 如果是双男主或双女主作品，必须在「角色」标签中补充「双男主」或「双女主」。
4. 主角人设必须使用 `A VS B` 对照格式，例如：`不被爱的富家千金 VS 温柔暖心哥哥`。

作品标签词表：
{work_tag_catalog_text()}

请输出本阶段产物，要求：
1. 只完成【{stage['label']}】阶段，不跳到后续阶段。
2. {artifact_requirement}
3. 短篇与长篇的区别只在写作前的设计粒度：短篇使用同名设计包、较少章节、`layered_outline` 采用 1 卷 1 弧压缩结构，并在全部章节过审后额外合并；进入章节写作后，写作顺序、机械审核、Editor 复审、返工门禁与长篇一致。
4. 章节正文必须一章一审；未通过 `reviews/` 机械门禁和审核报告复审，不得解锁下一章。
5. 明确下一阶段需要继承的作品标签、主角人设和关键信息。
{extra}
"""


def chapter_prompt(project: dict, number: int, mode: str) -> str:
    project = ensure_project_structure(project, persist=True)
    chapter = next((item for item in project["chapters"] if int(item["number"]) == number), None)
    if not chapter:
        raise ValueError("unknown chapter")
    fields = project["fields"]
    target_min, _target_max = parse_target_range(fields.get("target_chars", "25000-28000"))
    chapter_target = max(1, round(target_min / max(1, len(project["chapters"]))))
    cpath = output_relative_path(chapter_path(project, number))
    apath = output_relative_path(chapter_audit_path(project, number))
    previous_path = output_relative_path(chapter_path(project, number - 1)) if number > 1 else "无"
    context_paths = [
        output_relative_path(artifact_path(project["id"], "输入设定.md")),
        output_relative_path(artifact_path(project["id"], "故事圣经.md")),
    ]
    base = f"""你是小说项目 `{project['title']}` 的独立章节 agent：{project.get('agent', {}).get('id', 'novel-agent')}。

上下文规则：
- 只处理当前小说，不混入其他题材。
- 只处理第 {number:02d} 章，不提前写后续章节。
- 优先读取这些短上下文文件：{", ".join(context_paths)}
- 上一章路径：{previous_path}
- 当前章节正文必须保存到：{cpath}
- 当前章节审核必须保存到：{apath}
- 本章目标：约 {chapter_target} 非空字符。
- AI 味硬闸门：自研AIGC值 ≤ {fields.get('ai_taste_target', '5')}%，AI 创作度 ≤ {fields.get('ai_taste_target', '5')}%，六维合计 ≤ 1，段落重复为 0。
- 项目外部AIGC检测值：{fields.get('external_aigc_score') or '未提供'}。
- 外部 AIGC 硬闸门：腾讯朱雀/AIGC 值若以 0-1 小数提供，必须换算为百分比；高于目标值即不通过。例：0.8252 = 82.52%。
- 本地 AI 味风险闸门：`text_signals.py` 输出的本地AI味风险分 > 35/100 时，本章必须回改。
- 质量优先，不追求产出速度；如果本章质量不稳，先停在本章继续修，不解锁下一章。
- 短篇与长篇的区别只在写作前；进入本章后，写作、机械审核、复审和返工门禁按统一章级契约执行。
- 保存正文后系统会生成统一审核报告 `reviews/{number:02d}.md` 和结构化机械门禁 `reviews/{number:02d}_ai_gate.json`；机械门禁未通过时，本章不能标记达标。
"""
    if number == 1:
        base += f"""
第 01 章正文头部硬要求：
- 文章最上方必须按顺序写：`# {project['title']}`、作品标签、主角人设、金句、简介、话题标签，然后再进入第一章正文。
- 作品标签：{selected_work_tags_text(project)}
- 主角人设：{protagonist_profile_text(project)}
- 如果作品标签仍是“待生成”，必须从词表中选好类型 / 角色 / 情节 / 情绪；双男主或双女主作品必须在「角色」栏补充「双男主」或「双女主」。
- 主角人设使用 `A VS B` 格式，例如：`不被爱的富家千金 VS 温柔暖心哥哥`。
"""
    if mode == "audit":
        return base + f"""
请审核第 {number:02d} 章正文，输出可直接保存到 `{apath}` 的审核报告。

必须先读取并引用系统自动生成的机械审核产物：
- `{output_relative_path(chapter_ai_review_md_path(project, number))}`
- `{output_relative_path(chapter_ai_review_json_path(project, number))}`

如需复核，继续运行并引用：
1. `{audit_command('aigc_value.py', cpath)} --target {fields.get('ai_taste_target', '5')}`。
2. `{audit_command('text_signals.py', cpath)}`；若用户提供腾讯朱雀/AIGC 值，追加 `--external-aigc <值>`。
3. `{audit_command('paragraph_dup.py', cpath)}`。
4. `{audit_command('content_lint.py', cpath)}`。
5. `{audit_command('typo_scan.py', cpath)}`。

报告必须包含：
1. 自研AIGC值：0.xxxx / x%；引擎 `codex-local-aigc-v3`；是否过闸
2. 朱雀四维代理分和近年检测器代理层分数；列最高的 1-2 个 stats/signals
3. AI创作度：x%
4. 本地AI味风险分：x/100；综合AI味风险分：x/100
5. 腾讯朱雀AIGC值：原始值 / 换算百分比 / 是否过闸；未提供则写“未提供”
6. 六维合计：x，并列出六维明细
7. 段落重复：x；同时列完全重复段落、高度相似段落、重复长句数量
8. {"第 01 章是否已在文章最上方包含作品标签和主角人设。" if number == 1 else "本章是否误删或重复了正文头部元信息；非第 01 章不重复写作品标签和主角人设。"}
9. 情绪/动作/对话是否有模板感。
10. 需要回改的具体句段，必须引用原句或段落位置。
11. 机械门禁结论：读取 `reviews/` 机械门禁后写通过 / 不通过。
12. 审核结论：通过 / 不通过。机械门禁未过时，审核结论必须是不通过。
"""
    if mode == "revise":
        return base + f"""
请只回改第 {number:02d} 章正文，依据 `{apath}` 的审核问题处理。

要求：
1. 保留本章剧情推进和章末钩子。
2. 删除解释性旁白、模板化心理活动和整齐排比。
3. 优先修朱雀/本地风险高的信号：均匀句长、平滑转场、解释归纳腔、情绪命名、弱模型概率过稳、滑窗熵/TTR 过稳、段落复述。
4. 增加不可替换的动作、场景物件、口癖、错误信息、金额/时间戳和误会细节，但不能堆清单。
5. 保留人物不完美的声口：允许短句、打断、半句话、偏见和未说透的反应。
6. 输出完整第 {number:02d} 章正文，可直接覆盖 `{cpath}`。
7. 不写下一章。
"""
    return base + f"""
请产出第 {number:02d} 章正文，可直接保存到 `{cpath}`。

写作要求：
1. 一章一个明确主场景，开头 300 字内进入冲突。
2. 每 600-900 字有一次关系推进、信息反转或外部动作。
3. 人物不解释自己，靠动作、对话、细节露出情绪。
4. 章末必须留下下一章钩子，但不写下一章内容。
5. 写作时主动规避朱雀四维高分特征：突发性上避免连续均匀长句；困惑度上避免只选“正确安全”的套话；结构指纹上不要用“这让她意识到/不仅是更是/终于明白”“然而/与此同时/随后”组织段落；跨段一致性上让段长、对话密度、标点和情绪处理自然变化。
6. 同时规避近年检测器代理层高分特征：不要让每段都是同一种“概述+心理+转场”功能；动作段、对话段、物件细节段、沉默反应段要交替；局部词字多样度不能靠同义词替换，而要靠真实场景信息和角色声口。
7. 每章至少落 3 个不可替换的现场细节：具体物件、错误信息、时间/金额、口癖、打断动作或尴尬反应；不要堆感官清单。
8. 写完本章后停止，等待审核。
"""


def parse_target_range(value: str) -> tuple[int, int]:
    numbers = [int(item) for item in re.findall(r"\d+", str(value or ""))]
    if len(numbers) >= 2:
        return numbers[0], numbers[1]
    if len(numbers) == 1:
        return numbers[0], numbers[0]
    return 25000, 28000


def parse_float(value: str, default: float = 0.0) -> float:
    match = re.search(r"\d+(?:\.\d+)?", str(value or ""))
    if not match:
        return default
    return float(match.group(0))


def text_stats(path: Path) -> dict:
    if not path.exists():
        return {
            "chars": 0,
            "non_space_chars": 0,
            "content_chars": 0,
            "content_non_space_chars": 0,
            "cjk_chars": 0,
            "content_cjk_chars": 0,
            "lines": 0,
            "content_lines": 0,
            "updated_at": "",
        }
    text = path.read_text(encoding="utf-8")
    lines = text.splitlines()
    content = "\n".join(lines[1:]).strip() if lines and lines[0].lstrip().startswith("#") else text.strip()
    stat = path.stat()
    return {
        "chars": len(text),
        "non_space_chars": len(re.sub(r"\s+", "", text)),
        "content_chars": len(content),
        "content_non_space_chars": len(re.sub(r"\s+", "", content)),
        "cjk_chars": len(re.findall(r"[\u4e00-\u9fff]", text)),
        "content_cjk_chars": len(re.findall(r"[\u4e00-\u9fff]", content)),
        "lines": len(text.splitlines()),
        "content_lines": len(content.splitlines()) if content else 0,
        "updated_at": datetime.fromtimestamp(stat.st_mtime).isoformat(timespec="seconds"),
    }


def stats_from_text(text: str) -> dict:
    lines = text.splitlines()
    content = "\n".join(lines[1:]).strip() if lines and lines[0].lstrip().startswith("#") else text.strip()
    return {
        "chars": len(text),
        "non_space_chars": len(re.sub(r"\s+", "", text)),
        "content_chars": len(content),
        "content_non_space_chars": len(re.sub(r"\s+", "", content)),
        "cjk_chars": len(re.findall(r"[\u4e00-\u9fff]", text)),
        "content_cjk_chars": len(re.findall(r"[\u4e00-\u9fff]", content)),
        "lines": len(lines),
        "content_lines": len(content.splitlines()) if content else 0,
    }


def chapter_text_stats(path: Path) -> dict:
    stats = text_stats(path)
    if not path.exists():
        return stats
    text = path.read_text(encoding="utf-8")
    lines = text.splitlines()
    if lines and lines[0].lstrip().startswith("#"):
        kept = [lines[0]]
        kept.extend(line for line in lines[1:] if not line.strip().startswith(">"))
        text = "\n".join(kept)
    updated = stats_from_text(text)
    updated["updated_at"] = stats["updated_at"]
    return updated


def output_relative_path(path: Path) -> str:
    try:
        return str(path.relative_to(WORKSPACE))
    except ValueError:
        return str(path)


def timestamp_value(value: str) -> float:
    if not value:
        return 0.0
    try:
        return datetime.fromisoformat(value).timestamp()
    except ValueError:
        return 0.0


def artifact_target(name: str, book_target_min: int) -> int:
    target = artifact_meta(name)["target"]
    if target == "book_min":
        return book_target_min
    return int(target)


def enrich_artifact_stats(project_id: str, name: str, book_target_min: int) -> dict:
    path = artifact_path(project_id, name)
    stats = text_stats(path)
    meta = artifact_meta(name)
    target = artifact_target(name, book_target_min)
    content_chars = stats["content_non_space_chars"]
    progress = min(100, round(content_chars / target * 100, 1)) if target > 0 else 0
    if content_chars == 0:
        progress_status = "empty"
    elif progress >= 100:
        progress_status = "complete"
    else:
        progress_status = "generating"
    stats.update(
        {
            "name": name,
            "type": meta["type"],
            "type_label": meta["type_label"],
            "type_order": meta["type_order"],
            "target_chars": target,
            "progress_pct": progress,
            "progress_status": progress_status,
            "ready": content_chars > 0,
            "output_path": output_relative_path(path),
        }
    )
    return stats


def enrich_chapter_stats(project: dict, chapter: dict, chapter_target: int, ai_target: float) -> dict:
    number = int(chapter["number"])
    cpath = chapter_path(project, number)
    apath = chapter_audit_path(project, number)
    stats = chapter_text_stats(cpath)
    content_text = cpath.read_text(encoding="utf-8") if cpath.exists() else ""
    audit_text = apath.read_text(encoding="utf-8") if apath.exists() else ""
    if content_text.strip():
        sync_unified_ai_review_if_needed(project, number)
    ai_gate = combined_ai_gate(audit_text, content_text, ai_target, project=project, chapter_number=number)
    content_chars = stats["content_non_space_chars"]
    progress = min(100, round(content_chars / chapter_target * 100, 1)) if chapter_target > 0 else 0
    if chapter.get("status") == "passed" and ai_gate.get("passed") is True:
        progress_status = "complete"
    elif progress >= 100 and ai_gate.get("passed") is True:
        progress_status = "complete"
    elif content_chars > 0:
        progress_status = "generating"
    else:
        progress_status = "empty"
    stats.update(
        {
            "name": cpath.name,
            "chapter_number": number,
            "chapter_title": chapter.get("title") or f"第{number:02d}章",
            "type": "chapter",
            "type_label": DOCUMENT_TYPES["chapter"]["label"],
            "type_order": DOCUMENT_TYPES["chapter"]["order"],
            "target_chars": chapter_target,
            "progress_pct": progress,
            "progress_status": progress_status,
            "ready": content_chars > 0,
            "status": chapter.get("status", "locked"),
            "status_label": chapter_status_label(chapter.get("status", "locked")),
            "output_path": output_relative_path(cpath),
            "audit_output_path": output_relative_path(apath),
            "ai_review_json_path": output_relative_path(chapter_ai_review_json_path(project, number)),
            "ai_review_md_path": output_relative_path(chapter_ai_review_md_path(project, number)),
            "ai_gate": ai_gate,
        }
    )
    return stats


def document_type_progress(artifact_stats: list[dict]) -> list[dict]:
    groups = {}
    for stats in artifact_stats:
        group = groups.setdefault(
            stats["type"],
            {
                "type": stats["type"],
                "type_label": stats["type_label"],
                "type_order": stats["type_order"],
                "documents": 0,
                "ready": 0,
                "complete": 0,
                "content_non_space_chars": 0,
                "content_cjk_chars": 0,
                "progress_sum": 0.0,
            },
        )
        group["documents"] += 1
        group["ready"] += 1 if stats["ready"] else 0
        group["complete"] += 1 if stats["progress_status"] == "complete" else 0
        group["content_non_space_chars"] += stats["content_non_space_chars"]
        group["content_cjk_chars"] += stats["content_cjk_chars"]
        group["progress_sum"] += stats["progress_pct"]
    result = []
    for group in groups.values():
        documents = group["documents"] or 1
        group["progress_pct"] = round(group["progress_sum"] / documents, 1)
        del group["progress_sum"]
        result.append(group)
    result.sort(key=lambda item: item["type_order"])
    return result


def chapter_status_label(status: str) -> str:
    return {
        "locked": "未解锁",
        "writing": "写作中",
        "auditing": "待审核",
        "revising": "回改中",
        "passed": "已达标",
    }.get(status, status)


def latest_number(text: str, patterns: list[str]) -> Optional[float]:
    values = []
    for pattern in patterns:
        values.extend(re.findall(pattern, text, flags=re.I))
    if not values:
        return None
    return float(values[-1])


def normalize_percent_metric(value: Optional[float]) -> Optional[float]:
    if value is None:
        return None
    if 0 <= value <= 1:
        return round(value * 100, 2)
    return value


def content_lint_report_from_text(raw: str) -> dict:
    if any(
        func is None
        for func in [
            count_mismatch_issues,
            awkward_style_issues,
            semantic_clarity_issues,
            punctuation_emotion_issues,
        ]
    ):
        return {
            "available": False,
            "content_logic_issues": [],
            "awkward_style_issues": [],
            "semantic_clarity_issues": [],
            "punctuation_emotion_issues": [],
        }
    return {
        "available": True,
        "content_logic_issues": count_mismatch_issues(raw),
        "awkward_style_issues": awkward_style_issues(raw),
        "semantic_clarity_issues": semantic_clarity_issues(raw),
        "punctuation_emotion_issues": punctuation_emotion_issues(raw),
    }


def content_lint_issue_count(report: dict) -> int:
    return sum(
        len(report.get(name) or [])
        for name in [
            "content_logic_issues",
            "awkward_style_issues",
            "semantic_clarity_issues",
            "punctuation_emotion_issues",
        ]
    )


def content_lint_error_count(report: dict) -> int:
    issues = []
    for name in [
        "content_logic_issues",
        "awkward_style_issues",
        "semantic_clarity_issues",
        "punctuation_emotion_issues",
    ]:
        issues.extend(report.get(name) or [])
    return sum(1 for issue in issues if issue.get("severity") == "error")


def paragraph_duplicate_counts(aigc_report: dict) -> dict:
    dup = ((aigc_report.get("stats") or {}).get("paragraph_duplicates") or {})
    exact = int(dup.get("exact_count") or 0)
    similar = int(dup.get("similar_count") or 0)
    sentences = int(dup.get("sentence_duplicate_count") or 0)
    return {
        "exact_count": exact,
        "similar_count": similar,
        "sentence_duplicate_count": sentences,
        "total": exact + similar + sentences,
        "examples": dup.get("examples") or [],
    }


def build_unified_mechanical_gate(content_text: str, target: float) -> dict:
    content = content_text or ""
    missing = []
    failure_reasons = []
    violations = []
    content_report = content_lint_report_from_text(content)

    if not content.strip():
        missing.append("正文内容")
        return {
            "target": target,
            "engine": UNIFIED_AUDIT_ENGINE,
            "aigc_report": None,
            "content_lint": content_report,
            "paragraph_duplicates": {"exact_count": 0, "similar_count": 0, "sentence_duplicate_count": 0, "total": 0, "examples": []},
            "rule_violations": [],
            "passed": None,
            "failure_reasons": failure_reasons,
            "missing": missing,
        }
    if analyze_local_aigc_text is None:
        missing.append("自研AIGC检测器不可用")
        return {
            "target": target,
            "engine": UNIFIED_AUDIT_ENGINE,
            "aigc_report": None,
            "content_lint": content_report,
            "paragraph_duplicates": {"exact_count": 0, "similar_count": 0, "sentence_duplicate_count": 0, "total": 0, "examples": []},
            "rule_violations": [],
            "passed": None,
            "failure_reasons": failure_reasons,
            "missing": missing,
        }

    aigc_report = analyze_local_aigc_text(content)
    percent = float(aigc_report.get("aigc_percent") or 0)
    if percent > target:
        severity = "error" if percent >= 35 else "warning"
        violations.append(
            {
                "rule": "aigc_ratio",
                "severity": severity,
                "limit": f"{target}%",
                "actual": percent,
                "target": aigc_report.get("engine", UNIFIED_AUDIT_ENGINE),
            }
        )
        failure_reasons.append(f"自研AIGC值 {percent}% > {target}%")

    dup_counts = paragraph_duplicate_counts(aigc_report)
    if dup_counts["total"] > 0:
        violations.append(
            {
                "rule": "paragraph_duplicates",
                "severity": "error",
                "limit": "0",
                "actual": dup_counts["total"],
                "target": f"完全重复 {dup_counts['exact_count']}；高度相似 {dup_counts['similar_count']}；重复句 {dup_counts['sentence_duplicate_count']}",
            }
        )
        failure_reasons.append(
            f"段落/句子重复未清零：完全重复 {dup_counts['exact_count']}，高度相似 {dup_counts['similar_count']}，重复句 {dup_counts['sentence_duplicate_count']}"
        )

    lint_total = content_lint_issue_count(content_report)
    if lint_total > 0:
        lint_errors = content_lint_error_count(content_report)
        violations.append(
            {
                "rule": "content_lint",
                "severity": "error" if lint_errors else "warning",
                "limit": "0",
                "actual": lint_total,
                "target": f"error {lint_errors}",
            }
        )
        failure_reasons.append(f"内容硬检命中 {lint_total} 项")

    return {
        "target": target,
        "engine": aigc_report.get("engine", UNIFIED_AUDIT_ENGINE),
        "aigc_report": aigc_report,
        "content_lint": content_report,
        "paragraph_duplicates": dup_counts,
        "rule_violations": violations,
        "passed": not missing and not failure_reasons,
        "failure_reasons": failure_reasons,
        "missing": missing,
    }


def load_unified_mechanical_gate(project: dict, number: int, target: float) -> dict:
    for path in (chapter_ai_review_json_path(project, number), legacy_chapter_ai_review_json_path(project, number)):
        if not path.exists():
            continue
        try:
            payload = read_json(path)
            gate = payload.get("mechanical_gate")
            if gate:
                return gate
            if payload.get("aigc_report") or payload.get("rule_violations"):
                return mechanical_gate_from_payload(payload, target)
        except Exception:
            pass
    content_path = chapter_path(project, number)
    content = content_path.read_text(encoding="utf-8") if content_path.exists() else ""
    return build_unified_mechanical_gate(content, target)


def mechanical_gate_from_payload(payload: dict, target: float) -> dict:
    report = payload.get("aigc_report") or payload.get("report") or {}
    violations = payload.get("rule_violations") or []
    failure_reasons = []
    for violation in violations:
        if str(violation.get("severity", "")).lower() == "error":
            label = violation.get("rule", "mechanical_gate")
            actual = violation.get("actual", "")
            failure_reasons.append(f"{label} 未通过 actual={actual}")
    dup = paragraph_duplicate_counts(report) if report else {"exact_count": 0, "similar_count": 0, "sentence_duplicate_count": 0, "total": 0, "examples": []}
    return {
        "target": target,
        "engine": report.get("engine", UNIFIED_AUDIT_ENGINE),
        "aigc_report": report,
        "content_lint": {},
        "paragraph_duplicates": dup,
        "rule_violations": violations,
        "passed": not failure_reasons,
        "failure_reasons": failure_reasons,
        "missing": [],
    }


def render_unified_ai_review_markdown(number: int, gate: dict) -> str:
    report = gate.get("aigc_report") or {}
    dup = gate.get("paragraph_duplicates") or {}
    lint = gate.get("content_lint") or {}
    lines = [
        f"# 第{number:03d}章 统一审核",
        "",
        "> 本文件汇总机械门禁、AI 味信号和后续人工/Editor复审；结构化机械事实保留在同目录 JSON。",
        "",
        "## 机械门禁",
        "",
        f"- 引擎：{gate.get('engine', UNIFIED_AUDIT_ENGINE)}",
        f"- AI占比：{float(report.get('ai_ratio_percent') or report.get('aigc_percent') or 0):.2f}%",
        f"- 融合值：{float(report.get('blended_aigc_percent') or 0):.2f}%",
        f"- 朱雀分片风险下限：{float(report.get('segment_risk_floor_percent') or 0):.2f}%",
        f"- 风险标签：{report.get('risk_label', '')}｜置信度：{report.get('confidence', '')}",
        f"- 机械门禁：{'通过' if gate.get('passed') is True else '未通过'}",
        "",
        "## 高风险信号",
        "",
    ]
    for signal in (report.get("top_aigc_signals") or [])[:4]:
        lines.append(f"- {signal.get('score', 0)}｜{signal.get('dimension', '')}｜{signal.get('name', '')}：{signal.get('evidence', '')}")
    if not (report.get("top_aigc_signals") or []):
        dims = ((report.get("zhuque_dimensions") or {}).get("dimensions") or {})
        sorted_dims = sorted(dims.values(), key=lambda item: float(item.get("score") or 0), reverse=True)
        for dim in sorted_dims[:4]:
            lines.append(f"- {float(dim.get('score') or 0):.2f}%｜{dim.get('name', '')}")
            for sig in (dim.get("signals") or [])[:2]:
                lines.append(f"  - {sig.get('name', '')}：{sig.get('evidence', '')}")

    lines.extend(
        [
            "",
            "## 重复与内容硬检",
            "",
            f"- 段落重复：完全重复 {dup.get('exact_count', 0)}；高度相似 {dup.get('similar_count', 0)}；重复句 {dup.get('sentence_duplicate_count', 0)}",
            f"- 内容逻辑：{len(lint.get('content_logic_issues') or [])}",
            f"- 别扭/库存明喻：{len(lint.get('awkward_style_issues') or [])}",
            f"- 语义清晰：{len(lint.get('semantic_clarity_issues') or [])}",
            f"- 标点情绪层级：{len(lint.get('punctuation_emotion_issues') or [])}",
        ]
    )
    for name in ["content_logic_issues", "awkward_style_issues", "semantic_clarity_issues", "punctuation_emotion_issues"]:
        for issue in (lint.get(name) or [])[:3]:
            lines.append(f"  - {issue.get('severity', '')}｜{issue.get('rule', '')}｜line {issue.get('line', '-')}: {issue.get('target', '')}")

    if gate.get("rule_violations"):
        lines.extend(["", "## 规则命中", ""])
        for violation in gate.get("rule_violations") or []:
            line = f"- {violation.get('severity', '')}｜{violation.get('rule', '')}｜actual={violation.get('actual')}"
            if violation.get("target"):
                line += f"｜target={violation.get('target')}"
            lines.append(line)
    if gate.get("failure_reasons"):
        lines.extend(["", "## 失败原因", ""])
        for reason in gate.get("failure_reasons") or []:
            lines.append(f"- {reason}")
    lines.extend(["", "## Editor复审", "", "- 待复审。"])
    return "\n".join(lines) + "\n"


def save_unified_ai_review(project: dict, number: int) -> dict:
    reviews_dir(project).mkdir(parents=True, exist_ok=True)
    content = chapter_path(project, number).read_text(encoding="utf-8") if chapter_path(project, number).exists() else ""
    target = parse_float(project.get("fields", {}).get("ai_taste_target", "5"), 5.0)
    gate = build_unified_mechanical_gate(content, target)
    payload = {
        "chapter": number,
        "generated_at": now(),
        "mechanical_gate": gate,
        "aigc_report": gate.get("aigc_report"),
        "rule_violations": gate.get("rule_violations"),
    }
    chapter_ai_review_json_path(project, number).write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    chapter_ai_review_md_path(project, number).write_text(render_unified_ai_review_markdown(number, gate), encoding="utf-8")
    return gate


def sync_unified_ai_review_if_needed(project: dict, number: int) -> None:
    cpath = chapter_path(project, number)
    if not cpath.exists() or not cpath.read_text(encoding="utf-8").strip():
        return
    jpath = chapter_ai_review_json_path(project, number)
    mpath = chapter_ai_review_md_path(project, number)
    chapter_mtime = cpath.stat().st_mtime
    if not jpath.exists() or not mpath.exists() or jpath.stat().st_mtime < chapter_mtime or mpath.stat().st_mtime < chapter_mtime:
        save_unified_ai_review(project, number)


def latest_percent_metric(text: str, patterns: list[str]) -> Optional[float]:
    values = []
    for pattern in patterns:
        for match in re.finditer(pattern, text, flags=re.I):
            values.append(float(match.group(1)))
    if not values:
        return None
    return normalize_percent_metric(values[-1])


def extract_ai_metrics(text: str, target: float) -> dict:
    ai_percent = latest_percent_metric(
        text,
        [
            r"AI\s*创作度[^0-9]{0,24}(\d+(?:\.\d+)?)(?:\s*%)?",
            r"AI\s*味\s*(?!风险分)[^0-9]{0,24}(\d+(?:\.\d+)?)(?:\s*%)?",
            r"AI\s*占比[^0-9]{0,24}(\d+(?:\.\d+)?)(?:\s*%)?",
        ],
    )
    external_aigc_percent = latest_percent_metric(
        text,
        [
            r"(?:腾讯\s*朱雀|朱雀)[^\n]{0,50}(?:AIGC|AI)?(?:值|分|score|检测|概率)?[^0-9]{0,24}(\d+(?:\.\d+)?)(?:\s*%)?",
            r"(?:外部\s*AIGC\s*检测|AIGC\s*(?:值|分|score|检测值|概率)|AI\s*检测值)[^0-9]{0,24}(\d+(?:\.\d+)?)(?:\s*%)?",
        ],
    )
    local_ai_risk_score = latest_number(
        text,
        [
            r"(?:本地\s*)?AI\s*味风险分[^0-9]{0,24}(\d+(?:\.\d+)?)",
            r"本地信号分[^0-9]{0,24}(\d+(?:\.\d+)?)",
        ],
    )
    six_dim_total = latest_number(
        text,
        [
            r"六维(?:\s*AI\s*倾向分)?(?:合计|总分)?[^0-9]{0,24}(\d+(?:\.\d+)?)",
        ],
    )
    paragraph_duplicates = latest_number(
        text,
        [
            r"(?:段落级重复|重复段落|段落重复)[^0-9]{0,24}(\d+(?:\.\d+)?)",
        ],
    )

    missing = []
    if ai_percent is None:
        missing.append("AI创作度")
    if six_dim_total is None:
        missing.append("六维合计")
    if paragraph_duplicates is None:
        missing.append("段落重复")

    local_ai_risk_limit = 35.0
    passed = None
    failure_reasons = []
    if ai_percent is not None and ai_percent > target:
        failure_reasons.append(f"AI创作度 {ai_percent}% > {target}%")
        passed = False
    elif external_aigc_percent is not None and external_aigc_percent > target:
        failure_reasons.append(f"外部AIGC检测 {external_aigc_percent}% > {target}%")
        passed = False
    elif local_ai_risk_score is not None and local_ai_risk_score > local_ai_risk_limit:
        failure_reasons.append(f"本地AI味风险分 {local_ai_risk_score}/100 > {local_ai_risk_limit}/100")
        passed = False
    elif six_dim_total is not None and six_dim_total > 1:
        failure_reasons.append(f"六维合计 {six_dim_total} > 1")
        passed = False
    elif paragraph_duplicates is not None and paragraph_duplicates != 0:
        failure_reasons.append(f"段落重复 {paragraph_duplicates} != 0")
        passed = False
    elif ai_percent is not None and six_dim_total is not None and paragraph_duplicates is not None:
        passed = True

    return {
        "target": target,
        "ai_percent": ai_percent,
        "external_aigc_percent": external_aigc_percent,
        "local_ai_risk_score": local_ai_risk_score,
        "local_ai_risk_limit": local_ai_risk_limit,
        "six_dim_total": six_dim_total,
        "paragraph_duplicates": paragraph_duplicates,
        "passed": passed,
        "failure_reasons": failure_reasons,
        "missing": missing,
    }


def auto_aigc_gate_from_text(text: str, target: float) -> dict:
    content = text or ""
    if not content.strip():
        return {
            "target": target,
            "engine": UNIFIED_AUDIT_ENGINE,
            "aigc_value": None,
            "aigc_percent": None,
            "risk_label": "",
            "confidence": "",
            "passed": None,
            "failure_reasons": [],
            "missing": ["正文内容"],
            "reasons": [],
            "human_signals": [],
            "stats": {},
        }
    if analyze_local_aigc_text is None:
        return {
            "target": target,
            "engine": UNIFIED_AUDIT_ENGINE,
            "aigc_value": None,
            "aigc_percent": None,
            "risk_label": "",
            "confidence": "",
            "passed": None,
            "failure_reasons": [],
            "missing": ["自研AIGC检测器不可用"],
            "reasons": [],
            "human_signals": [],
            "stats": {},
        }

    report = analyze_local_aigc_text(content)
    percent = float(report.get("aigc_percent") or 0)
    passed = percent <= target
    failure_reasons = [] if passed else [f"自研AIGC值 {percent}% > {target}%"]
    return {
        "target": target,
        "engine": report.get("engine", UNIFIED_AUDIT_ENGINE),
        "aigc_value": report.get("aigc_value"),
        "aigc_percent": percent,
        "ai_ratio_percent": report.get("ai_ratio_percent", percent),
        "zhuque_dimensions": report.get("zhuque_dimensions", {}),
        "latest_detector_proxy": report.get("latest_detector_proxy", {}),
        "legacy_heuristic_percent": report.get("legacy_heuristic_percent"),
        "final_blend_weights": report.get("final_blend_weights", {}),
        "risk_label": report.get("risk_label", ""),
        "confidence": report.get("confidence", ""),
        "passed": passed,
        "failure_reasons": failure_reasons,
        "missing": [],
        "reasons": report.get("reasons", []),
        "human_signals": report.get("human_signals", []),
        "stats": report.get("stats", {}),
    }


def combined_ai_gate(audit_text: str, content_text: str, target: float, project: Optional[dict] = None, chapter_number: Optional[int] = None) -> dict:
    audit_gate = extract_ai_metrics(audit_text, target)
    auto_gate = auto_aigc_gate_from_text(content_text, target)
    if project is not None and chapter_number is not None:
        mechanical_gate = load_unified_mechanical_gate(project, chapter_number, target)
    else:
        mechanical_gate = build_unified_mechanical_gate(content_text, target)
    missing = list(audit_gate.get("missing") or [])
    failure_reasons = list(audit_gate.get("failure_reasons") or [])
    if auto_gate.get("missing"):
        missing.extend(auto_gate["missing"])
    if auto_gate.get("failure_reasons"):
        failure_reasons.extend(auto_gate["failure_reasons"])
    if mechanical_gate.get("missing"):
        missing.extend(mechanical_gate["missing"])
    if mechanical_gate.get("failure_reasons"):
        failure_reasons.extend(mechanical_gate["failure_reasons"])
    missing = list(dict.fromkeys(missing))
    failure_reasons = list(dict.fromkeys(failure_reasons))

    if audit_gate.get("passed") is False or auto_gate.get("passed") is False or mechanical_gate.get("passed") is False:
        passed = False
    elif audit_gate.get("passed") is True and auto_gate.get("passed") is True and mechanical_gate.get("passed") is True:
        passed = True
    else:
        passed = None

    gate = dict(audit_gate)
    gate.update(
        {
            "passed": passed,
            "failure_reasons": failure_reasons,
            "missing": missing,
            "audit_gate": audit_gate,
            "auto_aigc_gate": auto_gate,
            "mechanical_gate": mechanical_gate,
        }
    )
    return gate


def has_pass_conclusion(text: str) -> bool:
    return re.search(r"(?:审核结论|结论)\s*[：:]\s*通过(?:\s|$|。|，)", text) is not None


def image_plan_missing(text: str) -> list[str]:
    content = re.sub(r"\s+", "", text or "")
    missing = []
    if len(content) < 200:
        missing.append("图片生成方案内容不足")
    checks = [
        ("封面图提示词", r"(?:封面图|封面提示词|cover)"),
        ("关键场景图提示词", r"(?:关键场景图|场景图|插图|scene)"),
        ("图片模型提示词", r"(?:图片提示词|生成提示词|正向提示词|prompt)"),
        ("图片路径或保存说明", r"(?:图片文件|保存路径|输出路径|images/)"),
    ]
    for label, pattern in checks:
        if re.search(pattern, text or "", re.IGNORECASE) is None:
            missing.append(label)
    if re.search(r"生成失败|图片命令退出码|图片命令超时", text or ""):
        missing.append("图片生成失败")
    return missing


def clean_excerpt(text: str, limit: int = 180) -> str:
    value = re.sub(r"\s+", " ", text or "").strip()
    if len(value) <= limit:
        return value
    return value[:limit].rstrip("，。；、 ") + "..."


def content_paragraphs(text: str) -> list[str]:
    paragraphs = []
    for block in re.split(r"\n\s*\n+", text or ""):
        value = " ".join(line.strip() for line in block.splitlines() if line.strip())
        if not value:
            continue
        if value.startswith("#"):
            continue
        if re.match(r"^(?:作品标签|主角人设|金句|简介|话题标签)\s*[：:]", value):
            continue
        if len(re.sub(r"\s+", "", value)) < 12:
            continue
        paragraphs.append(value)
    return paragraphs


def split_story_sections(text: str, title: str) -> list[dict]:
    matches = list(re.finditer(r"(?m)^#{1,3}\s*(第[0-9零一二三四五六七八九十百]+章[^\n]*)\s*$", text or ""))
    if not matches:
        return [{"label": title, "text": text or ""}]
    sections = []
    for index, match in enumerate(matches):
        start = match.end()
        end = matches[index + 1].start() if index + 1 < len(matches) else len(text)
        sections.append({"label": match.group(1).strip(), "text": text[start:end].strip()})
    return sections or [{"label": title, "text": text or ""}]


def select_story_scene(sections: list[dict], slot: str) -> dict:
    if not sections:
        return {"source": "终版正文", "excerpt": ""}
    if slot == "start":
        section = sections[0]
        paragraphs = content_paragraphs(section["text"])
        excerpt = paragraphs[0] if paragraphs else section["text"]
    elif slot == "middle":
        section = sections[len(sections) // 2]
        paragraphs = content_paragraphs(section["text"])
        excerpt = paragraphs[len(paragraphs) // 2] if paragraphs else section["text"]
    else:
        section = sections[-1]
        paragraphs = content_paragraphs(section["text"])
        excerpt = paragraphs[-1] if paragraphs else section["text"]
    return {"source": section.get("label") or "终版正文", "excerpt": clean_excerpt(excerpt, 220)}


def visual_style_hint(project: dict) -> str:
    direction = project.get("direction", "custom")
    fields = project.get("fields", {})
    tags = " ".join(str(fields.get(key, "")) for key in ["tags", "work_tags", "style_notes"])
    if direction == "shuangnanzhu":
        return "现代都市情感，电影感写实光影，人物关系张力明确，克制高对比"
    if direction == "baihe":
        return "现代情感写实，细腻冷暖对照，人物眼神和距离感突出"
    if direction == "shuangwen" or any(token in tags for token in ["复仇", "打脸", "爽文", "反转"]):
        return "都市爽文视觉，强冲突构图，清晰轮廓，高对比但不廉价"
    return "番茄短篇网文封面质感，写实电影感，情绪直给，画面干净"


def character_consistency_text(project: dict) -> str:
    fields = project.get("fields", {})
    a = fields.get("character_a", "").strip() or "主角A：按故事圣经设定，保持年龄、气质、服装和标志物一致"
    b = fields.get("character_b", "").strip() or "主角B：按故事圣经设定，保持年龄、气质、服装和标志物一致"
    profile = protagonist_profile_text(project)
    lines = [
        f"- 主角 A：{clean_excerpt(a, 160)}",
        f"- 主角 B：{clean_excerpt(b, 160)}",
    ]
    if profile and profile != "待生成":
        lines.append(f"- 主角人设对照：{profile}")
    lines.append("- 同一角色在所有图片中保持发型、脸型、年龄感、常穿色系、随身物件一致；不要每张图换脸。")
    return "\n".join(lines)


def build_image_jobs(project: dict, final_text: str) -> list[dict]:
    sections = split_story_sections(final_text, project.get("title", "未命名短篇"))
    start_scene = select_story_scene(sections, "start")
    middle_scene = select_story_scene(sections, "middle")
    ending_scene = select_story_scene(sections, "end")
    style_hint = visual_style_hint(project)
    consistency = re.sub(r"\s+", " ", character_consistency_text(project)).strip()
    title = project.get("title", "未命名短篇")
    negative_prompt = "不要文字、水印、logo、二维码、低清晰度、塑料皮肤、错误肢体、多余手指、脸部崩坏、过度磨皮、廉价滤镜、血腥猎奇、未成年人擦边。"

    specs = [
        {
            "id": "cover",
            "label": "封面图",
            "source": "终版正文整体气质",
            "filename": "cover.png",
            "prompt_file": "cover.prompt.txt",
            "scene": f"围绕《{title}》的核心冲突做竖版网文封面，人物关系和情绪必须一眼能读出；参考开篇钩子：{start_scene['excerpt']}",
            "composition": "3:4 竖版封面构图，主角占画面中心 60%，上方和下方预留后期加书名与作者名的安全区域，画面内不直接生成文字。",
        },
        {
            "id": "scene_01",
            "label": "关键场景图 01：开篇钩子",
            "source": start_scene["source"],
            "filename": "scene_01.png",
            "prompt_file": "scene_01.prompt.txt",
            "scene": start_scene["excerpt"],
            "composition": "横竖皆可裁切的电影剧照构图，优先突出开篇冲突和人物第一反应。",
        },
        {
            "id": "scene_02",
            "label": "关键场景图 02：中段爆点",
            "source": middle_scene["source"],
            "filename": "scene_02.png",
            "prompt_file": "scene_02.prompt.txt",
            "scene": middle_scene["excerpt"],
            "composition": "中景构图，人物动作和场景物件共同传达反转，不做解释性海报。",
        },
        {
            "id": "scene_03",
            "label": "关键场景图 03：结尾落点",
            "source": ending_scene["source"],
            "filename": "scene_03.png",
            "prompt_file": "scene_03.prompt.txt",
            "scene": ending_scene["excerpt"],
            "composition": "结尾情绪落点构图，保留人物之间的距离、沉默或对峙，不做大团圆模板表情。",
        },
    ]
    jobs = []
    for spec in specs:
        output_path = images_dir(project) / spec["filename"]
        prompt_path = images_dir(project) / spec["prompt_file"]
        positive_prompt = (
            f"{spec['label']}，{style_hint}。"
            f"画面内容：{spec['scene']}。"
            f"角色一致性：{consistency}。"
            f"构图：{spec['composition']}。"
            "高质量数字绘画，电影感光影，清晰人物轮廓，真实材质，细节克制，适合中文网文平台。"
        )
        jobs.append(
            {
                **spec,
                "output_path": output_relative_path(output_path),
                "output_path_abs": str(output_path),
                "prompt_path": output_relative_path(prompt_path),
                "prompt_path_abs": str(prompt_path),
                "positive_prompt": positive_prompt,
                "negative_prompt": negative_prompt,
                "status": "pending_generation",
                "status_label": "待生成",
                "note": "未配置图片生成命令，已生成可复制提示词。",
            }
        )
    return jobs


def render_image_prompt_file(job: dict) -> str:
    return "\n".join(
        [
            f"# {job['label']}",
            "",
            f"- 章节来源：{job['source']}",
            f"- 建议文件名：{Path(job['output_path']).name}",
            f"- 保存路径：{job['output_path']}",
            f"- 生成状态：{job['status_label']}",
            "",
            "## 正向提示词",
            "",
            job["positive_prompt"],
            "",
            "## 负向提示词",
            "",
            job["negative_prompt"],
            "",
        ]
    )


def run_image_generation_command(project: dict, job: dict) -> None:
    command = os.environ.get("NOVEL_STUDIO_IMAGE_GENERATOR_CMD", "").strip()
    if not command:
        return
    timeout = parse_int(os.environ.get("NOVEL_STUDIO_IMAGE_GENERATOR_TIMEOUT", "300"), 300, 10, 3600)
    env = os.environ.copy()
    env.update(
        {
            "IMAGE_JOB_ID": job["id"],
            "IMAGE_TITLE": project.get("title", ""),
            "IMAGE_PROMPT": job["positive_prompt"],
            "IMAGE_NEGATIVE_PROMPT": job["negative_prompt"],
            "IMAGE_PROMPT_FILE": job["prompt_path_abs"],
            "IMAGE_OUTPUT": job["output_path_abs"],
            "IMAGE_OUTPUT_DIR": str(images_dir(project)),
        }
    )
    try:
        result = subprocess.run(
            command,
            shell=True,
            cwd=str(images_dir(project)),
            env=env,
            text=True,
            capture_output=True,
            timeout=timeout,
            check=False,
        )
    except subprocess.TimeoutExpired:
        job["status"] = "failed"
        job["status_label"] = "生成失败"
        job["note"] = f"图片命令超时：{timeout}s"
        return

    output_path = Path(job["output_path_abs"])
    if result.returncode == 0 and output_path.exists() and output_path.stat().st_size > 0:
        job["status"] = "generated"
        job["status_label"] = "已生成"
        job["note"] = "图片命令已生成文件。"
        return
    stderr = clean_excerpt(result.stderr or result.stdout or "命令未生成图片文件", 500)
    job["status"] = "failed"
    job["status_label"] = "生成失败"
    job["note"] = f"图片命令退出码 {result.returncode}：{stderr}"


def render_image_plan_markdown(project: dict, jobs: list[dict], source_context: dict) -> str:
    generator_configured = bool(os.environ.get("NOVEL_STUDIO_IMAGE_GENERATOR_CMD", "").strip())
    generated_count = sum(1 for job in jobs if job["status"] == "generated")
    pending_count = sum(1 for job in jobs if job["status"] == "pending_generation")
    failed_count = sum(1 for job in jobs if job["status"] == "failed")
    lines = [
        "# 图片生成方案",
        "",
        f"- 项目：{project.get('title', '')}",
        f"- 生成时间：{now()}",
        f"- 基于文件：`正文.md`、`故事圣经.md`、`审核报告.md`",
        f"- 图片目录：`{output_relative_path(images_dir(project))}`",
        f"- 图片任务清单：`{output_relative_path(images_dir(project) / 'image_jobs.json')}`",
        f"- 图片模型命令：{'已配置 NOVEL_STUDIO_IMAGE_GENERATOR_CMD' if generator_configured else '未配置，以下图片标记为待生成'}",
        f"- 执行状态：已生成 {generated_count} 张，待生成 {pending_count} 张，失败 {failed_count} 张",
        "",
        "## 角色一致性说明",
        "",
        character_consistency_text(project),
        "",
        "## 文字禁用说明",
        "",
        "- 默认不让图片模型直接生成书名、作者名、平台角标、宣传字和任何水印；封面只预留安全留白，文字后期排版。",
        "- 关键场景图只做画面，不加字幕、对话框和解释性标语。",
        "",
    ]
    for job in jobs:
        lines.extend(
            [
                f"## {job['label']}",
                "",
                f"- 章节来源：{job['source']}",
                f"- 画面主体：{job['scene']}",
                f"- 构图与光色：{job['composition']}",
                f"- 建议文件名：`{Path(job['output_path']).name}`",
                f"- 图片文件保存路径：`{job['output_path']}`",
                f"- 图片提示词文件：`{job['prompt_path']}`",
                f"- 生成状态：{job['status_label']}",
                f"- 备注：{job['note']}",
                "",
                "### 正向提示词",
                "",
                job["positive_prompt"],
                "",
                "### 负向提示词",
                "",
                job["negative_prompt"],
                "",
            ]
        )
    lines.extend(
        [
            "## 源文件校验",
            "",
            f"- 正文字数：{source_context.get('final_body_chars', 0)} 非空字符。",
            f"- 审核报告：{source_context.get('audit_status', '已读取')}。",
            f"- 方案刷新依据：任一源文件晚于本方案时，必须重新执行图片生成方案。",
            "",
        ]
    )
    return "\n".join(lines)


def image_source_paths(project: dict) -> list[tuple[str, Path]]:
    return [
        ("正文.md", artifact_path(project["id"], "正文.md")),
        ("故事圣经.md", artifact_path(project["id"], "故事圣经.md")),
        ("审核报告.md", artifact_path(project["id"], "审核报告.md")),
    ]


def image_plan_outdated_source(project: dict) -> str:
    plan = artifact_path(project["id"], "图片生成方案.md")
    if not plan.exists() or text_stats(plan)["content_non_space_chars"] == 0:
        return "图片生成方案.md"
    plan_mtime = plan.stat().st_mtime
    for name, path in image_source_paths(project):
        if path.exists() and path.stat().st_mtime > plan_mtime:
            return name
    return ""


def image_package_prerequisites(project: dict) -> tuple[bool, list[str], dict]:
    project_id = project["id"]
    final_body_path = artifact_path(project_id, "正文.md")
    bible_path = artifact_path(project_id, "故事圣经.md")
    audit_path = artifact_path(project_id, "审核报告.md")
    final_text = final_body_path.read_text(encoding="utf-8") if final_body_path.exists() else ""
    bible_text = bible_path.read_text(encoding="utf-8") if bible_path.exists() else ""
    audit_text = audit_path.read_text(encoding="utf-8") if audit_path.exists() else ""
    target_min, target_max = parse_target_range(project.get("fields", {}).get("target_chars", "25000-28000"))
    ai_target = parse_float(project.get("fields", {}).get("ai_taste_target", "5"), 5.0)
    final_stats = text_stats(final_body_path)
    bible_stats = text_stats(bible_path)
    audit_stats = text_stats(audit_path)
    reasons = []
    audit_is_stale = bool(final_body_path.exists() and audit_path.exists() and audit_path.stat().st_mtime < final_body_path.stat().st_mtime)

    if final_stats["content_non_space_chars"] == 0:
        reasons.append("终版正文未合并：正文.md 为空")
    elif final_stats["content_non_space_chars"] < target_min:
        reasons.append(f"终版正文字符未达标：{final_stats['content_non_space_chars']}/{target_min}")
    elif final_stats["content_non_space_chars"] > target_max:
        reasons.append(f"终版正文字符超上限：{final_stats['content_non_space_chars']}/{target_max}")
    if bible_stats["content_non_space_chars"] == 0:
        reasons.append("故事圣经.md 为空")
    if audit_stats["content_non_space_chars"] == 0:
        reasons.append("审核报告.md 为空")
    elif audit_is_stale:
        reasons.append("审核报告早于终版正文，需要先复审")

    audit_gate = {"passed": None}
    if final_text.strip() and audit_text.strip() and not audit_is_stale:
        audit_gate = combined_ai_gate(audit_text, final_text, ai_target)
        if audit_gate.get("passed") is not True:
            detail = "、".join(audit_gate.get("failure_reasons") or audit_gate.get("missing") or ["终版审核未通过"])
            reasons.append("终版审核未通过：" + detail)
        elif not has_pass_conclusion(audit_text):
            reasons.append("审核报告缺少通过结论")

    return (
        not reasons,
        list(dict.fromkeys(reasons)),
        {
            "final_text": final_text,
            "bible_text": bible_text,
            "audit_text": audit_text,
            "final_body_chars": final_stats["content_non_space_chars"],
            "audit_status": "通过" if audit_gate.get("passed") is True else "未通过",
        },
    )


def ensure_image_package(project: dict, force: bool = False, mark_stage: bool = False) -> dict:
    ready, reasons, context = image_package_prerequisites(project)
    if not ready:
        if project.get("stages", {}).get("image", {}).get("status") == "done":
            project["stages"]["image"]["status"] = "pending"
        return {"ready": False, "generated": False, "reasons": reasons}

    plan_path = artifact_path(project["id"], "图片生成方案.md")
    plan_text = plan_path.read_text(encoding="utf-8") if plan_path.exists() else ""
    outdated_source = image_plan_outdated_source(project)
    if not force and not outdated_source and not image_plan_missing(plan_text):
        if mark_stage:
            project["stages"]["image"]["status"] = "done"
        return {"ready": True, "generated": False, "reasons": [], "unchanged": True}

    images_dir(project).mkdir(parents=True, exist_ok=True)
    jobs = build_image_jobs(project, context["final_text"])
    for job in jobs:
        Path(job["prompt_path_abs"]).write_text(render_image_prompt_file(job), encoding="utf-8")
        run_image_generation_command(project, job)
        Path(job["prompt_path_abs"]).write_text(render_image_prompt_file(job), encoding="utf-8")

    manifest = {
        "project_id": project["id"],
        "title": project.get("title", ""),
        "generated_at": now(),
        "source_files": [output_relative_path(path) for _name, path in image_source_paths(project)],
        "generator": {
            "env": "NOVEL_STUDIO_IMAGE_GENERATOR_CMD",
            "configured": bool(os.environ.get("NOVEL_STUDIO_IMAGE_GENERATOR_CMD", "").strip()),
        },
        "jobs": [
            {
                key: job[key]
                for key in [
                    "id",
                    "label",
                    "source",
                    "filename",
                    "output_path",
                    "prompt_path",
                    "status",
                    "status_label",
                    "note",
                ]
            }
            for job in jobs
        ],
    }
    (images_dir(project) / "image_jobs.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    plan_path.write_text(render_image_plan_markdown(project, jobs, context), encoding="utf-8")
    failed_jobs = [job for job in jobs if job["status"] == "failed"]
    if failed_jobs:
        if project.get("stages", {}).get("image", {}).get("status") == "done":
            project["stages"]["image"]["status"] = "pending"
        labels = "、".join(job["label"] for job in failed_jobs[:3])
        return {"ready": False, "generated": True, "reasons": [f"图片生成失败：{labels}"], "jobs": manifest["jobs"]}
    if mark_stage:
        project["stages"]["image"]["status"] = "done"
    return {"ready": True, "generated": True, "reasons": [], "jobs": manifest["jobs"]}


def refresh_image_package_after_source_change(project: dict, source_name: str) -> dict:
    if source_name in {"正文.md", "故事圣经.md", "审核报告.md"} and image_plan_outdated_source(project):
        if project.get("stages", {}).get("image", {}).get("status") == "done":
            project["stages"]["image"]["status"] = "pending"
    result = ensure_image_package(project, force=False, mark_stage=True)
    if result.get("ready"):
        update_current_stage(project)
    return result


def project_metrics(project: dict) -> dict:
    project = ensure_project_structure(project, persist=True)
    project_id = project["id"]
    statuses = [project["stages"].get(stage["id"], {}).get("status", "pending") for stage in STAGES]
    stage_counts = {status: statuses.count(status) for status in ["done", "in_progress", "pending", "blocked"]}
    stage_total = len(STAGES)
    done = stage_counts["done"]
    current_stage_id = project.get("current_stage", "bible")
    current_stage = next((stage for stage in STAGES if stage["id"] == current_stage_id), STAGES[0])
    target_min, target_max = parse_target_range(project.get("fields", {}).get("target_chars", "25000-28000"))
    design_missing = design_artifact_missing(project)

    base_artifact_stats = []
    for name in ARTIFACTS:
        base_artifact_stats.append(enrich_artifact_stats(project_id, name, target_min))

    chapter_total = max(1, len(project.get("chapters", [])))
    chapter_target = max(1, round(target_min / chapter_total))
    ai_target = parse_float(project.get("fields", {}).get("ai_taste_target", "5"), 5.0)
    chapter_stats = [
        enrich_chapter_stats(project, chapter, chapter_target, ai_target)
        for chapter in project.get("chapters", [])
    ]
    artifact_stats = base_artifact_stats + chapter_stats

    final_body = next(item for item in base_artifact_stats if item["name"] == "正文.md")
    draft_body = next(item for item in base_artifact_stats if item["name"] == "正文草稿.md")
    final_audit = next(item for item in base_artifact_stats if item["name"] == "审核报告.md")
    image_plan = next(item for item in base_artifact_stats if item["name"] == "图片生成方案.md")
    chapter_body = {
        "content_non_space_chars": sum(item["content_non_space_chars"] for item in chapter_stats),
        "content_cjk_chars": sum(item["content_cjk_chars"] for item in chapter_stats),
        "content_lines": sum(item["content_lines"] for item in chapter_stats),
    }
    if final_body["content_non_space_chars"] > 0:
        body_source = "正文.md"
        body_stats = final_body
    elif chapter_body["content_non_space_chars"] > 0:
        body_source = "分章合计"
        body_stats = chapter_body
    else:
        body_source = "正文草稿.md"
        body_stats = draft_body
    target_pct = 0
    if target_min > 0:
        target_pct = min(100, round(body_stats["content_non_space_chars"] / target_min * 100, 1))

    audit_text = "\n".join(
        artifact_path(project_id, name).read_text(encoding="utf-8")
        for name in ["降AI味记录.md", "审核报告.md"]
        if artifact_path(project_id, name).exists()
    )
    stage_artifacts = []
    for stage in STAGES:
        stats = next(item for item in base_artifact_stats if item["name"] == stage["artifact"])
        stage_artifacts.append(
            {
                "stage_id": stage["id"],
                "stage_label": stage["label"],
                "artifact": stage["artifact"],
                "type": stats["type"],
                "type_label": stats["type_label"],
                "ready": stats["ready"],
                "target_chars": stats["target_chars"],
                "progress_pct": stats["progress_pct"],
                "progress_status": stats["progress_status"],
                "content_non_space_chars": stats["content_non_space_chars"],
                "content_cjk_chars": stats["content_cjk_chars"],
                "content_lines": stats["content_lines"],
            }
        )
    ready_artifacts = sum(1 for item in artifact_stats if item["ready"])
    complete_artifacts = sum(1 for item in artifact_stats if item["progress_status"] == "complete")
    document_progress_pct = round(sum(item["progress_pct"] for item in artifact_stats) / len(artifact_stats), 1)
    total_content = {
        "content_non_space_chars": sum(item["content_non_space_chars"] for item in artifact_stats),
        "content_cjk_chars": sum(item["content_cjk_chars"] for item in artifact_stats),
        "content_lines": sum(item["content_lines"] for item in artifact_stats),
    }
    chapter_missing = [
        f"第{item['chapter_number']:02d}章"
        for item in chapter_stats
        if item["status"] != "passed" or item.get("ai_gate", {}).get("passed") is not True
    ]
    chapter_gate_failures = []
    for item in chapter_stats:
        gate = item.get("ai_gate", {})
        if item["status"] == "passed" and gate.get("passed") is not True:
            detail = "、".join(gate.get("failure_reasons") or gate.get("missing") or ["审核/自研AIGC门控未过"])
            chapter_gate_failures.append(f"第{item['chapter_number']:02d}章：{detail}")
    body_chars = body_stats["content_non_space_chars"]
    quality_missing = []
    if design_missing and project.get("current_stage") != "done":
        quality_missing.append("设计包缺项：" + "、".join(design_missing[:8]))
    if chapter_missing:
        quality_missing.append("未达标章节：" + "、".join(chapter_missing))
    if chapter_gate_failures:
        quality_missing.append("章节复审门控未过：" + "；".join(chapter_gate_failures[:3]))
    final_body_chars = final_body["content_non_space_chars"]
    if final_body_chars == 0:
        quality_missing.append("终版正文未合并：正文.md 为空")
    elif final_body_chars < target_min:
        quality_missing.append(f"终版正文字符未达标：{final_body_chars}/{target_min}")
    elif final_body_chars > target_max:
        quality_missing.append(f"终版正文字符超上限：{final_body_chars}/{target_max}")
    final_body_text = artifact_path(project_id, "正文.md").read_text(encoding="utf-8") if artifact_path(project_id, "正文.md").exists() else ""
    final_audit_text = artifact_path(project_id, "审核报告.md").read_text(encoding="utf-8") if artifact_path(project_id, "审核报告.md").exists() else ""
    final_audit_gate = combined_ai_gate(final_audit_text, final_body_text, ai_target)
    if final_body_text.strip():
        ai_gate_body_text = final_body_text
    elif artifact_path(project_id, "正文草稿.md").exists():
        ai_gate_body_text = artifact_path(project_id, "正文草稿.md").read_text(encoding="utf-8")
    else:
        ai_gate_body_text = "\n\n".join(
            chapter_path(project, int(chapter["number"])).read_text(encoding="utf-8")
            for chapter in project.get("chapters", [])
            if chapter_path(project, int(chapter["number"])).exists()
        )
    overall_ai_gate = combined_ai_gate(audit_text, ai_gate_body_text, ai_target)
    if final_body_chars > 0:
        if final_audit["content_non_space_chars"] == 0:
            quality_missing.append("终版正文未复审：审核报告.md 为空")
        elif timestamp_value(final_audit["updated_at"]) < timestamp_value(final_body["updated_at"]):
            quality_missing.append("终版正文已更新，需要重新审核")
        elif final_audit_gate.get("passed") is not True:
            if final_audit_gate.get("missing"):
                quality_missing.append("终版正文复审缺项：" + "、".join(final_audit_gate["missing"]))
            elif final_audit_gate.get("failure_reasons"):
                quality_missing.append("终版正文复审未通过：" + "、".join(final_audit_gate["failure_reasons"]))
            else:
                quality_missing.append("终版正文复审未通过")
        elif not has_pass_conclusion(final_audit_text):
            quality_missing.append("终版正文复审缺少通过结论")
        image_plan_text = artifact_path(project_id, "图片生成方案.md").read_text(encoding="utf-8") if artifact_path(project_id, "图片生成方案.md").exists() else ""
        missing_image_plan = image_plan_missing(image_plan_text)
        if image_plan["content_non_space_chars"] == 0:
            quality_missing.append("图片生成方案未完成：图片生成方案.md 为空")
        elif outdated_source := image_plan_outdated_source(project):
            quality_missing.append(f"{outdated_source} 已更新，需要重新生成图片方案")
        elif missing_image_plan:
            quality_missing.append("图片生成方案缺项：" + "、".join(missing_image_plan))

    return {
        "stage_total": stage_total,
        "stage_counts": stage_counts,
        "progress_pct": round(done / stage_total * 100, 1),
        "artifact_progress": {
            "ready": ready_artifacts,
            "complete": complete_artifacts,
            "total": len(artifact_stats),
            "pct": document_progress_pct,
        },
        "type_progress": document_type_progress(artifact_stats),
        "total_content": total_content,
        "current_stage": {
            "id": current_stage_id,
            "label": current_stage["label"],
            "status": project["stages"].get(current_stage_id, {}).get("status", "pending"),
        },
        "target_min": target_min,
        "target_max": target_max,
        "target_pct": target_pct,
        "body_source": body_source,
        "body_stats": body_stats,
        "artifact_stats": artifact_stats,
        "chapter_stats": chapter_stats,
        "stage_artifacts": stage_artifacts,
        "ai_gate": overall_ai_gate,
        "design_gate": {
            "passed": not design_missing,
            "missing": design_missing,
            "artifacts": DESIGN_ARTIFACTS,
        },
        "quality_gate": {
            "passed": not quality_missing,
            "missing": quality_missing,
            "body_chars": final_body_chars,
            "target_min": target_min,
            "passed_chapters": len(chapter_stats) - len(chapter_missing),
            "total_chapters": len(chapter_stats),
            "final_body_path": final_body["output_path"],
            "final_audit_path": final_audit["output_path"],
            "image_plan_path": image_plan["output_path"],
            "final_audit_gate": final_audit_gate,
        },
        "updated_at": now(),
    }


def find_chapter(project: dict, number: int) -> dict:
    chapter = next((item for item in project.get("chapters", []) if int(item["number"]) == number), None)
    if not chapter:
        raise ValueError("unknown chapter")
    return chapter


def previous_chapter_passed(project: dict, number: int) -> bool:
    if number <= 1:
        return True
    previous = find_chapter(project, number - 1)
    return previous.get("status") == "passed"


def set_chapter_status(project: dict, number: int, status: str) -> dict:
    if status not in CHAPTER_STATUSES:
        raise ValueError("unknown chapter status")
    chapter = find_chapter(project, number)
    if status in {"writing", "auditing", "revising", "passed"} and not previous_chapter_passed(project, number):
        raise ValueError("previous chapter is not passed")
    if status == "passed":
        save_unified_ai_review(project, number)
        audit_text = chapter_audit_path(project, number).read_text(encoding="utf-8")
        content_text = chapter_path(project, number).read_text(encoding="utf-8") if chapter_path(project, number).exists() else ""
        ai_target = parse_float(project.get("fields", {}).get("ai_taste_target", "5"), 5.0)
        audit_gate = combined_ai_gate(audit_text, content_text, ai_target, project=project, chapter_number=number)
        if audit_gate.get("passed") is not True:
            missing = "、".join(audit_gate.get("missing") or [])
            reasons = "、".join(audit_gate.get("failure_reasons") or [])
            detail = missing or reasons
            raise ValueError(f"chapter audit is not passed{': ' + detail if detail else ''}")
    chapter["status"] = status
    chapter["updated_at"] = now()
    if status == "passed":
        next_chapter = next((item for item in project.get("chapters", []) if int(item["number"]) == number + 1), None)
        if next_chapter and next_chapter.get("status") == "locked":
            next_chapter["status"] = "writing"
            next_chapter["updated_at"] = now()
        project["stages"]["draft"]["status"] = "in_progress"
        project["current_stage"] = "draft"
    project["events"].append({"time": now(), "text": f"第{number:02d}章 -> {chapter_status_label(status)}"})
    write_json(project_json(project["id"]), project)
    return project


def assert_quality_gate(project: dict) -> None:
    gate = project_metrics(project)["quality_gate"]
    if not gate["passed"]:
        raise ValueError("quality gate not passed: " + "；".join(gate["missing"]))


def chapter_payload(project: dict) -> dict:
    metrics = project_metrics(project)
    return {
        "chapters": metrics["chapter_stats"],
        "chapter_dir": output_relative_path(novel_dir(project)),
        "agent": project.get("agent", {}),
    }


def read_json_object(path: Path):
    if not path.exists():
        return None
    try:
        with path.open("r", encoding="utf-8") as f:
            return json.load(f)
    except Exception:
        return None


def novel_scan_dirs() -> list[Path]:
    dirs: list[Path] = []
    env_dirs = os.environ.get("NOVEL_STUDIO_NOVEL_DIRS", "")
    for raw in env_dirs.split(os.pathsep):
        raw = raw.strip()
        if raw:
            dirs.append(Path(raw).expanduser())
    dirs.append(DEFAULT_NOVEL_DIR)
    for pattern in [
        "data/runs/*/output/novel",
        "runs/*/output/novel",
        "output/novel",
    ]:
        dirs.extend(WORKSPACE.glob(pattern))
    try:
        for progress_path in WORKSPACE.rglob("output/novel/meta/progress.json"):
            dirs.append(progress_path.parent.parent)
    except OSError:
        pass

    seen = set()
    result = []
    for path in dirs:
        try:
            resolved = path.expanduser().resolve()
        except OSError:
            continue
        parts = set(resolved.parts)
        if "backups" in parts or "_cleared_history" in parts:
            continue
        key = str(resolved)
        if key in seen:
            continue
        if resolved.exists() or (resolved / "meta" / "progress.json").exists():
            seen.add(key)
            result.append(resolved)
    return result


def novel_id(root: Path) -> str:
    return hashlib.sha1(str(root.resolve()).encode("utf-8")).hexdigest()[:16]


def novel_root_by_id(nid: str) -> Path:
    for root in novel_scan_dirs():
        if novel_id(root) == nid:
            return root
    raise FileNotFoundError("novel project not found")


def safe_material_path(root: Path, raw: str) -> Path:
    rel = Path(raw or "")
    if rel.is_absolute():
        raise ValueError("material path must be relative")
    target = (root / rel).resolve()
    root_resolved = root.resolve()
    if target != root_resolved and root_resolved not in target.parents:
        raise ValueError("material path escapes novel root")
    if not target.exists() or target.is_dir():
        raise FileNotFoundError("material not found")
    return target


def iso_mtime(path: Path) -> str:
    try:
        return datetime.fromtimestamp(path.stat().st_mtime).isoformat(timespec="seconds")
    except OSError:
        return ""


def max_mtime(root: Path) -> str:
    newest = 0.0
    try:
        for path in root.rglob("*"):
            if path.is_file():
                newest = max(newest, path.stat().st_mtime)
    except OSError:
        pass
    if newest <= 0:
        return ""
    return datetime.fromtimestamp(newest).isoformat(timespec="seconds")


def count_files(root: Path, rel: str) -> int:
    base = root / rel
    if not base.exists() or not base.is_dir():
        return 0
    return sum(1 for path in base.iterdir() if path.is_file())


def count_nonempty_files(root: Path, rel: str) -> int:
    base = root / rel
    if not base.exists() or not base.is_dir():
        return 0
    total = 0
    for path in base.iterdir():
        if path.is_file():
            try:
                if path.stat().st_size > 0:
                    total += 1
            except OSError:
                pass
    return total


def chapter_numbers_from_dir(root: Path) -> list[int]:
    chapters_dir = root / "chapters"
    if not chapters_dir.exists():
        return []
    numbers = []
    for path in chapters_dir.glob("[0-9][0-9].md"):
        try:
            numbers.append(int(path.stem))
        except ValueError:
            continue
    return sorted(numbers)


def novel_source_label(root: Path) -> str:
    try:
        rel = root.relative_to(WORKSPACE).as_posix()
    except ValueError:
        return "外部项目"
    if rel == "output/novel":
        return "当前工作区"
    if rel.startswith("data/runs/"):
        return "data/runs"
    if rel.startswith("runs/"):
        return "runs"
    return "workspace"


def novel_material_group(rel: str) -> tuple[str, str, int]:
    first = rel.split("/", 1)[0]
    if first == "chapters":
        return "chapters", "章节终稿", 10
    if first == "drafts":
        return "drafts", "草稿与章纲", 20
    if first == "reviews":
        return "reviews", "统一审核", 30
    if first == "reviews_ai":
        return "reviews_ai", "旧AI味审核", 40
    if first == "summaries":
        return "summaries", "章节/弧卷摘要", 50
    if first == "meta":
        return "meta", "运行元数据", 60
    if first == "logs":
        return "logs", "运行日志", 70
    suffix = Path(rel).suffix.lower()
    if suffix in {".txt", ".epub"}:
        return "exports", "导出成品", 80
    if suffix in {".md", ".json", ".jsonl"}:
        return "design", "设计与提交资料", 5
    return "other", "其他产物", 90


def novel_file_stats(root: Path, path: Path) -> dict:
    rel = path.relative_to(root).as_posix()
    group, group_label, group_order = novel_material_group(rel)
    stat = path.stat()
    stats = text_stats(path) if path.suffix.lower() in {".md", ".txt", ".json", ".jsonl", ".log", ".csv", ".tsv"} else {}
    return {
        "path": rel,
        "name": path.name,
        "group": group,
        "group_label": group_label,
        "group_order": group_order,
        "size": stat.st_size,
        "updated_at": datetime.fromtimestamp(stat.st_mtime).isoformat(timespec="seconds"),
        "content_non_space_chars": stats.get("content_non_space_chars", 0),
        "content_lines": stats.get("content_lines", 0),
        "extension": path.suffix.lower(),
    }


def novel_materials(root: Path) -> list[dict]:
    if not root.exists():
        return []
    materials = []
    for path in root.rglob("*"):
        if not path.is_file():
            continue
        if path.name.startswith("."):
            continue
        try:
            materials.append(novel_file_stats(root, path))
        except OSError:
            continue
    materials.sort(key=lambda item: (item["group_order"], item["path"]))
    return materials


def json_list(root: Path, rel: str) -> list:
    data = read_json_object(root / rel)
    return data if isinstance(data, list) else []


def json_dict(root: Path, rel: str) -> dict:
    data = read_json_object(root / rel)
    return data if isinstance(data, dict) else {}


def json_dict_first(root: Path, rels: list[str]) -> dict:
    for rel in rels:
        data = json_dict(root, rel)
        if data:
            return data
    return {}


def find_by_chapter(items: list, number: int) -> dict:
    for item in items:
        if isinstance(item, dict) and int(item.get("chapter") or 0) == number:
            return item
    return {}


def list_limit(items: list, limit: int) -> list:
    return items[:limit] if len(items) > limit else items


def novel_existing_file_refs(root: Path, paths: list[str]) -> list[dict]:
    refs = []
    for rel in paths:
        path = root / rel
        if path.exists() and path.is_file():
            refs.append(novel_file_stats(root, path))
    return refs


def novel_overview(root: Path) -> dict:
    progress = json_dict(root, "meta/progress.json")
    project_progress = json_dict(root, "meta/project_progress.json")
    chapter_progress = json_dict(root, "meta/chapter_progress.json")
    character_continuity = json_dict(root, "meta/character_continuity.json")
    resource_ledger = json_dict(root, "meta/resource_ledger.json")
    evolution = json_dict(root, "meta/evolution_report.json")
    characters = json_list(root, "characters.json")
    relationships = json_list(root, "relationship_state.json")
    world_rules = json_list(root, "world_rules.json")
    timeline = json_list(root, "timeline.json")
    outline = json_list(root, "outline.json")
    current_chapter = int(progress.get("current_chapter") or chapter_progress.get("current_chapter") or 0)

    continuity_by_name = {
        item.get("name"): item
        for item in character_continuity.get("entries", [])
        if isinstance(item, dict) and item.get("name")
    }
    relationship_tension = project_progress.get("relationship_tension") or []
    character_cards = []
    for item in characters:
        if not isinstance(item, dict):
            continue
        name = item.get("name", "")
        continuity = continuity_by_name.get(name, {})
        related_edges = [
            rel for rel in relationships
            if isinstance(rel, dict) and name in {rel.get("character_a"), rel.get("character_b")}
        ]
        character_cards.append(
            {
                "name": name,
                "role": item.get("role", ""),
                "tier": item.get("tier", ""),
                "traits": item.get("traits") or [],
                "description": item.get("description", ""),
                "arc": item.get("arc", ""),
                "last_seen_chapter": continuity.get("last_seen_chapter"),
                "appearance_count": continuity.get("appearance_count", 0),
                "appearance_chapters": continuity.get("appearance_chapters") or [],
                "current_facts": list_limit(continuity.get("current_facts") or [], 3),
                "future_uses": list_limit(continuity.get("future_uses") or [], 3),
                "relationships": related_edges,
            }
        )

    timeline_window = [
        item for item in timeline
        if isinstance(item, dict)
        and (current_chapter <= 0 or current_chapter - 4 <= int(item.get("chapter") or 0) <= current_chapter + 4)
    ]
    if not timeline_window:
        timeline_window = timeline[-12:]

    key_files = novel_existing_file_refs(
        root,
        [
            "premise.md",
            "outline.md",
            "outline.json",
            "layered_outline.md",
            "layered_outline.json",
            "characters.md",
            "characters.json",
            "relationship_state.md",
            "relationship_state.json",
            "world_rules.md",
            "world_rules.json",
            "timeline.md",
            "timeline.json",
            "foreshadow_ledger.md",
            "foreshadow_ledger.json",
            "meta/project_progress.md",
            "meta/project_progress.json",
            "meta/chapter_progress.md",
            "meta/chapter_progress.json",
            "meta/character_continuity.md",
            "meta/character_continuity.json",
            "meta/chapter_timeline_progress.md",
            "meta/chapter_timeline_progress.json",
            "meta/resource_ledger.md",
            "meta/resource_ledger.json",
            "meta/evolution_report.md",
            "meta/evolution_report.json",
            "meta/douban-submission-copy-20260703.md",
        ],
    )

    return {
        "outline_status": project_progress.get("outline_status") or [],
        "outline": list_limit(outline, 40),
        "planning": {
            "next_plan": chapter_progress.get("next_plan") or {},
            "next_chapter_actions": project_progress.get("next_chapter_actions") or [],
            "hook_analysis": project_progress.get("hook_analysis") or {},
            "resource_hygiene": project_progress.get("resource_hygiene") or {},
            "health": evolution.get("health") or {},
            "patterns": list_limit(evolution.get("patterns") or [], 6),
        },
        "characters": character_cards,
        "relationship_tension": relationship_tension,
        "relationships": relationships,
        "world": {
            "rules": world_rules,
            "timeline": list_limit(timeline_window, 24),
            "resources": list_limit(resource_ledger.get("claims") or [], 18),
            "foreshadow_plan": list_limit(project_progress.get("foreshadow_plan") or [], 12),
        },
        "files": key_files,
    }


def chapter_file_matches(rel: str, number: int) -> bool:
    nn = f"{number:02d}"
    nnn = f"{number:03d}"
    name = Path(rel).name
    if rel.startswith(("chapters/", "drafts/", "reviews/", "reviews_ai/", "summaries/")):
        if re.search(rf"(^|[^0-9])0*{number}([^0-9]|$)", name):
            return True
        if f"第{nnn}章" in name or f"第{number}章" in name:
            return True
    if rel.startswith("meta/chapter_metrics/") and re.search(rf"(^|[^0-9])0*{number}([^0-9]|$)", name):
        return True
    if rel.startswith("meta/sessions/agents/") and re.search(rf"(writer|editor|architect)[-_].*ch0*{number}([^0-9]|$)", name):
        return True
    return False


def chapter_artifact_role(rel: str) -> tuple[str, int]:
    name = Path(rel).name
    if rel.startswith("chapters/") and ".pre-rewrite" in name:
        return "改写前正文", 12
    if rel.startswith("chapters/"):
        return "章节终稿", 10
    if rel.startswith("drafts/") and name.endswith(".plan.json"):
        return "章纲计划", 20
    if rel.startswith("drafts/"):
        return "正文草稿", 22
    if rel.startswith("reviews/") and re.match(r"^\d{2}\.md$", name):
        return "统一审核", 30
    if rel.startswith("reviews_ai/") or "AI味审核" in name or "ai_voice" in name or "ai_gate" in name:
        return "AI味审核", 40
    if rel.startswith("reviews/"):
        return "结构化审核", 32
    if rel.startswith("summaries/"):
        return "章节摘要", 50
    if rel.startswith("meta/chapter_metrics/"):
        return "章节指标", 60
    if rel.startswith("meta/sessions/agents/"):
        return "Agent会话", 70
    return "相关产物", 90


def novel_chapter_artifacts(root: Path, number: int) -> list[dict]:
    artifacts = []
    try:
        paths = [path for path in root.rglob("*") if path.is_file()]
    except OSError:
        paths = []
    for path in paths:
        rel = path.relative_to(root).as_posix()
        if not chapter_file_matches(rel, number):
            continue
        try:
            item = novel_file_stats(root, path)
        except OSError:
            continue
        role, role_order = chapter_artifact_role(rel)
        item["artifact_role"] = role
        item["artifact_order"] = role_order
        artifacts.append(item)
    artifacts.sort(key=lambda item: (item["artifact_order"], item["path"]))
    return artifacts


def novel_chapter_detail(root: Path, number: int) -> dict:
    chapter_progress = json_dict(root, "meta/chapter_progress.json")
    entry = find_by_chapter(chapter_progress.get("entries") or [], number)
    summary = json_dict(root, f"summaries/{number:02d}.json")
    plan = json_dict(root, f"drafts/{number:02d}.plan.json")
    review = json_dict(root, f"reviews/{number:02d}.json")
    ai_review = json_dict_first(root, [f"reviews/{number:02d}_ai_gate.json", f"reviews_ai/{number:02d}.json"])
    metrics = json_dict(root, f"meta/chapter_metrics/{number:02d}.json")
    timeline_events = [
        item for item in json_list(root, "timeline.json")
        if isinstance(item, dict) and int(item.get("chapter") or 0) == number
    ]
    state_changes = [
        item for item in json_list(root, "meta/state_changes.json")
        if isinstance(item, dict) and int(item.get("chapter") or 0) == number
    ]
    relationship_updates = [
        item for item in json_list(root, "relationship_state.json")
        if isinstance(item, dict) and int(item.get("chapter") or 0) == number
    ]
    resource_changes = [
        item for item in (json_dict(root, "meta/resource_ledger.json").get("claims") or [])
        if isinstance(item, dict) and int(item.get("chapter") or 0) == number
    ]
    outline_item = find_by_chapter(json_list(root, "outline.json"), number)
    ai_report = ai_review.get("aigc_report") or ai_review.get("report") or {}
    return {
        "number": number,
        "title": plan.get("title") or summary.get("title") or outline_item.get("title") or f"第{number:02d}章",
        "summary": summary.get("summary") or entry.get("summary") or "",
        "goal": plan.get("goal") or outline_item.get("core_event") or "",
        "conflict": plan.get("conflict", ""),
        "hook": plan.get("hook") or outline_item.get("hook") or entry.get("outline_hook") or "",
        "review_status": entry.get("review_status", ""),
        "review_summary": entry.get("review_summary", ""),
        "ai": {
            "percent": ai_report.get("blended_aigc_percent") or ai_report.get("aigc_percent") or ai_report.get("ai_ratio_percent"),
            "risk_label": ai_report.get("risk_label", ""),
            "engine": ai_report.get("engine", ""),
        },
        "plan": plan,
        "review": {
            "issues": list_limit(review.get("issues") or [], 8),
            "dimensions": list_limit(review.get("dimensions") or [], 8),
        },
        "timeline_events": timeline_events or entry.get("timeline_events") or [],
        "state_changes": state_changes or entry.get("state_changes") or [],
        "relationship_updates": relationship_updates,
        "resource_changes": resource_changes or entry.get("resource_changes") or [],
        "metrics": metrics,
        "artifacts": novel_chapter_artifacts(root, number),
    }


def relationship_graph_svg(root: Path) -> str:
    characters = json_list(root, "characters.json")
    relationships = json_list(root, "relationship_state.json")
    names = []
    for item in characters:
        if isinstance(item, dict) and item.get("name") and item.get("name") not in names:
            names.append(item["name"])
    for rel in relationships:
        if not isinstance(rel, dict):
            continue
        for key in ("character_a", "character_b"):
            value = rel.get(key)
            if value and value not in names:
                names.append(value)
    names = names[:18]
    width, height = 1200, 760
    if not names:
        return f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {width} {height}"><rect width="100%" height="100%" fill="#fbfcfa"/><text x="60" y="90" font-size="28" fill="#667172">暂无人物关系数据</text></svg>'
    cx, cy = width / 2, height / 2
    rx, ry = 420, 250
    positions = {}
    for index, name in enumerate(names):
        angle = -math.pi / 2 + 2 * math.pi * index / max(len(names), 1)
        positions[name] = (cx + rx * math.cos(angle), cy + ry * math.sin(angle))
    role_by_name = {
        item.get("name"): item.get("role", "")
        for item in characters
        if isinstance(item, dict)
    }
    lines = [
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {width} {height}" width="{width}" height="{height}">',
        '<rect width="100%" height="100%" rx="18" fill="#fbfcfa"/>',
        '<text x="44" y="56" font-size="26" font-weight="700" fill="#1f2528">人物关系图</text>',
        '<text x="44" y="88" font-size="15" fill="#667172">由 characters.json 与 relationship_state.json 动态生成</text>',
    ]
    for rel in relationships[:36]:
        if not isinstance(rel, dict):
            continue
        a, b = rel.get("character_a"), rel.get("character_b")
        if a not in positions or b not in positions:
            continue
        x1, y1 = positions[a]
        x2, y2 = positions[b]
        mx, my = (x1 + x2) / 2, (y1 + y2) / 2
        label = compact_text(rel.get("relation", ""), 28)
        lines.append(f'<line x1="{x1:.1f}" y1="{y1:.1f}" x2="{x2:.1f}" y2="{y2:.1f}" stroke="#9cb4ad" stroke-width="2"/>')
        if label:
            lines.append(f'<rect x="{mx - 112:.1f}" y="{my - 15:.1f}" width="224" height="30" rx="15" fill="#ffffff" stroke="#d9dfda"/>')
            lines.append(f'<text x="{mx:.1f}" y="{my + 5:.1f}" text-anchor="middle" font-size="12" fill="#405052">{html.escape(label)}</text>')
    for name, (x, y) in positions.items():
        role = role_by_name.get(name, "")
        fill = "#256d5a" if "主角" in role else "#0d3f67" if "重要" in role else "#7d5b2f" if "反派" in role else "#5f686b"
        lines.append(f'<circle cx="{x:.1f}" cy="{y:.1f}" r="44" fill="{fill}" stroke="#ffffff" stroke-width="4"/>')
        lines.append(f'<text x="{x:.1f}" y="{y - 3:.1f}" text-anchor="middle" font-size="17" font-weight="700" fill="#ffffff">{html.escape(name[:6])}</text>')
        lines.append(f'<text x="{x:.1f}" y="{y + 20:.1f}" text-anchor="middle" font-size="12" fill="#eef6f2">{html.escape(compact_text(role, 10))}</text>')
    lines.append("</svg>")
    return "\n".join(lines)


def novel_chapter_cards(root: Path, progress: dict) -> list[dict]:
    completed = set(int(ch) for ch in progress.get("completed_chapters") or [] if str(ch).isdigit())
    word_counts = progress.get("chapter_word_counts") or {}
    total = int(progress.get("total_chapters") or 0)
    numbers = sorted(set(chapter_numbers_from_dir(root)) | completed)
    if total > 0:
        numbers = sorted(set(numbers) | set(range(1, min(total, max(numbers or [0], default=0) + 3) + 1)))
    cards = []
    for number in numbers:
        chapter_path_obj = root / "chapters" / f"{number:02d}.md"
        draft_path_obj = root / "drafts" / f"{number:02d}.draft.md"
        review_path_obj = root / "reviews" / f"{number:02d}.md"
        ai_json_path = root / "reviews" / f"{number:02d}_ai_gate.json"
        ai_md_path = root / "reviews" / f"{number:02d}.md"
        legacy_ai_json_path = root / "reviews_ai" / f"{number:02d}.json"
        legacy_ai_md_path = root / "reviews_ai" / f"第{number:03d}章_AI味审核.md"
        status = "完成" if number in completed or chapter_path_obj.exists() else "未完成"
        if number == int(progress.get("in_progress_chapter") or 0):
            status = "写作中"
        elif number == int(progress.get("current_chapter") or 0) and number not in completed:
            status = "当前章"
        cards.append(
            {
                "number": number,
                "status": status,
                "word_count": int(word_counts.get(str(number)) or 0),
                "chapter": chapter_path_obj.exists(),
                "draft": draft_path_obj.exists(),
                "review": review_path_obj.exists(),
                "ai_review": ai_json_path.exists() or ai_md_path.exists() or legacy_ai_json_path.exists() or legacy_ai_md_path.exists(),
                "updated_at": iso_mtime(chapter_path_obj) or iso_mtime(draft_path_obj) or iso_mtime(review_path_obj),
            }
        )
    return cards


def novel_summary(root: Path, include_materials: bool = False) -> dict:
    progress = read_json_object(root / "meta" / "progress.json") or {}
    pipeline = read_json_object(root / "meta" / "pipeline.json") or {}
    chapters = chapter_numbers_from_dir(root)
    completed = progress.get("completed_chapters") or chapters
    total_chapters = int(progress.get("total_chapters") or max(chapters or [0], default=0) or len(completed))
    completed_count = len(completed)
    pct = round(completed_count / total_chapters * 100, 1) if total_chapters > 0 else 0.0
    if progress.get("phase") == "complete" and total_chapters > 0:
        pct = 100.0
    title = str(progress.get("novel_name") or "").strip()
    if not title:
        title = root.parent.name if root.name == "novel" else root.name
    materials = novel_materials(root) if include_materials else []
    material_counts = {}
    for item in materials:
        material_counts[item["group"]] = material_counts.get(item["group"], 0) + 1
    return {
        "id": novel_id(root),
        "title": title,
        "root": str(root),
        "relative_root": output_relative_path(root),
        "source": novel_source_label(root),
        "phase": progress.get("phase", "unknown"),
        "flow": progress.get("flow", "unknown"),
        "current_chapter": int(progress.get("current_chapter") or 0),
        "in_progress_chapter": int(progress.get("in_progress_chapter") or 0),
        "total_chapters": total_chapters,
        "completed_chapters": completed,
        "completed_count": completed_count,
        "progress_pct": pct,
        "total_word_count": int(progress.get("total_word_count") or 0),
        "target_word_count_range": progress.get("target_word_count_range", ""),
        "current_volume": progress.get("current_volume", 0),
        "current_arc": progress.get("current_arc", 0),
        "pending_rewrites": progress.get("pending_rewrites") or [],
        "pipeline": {
            "stages": pipeline.get("stages") or [],
            "completed": pipeline.get("completed") or [],
            "updated_at": pipeline.get("updated_at", ""),
        },
        "counts": {
            "chapters": count_nonempty_files(root, "chapters"),
            "drafts": count_nonempty_files(root, "drafts"),
            "reviews": count_nonempty_files(root, "reviews"),
            "reviews_ai_legacy": count_nonempty_files(root, "reviews_ai"),
            "summaries": count_nonempty_files(root, "summaries"),
            "meta": count_files(root, "meta"),
            "materials": len(materials) if include_materials else len(novel_materials(root)),
        },
        "material_counts": material_counts,
        "materials": materials if include_materials else [],
        "overview": novel_overview(root) if include_materials else {},
        "chapters": novel_chapter_cards(root, progress),
        "updated_at": progress.get("updated_at") or max_mtime(root),
    }


def list_novels() -> list[dict]:
    novels = [novel_summary(root, include_materials=False) for root in novel_scan_dirs()]
    novels.sort(key=lambda item: timestamp_value(item.get("updated_at", "")), reverse=True)
    return novels


def novel_file_payload(root: Path, raw_path: str) -> dict:
    path = safe_material_path(root, raw_path)
    rel = path.relative_to(root).as_posix()
    stat = path.stat()
    binary = path.suffix.lower() not in {".md", ".txt", ".json", ".jsonl", ".log", ".csv", ".tsv", ".yaml", ".yml"}
    if binary:
        return {
            "path": rel,
            "content": "",
            "binary": True,
            "truncated": False,
            "size": stat.st_size,
            "updated_at": datetime.fromtimestamp(stat.st_mtime).isoformat(timespec="seconds"),
        }
    data = path.read_bytes()
    truncated = len(data) > MATERIAL_READ_LIMIT
    text = data[:MATERIAL_READ_LIMIT].decode("utf-8", errors="replace")
    return {
        "path": rel,
        "content": text,
        "binary": False,
        "truncated": truncated,
        "size": stat.st_size,
        "updated_at": datetime.fromtimestamp(stat.st_mtime).isoformat(timespec="seconds"),
    }


class Handler(BaseHTTPRequestHandler):
    server_version = "ShortStoryService/0.1"

    def log_message(self, fmt: str, *args) -> None:
        sys.stderr.write("[%s] %s\n" % (datetime.now().strftime("%H:%M:%S"), fmt % args))

    def read_body(self) -> dict:
        length = int(self.headers.get("Content-Length", "0"))
        if length == 0:
            return {}
        raw = self.rfile.read(length)
        if not raw:
            return {}
        return json.loads(raw.decode("utf-8"))

    def send_json(self, data, status=HTTPStatus.OK) -> None:
        body = json.dumps(data, ensure_ascii=False).encode("utf-8")
        try:
            self.send_response(status)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Cache-Control", "no-store")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            return

    def send_error_json(self, status, message) -> None:
        self.send_json({"error": message}, status)

    def send_text(self, text: str, content_type: str, status=HTTPStatus.OK) -> None:
        body = text.encode("utf-8")
        try:
            self.send_response(status)
            self.send_header("Content-Type", content_type)
            self.send_header("Cache-Control", "no-store")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            return

    def do_GET(self) -> None:
        parsed = urlparse(self.path)
        path = parsed.path
        try:
            if path == "/api/health":
                self.send_json({"ok": True, "time": now()})
                return
            if path == "/api/stages":
                self.send_json(
                    {
                        "stages": STAGES,
                        "artifacts": ARTIFACTS,
                        "directions": DIRECTION_LABELS,
                        "document_types": DOCUMENT_TYPES,
                        "artifact_meta": {name: artifact_meta(name) for name in ARTIFACTS},
                        "output_root": str(OUTPUT_ROOT),
                    }
                )
                return
            if path == "/api/projects":
                self.send_json({"projects": list_projects()})
                return
            if path == "/api/novels":
                self.send_json({"novels": list_novels(), "default_root": str(DEFAULT_NOVEL_DIR)})
                return
            match = re.fullmatch(r"/api/novels/([^/]+)/relationships\.svg", path)
            if match:
                root = novel_root_by_id(unquote(match.group(1)))
                self.send_text(relationship_graph_svg(root), "image/svg+xml; charset=utf-8")
                return
            match = re.fullmatch(r"/api/novels/([^/]+)/chapters/(\d+)", path)
            if match:
                root = novel_root_by_id(unquote(match.group(1)))
                self.send_json(novel_chapter_detail(root, int(match.group(2))))
                return
            match = re.fullmatch(r"/api/novels/([^/]+)/materials", path)
            if match:
                root = novel_root_by_id(unquote(match.group(1)))
                self.send_json({"materials": novel_materials(root)})
                return
            match = re.fullmatch(r"/api/novels/([^/]+)/file", path)
            if match:
                root = novel_root_by_id(unquote(match.group(1)))
                query = parse_qs(parsed.query)
                material_path = query.get("path", [""])[0]
                self.send_json(novel_file_payload(root, material_path))
                return
            match = re.fullmatch(r"/api/novels/([^/]+)", path)
            if match:
                root = novel_root_by_id(unquote(match.group(1)))
                self.send_json(novel_summary(root, include_materials=True))
                return
            match = re.fullmatch(r"/api/projects/([^/]+)/metrics", path)
            if match:
                project = load_project(unquote(match.group(1)))
                self.send_json(project_metrics(project))
                return
            match = re.fullmatch(r"/api/projects/([^/]+)/chapters", path)
            if match:
                project = load_project(unquote(match.group(1)))
                self.send_json(chapter_payload(project))
                return
            match = re.fullmatch(r"/api/projects/([^/]+)/chapters/(\d+)/prompt", path)
            if match:
                project = load_project(unquote(match.group(1)))
                query = parse_qs(parsed.query)
                mode = query.get("mode", ["write"])[0]
                if mode not in {"write", "audit", "revise"}:
                    raise ValueError("unknown prompt mode")
                self.send_json({"prompt": chapter_prompt(project, int(match.group(2)), mode)})
                return
            match = re.fullmatch(r"/api/projects/([^/]+)/chapters/(\d+)/(content|audit)", path)
            if match:
                project = load_project(unquote(match.group(1)))
                number = int(match.group(2))
                kind = match.group(3)
                path_obj = chapter_path(project, number) if kind == "content" else chapter_audit_path(project, number)
                self.send_json({"number": number, "kind": kind, "content": path_obj.read_text(encoding="utf-8")})
                return
            match = re.fullmatch(r"/api/projects/([^/]+)", path)
            if match:
                self.send_json(load_project(unquote(match.group(1))))
                return
            match = re.fullmatch(r"/api/projects/([^/]+)/prompt", path)
            if match:
                project = load_project(unquote(match.group(1)))
                query = parse_qs(parsed.query)
                stage_id = query.get("stage", [project.get("current_stage", "bible")])[0]
                self.send_json({"prompt": stage_prompt(project, stage_id)})
                return
            match = re.fullmatch(r"/api/projects/([^/]+)/artifacts/([^/]+)", path)
            if match:
                project_id = unquote(match.group(1))
                name = unquote(match.group(2))
                content = artifact_path(project_id, name).read_text(encoding="utf-8")
                self.send_json({"name": name, "content": content})
                return
            self.serve_static(path)
        except FileNotFoundError:
            self.send_error_json(HTTPStatus.NOT_FOUND, "not found")
        except ValueError as exc:
            self.send_error_json(HTTPStatus.BAD_REQUEST, str(exc))
        except Exception as exc:
            self.send_error_json(HTTPStatus.INTERNAL_SERVER_ERROR, str(exc))

    def do_POST(self) -> None:
        parsed = urlparse(self.path)
        path = parsed.path
        try:
            if path == "/api/projects/batch":
                self.send_json(create_batch_projects(self.read_body()), HTTPStatus.CREATED)
                return
            if path == "/api/projects":
                project = create_project(self.read_body())
                self.send_json(project, HTTPStatus.CREATED)
                return
            match = re.fullmatch(r"/api/projects/([^/]+)/chapters/(\d+)/status", path)
            if match:
                project = load_project(unquote(match.group(1)))
                number = int(match.group(2))
                body = self.read_body()
                project = set_chapter_status(project, number, body.get("status", "auditing"))
                self.send_json(chapter_payload(project))
                return
            match = re.fullmatch(r"/api/projects/([^/]+)/stage", path)
            if match:
                project_id = unquote(match.group(1))
                project = load_project(project_id)
                body = self.read_body()
                stage_id = body.get("stage")
                status = body.get("status")
                if stage_id not in project["stages"]:
                    raise ValueError("unknown stage")
                if status not in {"pending", "in_progress", "done", "blocked"}:
                    raise ValueError("unknown status")
                if stage_id == "bible" and status == "done":
                    missing_design = design_artifact_missing(project)
                    if missing_design:
                        raise ValueError("design gate not passed: " + "；".join(missing_design[:8]))
                image_result = None
                if stage_id == "image" and status == "done":
                    image_result = ensure_image_package(project, force=True, mark_stage=True)
                    if not image_result.get("ready"):
                        raise ValueError("image package not ready: " + "；".join(image_result.get("reasons") or []))
                if stage_id == "final" and status == "done":
                    assert_quality_gate(project)
                project["stages"][stage_id]["status"] = status
                project["stages"][stage_id]["note"] = body.get("note", project["stages"][stage_id].get("note", ""))
                project["events"].append({"time": now(), "text": f"{stage_id} -> {status}"})
                if image_result and image_result.get("generated"):
                    project["events"].append({"time": now(), "text": "已执行图片生成方案并刷新图片包。"})
                update_current_stage(project)
                write_json(project_json(project_id), project)
                self.send_json(project)
                return
            match = re.fullmatch(r"/api/projects/([^/]+)/next", path)
            if match:
                project_id = unquote(match.group(1))
                project = load_project(project_id)
                current = project.get("current_stage")
                if current == "bible":
                    missing_design = design_artifact_missing(project)
                    if missing_design:
                        raise ValueError("design gate not passed: " + "；".join(missing_design[:8]))
                if current == "final":
                    assert_quality_gate(project)
                if current in project["stages"]:
                    project["stages"][current]["status"] = "done"
                update_current_stage(project)
                if current == "audit":
                    image_result = ensure_image_package(project, force=False, mark_stage=True)
                    if image_result.get("ready"):
                        project["events"].append({"time": now(), "text": "审核完成后已自动执行图片生成方案。"})
                        update_current_stage(project)
                    elif image_result.get("generated") and image_result.get("reasons"):
                        project["events"].append({"time": now(), "text": "图片生成方案已执行但未完成：" + "；".join(image_result["reasons"][:3])})
                    elif image_result.get("reasons"):
                        project["events"].append({"time": now(), "text": "图片生成方案暂未执行：" + "；".join(image_result["reasons"][:3])})
                if project["current_stage"] in project["stages"]:
                    project["stages"][project["current_stage"]]["status"] = "in_progress"
                project["events"].append({"time": now(), "text": f"推进到 {project['current_stage']}"})
                write_json(project_json(project_id), project)
                self.send_json(project)
                return
            self.send_error_json(HTTPStatus.NOT_FOUND, "not found")
        except FileNotFoundError:
            self.send_error_json(HTTPStatus.NOT_FOUND, "not found")
        except ValueError as exc:
            self.send_error_json(HTTPStatus.BAD_REQUEST, str(exc))
        except Exception as exc:
            self.send_error_json(HTTPStatus.INTERNAL_SERVER_ERROR, str(exc))

    def do_PUT(self) -> None:
        parsed = urlparse(self.path)
        chapter_match = re.fullmatch(r"/api/projects/([^/]+)/chapters/(\d+)/(content|audit)", parsed.path)
        if chapter_match:
            try:
                project = load_project(unquote(chapter_match.group(1)))
                number = int(chapter_match.group(2))
                kind = chapter_match.group(3)
                chapter = find_chapter(project, number)
                if chapter.get("status") == "locked":
                    raise ValueError("chapter is locked")
                body = self.read_body()
                target = chapter_path(project, number) if kind == "content" else chapter_audit_path(project, number)
                target.write_text(body.get("content", ""), encoding="utf-8")
                if kind == "content":
                    save_unified_ai_review(project, number)
                    if chapter.get("status") in {"writing", "auditing", "revising", "passed"}:
                        chapter["status"] = "auditing"
                elif kind == "audit":
                    content_text = chapter_path(project, number).read_text(encoding="utf-8") if chapter_path(project, number).exists() else ""
                    save_unified_ai_review(project, number)
                    audit_gate = combined_ai_gate(
                        body.get("content", ""),
                        content_text,
                        parse_float(project.get("fields", {}).get("ai_taste_target", "5"), 5.0),
                        project=project,
                        chapter_number=number,
                    )
                    if audit_gate.get("passed") is True:
                        chapter["status"] = "passed"
                        next_chapter = next((item for item in project.get("chapters", []) if int(item["number"]) == number + 1), None)
                        if next_chapter and next_chapter.get("status") == "locked":
                            next_chapter["status"] = "writing"
                            next_chapter["updated_at"] = now()
                    elif not audit_gate.get("missing"):
                        chapter["status"] = "revising"
                    else:
                        chapter["status"] = "auditing"
                chapter["updated_at"] = now()
                project["stages"]["draft"]["status"] = "in_progress"
                project["current_stage"] = "draft"
                project["events"].append({"time": now(), "text": f"保存第{number:02d}章{('正文' if kind == 'content' else '审核')}"})
                write_json(project_json(project["id"]), project)
                self.send_json(chapter_payload(project))
            except FileNotFoundError:
                self.send_error_json(HTTPStatus.NOT_FOUND, "not found")
            except ValueError as exc:
                self.send_error_json(HTTPStatus.BAD_REQUEST, str(exc))
            except Exception as exc:
                self.send_error_json(HTTPStatus.INTERNAL_SERVER_ERROR, str(exc))
            return
        match = re.fullmatch(r"/api/projects/([^/]+)/artifacts/([^/]+)", parsed.path)
        if not match:
            self.send_error_json(HTTPStatus.NOT_FOUND, "not found")
            return
        try:
            project_id = unquote(match.group(1))
            name = unquote(match.group(2))
            body = self.read_body()
            path = artifact_path(project_id, name)
            path.write_text(body.get("content", ""), encoding="utf-8")
            project = load_project(project_id)
            project["events"].append({"time": now(), "text": f"保存 {name}"})
            if name in {"正文.md", "故事圣经.md", "审核报告.md"}:
                image_result = refresh_image_package_after_source_change(project, name)
                if image_result.get("generated") and image_result.get("ready"):
                    project["events"].append({"time": now(), "text": f"保存 {name} 后已自动执行图片生成方案。"})
                elif image_result.get("generated") and image_result.get("reasons"):
                    project["events"].append({"time": now(), "text": f"保存 {name} 后图片生成方案已执行但未完成：" + "；".join(image_result["reasons"][:3])})
                elif image_result.get("ready") and image_result.get("unchanged"):
                    project["events"].append({"time": now(), "text": f"保存 {name} 后图片生成方案仍为最新。"})
                elif image_result.get("reasons"):
                    project["events"].append({"time": now(), "text": "图片生成方案暂未执行：" + "；".join(image_result["reasons"][:3])})
            write_json(project_json(project_id), project)
            self.send_json({"ok": True, "path": str(path)})
        except FileNotFoundError:
            self.send_error_json(HTTPStatus.NOT_FOUND, "not found")
        except ValueError as exc:
            self.send_error_json(HTTPStatus.BAD_REQUEST, str(exc))
        except Exception as exc:
            self.send_error_json(HTTPStatus.INTERNAL_SERVER_ERROR, str(exc))

    def serve_static(self, path: str) -> None:
        if path == "/":
            path = "/index.html"
        rel = Path(path.lstrip("/"))
        full = (STATIC_DIR / rel).resolve()
        if not str(full).startswith(str(STATIC_DIR.resolve())) or not full.exists() or full.is_dir():
            self.send_error(HTTPStatus.NOT_FOUND)
            return
        ctype = mimetypes.guess_type(str(full))[0] or "application/octet-stream"
        body = full.read_bytes()
        self.send_response(HTTPStatus.OK)
        self.send_header("Content-Type", ctype)
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main() -> None:
    parser = argparse.ArgumentParser(description="Short story local service")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8765)
    args = parser.parse_args()
    DATA_ROOT.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer((args.host, args.port), Handler)
    print(f"Short story service running at http://{args.host}:{args.port}")
    print(f"Data root: {DATA_ROOT}")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
