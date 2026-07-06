---
name: novel-export
description: "把已完成章节合并导出为 TXT 或 EPUB。触发：「导出小说」「合并成 txt」「生成 epub」「拿现阶段成品」，需要可读成品文件时使用。只读操作，不影响创作。"
---
# novel-export：导出已完成章节

合并已完成章节导出，默认 TXT 写到 `{novelDir}/{书名}.txt`。只读操作，写作中途也可随时拿
"现阶段成品"。格式由 `--out` 后缀决定（`.txt` / `.epub`）。

## 前置条件

- 已完成首次配置（见 `skills/README.md`）。
- 在项目目录（含 `output/novel/`）下运行；至少有一章已完成。

## 执行

```bash
# 默认 TXT，写到 {novelDir}/{书名}.txt
novel-studio --export

# 指定输出路径与格式（后缀决定）
novel-studio --export --out ~/光斑.epub
novel-studio --export --out ~/光斑.txt

# 章节区间 + 覆盖已存在文件
novel-studio --export --from 10 --to 30 --overwrite
```

## 参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `--out <path>` | `{novelDir}/{书名}.txt` | 输出路径；后缀 `.txt`/`.epub` 决定格式 |
| `--from <n>` | `0`（第 1 章） | 起始章号（含） |
| `--to <n>` | `0`（最后一章） | 结束章号（含） |
| `--overwrite` | 关 | 目标文件已存在时覆盖（否则报错） |

## 产物

- **TXT** — `《书名》` → 卷分隔 → 章节正文（premise 与弧分隔不进导出）
- **EPUB** — EPUB 3 容器：封面页 + 目录 + 按章 XHTML；不带封面图

范围内未完成的章节会跳过并列在结果里，不算错误。
