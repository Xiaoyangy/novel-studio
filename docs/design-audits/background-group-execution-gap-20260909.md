# 背景群体执行：现有实现与剩余交付边界

审计日期：2026-09-09。基线 HEAD：`013794e`；本记录检查该时刻未提交的 BG/V4 实现。只读源码及 fake/纯函数测试，不调用真实模型、不修改评测、配置或运行状态。

**状态：未完成，不得启用未接通 V4，也不得将本审计计作背景群体能力已交付。** 当前生产修复恢复原作者允许的船运/岸上路径，并由新的 rehearsal capability gate 拒绝不存在的执行能力；这不代替下面的完整目标，也不替角色选择路线。

## 已有代码实际覆盖什么

| 边界 | 已有实现 | 尚未覆盖 |
| --- | --- | --- |
| 作者来源 | `WorldBackgroundDefinitionV1` 明确资源、岗位、机制、周期、时长及潜在收件人；Host 派生稳定 ID | 岗位办理请求、签收与正向安全确认的声明/结果 |
| 测量 | `FinalizeBackgroundDutyExecutionV1` 校验真实时点/后态、资源访问、时长、重复槽、岗位区间冲突；失败/未知不伪装成功 | 不能将测量自动解释成报告送达、许可或签收 |
| 裁决 | `deriveWorldBackgroundReceiptV1` 从 verified round 派生账本/测量；动态信号无当轮 realization/合法 settlement 时屏蔽旧值 | 生产 runner 尚未提供完整 BG 上下文与执行路径 |
| 公开信息 | `PublicBackgroundDutyFactsV1` 只输出显式公开日程，不输出私有初值 | 该函数无生产调用；无实际报告送达/公开刺激入口 |
| 恢复 | 原始作者 capsule、shadow 来源绑定、同 generation 的前章 verified prefix | accepted canon 与跨窗口/跨 generation 的 BG 来源 capability |

背景岗位不是重要角色 Agent。`RecipientAgentIDs` 只是潜在收件资格；现有 adapter 测试明确断言测量不会改变角色 `ReceivedFacts`。

## 已证实的生产断点

- `internal/agents/character_activation_selection.go:characterActivationExecutionLimit` 的 frozen policy 白名单仍拒 V4；`character_activation_chapter.go:runCharacterActivationChapter` 无条件拒绝全员休眠周期。
- `FreezeBackgroundOpeningSource`、`BindBackgroundActivationSource` 无生产调用；`NewWorldBackgroundContextFromPrefixV1` 仅供 domain 验证/测试使用，runner 没有构建输入。
- `worldBackgroundArbiterPromptV1` 只进入 protocol digest，没有接入实际 Arbiter 提示；`NewScopedWorldBackgroundReferenceCodecV1` 没有生产调用。
- `world_background_source.go:backgroundOpeningSourceUnlocked` 仅接受 BaseCanonChapter=0、FirstProjectedChapter=1；`world_background_workspace.go:backgroundWorkspaceParent` 也要求 workspace base=0。
- `world_background_context.go:NewVerifiedCharacterActivationPrefixFromBackgroundPreviousChapterV1` 只接受同 generation 的连续前章，明确不把 projected chapter 当 accepted canon。
- simulation/grounding/promoted fallback/memory 的若干调用仍经 `VerifyCharacterActivationChapter` 或 `VerifiedStepsForCharacterActivationChapter`，无法凭 flat BG JSON 重新制造 opening authority；不能通过删除这项校验接通。
- 现有 `CharacterPassiveReceptionV2` 要求实际角色 proposal/resolution，不能伪造 `ca_` 背景发送者借用；现有 artifact 签名也绑定真实角色提案。

## 三个有界实施包（尚未实施）

### A. 接通现有背景测量与冻结恢复

复用现有 V4 capability/producer 和 V3 verified-round 引擎，不另起调度系统。新 generation 准备时冻结原始来源，再绑定 shadow；从真实 prefix 构建每轮 BG context，接入专用短提示与 typed reference codec。全员休眠仅在真实到期背景工作能支撑裁决终点时运行；禁止空转、补造历史工时或反复结算资源/时钟。

最小复用集合：

- domain：`world_background_{foundation,duty,context,arbitration,chapter}.go`、`character_activation_policy_v4.go`。
- Store：`world_background_{source,workspace}.go`，以及原 `projected_chronology_source.go` capsule 桥。
- modelinput：`scoped_world_background_references.go`；tools：`background_foundation_presence.go`、`world_background_schema.go`；agents：`character_activation_policy_v4.go`。
- 仅提取这些能力所需的 BookWorld/coherence、source allowlist、round/prefix/chapter、save_foundation、tool schema 和 CLI/runner policy hunks。排除 watchdog、closed-single-tool、无关文档/测试及其它 dirty 修改。

上述 12 个新增生产文件合计 2,256 行，是可复用内核而非可直接启用的完整功能。旧 V1/V2、三种已发布 V3 producer 的标记、字节和恢复路径必须保持。

### B. 有限岗位交互：告知、签收与检查结果

沿用 duty/receipt/ledger，补两类边界：真实测量结果的定向/公开送达；按真实角色请求、准确产物版本/声明和既有机制办理的岗位结果。二者都须绑定岗位身份/权限、地点、实际耗时、原始来源和受众。

报告送达不是收件人回应；签收不是同意全部正文内容；测量或检查结果不是无条件安全许可。不得凭日程、岗位名称、机制自然语言或背景声明制造成功，不得将背景岗位升级为重要角色 Agent。具体类型应在实施前按已有 receipt/source 约束定稿，本记录不另定平行协议。

### C. 正式验收与下一窗口来源

复用 `chronologyAcceptedSource`、sealed bundle、promotion、actual outcome 的现有核验，补真实 accepted BG 来源 capability；保留原 definition/program、最后实际 receipt、ledger root 和时钟/物理根。下一窗口只从已接受前驱继承，不重新播种初值，不接受调用者给出的 digest/bool 作为认证。

simulation、grounding、promoted source 与 memory publication 应共享 Store 获得的同一 `VerifiedCharacterActivationChapter`，不能退回 flat 自证。旧 generation 不静默升级；未验收的 projected BG 结果不得流入 accepted canon。

## 验真清单与成本

已有定向测试通过（Go 1.27.1）：domain、Store、tools、modelinput、agents；覆盖调度/测量/伪造来源/动态未知/同代次恢复及旧 codec 金样。运行命令：

```sh
env -u GOROOT PATH=/opt/homebrew/bin:$PATH GOTOOLCHAIN=local go test \
  ./internal/domain ./internal/store ./internal/tools ./internal/modelinput ./internal/agents \
  -run '^(TestBackground|TestWorldBackground|TestScopedWorldBackground|TestScopedBackgroundLegacy|TestSaveFoundationBackground)' \
  -count=1 -timeout=180s
```

交付前仍必须新增：

1. 真实 fake runner/tool 正反链：作者来源→首次 context→实际 BG 裁决→重读；全员休眠但有到期任务的正例，以及无任务空转拒绝。
2. 缺资源 ID、旧动态读数、错时点、缺权限、重复结算、伪造 receipt、跨 owner/跨来源拒绝；不得降低原断言通过。
3. 实测但未送达保持未知；错误受众、伪造公开刺激、缺请求/旧产物版本/越权签收拒绝；报告不升级为许可。
4. 中断后 source/prefix/receipt 原字节恢复，以及真实 seal→promote→body/review/outcome acceptance→下一三章窗口的 BG ledger 继承。
5. 独立 HEAD 最小切片完整测试、定向 race、vet/build；旧 producer 金样和恢复全通过，且无 BG 角色 Agent 调用。

工程量估计为 2–3 个有界改动包、约 1–2 个工作日实现及独立验证，主要成本在端到端验收与恢复证明；不是当前交付承诺。运行成本不应随岗位数量新增角色模型调用，只在真实背景独立推进时增加必要的 Arbiter/readiness 轮。以上缺口保留为后续完整背景群体能力目标。
