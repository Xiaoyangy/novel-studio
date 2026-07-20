# 2026-07-20 番茄短篇生产事故复盘

## 结论

《零点四十分》（生产项目名《我在直播间给凶手点外卖》）最终完成 12 章、28,976 字，并通过逐章 exact-body review、sealed actual matcher、全文终审和交付校验。但从 C1 promote 到 C12 actual accept 用时 34 小时 29 分 37 秒；到全文终审 accept 共 34 小时 45 分 22 秒。这个时长不是正文生成的合理成本，而是恢复状态机、计划门禁、自然语言 matcher 和 formal-review parser 反复把同一章送回流水线造成的工程事故。

## 时间线与热点

| 章节 | promote → actual accept |
|---|---:|
| C1 | 2:29:46 |
| C2 | 0:46:03 |
| C3 | 4:04:19 |
| C4 | 11:37:45 |
| C5 | 11:58:57 |
| C6 | 0:35:42 |
| C7 | 0:21:24 |
| C8 | 1:04:16 |
| C9 | 0:16:23 |
| C10 | 0:31:29 |
| C11 | 0:12:58 |
| C12 | 0:14:32 |

C4 与 C5 合计 23 小时 36 分 42 秒，占章节生产墙钟时间约 68.4%。finalize 与 deliver 只用了约 21 秒，不是瓶颈。

日志中还能确认至少四段无 pipeline 模型活动的大窗口，合计约 16 小时 11 分。现有旧日志不能再区分这些时间属于测试、编译、工程调试还是任务停顿；可以确定它们不是正文模型生成。以后必须由持久 timing ledger 分类，而不是事后猜测。

## 重复调用与成本

- retired render candidate 共 61 个：18 个 rejected、8 个 actual-mismatch、12 个 stale-postcommit、10 个 stale-superseded-by-semantic-rewrite，其余恢复/操作性错误 13 个。
- Drafter 有 75 次可恢复 prose 调用，其中 67 次真实生成、8 次缓存；理想值是 12 次真实生成，实际多出 55 次。模型等待合计约 2 小时 29 分 20 秒。
- 字数纠偏 26 次，耗时约 39 分 43 秒；7 次纠偏后仍不达标，1 次硬超时。
- Finalizer dispatch 44 次，理想值 12 次；累计约 1 小时 14 分 24 秒，180 次内部工具调用中有 5 次失败。
- 可观测的 DeepSeek draft judge provider attempt 20 次，合计至少 19 分 46 秒；其中 4 次网络/读取超时约 5 分 43 秒。早期章节缺少完整埋点，因此这是下界。
- 8 次 deterministic actual-mismatch 从首次误判到同正文恢复 accept，额外串行延迟约 1 小时 37 分 44 秒。
- `llm_calls.jsonl` 记录下界为 663 次调用、22,577,212 input tokens、362,864 output tokens，记录成本约 121.79 美元。部分 streaming usage 缺失，实际值只会更高。

## 根因

### 1. 一个章节不是单一、按正文哈希幂等的事务

旧路径把 `drafted → judged → consistency → committed → formal reviewed → matched → published` 分散在多个 Agent session 和恢复分支中。任何一步中断都可能重新启动整个 render session，导致已通过的正文再次进入 Drafter、Finalizer 或 Reviewer。

### 2. C5 的规划前置条件互相矛盾

convergence replan 同时遇到 planning context 超限、authority binding/receipt 失配、RAG `hit.ref` 未注入、predecessor immutable contract 不可见，以及 Planner 被要求调用当前边界禁止的工具。问题应在 seal/promote 前由 Host 一次性预检，却被拖到昂贵模型回合内逐个发现。

### 3. matcher 从中文合同反推语义拓扑

同一事实可能跨段表达，而旧 matcher 把较长的 `after + cause` 当作近似句面合同；它既会因窗口过窄产生假阴性，也会因低 bigram 阈值产生假阳性。C10/C11/C12 的部分失败不是正文缺事实，而是 locator 没有识别“多源确认、候选排除、操作性决定、后续独立后果”等结构。

### 4. formal review 从 Markdown 魔法词猜 disposition

至少 C4、C5、C6、C8、C10、C12 出现过同一正文哈希先 reject、修 parser/policy 后 accept。自然语言中的“判通过”“不触发返工”“无需改写”等表述不应承担机器状态转换。

### 5. AIGC 合同进入得太晚

早期路径先让 Drafter 自由生成，再把 detector 反馈当作返工规则，造成整章重写。AIGC/反机械表达规则必须在首个正文 token 前进入 frozen render packet，且 authoritative provider 的 exact-hash 结论不能被本地概率代理覆盖。

### 6. 为救单本作品而污染通用引擎

事故处理中曾出现按书名、角色名、具体章号和七单地点写死的补丁，甚至会对其他作品的第 4 章注入本书内容。这类补丁已从通用生产代码全部删除。作品事实只能来自项目的 outline、simulation、user rules 和 frozen packet，不能写进引擎源码。

## 本次已落地的修复

1. **首次落笔前注入 AIGC 合同**：新旧 sealed v11 路径都会在独立 prose call 前得到 `anti_ai_render_contract` 和 event timing safeguards；章级合同优先于兼容默认值。
2. **正文证据前置门禁**：sealed 新正文提交后，先运行 deterministic body-evidence matcher；mismatch 先持久化并停止，DeepSeek/Editor formal review 不再被无谓调用。已 formal accept 的同正文恢复仍只重跑最终 matcher。
3. **机械 Finalizer 快路**：严格 sealed_v2、exact-body DeepSeek approved、无 local-soft/retest/pending saga 时，Host 直接复用原 `check_consistency + commit_chapter`；异常或需要编辑时回原 Agent 路径，提交错误失败关闭。正常章可少约 30 秒及 16–17 万 input tokens。
4. **exact-hash 外判恢复**：补齐超时、有限重试、格式重试、同哈希缓存和 quota/transport 恢复；同正文通过后不再重复生成。
5. **全局终审读取面修复**：全文终审读取 canonical accepted finals/merged manuscript，不再受单章 render-only read guard 和旧 edit seed 影响。
6. **持久耗时账本**：每次 stage、frozen host turn、exact-hash judge、mechanical finalize 和 formal review 都向 `meta/pipeline_timings.jsonl` 追加 `started_at/finished_at/elapsed_ms/status/attempt/chapter/budget/error`，失败尝试不会被 `pipeline.json` 覆盖。
7. **去除作品硬编码**：通用生产 `.go` 中已无本书书名、人物、地点、七单或具体章位补丁；保留的门禁均从 store/outline/frozen contract 动态推导。
8. **有界收敛与恢复**：render candidate、formal rejection、actual mismatch、sealed replan 和 directory publish 使用持久 receipt/ledger；同 hash 的已完成步骤可恢复，正史发布保持事务化。

## 尚未冒进切换的架构

以下方向正确，但需要跨多题材回放后再升级为权威协议：

- frozen bundle 增加 `render_evidence_contract.v1`，用 typed topology（多源确认、候选收束、顺序、窗口、终态反证等）代替解析中文 `after/cause`；先 shadow 运行 C1–C12 和其他题材，再切换 matcher 权威。
- Editor 输出绑定 `finding_id + body_sha256 + rule_id + disposition + reason_code + evidence span/hash` 的结构化裁决；Markdown 仅由结构化结果生成供人阅读。
- DeepSeek 以 `body_sha + protocol_version` 做 singleflight；malformed response 只修格式，不重做整次判定。

## 生产 SLO

- 正常短篇章节 `promote → exact-body accept`：目标 12–18 分钟。
- 超过 25 分钟：自动输出当前 stage、body SHA、累计模型调用、rerender 次数与唯一 blocker。
- 同一 sealed plan 最多两次完整 prose rerender；第三次打开 circuit breaker，进入工程诊断，不继续消耗模型。
- 已通过 authoritative exact-hash gate 的正文，任何 deterministic/parser 失败都只能重跑对应门禁，不能重新 Drafter。

## 验收要求

- `go test ./... -count=1`
- `git diff --check`
- 完整短篇应满足：章节连续、逐章 exact-body accept、terminal arc receipt、全文 global accept、`正文.md` 与 finalization manifest 精确同哈希。
- 每次新生产事故必须先从 `meta/pipeline_timings.jsonl` 导出阶段时间线，再决定是否允许重渲染。
