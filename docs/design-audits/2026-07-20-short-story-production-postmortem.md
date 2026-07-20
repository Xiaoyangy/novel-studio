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

1. **首次落笔前注入 AIGC 合同**：seal/promote/render 先用同一套 typed validator 校验 exact v11、唯一 `render_packet`、目标章号和完整合同；真正调用 Drafter 时，Host 再加载冻结载荷，以不进入会话历史的 server-owned envelope 注入。合同加载、形状或身份校验失败时 provider 调用数为 0；章级合同优先于只用于历史 v11 缺字段的兼容默认值。
2. **正文证据前置门禁**：sealed 新正文提交后，先运行 deterministic body-evidence matcher；mismatch 先持久化并停止，DeepSeek/Editor formal review 不再被无谓调用。已 formal accept 的同正文恢复仍只重跑最终 matcher。
3. **机械 Finalizer 快路**：严格 sealed_v2、exact-body DeepSeek approved、无 local-soft/retest/pending saga 时，Host 直接复用原 `check_consistency + commit_chapter`；异常或需要编辑时回原 Agent 路径，提交错误失败关闭。正常章可少约 30 秒及 16–17 万 input tokens。
4. **exact-hash 外判恢复**：补齐超时、有限重试、格式重试、同哈希缓存和 quota/transport 恢复；同正文通过后不再重复生成。
5. **全局终审读取面修复**：全文终审读取 canonical accepted finals/merged manuscript，不再受单章 render-only read guard 和旧 edit seed 影响。
6. **持久耗时账本**：每次 stage、frozen host turn、exact-hash judge、mechanical finalize 和 formal review 都向 `meta/pipeline_timings.jsonl` 追加 `started_at/finished_at/elapsed_ms/status/attempt/chapter/budget/error`，失败尝试不会被 `pipeline.json` 覆盖。
7. **去除作品硬编码**：通用生产 `.go` 中已无样书书名、人物、地点、专属机制或具体章位补丁；保留的门禁均从 store/outline/user rules/frozen contract 动态推导，并由生产源码污染回归测试阻止样书事实再次进入引擎。
8. **有界收敛与恢复**：render candidate、formal rejection、actual mismatch、sealed replan 和 directory publish 使用持久 receipt/ledger；同 hash 的已完成步骤可恢复，正史发布保持事务化。
9. **typed 零模型预检**：seal 一次汇总所有章节的 bundle/context/AIGC/sealed-identity blocker；promote 与 render 在任何模型调用和正史变更前复核同一份冻结证据。旧 v11 bundle 保持字节不变，由带版本的兼容层在模型首 token 前注入 prospective AIGC 合同；新章级合同不会被覆盖。
10. **两级 content-addressed 章节事务**：以 sealed plan identity 和 exact body SHA 分层寻址，`body_ready → committed → formal → actual_matched → published → accepted → completed` 每一步写不可变 digest chain。进程崩溃后按真实回执跳过 Writer、Reviewer 或 directory publish；runtime owner 变化不再把已发布正文误判成失败。
11. **完整正文调用熔断与单调用快路**：同一 sealed candidate 的完整正文 realization 在 Host 前持久预留，随后转换成绑定当前 render lease 的一次性 permit；permit 只在合同预注入成功后、真实 provider 边界前原子消费。最多 3 次 realization（初稿 + 2 次整章重渲染），进程崩溃不返还名额，第 4 次在 provider 前拒绝。冻结 Codex 渲染直接走一次 isolated prose schema，由程序合成 `draft_chapter` 调用，跳过占位工具决策、三采样、pairwise judge、正文缓存和同一授权内的字数 repair；空稿或字数越界直接交还外层有界重渲染。finalizer 与纯复审不消耗正文名额。
12. **exact-body review singleflight**：Editor 与 DeepSeek 按完整 request identity 使用进程内外同一把有界文件锁；等待者在锁内二次读缓存，只有胜者调用模型。缓存写入在释放锁前完成，崩溃由内核释放锁，避免两个进程重复评审同一正文。
13. **独立 watchdog 控制面**：15 秒 heartbeat 与显式 progress 分离，5 分钟无进展标记 stalled，25 分钟生成一次只含 opaque identity/hash/timing 的诊断。状态位于项目同级的稳定 control root，不参与 live canon、候选复制或 DirectoryPublish CAS；目录换根时仍显式 Pause/Resume，避免后台写入与原子发布竞争。
14. **候选控制路径与许可身份认证**：dispatch permit 同时绑定 candidate、generation、chapter、plan、checkpoint、bundle、promotion 七项身份及当前执行租约；候选、`convergence`、ledger 和 lock 的派生路径逐级拒绝 symlink/alias，锁和 provider 侧 ledger 读取使用 no-follow 打开。相对路径、外部绝对路径、路径别名和控制目录替换均在 reserve/finish/sync/provider 前失败，且不得在目标外留下目录、锁或账本。

## 尚未冒进切换的架构

以下方向正确，但需要跨多题材回放后再升级为权威协议：

- frozen bundle 增加 `render_evidence_contract.v1`，用 typed topology（多源确认、候选收束、顺序、窗口、终态反证等）代替解析中文 `after/cause`；先 shadow 运行 C1–C12 和其他题材，再切换 matcher 权威。
- Editor 输出绑定 `finding_id + body_sha256 + rule_id + disposition + reason_code + evidence span/hash` 的结构化裁决；Markdown 仅由结构化结果生成供人阅读。
- Editor/DeepSeek 的最终裁决升级为共同的 typed decision schema；现有 exact-request singleflight 已解决重复调用，但 malformed response 的格式修复仍应与语义重判彻底分离。

## 生产 SLO

- 正常短篇章节 `promote → exact-body accept`：下一次完整短篇生产的验证目标为 12–18 分钟；这不是本次事故数据已经证明的承诺，必须用 timing ledger 实测后再收紧。
- 超过 25 分钟：watchdog 自动生成一次脱敏快照，包含 stage、chapter、plan/body SHA、累计耗时和最后一次持久进展；模型调用与 rerender 次数分别从 timing ledger 和 dispatch ledger 汇总。当前实现只诊断，不自动取消仍在运行的 provider。
- 同一 sealed plan 最多两次完整 prose rerender；第三次打开 circuit breaker，进入工程诊断，不继续消耗模型。
- 已通过 authoritative exact-hash gate 的正文，任何 deterministic/parser 失败都只能重跑对应门禁，不能重新 Drafter。

## 验收要求

- `go test ./... -count=1`
- `git diff --check`
- 生产源码污染回归测试通过，非测试 `.go` 不含退役样书的专名与专属机制。
- 冻结 render 的一次 prose permit 最多对应一次底层正文 provider 执行；空稿、越界、配置漂移和第二次调用都不得触发隐式 repair/retry。
- 完整短篇应满足：章节连续、逐章 exact-body accept、terminal arc receipt、全文 global accept、`正文.md` 与 finalization manifest 精确同哈希。
- 每次新生产事故必须先从 `meta/pipeline_timings.jsonl` 导出阶段时间线，再决定是否允许重渲染。
