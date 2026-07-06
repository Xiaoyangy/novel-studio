# 目录英文化映射表(2026-07-06)

范围:仅结构/组织目录。书名、作品/项目目录(戏神、鬼舍、十日、data/runs/鬼城、data/runs/她的第二算法、generated-output 全部、reference-library 各题材下的书目)与角色数据目录(meta/characters/*)保留原名,避免破坏自引用、RAG 索引路径与 eval 基线。

RAG 门禁:`internal/rag/policy.go`、`cmd/novel-studio/rag_cmd.go`、`cmd/novel-studio/zero_init_cmd.go` 在保留中文段(兼容旧索引/旧 chunk 文本)的同时,新增英文段 `deconstruction-library`。

## 顶层

| 原路径 | 新路径 |
|---|---|
| `拆文库/` | `deconstruction-library/` |
| `data/reference-library/写作技巧/` | `data/reference-library/writing-craft/` |

## 拆文库(现 deconstruction-library)内部

| 原路径(相对 deconstruction-library/) | 新路径(相对 deconstruction-library/) |
|---|---|
| `审核优化使用/` | `review-calibration/` |
| `审核优化使用/高质量人工文笔/` | `review-calibration/high-quality-human-prose/` |
| `审核优化使用/普通人工文笔/` | `review-calibration/ordinary-human-prose/` |
| `写作手法以及技巧/` | `writing-techniques/` |
| `写作手法以及技巧/小说塑造方法论/` | `writing-techniques/novel-craft-methodology/` |
| `写作手法以及技巧/奇幻篇/` | `writing-techniques/fantasy/` |
| `写作手法以及技巧/古代历史篇/` | `writing-techniques/ancient-history/` |
| `写作手法以及技巧/武器篇/` | `writing-techniques/weapons/` |
| `写作手法以及技巧/术法篇/` | `writing-techniques/magic-arts/` |
| `写作手法以及技巧/术法篇/游戏技能类/` | `writing-techniques/magic-arts/game-skills/` |
| `写作手法以及技巧/科幻篇/` | `writing-techniques/scifi/` |
| `写作手法以及技巧/外貌描写/` | `writing-techniques/appearance/` |
| `_templates/长篇拆文项目/` | `_templates/longform-decon-project/` |
| `_templates/长篇拆文项目/` 内部子目录(章节/角色/剧情/原文/设定/势力/世界观) | **保留中文**——它们是拆解流水线的数据契约(与保留中文的 `{书名}/` 目录、`拆文报告.md` 等产物文件同层),100+ 个 skill 操作文档按此契约读写 |
| `novel_sucai/小说大纲/` | `novel_sucai/outlines/` |
| `novel_sucai/小说大纲/写作大纲/` | `novel_sucai/outlines/writing-outlines/` |
| `novel_sucai/小说大纲/大纲示例（参考）/` | `novel_sucai/outlines/outline-examples/` |
| `novel_sucai/小说大纲/10.27日小说名字生成器&大纲/` | `novel_sucai/outlines/name-generator-and-outline-1027/` |
| `novel_sucai/小说素材女主人设+高频替换词/`(两层同名) | `novel_sucai/heroine-persona-replacement-words/`(两层) |

## 外貌描写子目录(writing-techniques/appearance/)

| 原 | 新 | 原 | 新 |
|---|---|---|---|
| 容貌 | looks | 身材 | figure |
| 脸 | face | 眼睛 | eyes |
| 眉毛 | eyebrows | 睫毛 | eyelashes |
| 鼻子 | nose | 嘴舌 | mouth-tongue |
| 牙齿 | teeth | 耳朵 | ears |
| 额头 | forehead | 皮肤 | skin |
| 头发 | hair | 胡子 | beard |
| 颈肩 | neck-shoulders | 手臂 | arms |
| 胸部 | chest | 腰背 | waist-back |
| 腿脚 | legs-feet | 肌肉 | muscles |
| 声音 | voice | 眼泪 | tears |
| 服饰 | clothing | 综合形容外貌详解 | appearance-glossary |
| 综合描写男子外貌的词语短语 | male-appearance-phrases | 综合描写女子外貌的词语短语 | female-appearance-phrases |
