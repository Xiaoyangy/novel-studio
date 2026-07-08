# 3000 字整章 AI 检测口径

本文用于 writer / drafter / editor 共用：把一章约 3000 字的小说当成读者会整段复制到检测平台的真实场景处理，不得只看局部片段或 blended 平均值。

## 外部机制摘录

- Turnitin 的 AI Writing Report 要求至少 300 words 的长篇 prose，且可处理到 30,000 words。约 3000 中文字的小说章已经远超“可生成百分比”的长文门槛，应按整篇长文被评分处理。来源：https://guides.turnitin.com/hc/en-us/articles/22774058814093-Using-the-AI-Writing-Report
- Turnitin 早期 FAQ 披露过分段思路：提交会被拆成重叠的几百词片段，对句子和片段打分后再汇总。工程含义：约 3000 字整章如果每个窗口都节奏均匀，平台可能给出很高的整体比例；只改开头/结尾几句很难拉低。来源：https://cte.ku.edu/sites/cte/files/images/2023/AI%20Writing%20FAQs%20March%202023%20%281%29.pdf
- Copyleaks AI Text Detection API 返回整体分类，也返回 section 位置、classification 和 probability。工程上必须保留“整章值”和“分段最高风险”两条线，不能只看平均。来源：https://docs.copyleaks.com/reference/data-types/ai-detector/ai-text-detector-response/
- Copyleaks 页面说明其报告会给出 AI Logic、AI Phrases 和 Source Match。工程含义：不要只做同义词替换；高频 AI 短语、结构化说明和可追溯来源匹配都可能暴露问题。来源：https://copyleaks.com/ai-detector
- GPTZero 对公开机制的解释包括 perplexity 与 burstiness：文本太可预测、全篇节奏太均匀会更像 AI；人类文本通常有短长句、简单句和复杂句混合。来源：https://gptzero.me/news/how-ai-detectors-work/
- GPTZero 当前公开 FAQ 也把检测描述为综合 patterns、perplexity、burstiness、style/repetition 等大量因素，并提醒没有检测器完美。工程含义：外部 70% 不能直接等同作弊判定，但足以说明整章统计结构需要返工。来源：https://gptzero.me/
- Originality.ai 对 perplexity/burstiness 的说明强调：过于可预测的下一个词、重复词爆发、缺少句式变化会抬高 AI 风险。工程含义：章节要改段落功能、信息释放和词汇场，而不是局部加冷僻词。来源：https://originality.ai/blog/perplexity-and-burstiness-in-writing
- Stanford SCALE 对 GPTZero 的评估提醒：AI 检测存在混合准确度和误伤风险。工程含义：报告不能当唯一事实，但用户要投平台/外审时，工程门禁应按更严格的整章风险处理。来源：https://scale.stanford.edu/ai/repository/assessing-gptzeros-accuracy-identifying-ai-vs-human-written-essays
- 腾讯朱雀公开报道描述其文本检测会对比检测文本与大模型预测内容，推测 AI 生成概率，并覆盖新闻、公文、小说、散文等文体。来源：https://m.dzplus.dzng.com/share/general/0/NEWS2096476LYGRELBVDXQED
- Pangram 对困惑度/突发性路线的批评提醒：这些指标会误伤训练集中常见、规范、被反复转载的文本。工程上不能靠脏码和随机词“骗分”，应让正文真实承担功能差异。来源：https://www.pangram.com/zh/blog/why-perplexity-and-burstiness-fail-to-detect-ai

## 本工程门禁

- `EffectiveGatePercent` 是唯一门禁采用值。短章（`hanzi <= 5000`）按整章单检测片段处理，`segment_risk_floor` 或 raw AI 占比高时，不得被普通 `blended_aigc_percent` 稀释放行；只有 `human_anchor_final_cap_percent` 已触发、无脏码/真重复、AI voice 与 Editor 均通过时，强人工锚点 cap 才能成为门禁采用值，同时仍展示 raw floor。
- 统一审核报告必须同时展示 `AI 占比`、`门禁采用值`、`融合值`、`朱雀分片风险下限`。交付判断看 `门禁采用值`，不是只看 `融合值`。
- `aigc_ratio >= 35%` 是 error，必须返工；`5% <= aigc_ratio < 35%` 是 warning，也不得作为交付完成。
- 报告中“主要问题”不是装饰文字。只要主要问题仍列出机械 error、阻断 warning、Editor warning 或功能性风险，就不能称为完全通过；应继续改到机械规则清空、AI voice 通过、Editor 主要问题为空或只剩非交付阻断的题材取舍。
- 外部平台出现约 70% 的整章 AI 判断时，按“章节统计结构失败”处理，而不是按局部措辞失败处理：必须重新选择页面显性节拍、压缩计划清单、改变段落功能分布，并保留整章/分段两套证据。

## 写作策略

目标不是随机换词，而是破除“整章每 180 字窗口都同样稳定”的机器曲线：

- 让段落功能换挡：事故触发、误判、口头争执、物件迟到、私人生活侵入、现场沉默、权限后果不能都用同一叙述速度。
- 让词汇场切换自然发生：技术词、生活词、人物口癖、动作词、物件部件名分布要随场景改变，而不是整章平均铺开。
- 允许局部普通、重复、口语和不完整：真人争执会重复“别点”“我没点”“谁签”，但重复必须由角色压力推动，不能堆无意义字串。
- 降低功能句密度：不要连续多段都在说“保全/导出/权限/说明/审批”。每一条流程信息必须由谁怕担责、谁想甩锅、谁不愿签字来驱动。
- 具体物件不是清单：只保留会触发行动、入账、被拍照、被误读或后文回收的物件。为抬 TTR 堆物件、店名、冷僻词，一律视为清单灌水。
- 主角必须有可见裂缝：她可以专业，但不能全程像审核员。用误按、删掉文件名、差点截图、没听清、被私人消息打断等具体动作替代“内心复杂”。
- 计划是素材池，不是正文清单：把大纲、台账、角色状态、世界层、信息差先筛成 `surface_beats`、`latent_context`、`reveal_budget`、`cut_or_compress`。正文只显性写 surface；latent 只约束行动；reveal 只露证据；cut/compress 不得还原成长篇说明。

## 审核策略

- 先看 `mechanical_gate.effective_gate_percent` / `gate_percent`，再看四维、latest detector proxy 和 `zhuque_segment_proxy.segments`。
- 若 `segment_risk_floor >= 50`，必须把整章当一个风险片段读，检查局部熵/TTR曲线是否过平、语义功能是否过稳、段落是否均匀正确；若报告同时出现 `human_anchor_final_cap_percent`，可在机械规则清空、AI voice 通过、Editor accept 后按 cap 交付，但 raw floor 仍进入风险提示。
- Editor 不得因为正文“看起来不错”而覆盖机械 error。Editor accept 只能说明设定/角色/钩子可用，不代表可交付。
- 返工建议必须具体到段落功能：例如“第18-24段连续流程辩论，改成安全组甩锅、投屏同事自保、记录员怕签错、主角被私人消息打断”，不能只写“增加人味”。
