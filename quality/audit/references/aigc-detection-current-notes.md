# AI 文本识别逻辑检索摘要（2026-07-01）

本文件用于维护 `codex-local-aigc-v3` 的设计依据。它不是外部检测器复刻，而是把近年公开论文/系统里可本地实现的信号转成小说生产流程的审核规则。

## 公开检测主线

- **黑盒/弱模型特征融合**：Ghostbuster 使用多个较弱语言模型的特征组合并训练分类器，不需要目标生成模型的 token probability。落地到本地：增加“弱语言模型一致性”，用 unigram/bigram 惊讶度曲线近似检测“过于好猜且过于平滑”的文本。
  - 来源：https://aclanthology.org/2024.naacl-long.95/
- **概率曲率/条件概率差异**：Fast-DetectGPT 用 conditional probability curvature 区分机器与人类文本，并强调比 DetectGPT 更高效。落地到本地：不用真实 LM 概率，但保留“句级概率曲线是否过稳”的代理信号。
  - 来源：https://arxiv.org/abs/2310.05130
- **风格计量轻量模型**：NEULIF 把文本拆成 stylometric/readability features，再用小型 CNN/RF 分类。落地到本地：保留句长分布、标点、局部 TTR、段落向量相似度等可复算特征。
  - 来源：https://arxiv.org/abs/2511.21744
- **Transformer + 辅助风格特征**：RANLP 2025 M-DAIGT 方向继续使用 DeBERTa 等分类器；AAAI 2025 任务中也出现 RoBERTa AI detector、stylometry、E5 embedding、token-level perplexity/entropy 的融合。落地到本地：不要只看“AI 套话”，要把风格计量、局部熵和语义平滑一起纳入审核。
  - 来源：https://aclanthology.org/2025.ranlp-mdaigt.2/
  - 来源：https://arxiv.org/html/2505.11550v1
- **水印/来源证明**：SynthID-Text 代表生成时嵌入水印、检测时读出统计信号的路线。它属于 provenance/watermark，不是纯文本风格检测；本地不能判断第三方模型是否带水印，只能在报告里说明“文本风格检测不能替代来源证据”。
  - 来源：https://www.nature.com/articles/s41586-024-08025-4

## v2 落地规则

最终 `AI占比` 三层合成：

- 55%：朱雀四维代理分。保留突发性、困惑度代理、结构指纹、跨段一致性。
- 30%：近年检测器代理层。新增弱语言模型一致性、局部熵/TTR 波动、风格计量/可读性、语义平滑/概括腔。
- 15%：既有 AI 味启发式。保留套路密度、解释归纳腔、工程词泄漏、段落复述等。

### 2026-07 追加：反检测式碎段 / humanizer 痕迹

外部平台对改写后的 `01.md` 给出 `0.7718` 高风险，而本地 v2 初版只给 `0.0032`。复盘发现：该稿没有套路词、重复段落或解释归纳腔，却出现了大量“单句短段 + 独立条款块 + 极短断行”的反检测式人造节奏：

- 单句段比例 0.72
- 12 字以内短段比例 0.64
- 6 字以内极短段比例 0.32
- `【...】` 独立条款块比例 0.14

这类文本会骗过“套路词/重复/ngram”规则，但容易被平台的段级/句级分类器判为被 AI 改写器或 humanizer 处理过。已加入：

- `over_staccato_humanizer`：短句比例过高且大量单句短段。
- `fragmented_single_sentence_paragraphs`：段落中位字数很低、单句段密集。
- `very_short_paragraph_overuse`：极短段比例异常。
- `contract_block_density`：独立账单/条款块占比偏高。

调参原则：不是禁止短句，而是防止整章被切成“每个信息点一段”的机械节拍。正常网文章节可以有短句和账单块，但需要把一部分动作、误会、取证、犹豫、环境反应合并成自然段，让段落功能有真实起伏。

### 2026-07 公开平台说明刷新

- Turnitin 的 AI Writing Report 会给出整体 AI 百分比，并区分 `AI-generated only` 与 `AI-generated text that was AI-paraphrased`；低于 20% 的分数不再显示精确百分比，原因是 1%-19% 区间误报可能更高。
  - 来源：https://guides.turnitin.com/hc/en-us/articles/28294949544717-AI-writing-detection-model
- Turnitin 2026-05 更新说明显示，它持续更新模型以跟进新 LLM，并强调对超过 20% AI-written text 的文档维持低误报率；这说明本地门槛用 20% 做硬闸门比 5% 更贴近平台公开阈值语义。
  - 来源：https://guides.turnitin.com/hc/en-us/articles/29645383597965-Turnitin-product-updates
- GPTZero 公开说明其检测包含 sentence-by-sentence classifier、mixed classification、Advanced Scan 片段定位和 Paraphraser Shield，用于识别改写、替换字符等绕检策略。
  - 来源：https://gptzero.me/technology
- Copyleaks 说明 AI 检测是 probability and context，不是二元判定；其公开资料反复强调会识别 paraphrased / interspersed AI text，并提供 confidence/probability 层面的报告。
  - 来源：https://copyleaks.com/blog/can-ai-detectors-be-fooled
  - 来源：https://docs.copyleaks.com/concepts/products/ai-text-detection-api/
- OpenAI 已在官方 AI classifier 页面提示：该分类器自 2023-07-20 因准确率低下线，并明确提醒文本检测不应作为主要决策工具。
  - 来源：https://openai.com/index/new-ai-classifier-for-indicating-ai-written-text/

本地新增 `layout_humanizer_fingerprint`，对应平台的句级/段级定位与改写检测逻辑：关注单句短段网格化、微段落中位字数过低、独立规则卡片密度、短段与条款块同时密集。它不直接模拟某一家平台，而是把公开可复算的布局特征纳入内部质量门禁。

### 2026-07 追加：v3 概率曲率 / 句级分类代理

对当前 `01.md` 继续复盘后发现，单句短段和条款块已被回改消除，但仍保留更接近现代检测器的高风险信号：句级 bigram / unigram 惊讶度曲线和滑窗熵曲线过于稳定。公开资料支持把这类信号提升为主门控：

- GPTZero 公开说明使用 sentence-by-sentence classification，并提供 document-level 与 granular insights；其 Paraphraser Shield 针对绕检改写、字符替换等策略。
  - 来源：https://gptzero.me/technology
- Copyleaks AI Text Detection API 返回 section-level 分类、overall human-versus-AI summary，并可标出具体 AI phrases；公开说明包括 Heavy Edits / paraphrased AI text 检测。
  - 来源：https://docs.copyleaks.com/concepts/products/ai-text-detection-api/
- Ghostbuster 不依赖目标模型 token probability，而是通过多个弱语言模型特征组合训练分类器，且包含 creative writing benchmark。
  - 来源：https://aclanthology.org/2024.naacl-long.95/
- DetectGPT / Fast-DetectGPT 代表概率曲率路线：DetectGPT 使用 log probability curvature，Fast-DetectGPT 使用 conditional probability curvature，并通过更高效 sampling 近似检测。
  - 来源：https://arxiv.org/abs/2301.11305
  - 来源：https://arxiv.org/abs/2310.05130
- SynthID-Text 说明水印检测测量生成过程中留下的 statistical signature；同时也指出 post hoc detectors 依赖人机文本统计差异且需要持续维护。
  - 来源：https://www.nature.com/articles/s41586-024-08025-4

v3 调整：

- 引擎标记升为 `codex-local-aigc-v3`。
- 最终分权重改为：近年检测器代理层 85%，朱雀四维 10%，既有启发式 5%。原因：当前目标是模拟平台级句级/段级分类器的内部门禁，而不是传统“AI 味词表”。
- 新增 `probability_curvature_proxy`：将句级 bigram/unigram 惊讶度 CV、滑窗熵 std/CV、滑窗 TTR CV 合成概率曲率代理。
- 提高 `weak_lm_uniformity` 敏感度：当句级 bigram CV 与 unigram CV 同时异常低时，直接判高风险。
- 提高 `local_entropy_uniformity` 敏感度：不再要求低多样度；若熵本身很高但跨窗波动极低，同样视作生成分布过稳。
- 新增 `over_balanced_sentence_band_distribution`：短句、中短句、中长句、长句四档占比过于均衡时，作为风格计量辅助信号。

当前 `output/novel/chapters/01.md` 在 v3 下的关键证据：

- `fast_detectgpt_curve_proxy_high`：bigram CV=0.026、unigram CV=0.048、entropy std=0.006。
- `bigram_unigram_curve_too_flat`：弱模型概率曲线异常平滑。
- `window_entropy_signature_flat`：滑窗熵 std=0.006、CV=0.006。
- `over_balanced_sentence_band_distribution`：四档句长占比约 0.244 / 0.269 / 0.227 / 0.261。

因此当前 `01.md` 可被本地 v3 判为 `0.7019`，进入高风险区。注意：这仍是内部质量门控，不是作者身份定论；低误报需要依赖更多人工样本和外部标注集校准。

### 2026-07 追加：朱雀 PDF 报告分片校准

用户提供桌面报告 `/Users/chenhongyang/Desktop/朱雀AI检测助手.pdf`。报告时间 `2026/7/1 22:50:09`，朱雀将当前 `01.md` 分成 3 个片段：

| 片段 | 占全文比例 | 占字符数 | 朱雀 AIGC值 |
|---|---:|---:|---:|
| 片段1 | 18.53% | 540 | 0.6271 |
| 片段2 | 18.81% | 548 | 0.4289 |
| 片段3 | 62.66% | 1826 | 0.8694 |

报告饼图显示：人工特征 18.81%，疑似 AI 81.19%，AI 特征 0%。这暴露出本地 v3 的一个漏判：全文平均指标可能被局部低熵/高熵切换稀释，导致长尾片段高风险没有进入最终门禁。

本地新增 `zhuque_segment_proxy`：

- 对 2800-3400 字左右章节，按朱雀报告形态构造两个约 540 字前置片段 + 一个长尾片段的本地代理。
- 每个片段单独跑本地 AIGC 子分析，但不递归套用分片代理。
- 结合弱语言模型曲线、具体物密度、动作密度、TTR、归一化熵、片段占比生成片段级 AIGC 代理值。
- 输出 `suspected_ai_ratio_percent`、`human_ratio_percent`、`max_segment_percent`、`risk_floor_percent`。
- 当疑似 AI 占比和最高片段风险都高时，最终 `aigc_percent` 取 `全文综合分` 与 `分片风险下限` 的较高者，防止长尾片段被全文平均掩盖。

对同一版 `01.md`，本地分片代理现在输出：

| 片段 | 本地代理 | 类别 | 占比 |
|---|---:|---|---:|
| 片段1 | 65.00% | 疑似AI | 19.17% |
| 片段2 | 42.00% | 人工特征 | 19.45% |
| 片段3 | 86.00% | 疑似AI | 61.38% |

本地疑似 AI 占比 `80.55%`，接近朱雀报告的 `81.19%`；最终自研 AIGC 值从全文综合 `16.73%` 被分片风险下限提升到 `72.50%`，门禁失败。

### 2026-07 追加：朱雀二次报告校准 / 拟声噪声绕检

用户在腾讯朱雀重新检测最新 `01.md`、`02.md`、`03.md`，桌面报告分别为：

| 文件 | 报告时间 | 朱雀片段 | 外部加权AIGC | 外部疑似AI占比 | 本地校准前 | 本地校准后 |
|---|---|---|---:|---:|---:|---:|
| `朱雀AI检测助手.pdf` / `01.md` | 2026/7/1 23:24:17 | 0.6736 / 0.4335 / 0.8590 | 75.08% | 82.64% | 1.20% | 74.07% |
| `朱雀AI检测助手2.pdf` / `02.md` | 2026/7/1 23:28:11 | 0.8207（单片段） | 82.07% | 100.00% | 38.60% | 90.00% |
| `朱雀AI检测助手3.pdf` / `03.md` | 2026/7/1 23:32:09 | 0.7964 / 0.4640 / 0.8204 | 75.40% | 84.54% | 74.46% | 74.46% |

关键复盘：

- `01.md` 本地误判为低风险，是因为新增的长串拟声词（如 `嗒嗒、咯、叩、沙、咔...`）人为抬高了局部熵/TTR波动，使 `weak_lm_uniformity`、`probability_curvature_proxy` 和 `local_entropy_uniformity` 同时归零。朱雀并没有被这种局部声响扰动降权，仍将长尾片段判为 `0.8590`。
- `02.md` 朱雀没有拆成三片，而是将约 2600 字文本作为一个片段整体判 `0.8207`。本地固定三片策略会让前两段被“具体物/动作密度”降权，低估整体疑似比例。
- `03.md` 的本地疑似片段占比 `82.73%` 与朱雀 `84.54%` 接近，说明长尾片段风险下限方向正确；主要误差来自朱雀真实分片边界不是固定 540/548/长尾。

本地调整：

- 新增 `SOUND_NOISE_RE` 与 `normalize_detector_curve_text`：普通风格统计仍保留拟声词，但在句级概率曲线、弱语言模型一致性、滑窗熵/TTR 计算前，把密集拟声/重复声响归并为 `声响。`。这不是惩罚正常声效，而是防止“噪声注入”稀释检测曲线。
- `latest_detector_proxy` 增加 `detector_noise` 记录；章节报告在检测到拟声噪声时显示“曲线去噪”行。
- `zhuque_like_segment_bounds` 对 `1800 <= visible_chars <= 3600` 的文本改为单片段代理。2026-07-02 最新 `01.md`、`02.md`、`03.md` 报告均显示：约 3k 字章节会被朱雀作为整章单段判分，旧的“三片代理”会把整章风险切碎稀释。

### 2026-07 追加：高质量人工样本正向校准

用户在 `novel-studio/deconstruction-library/review-calibration/high-quality-human-prose` 提供了一批高质量人工写作样本，并标定为 AI率 0。复盘发现，旧 v3 会把其中 6/8 个非空样本判到 76%-90%，属于明显误伤。误伤集中在曲线类信号：

- 封闭场景、规则推理和强对话会让关键词、称谓、物件稳定复现，导致滑窗熵/TTR 波动偏低。
- 对话密集会压低句级 bigram/unigram 惊讶度 CV。
- 规则类悬疑中的“参与者、规则、首先/最后”等词可能是剧情规则文本，不一定是模板化生成。
- 长文本按 640 字切片时，单片段因长度不足无法形成完整人工证据，容易被分片代理误伤。

本地新增 `human_anchor` 正向校准。它只在没有以下硬风险时启用：无语义脏码/字符汤、密集拟声噪声、段落/长句真重复、工程词泄漏、套路密度压过场景锚点、短段+条款块密集的人味化后处理。启用后，它检查：

- 句长 CV、段长 CV 是否有自然疏密。
- 12字以内短句是否形成自然断气。
- 对话/引号密度是否提供人物声口。
- 物件、动作、感官密度是否足以承载场景。
- 抽象词是否低于场景锚点。
- 标点层次和套路密度是否健康。

校准作用于 `probability_curvature_proxy`、`weak_lm_uniformity`、`local_entropy_uniformity`、`stylometry_readability`、`zhuque_segment_proxy` 这些容易误伤人工类型文的曲线、风格计量和分片信号；不覆盖 `content_integrity`、真重复、工程词泄漏和语义平滑风险。对 420 字以上的分片，若唯一 blocker 是“长度不足”且人工锚点分足够高，分片代理也允许局部降权。但小说正文的 `narrative_scene` 锚点只做软校准，不能再给整章最终 4.80% 上限。

校准后，8 个非空人工正样本用于判断“可能误伤的叙事特征”，但不再作为外部朱雀通过性承诺；两类脏码样例仍被内容完整性硬下限拉到 100%。

### 2026-07-02 追加：朱雀三章整段报告与技术说明文人工锚点

用户提供桌面最新朱雀报告：

| 文件 | 报告时间 | 朱雀片段形态 | 外部 AIGC |
|---|---|---|---:|
| `朱雀AI检测助手1.pdf` / `01.md` | 2026/7/2 01:10:02 | 整章单段，2960 字符 | 80.15% |
| `朱雀AI检测助手2.pdf` / `02.md` | 2026/7/2 01:14:44 | 整章单段，3093 字符 | 79.54% |
| `朱雀AI检测助手3.pdf` / `03.md` | 2026/7/2 01:15:08 | 整章单段，3284 字符 | 77.63% |

同时，用户提供 `朱雀人工事例.md`，并标明该样本被朱雀判为 100% 人工。该样本不是小说，而是技术摘要/说明文，主要人工特征为：领域术语密集、句子承载长、中英术语与缩写并置、段落形态接近摘要、无对话、低套路词。这说明本地审核不能只用小说的物件/动作/对话锚点识别人工文本。

本次规则调整：

- `technical_expository` 锚点：面向技术摘要、说明文、论文式段落；允许 320 汉字以上、领域术语/英文缩写密度高、长句承载稳定、无脏码和真重复的文本使用 4.80% 最终误判上限。
- `narrative_scene` 锚点：面向小说正文；只允许软降权曲线类误伤，不再允许最终 4.80% 硬上限。
- `zhuque_segment_proxy`：对 1800-3600 可见字符的文本采用整章单段代理；若被人工锚点降权前的概率曲率、弱模型一致性或滑窗熵/TTR 原始值仍高于 80%，整段分片风险按 76%-86% 区间抬升，并进入最终风险下限。

校准后，本地结果：

| 文件 | 本地 AIGC | 外部朱雀 AIGC | 结论 |
|---|---:|---:|---|
| `01.md` | 82.00% | 80.15% | 不通过，本地已能识别 0.7+ 风险 |
| `02.md` | 80.00% | 79.54% | 不通过，本地已能识别 0.7+ 风险 |
| `03.md` | 80.00% | 77.63% | 不通过，本地已能识别 0.7+ 风险 |
| `朱雀人工事例.md` | 4.80% | 朱雀人工样本 | 通过 |

这次网上资料刷新后的技术依据：

- RAID 作为机器生成文本检测鲁棒性基准，强调检测器在不同模型、领域和扰动/攻击下会明显漂移，不能只用单一表面特征。
  - 来源：https://arxiv.org/abs/2405.07940
- Binoculars 用两个语言模型的困惑度 / 交叉困惑度比例做零样本检测，说明现代检测会关注语言模型概率关系，而不是只看词表。
  - 来源：https://arxiv.org/abs/2401.12070
- Fast-DetectGPT / DetectGPT 代表概率曲率路线，关注文本在模型概率曲面上的曲率/条件概率差异；本地的 `probability_curvature_proxy` 继续保留并提高优先级。
  - 来源：https://arxiv.org/abs/2310.05130
  - 来源：https://arxiv.org/abs/2301.11305
- Ghostbuster 说明可用弱语言模型特征组合做检测，不依赖目标生成模型概率；本地 `weak_lm_uniformity` 与曲线去噪保持这个路线。
  - 来源：https://aclanthology.org/2024.naacl-long.95/
- GPTZero / Copyleaks 公开资料均强调片段级、句级、混合文本、改写/绕检检测；本地因此保留 `layout_humanizer_fingerprint`、`zhuque_segment_proxy` 和噪声中和。
  - 来源：https://gptzero.me/technology
  - 来源：https://docs.copyleaks.com/concepts/products/ai-text-detection-api/

## 生成与回改原则

- 内容质量先于审核通过性。任何为了打断检测曲线而插入的无语义 OCR 脏码、随机汉字串、稀有神怪名词/怪字堆叠、长串词表、拟声噪声, 都应判为内容完整性失败。
- 不做“随机同义词替换”。这种处理只会制造浅层噪声，无法解决概率曲线、段落功能同质和语义平滑。
- 每章要有段落功能差异：动作推进、对话误会、物件细节、沉默反应、信息揭示交替出现。
- 具体细节要服务场景，不堆清单。优先使用金额、时间戳、错误提示、物件状态、角色口癖和尴尬动作。
- 审核报告必须列 `latest_detector_proxy` 的最高风险项，不能只写“朱雀四维”或“AI 味偏重”。

### 2026-07-02 追加：stylometry / psycholinguistic 资料刷新

本轮目标审计再次检索公开资料，结论与当前本地审核方向一致：

- `arXiv:2505.01800` 强调把 stylometric features 映射到词汇检索、话语规划、认知负荷管理和自我监控等心理语言过程。落地到本地：AI 文本识别要解释“为什么像机器”，不能只给单分数。
- `arXiv:2507.00838` 表明短样本可用词汇、语法、句法、标点等 stylometric features 做区分，同时指出更强的 LLM 有更高语法标准化倾向。落地到本地：继续保留句长/段长/标点/语法标准化/局部 TTR 和 n-gram 曲线类信号。
- RAID benchmark (`arXiv:2405.07940`) 说明检测器在模型、领域、采样策略和 adversarial attacks 下会明显漂移。落地到本地：本地 AIGC 值是质量门控和风险解释，不是作者身份裁决。
- 2025 creative-writing stylometry 研究显示，人类创意文本更异质，LLM 输出更容易按模型形成紧密风格簇。落地到写作：所谓“人味”不是随机化，而是场景功能、视角偏差、物件回扣、关系误判和节奏非均匀性共同形成的可读差异。

来源：

- https://arxiv.org/abs/2505.01800
- https://arxiv.org/abs/2507.00838
- https://arxiv.org/html/2405.07940v1
- https://www.nature.com/articles/s41599-025-05986-3

### 2026-07-02 追加：朱雀 03:44 单片段报告与边界阈值校准

用户提供桌面最新报告 `/Users/chenhongyang/Desktop/朱雀AI检测助手.pdf`。报告时间 `2026/7/2 03:44:39`，朱雀将当前 `01.md` 作为整章单片段处理：

| 文件 | 朱雀片段形态 | 占字符数 | 外部分布 | 片段 AIGC |
|---|---|---:|---|---:|
| `01.md` | 整章单段，占全文 100.00% | 3374 | 人工特征 0%；疑似AI 100%；AI特征 0% | 0.7436 |

本地旧版曾把同一版 `01.md` 判到 `1.32%`，漏判原因是边界条件过窄：滑窗 TTR CV 约 `0.075`，刚好被 `<0.075` 排除，导致 `probability_curvature_proxy` 与 `local_entropy_uniformity` 没有触发；同时 `zhuque_segment_proxy` 对单片段 `50%-80%` 的疑似风险只给折扣地板，叙事人工锚点又进一步软降权，最终把外部整段疑似 AI 信号稀释掉。

本次校准：

- `probability_curvature_proxy` 与 `local_entropy_uniformity` 的中高风险边界改为 `ttr_cv <= 0.080`，覆盖朱雀这类边缘但整段稳定的章节。
- `zhuque_segment_proxy` 若只有 1 个片段、疑似 AI 占比约 `100%`、最高片段分不低于 `50%`，则最终分片风险下限直接采用该片段分，不能再被叙事人工锚点压成通过。
- `text_signals.py` 把 `aigc_value.py` 的本地 AIGC 值接入综合风险；当自研 AIGC 高于 `35%` 时，综合风险必须同步抬升，不能出现“表层 AI 味 0 分但 AIGC 高风险”的报告错配。

校准后，当前 `01.md` 本地输出：

| 指标 | 数值 |
|---|---:|
| 自研 AIGC 值 | 78.80% |
| 分片形态 | 单片段，占比 100.00% |
| 朱雀式分片代理最高片段 | 78.80% |
| 外部朱雀片段 AIGC | 74.36% |

结论：约 3000 字章节只要被外部平台按整章单片段判为疑似 AI，本地门禁也必须按整段风险处理。写作回改不能只修标点、比喻或局部句子，而要重写整章的功能分布：让动作推进、对话摩擦、物件证据、错误判断、规则代价和沉默反应在全章交替出现，避免整章呈现同一种平滑叙述流。
