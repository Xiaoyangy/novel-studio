# 平台整治口径映射（Task 070）

番茄 2026-02-04 专项整治口径 → 本系统信号映射：

| 平台口径 | 本系统覆盖信号 | 状态 |
|---|---|---|
| 语言僵硬（AI 腔） | aigc v3 blended 分、editor/rules 红旗（格言/清单/编号阶梯/即时目的回答）、八维 ai_voice_detection、slop_avoidlist 生成期规避 | ✅ 多层 |
| 内容空洞 | 八维 aesthetic/pacing、契约履约率、ConfidenceReport doubts | ✅ |
| 同质化（章间结构雷同） | HookHistory/StrandHistory 反雷同、chapter_function_repetition、ending_hook_uniformity；书级统计见 stylestat BookReport（Task 063） | ⚠️ 书级统计新增，需累积样本 |
| 批量生成占流量 | 非目标场景（本系统单书全流程审核+返工，产能受审核门禁约束） | N/A |
| 检测器判定（朱雀等） | 外部检测登记 + 校准相关性（Task 060/062） | ⚠️ 需人工抽检积累 |

## 人工创作证据链（申诉用）
`novel-studio --export --evidence-pack`：打包 reviews/（八维评审+机械门禁 ai_gate）、
返工前后 diff（.pre-rewrite 对照）、外部检测登记、prompt_manifest——对应平台"重证据、
一次复审机会"的现实约束。
