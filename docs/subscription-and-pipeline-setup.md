# 订阅接入 + 新流水线使用说明（2026-07-07）

## 一、订阅接入（Codex/ChatGPT 订阅跑 GPT）

novel-studio 原本只支持 api_key+base_url 的 HTTP provider。现新增 **`codex-cli`** provider 类型，
经本机 Codex CLI（`/Applications/Codex.app/Contents/Resources/codex`）用 **ChatGPT/Codex 订阅**跑 GPT，
无需 api_key。

### 原理
`internal/llmcodex/codex.go` 把 agentcore 的一次「消息+工具→工具调用/文本」推理，翻译成一次
`codex exec --output-schema` 调用（sandbox=read-only，禁止 codex 跑命令/改文件），解析回工具调用。
翻译逻辑已单测（`codex_test.go`）；实际 `codex exec` 调用用订阅额度。

### 配置（已写入 .novel-studio/config.json）
```jsonc
"providers": { "codex": { "type": "codex-cli", "models": ["gpt-5.5"] } }  // 无需 api_key
"roles": {
  "coordinator/architect/writer/editor": provider=codex(gpt-5.5), 兜底 minimax,
  "reviewer": provider=minimax, 兜底 codex
}
```
即：**GPT 跑推演+渲染，MiniMax review，互为兜底降级**。窗口设 400K（GPT 大上下文无压力）。

### 当前状态
- `--check`：codex-cli provider 已被识别、配置合法；**主模型显示不可用只因 Codex 订阅触发用量上限
  （今晚 21:08 重置）**，已可走 MiniMax 兜底降级创作。
- 21:08 后（或订阅额度恢复）无需改配置，creative 角色自动走 GPT。
- 若 codex 二进制不在默认路径，可在 provider 里用 `base_url` 指定 codex 可执行路径。

### E2E 待验证（额度恢复后）
`codex exec` 是否稳定按 output-schema 只产出「工具调用/文本」（不跑 shell）。若 codex 有多余行为，
调 `runCodex` 的 sandbox/prompt 约束即可，翻译逻辑不动。

## 二、新流水线能力

### 新建小说（从头脑风暴开始）
```
novel-studio --pipeline --new-novel --prompt "<你的小说想法>"
```
先跑**头脑风暴**：web_research 调研题材 + craft_recall 取手法 + 推敲逻辑 → 落盘
`data/runs/<书名>/brainstorm.md`（预期字数/题材/类型/主角与CP/世界观/关键角色/核心爽点/给
Architect 的交接…）。之后自动把项目目录设为 `data/runs/<书名>`，Architect **基于 brainstorm.md**
初始化世界 → zero-init → 写作。brainstorm.md 是整本书产出的基础。

### 列出所有小说
```
novel-studio list
```
扫 data/runs/，显示每本的阶段（brainstorm/foundation/zero-init/writing/complete）、章节进度、字数、
是否有脑暴，并给出续写命令。

### 续写已有小说
```
novel-studio --pipeline --dir data/runs/<书名>
```
断点续跑：从当前阶段继续推演/写作/生成新章节。

### 长短篇已统一
不再区分长短篇——所有项目都走 architect_long 的分层（卷→弧→章）+ 世界推演 + planner-drafter
拆分 + world_tick 全套逻辑。短篇只是卷弧更少、字数预算更小的特例。
