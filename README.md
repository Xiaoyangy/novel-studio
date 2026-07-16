# novel-studio：AI 长篇小说自动写作与动态世界推演引擎

[![Release](https://img.shields.io/github/v/release/Xiaoyangy/novel-studio)](https://github.com/Xiaoyangy/novel-studio/releases/latest)
[![License](https://img.shields.io/github/license/Xiaoyangy/novel-studio)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](go.mod)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)](https://github.com/Xiaoyangy/novel-studio/releases/latest)

**novel-studio** 是一个开源、自托管、local-first 的 AI 小说生产系统，面向长篇网文、百万字连载、短篇投稿和整书工程化创作。它把 **动态世界推演、多智能体写作、章节规划与渲染、RAG/Qdrant 长程记忆、质量审核返工、AIGC 门禁、断点恢复和进度看板** 放在同一套可验证 pipeline 中。

它不是“把上一段继续写长”的文本生成器。系统先推进角色和世界，再把主角可见的部分投影成章节计划；正文、评审、返工和交付都必须引用同一组落盘事实与正文指纹。

**Search keywords / 检索关键词**：AI 小说写作、AI 网文生成器、长篇小说自动生成、自动写小说、小说创作 Agent、multi-agent novel writing、LLM writing pipeline、dynamic world simulation、local RAG、Qdrant、self-hosted AI writing、AIGC review、GitHub Release、Release 升级、novel-studio update。

## 最新升级：整章同稿门禁、可执行返工计划与 RAG 实用化（2026-07-16）

本轮升级到 `render_packet v9`，并继续使用 `literary_render_contract` 和题材 `style_contract`。v9 把完整 `preserve_facts`、全部 hard outcomes、金额数量、事实连续性、人物知识边界、授权边界、安全后果与 `forbidden_moves` 原样投影给 Drafter，不再用 TopN、截断或摘要丢掉长尾事实；镜头、物件、笑点、payoff 方向和示例措辞仍是可重排、替换或省略的 soft candidates。整篇检测闭环同时要求：本地 whole-text 风险、DeepSeek 裸正文判定、命名平台、Editor 结论和正式 review 必须绑定同一个 `body_sha256`；未记录或未绑定当前 SHA 的分数只能留作历史信号，不能放行当前正文。

完整事实只在 `working_memory.render_packet` 出现一次。返工源、世界模拟覆盖和正式 plan 继续保留 path、count、SHA 与验证收据，Drafter 可核对来源却不会为同一批事实支付三次上下文成本；世界模拟的主角投影被 packet 吸收后也不再重复下发。引号字形变体按精确 identity 去重，权威 source 原句和顺序优先；金额、否定、因果顺序、状态台账或知识边界变化绝不会模糊合并。青山县第一章真实 formal plan 的 draft profile 已在固定 `64 KiB` 上限内完成 ordered-JSON 实测，无需抬高硬预算或删除独立事实。

整篇单段 AIGC 返工不再允许“挂了资料名、仍由 Drafter 自由发挥”。只要当前章仍有命名外部复测、whole-text/segment 结构阻断或结构重渲染升级，正式计划就必须同时具备完整 `anti_ai_execution_plan` 与有效 `literary_rendering_plan`。前者六个字段缺一不可：`risk_signals`、`counter_moves`、`sentence_rhythm_policy`、`object_response_budget`、`dialogue_function_plan`、`review_checks`；后者除焦点人物、知识边界和叙事距离外，至少要有一个明确的 `scene_mode`（scene / summary / omission / pause）和一个带文学卡或 RAG `source_refs` 的 `active_lens`。plan finalize、Flow 派发、render-only 复用和正文写侧入口使用同一校验，旧空计划不能再凭一枚历史 RAG receipt 进入渲染。

正式正文由独立 `Drafter` 角色渲染，不再让负责世界模拟和 POV 规划的 Writer 顺手续写。Writer / World Simulator 使用 DeepSeek 做长上下文因果推演，Drafter / Editor 使用 Codex 做正文与审校；角色分离不等于自由发挥，Drafter 只能消费冻结后的 v9 packet、有效 craft receipt 和章节指令。anti-AI 计划进入正文侧时只保留定性写法决策与精确的物件/对白功能合同，CV、TTR、百分比和数字化节奏配方不会变成“按指标写小说”的提示。

进入任何 DeepSeek、命名平台或正式 review 之前，pipeline 会先复验 retained draft 的 plan/body causal epoch、rewrite source、brief、canonical state、章节指令与 craft receipt。过期候选会连同旧 parts/plan/simulation 投影原子移入 `meta/quarantine/causal_preflight/`，保留 manifest、路径和 SHA；随后只从当前权威工件重建，旧稿不会被误送检测器。普通续写与返工都先跑标题、字数和 hard-fact anchors；不通过时真实草稿与 checkpoint 均不改变。

`0.7866` 与 `79.33%` 看起来接近，但来源不同。前者是外部平台对一次“整篇作为一个检测片段”的提交给出的 `78.66%`，已绑定旧第一章 SHA `873d2a90...b2c37`，不能归到后续版本；后者是本地 detector 在三条原始概率曲线共同高危、并有独立叙事或结构证据时生成的 whole-text 风险下限。2026-07-15 的写前触发快照中，用户对当时正式字节整篇单段复测得到第一章 `0.86`（SHA `e3b1ef17...31399`）、第二章 `0.83`（SHA `b798734c...c74c`）。这些都是返工触发事件，不是通过证据；正文一变，必须对新 SHA 整篇重测。所有数值都不是“逐段分数平均”或“多少句子是 AI”。

2026-07-16 当前 active run 已生成第一章候选 `drafts/01.draft.md`，完整 SHA-256 为 `c2c1c36243c086a296aec6d8ca5eef7c35e6072f6b33087f1bd87f9307587a0f`。该精确字节在运行时 edit gate 中得到 `raw_local_gate_percent=2.54%`，随后用当前 Python 审计入口独立复算为 `2.75%`，两者都严格 `<4%`；同稿 DeepSeek 裸正文判定为 `human_like / low / 2%`、`blocking=false`。但 `zhuque/novel-whole-text-single-segment` 仍未取得或登记同哈希结果，网页流程停在验证码许可边界。因此它仍是 `rejudge_pending` 候选，不是已通过朱雀的正式章，也不能 commit / deliver；旧正式章的 `0.86` 与第二章的 `0.83` 仍只保留为历史返工触发证据。

Go detector 与 Python 审计脚本现在使用同一套中文小说对白识别口径。明确处于段首台词、说话提示加冒号或人物标签加完整话语位置的 ASCII 双引号中文，会与 `“……”` / `「……」` 一样进入 quoted-Hanzi、对白段占比、连续对白、动作引导、密集窗口和微句号话轮统计，不能再借英文直引号逃过整章检测。正文侧另有 error 级 `ascii_chinese_dialogue_quote`：draft、分片、merge、edit、lint/consistency 与 initial/rewrite commit 都会拒绝明确的 ASCII 中文人物对白；普通英文、配置值、标题、术语和概念引文仍可使用直引号，不会被宽泛误报。

当报告命中 `pov_interiority_thin`、`pov_interiority_low` 或情绪范围过平时，返工反馈不再只说“加一点心理”。它会同时给出对白段占比、主观密度与流程密度，并要求至少重建两条分处不同场景的完整人物链：`刺激 → 主观体验或误判 → 调节/压住/转移 → 因此改变的选择 → 关系或现实余波`；每增加一段主观链，都要删除等量安装、票据、付款说明或非必要对白原话。情绪名词、抬眼/攥手等微动作，以及“他意识到 / 他觉得”单独出现都不算主观因果。

连续 render-only 也不再无限沿用旧 plan。每个命中 whole-text/segment 结构阻断的不同整章哈希会写入幂等的 `draft-structural-block` checkpoint；当前字节仍能由本地确定性门禁复现该结构失败时，先消耗有界整章重渲染预算，不要求把明显失败的中间稿送到注册平台。达到 plan 的 `review_refinement.iteration_limit` 后，Flow Router 自动废弃旧场景/对白投影并强制重做 POV plan；若本章世界推演仍有缺口，则先补齐或重做 `chapter_world_simulation`。相同哈希重试不重复计数，新 plan 或推演 checkpoint 会开启新的迭代边界；所有注册 detector/mode 义务始终保留，并在本地结构门禁干净的最终候选 SHA 上恢复为强制复测。

章节级用户指令现在以全文和 SHA 一起穿过推演、规划与渲染，不再只在外层命令里出现一次。`rerender-request` 保存指令原文和 `instruction_sha256`；当前 `meta/pipeline.json#prompt` 存在时作为 live truth 覆盖旧请求内容，并生成新的 `chapter_pipeline_instruction:sha256:*` source token。`world_simulation`、`planning`、`draft` 三种 context profile 都保留同一硬合同，simulation 必须把精确 token 写入 `sources`，POV plan 必须继承到 `context_sources`；指令/prompt 变化、请求 artifact 偏离 checkpoint、旧 token 仍被引用时都会 fail closed。旧项目仍由最近的 `rerender-request` checkpoint 提供章节作用域，不会把新要求误投到别章。

全角色推演新增 `simulation_character_authority`，与未截断的 `simulation_characters` 名册一一对应。世界推演 profile 以 `layered_v1` 传输：公共 `mode_policies` 只发送一次，每个 entry 再保留该角色当前模式真正需要的字段。权威包只接受角色卡、dossier、cast ledger 中可证实的身份、位置、行动、资源、关系、决策模型、知识与通信边界；占位文字会降为显式 `unknown`，未来 `arc` 不作为当前事实下发。`authoritative` 保留完整当前因果输入；本章可见但档案仍有缺口的角色使用 `rewrite_source_only_contract`；离屏且缺权威状态的角色使用 `hold_baseline_contract`；当前 partial 已有决定则使用 `reuse_saved_decision`。blocking entry 的逐字段 exact contract 始终保留，任何额外职业、地点、关系、资源、通信、动机或未来行动都会在 `simulate_chapter_world` 落盘前被拒绝。该严格 guard 在项目已有 dossier 语料时启用；完全没有 dossier 的旧导入项目保留兼容路径，避免无迁移资料时假装拥有权威状态。

`preserve_facts` 中属于某个角色的“不知道 / 不得知道 / 不能推断 / 不得凭……”条款会独立抽取为 `required_knowledge_boundaries`。这不是 dossier 通用知识说明的摘要，而是一把逐条知识锁：新决定的 `knowledge_boundary` 必须原样包含每条锁定句，不能删除、弱化成“可能知道”，也不能让模型用新的票据、背景音或无关证据洗成已知。它同时进入对应的 `layered_v1` entry 和写入前 validator，即使其他角色字段都完整，知识锁缺一条也不能 staging/finalize。

`rewrite_source_only` 的可见动作由项目从旧正文提取，不再让模型概括。专用句界 tokenizer 识别 `。！？!?`，把连续终止符以及紧随其后的中英文引号、圆/方括号和书名号闭合符保留在同一句中，并在下一句首字符前精确切分；超长原句也整句保留，不再按旧的 rune 上限截断。由此生成的 `rewrite_source_evidence` 与 contract `action` 必须逐字一致，少一个句末引号或把原句扩成动机、婚姻、手机行为、未来影响都会被字段级拒绝。

正文写入现在使用 `draft_write_intent` 关闭“Markdown 已替换、checkpoint 尚未追加”的崩溃窗口；恢复会在调用者同一 Store 实例内重建 draft/AI 指标/结构证据，避免旧 checkpoint cache 再次覆盖已恢复候选。显式重渲染或正式 review 要求换稿时，工具在改正文前先落 `render_only_authorization` 待判合同；新稿无论正常返回还是崩溃恢复，都必须停在 current-hash whole-draft 复判边界，不能因为旧授权已被消费就直接 commit。Drafter StopGuard 只接受本轮新产生的结构 checkpoint 或真实 `rejudge_pending`，不会把旧授权误当作已完成，也不会在工具明确停笔后反向催模型继续修改。

edit 与 commit 也回到同一份精确字节上。`edit_chapter` 先在隔离临时稿计算候选，再以 write intent 原子换入、写 `edit` checkpoint，并立即对编辑后的完整正文重跑本地 whole-text 门禁；新 SHA 会使旧外判与旧 consistency 失效，若仍命中结构阻断则停止继续打补丁，交还 pipeline 做整章重渲染或重规划。`consistency_check` 必须绑定实际 `drafts/NN.draft.md`、当前 digest，且严格晚于本次最新 draft/edit 事件；即使正文回到历史相同 SHA，也不能借用旧 epoch 的检查。pipeline commit 以及显式 rewriting/polishing 在覆盖终稿前还会再对当前精确正文复算本地 AIGC，旧 checkpoint、provider 单独低分或重试次数都不能绕过。

一致性检查会额外落一份 exact-body hard receipt，绑定正文 SHA、plan checkpoint seq+digest 与当前 body checkpoint seq+digest。金额数量、知识边界、正文元数据泄漏或其他 hard violation 存在时，只写 `consistency_check_failed` 诊断，不产生可提交的 `consistency_check`；commit 和崩溃恢复都会重算并要求 `passed=true` 的同稿收据，不能用普通 checkpoint 或历史相同 SHA 冒充。

`PendingCommit` 现在保存的不是一个宽泛“提交到哪了”，而是一份不可变提交身份。首次提交和 rewrite/polish 都会在第一个不可逆写入前记录 `mode`、解析规范化后的 canonical payload 及其 SHA、正文 SHA/字数、plan/body/consistency checkpoint 的 seq+digest、外判正文 SHA 和 strict-AIGC 标记；rewrite 还保留覆盖前终稿字节与 SHA、原 rewrite flow。恢复只消费这份 canonical payload，不采信后来一次工具调用携带的可变参数，并会重新验证当前 plan/body epoch、consistency、外判、字数、吸引力合同和本地 AIGC。身份未变时按 `state_applied → progress_marked → quality_checked → checkpointed → rag_indexed` 从首个缺证阶段幂等重放；任一正文、计划、检查或终稿身份变化都 fail closed。首次提交与返工覆盖共用这套可恢复 saga，rewrite 队列即使已在崩溃前 drain，再次恢复也保持幂等，所有阶段留证后才清除 pending。

hard/soft 分流现在按精确语义匹配，而不是宽泛子串。金额和百分比只在出现具体数值与单位时提升为 hard fact；知识、授权、同意、审批和安全责任只在出现明确边界或后果表述时提升。`元气`、`安全感`、普通的“知道/同意”等词不会因包含局部字面而误判；真正的金额、未经授权、保密边界、触电/事故后果即使被旧计划误放进 `payoff_points` 或 `scene_anchors`，也会自动晋升到事实连续性合同。剩余镜头、笑点、物件和技法继续作为 soft candidates，避免把正文写成逐项打勾的流程记录。

注册平台复测补齐了正式稿与候选稿之间的严格桥接。高分正式章重渲染后，人工网页复测结果可以直接绑定实际 `drafts/NN.draft.md`，但脚本会同时验证 named detector/mode 合同、精确 SHA、已收口 write intent、最新 draft/edit checkpoint、无更新的结构阻断以及真正的 `rejudge_pending`；任意复制品或仍在本地失败预算内的中间稿都会拒绝。候选在所有注册 identity 和 DeepSeek 上同哈希通过后才能 commit，交付再以相同正式 SHA 复核。

阈值不再从历史 artifact 反向读取。DeepSeek 当前哈希必须严格 `<4%`，每个命名 `detector/mode` 也必须各自严格 `<4%`；旧文件中的 `pass_threshold`、`threshold_percent` 或相似字段只做诊断展示，不能把运行门槛抬成 5%、10% 或更高。独立 raw-local gate 也不会因 DeepSeek 低分而被“校准掉”。当同一当前哈希已经取得全部命名平台 `<4%` 结果后，正文进入 named freeze：除非出现新的显式整章重渲染请求或新的确定性 blocking 事件，pipeline 只能继续 commit，不能为了润色再改一个字并作废昂贵复测。

显式 `--force-rerender` 的优先级现在高于旧候选 SHA 的命名平台预检。Host 会在任何 external-gate inspection 之前确认 `rerender-request` 已使当前 draft hash 失效，因此不会要求用户对马上要被替换的旧字节再做一次 Zhuque 等 named detector 复测；原 detector/mode 义务仍持久保留，替换候选一旦通过本地结构门禁，必须在它自己的精确 SHA 上完成同模式复测。这个顺序只消除无意义的旧稿复测，不会删除外部合同或降低 `<4%` 门槛。

多 detector 的检查点采用“历史语义 digest 幂等”，不采用 causal latest-only 语义：同一正文上 Zhuque、另一个平台各自的精确检测行只落一次 `registered-external-detection` checkpoint，原样重跑 review 不会制造 A、B、A、B 的假检测历史；只有 detector/mode/分值/正文 SHA 等语义真正产生新登记行时才新增 checkpoint。plan、draft、edit 等会开启因果轮次的 artifact 仍使用 latest/across-family 规则，因此 A→B→A 会得到新的正文或计划 epoch，两类幂等口径不会互相混淆。

待判状态现在统一 fail closed。pipeline 管理的草稿只要已有精确 draft/edit checkpoint，缺失或过期的 DeepSeek 裸文结果就归为 `rejudge_pending`，不会再退成 `not_required`；blocking 结果缺少完整证据或修改计划时归为 `advice_incomplete`。这两种状态都会暂停继续派发正文并拒绝 edit、commit 和 deliver，consistency 只能报告硬阻断、不能把草稿判成可提交；外部登记日志损坏、分值尺度矛盾或 payload/hash 不一致同样直接报错，不能靠旧缓存放行。

返工简报中的当前 `合同漏项`、`必须修正`、带严格日期的 `最新整篇单段门禁` 和 `验收条件` 会在 plan finalize 时确定性投影到 `causal_simulation.review_refinement`，避免 Planner 上下文压缩后丢失门禁证据。投影只读取这些 H2 下的顶层列表，忽略 H3、代码围栏、`说明：` 摘要和“已解决”历史区段；负向诊断里出现的跨项目禁用词会先脱敏，不会因为“删除旧元素”反而把旧元素重新写进计划并触发污染门禁。

项目级 attraction contract 也已统一到 plan finalize、Flow 和 commit：需求从 `user_rules` 与 `meta/web_reference_brief.md` 共同推导，结构化 `meta/web_reference_brief.json` 负责锚定逐章 trend 候选和系统同伴策略。逐章 trend map 存在时只把已映射候选投到当前章；“不堆网络梗”只是密度上限，不会反向变成每章必须造梗。用户最新规则若另行明确要求本章使用 trend，仍具有最高优先级；缺少本章映射会暴露为配置缺口，而不是允许模型伪造简报来源或静默降级。项目要求 reader entertainment、长篇首章或系统同伴声口时同样必须补齐相应计划；Flow 在派发 Drafter 前调用 `ChapterAttractionPlanReadyForProject` 拦截旧计划，所有 render-only 复用入口也执行同一检查，commit 再通过 `requireChapterAttractionContent` 复核，避免整章写完才发现契约缺失。

项目隔离也从提示约定升级为初始化和审核规则。青山县与《她的第二算法》的 zero-init 历史兼容 profile 分别只在项目名精确等于 `只许把钱花在青山县`、`她的第二算法` 时启用，不再用“县城 / 系统 / 夜市”“澄光 / 男主 / 项目负责人”等题材、公司或角色词猜项目；相似新书不会继承林澈、许闻溪、梁渡等项目专属人物和声口。零章关系对象、review 判断依据和 render outcome 人物优先级都从当前 protagonist、FirstCast、正式 plan、可见 stage、首次登场与对白参与者动态推导。core/important 角色若不属于第一章 FirstCast，会按 `first_mention` 保持 offscreen：只推进自己的生活、职业、资源和关系压力，不预装第一章现场知识、即时情绪、债务、协作或救场资格，直到合法联系或首次入场条件成立。

小说正文新增确定性的 orchestration metadata 泄漏硬门。`simulation_id`、`world_simulation_id`、`source_refs`、`craft_recall_receipt`、`receipt_id`、`render_packet`、`rewrite_source`、`plan_details`、`body_sha256`、`checkpoint`、`sha256` 等精确机器键一旦进入正文，会产生 error 级 `orchestration_metadata_leak`：整章草稿和分片在写前拒绝，机械 lint/consistency 明确阻断，initial、恢复提交与 rewrite commit 也会在终稿写入前复验，不能靠重试上限降级。匹配按完整 key 边界进行；普通小说里的“系统提示：余额不足”面板、自然语言中的系统一词、没有 `sha256` 标签的普通数字，以及 `mycheckpointcafe` 这类包含片段的单词都不会误报。

章节结果现在还会执行高置信数量契约。系统从当前章大纲、rewrite brief、required beats 与 reader payoff 提取“扩到 / 增至 / 达到 / 完成 N”的本章结果下限，再与世界模拟和 POV plan 的最终主角决定中的“维持 N 上限 / 最多 N / 不超过 N”比较；只在同一实体族确定出现 `cap < target` 时阻断。摊位/商户与交易/订单分开计数，中文和阿拉伯数字统一；开场五家随后扩到十家、被明确取消的旧上限、章末下一批 hook 都不会误当终态。该检查同时位于 world simulation finalize、plan finalize、Flow readiness 和 render-only 复用入口，旧 plan 不能靠直接派 Drafter 绕过。

正文写入、分片合并、edit、consistency 与 commit 还共享同一组 hard-fact anchors：金额、距离、碗/摊/套/桌等实体数量、序数和 `少糖` 会从当前正式 plan 确定性抽取并绑定候选字节。中文、全角、阿拉伯数字和带千位分隔符的等值写法互通；`一百万到账`、`一百万专项经营额度`、`100万专项额度` 与 `1,000,000元` 只在明确货币语境中等价，裸“一百万”、不同金额、否定或尚未成立的事实不能冒充完成。

RAG 不再只证明“调用过”。项目事实候选进入 Top6 前会规范化绝对/相对路径、折叠近重复，并以确定性 MMR 平衡相关性、来源和内容差异；`outline`、`layered_outline`、accepted outline 作为同一权威族最多占一槽，低于最高相关度 30% 的弱材料不会为了凑六条被硬塞。Markdown 中内嵌图片、压缩包等长 base64 二进制会在切块阶段解码判别并丢弃，不再让数万垃圾块膨胀 BM25/Qdrant 候选。返工章还会根据当前同哈希 review、AI voice 和机械 gate 自动推导最多两个 methodology / dialogue / scene craft need，生成绑定 generation、正文 SHA、brief SHA、当前索引、触发证据、命中 chunk 与 payload hash 的持久 receipt；缺素材显式记录 `no_material`。只要 need 有命中，Planner 就必须引用该 need 实际返回的至少一个完整精确 `hit.ref`，空 `source_refs`、截断 ref、跨 need 借 ref 或只写 receipt 名都会 fail closed。命中手法必须先转成本章动作才能 finalize；Drafter 只得到最多两条 `craft_methods`，不会看到或复制原 benchmark 正文。索引、正文、brief 或触发证据变化时，Flow、draft context、draft tool 和 render-only 都会拒绝旧 receipt 并退回重规划。

planning、world 与 draft context 现在按阶段做“先验证、后折叠”的分层压缩。World Simulation 的预算保持 `96 KiB`：未完成或 invalid 状态保留完整 `layered_v1` 全名册、gaps 和每个 blocking entry 的 exact contract，同时删除笨重 full dossiers、重复写法资料和旧渲染快照；ready 或 `ready_to_finalize` 后则把全量 authority 折叠为 `simulation_authority_receipt`，禁止模型重发。Planning 首选 `64 KiB`：正式 simulation 的离屏决定留在磁盘，只交付精确 `protagonist_projection`、逐条 `preserve_facts`、coverage count+SHA、结构化 rewrite brief、RAG receipt 以及 `finalized_and_source_bound` 权威收据；旧正文和完整 brief 仍由 path+SHA 可寻址，不在每轮重复注入。Draft 同样固定 `64 KiB`，但完整事实只留在 v9 packet，source、coverage、formal plan 与已吸收的 projection 改用收据引用，不截断事实来换预算。

预算判断使用应用 profile、分层压缩、关键字段排序之后的最终 ordered JSON 字节，不用“重复字符样本大概能装下”代替真实形状验证。回归用 19 名角色、混合 authoritative/hold/rewrite-only entry、invalid world gaps 和重型 ready planning payload 验证：world 最终不超过 `96 KiB`，正常 planning 不超过 `64 KiB`。青山县第一章 active restart 的 exact ordered JSON 实测为 `97,377 / 98,304` 字节；上下文仍完整保留 19 人名册、16 条逐项 `preserve_facts`、章节指令全文及其 source token，以及每个 blocking entry 的 exact contract，没有靠删角色、删事实或抬预算过门。只有受保护返工 planning 在裁掉镜像、宽快照和低优先级资料后仍无法收敛时，才允许使用 `96 KiB` 硬上限，并写出 `_context_budget=rewrite_critical_overflow`；超过硬上限会明确失败，不会静默删除当前任务。结构重规划时，已耗尽的旧正式 simulation 只暴露 ID、base tick、角色数和 source 身份；无因果内容空壳可在第一批有效提交时受控重建，但任何已有决定、事实覆盖或实质投影都绝不清空。Drafter 仍使用 `64 KiB` 精简 render packet。

落盘后的 authority 与 projection 也不是一次通过永久有效。incoming batch 在保存前先校验 exact authority contract、`required_knowledge_boundaries` 和 preserve/projection invariants；正式 `chapter_world_simulation` 每次复用时还会重新执行当前版本的 authority guard、主角决定一致性、禁止 hidden-pressure 回流、pipeline instruction token 与数量结果检查。旧构建保存的 `rewrite_source_only` 动作若缺句末引号，或旧 projection 违背当前 preserve facts，会重新变成 gap，不能借历史 `SimulationID` 进入 Planner。只有复验通过且 source-bound 的正式推演才会向 planning 暴露 `simulation_authority_receipt.validation=finalized_and_source_bound`。

2026-07-16 的青山县项目维护快照共读取 `882` 个来源文件，把 RAG 从历史混杂索引重建为 `15,893` 个有效 chunk，长 base64/编码二进制命中为 `0`；其中 `899` 个需要语义召回的 project-safe point 使用 `qwen3-embedding-0.6b`（1024 维）进入 Qdrant，其余 `14,994` 个 design chunk 保留词法检索，pending upsert 为 `0`，第一章 probe 能返回 `6` 条去重结果。plan finalize 对每个实际命中的 craft need 机械要求至少一个完整精确的 `hit.ref`，不是只检查“存在 RAG receipt”；自动返工仍只能消费结构化安全方法卡，不能看到原 benchmark 正文。这里记录的是本轮实测配置，不把 0.6B 写死成全局唯一模型；运行时仍允许按项目配置其他 embedding。

本轮维护构建产物的最终 SHA-256 为 `ebd462efc7701d5f285eb959fcadb9ba892c535350bef42ec13c3058ccfaea96`。它只标识完成验证的新二进制快照，不表示已经替换、部署或热加载到任何当前仍在运行的 pipeline 进程；长任务仍以其启动时加载的二进制为准。

完整文学合同、上下文压缩、整章 detector 设计和 2026-07-14 验证矩阵见 [README-20260714.md](README-20260714.md)；本文以下章节记录当前稳定运行口径。第一、二章旧分数只以带日期和 SHA 的“返工触发快照”出现；第一章新候选虽已通过本地 raw 与 DeepSeek 同稿门禁，仍不得在朱雀同哈希结果登记前表述为正式通过。

## 效果图

![novel-studio AI 小说创作进度看板总览：pipeline、章节审核、RAG、模型用量和运行队列](docs/assets/dashboard-overview-20260710.png)

*总览：主线下一章、实际工作章、pipeline 阶段、评审门禁、RAG、模型用量和运行队列统一展示。*

![novel-studio 人物页签：角色档案、OCEAN 画像、目标压力、知识边界和关系契约](docs/assets/dashboard-characters-20260710.png)

*人物：角色档案、OCEAN 画像、目标与压力、知识边界、关系契约和长期弧线一屏可查。*

![novel-studio 离屏世界页签：动态世界推演、势力进度钟、社会情绪和角色独立行动](docs/assets/dashboard-offscreen-20260710.png)

*离屏世界：世界推演游标、角色独立行动、势力进度钟、社会情绪和信息传播持续推进。*

## 核心能力

| 能力 | 当前实现 |
|---|---|
| 一句话开书 | `--pipeline --new-novel` 先做 brainstorm，再生成 foundation、zero-init 资产并进入写作 |
| 动态世界 | 每章先运行全角色世界模拟；完整名册由 `simulation_character_authority` 逐人约束，未知状态只能引用返工原文或冻结基线，不能由模型补猜 |
| 主视角边界 | `protagonist_projection` 只暴露主角可见事实，hidden / delayed 信息不能提前进入正文 |
| 规划与正文隔离 | World Simulator、Planner、Drafter 使用独立 prompt、工具集和上下文预算 |
| 章节指令同源 | 当前用户指令以全文、SHA 和 source token 同时进入 simulation、plan 与 draft；任一阶段引用旧 token 都会失效 |
| 题材文风合同 | 项目 style 与 `user_rules` 确定性选择 genre profile；合同随 render packet 进入正文，未命中时不注入题材假设 |
| 候选选优 | 正常草稿基于同一冻结计划并发生成 3 个候选，确定性初筛后可交给异模型 Reviewer 二选一 |
| 质量闭环 | 机械规则、本地 AIGC、Editor、异模型裸正文判定和统一报告共同决定 accept / warning / blocking |
| 可执行 AIGC 返工 | whole-text 结构返工必须先补齐六字段 anti-AI 计划，以及至少一个 scene mode 和一个有来源 active lens，再允许渲染 |
| 主观因果返工 | 低主观/高流程报告要求至少两条不同场景的完整人物因果链，并以等量删除流程说明或非必要对白换取篇幅 |
| 结构失败升级 | 同一 plan 下不同正文哈希连续命中 whole-text/segment 阻断时，按 `iteration_limit` 使旧 plan 失效；推演有缺口时先补推演，再重规划 |
| 章节结果契约 | 大纲/brief 的本章数量下限与 simulation/plan 最终硬上限按实体族比较，确定的 `cap < target` 在正文生成前阻断 |
| RAG 实用化 | 项目事实 Top6 去重/MMR；返工技法召回以可审计 receipt 进入 plan 和精简 `craft_methods`，缺料显式可见 |
| 项目隔离 | 青山县与《她的第二算法》zero-init 兼容分支按精确项目名启用；人物优先级与延迟入场由当前 protagonist、FirstCast、plan 和 first mention 推导 |
| 正文元数据隔离 | `simulation_id`、`source_refs`、`body_sha256`/`sha256` 标签、receipt、checkpoint 等内部键进入小说正文时确定性硬阻断，普通剧情系统面板不误报 |
| 中文对白格式 | ASCII 中文人物对白与全角引号对白使用同一统计口径，并由 `ascii_chinese_dialogue_quote` 在所有写入/提交路径硬阻断；普通术语引文不误报 |
| 可恢复提交 | initial 与 rewrite/polish 都按 state applied → progress marked → quality checked → checkpointed → RAG indexed 分阶段恢复，不会用半完成状态冒充交付 |
| 长程记忆 | 项目事实 RAG、写法库、对标库和审核校准库分通道路由，支持 embedding、Qdrant、本地向量与 BM25 |
| 运行保护 | 阶段证据指纹、正文 SHA-256、审核缓存、返工来源绑定、预算保险丝和无进展熔断 |
| 可观测性 | 浏览器看板只读聚合全部书目，交叉核对正文、进度、评审、RAG、检查点与运行事件 |

## 快速开始

### 1. 安装

稳定版本从 [GitHub Releases](https://github.com/Xiaoyangy/novel-studio/releases/latest) 下载。Release 使用 GoReleaser 构建 macOS、Linux、Windows 的 x86_64 / arm64 包并附带 SHA-256 checksums。仓库 `main` 可能包含尚未进入最新 Release 的变更。

~~~bash
# macOS / Linux：安装最新 Release
curl -fsSL https://raw.githubusercontent.com/Xiaoyangy/novel-studio/main/scripts/install.sh | sh

# 安装指定版本
curl -fsSL https://raw.githubusercontent.com/Xiaoyangy/novel-studio/main/scripts/install.sh | sh -s -- v0.2.0

# 安装到用户目录；环境变量必须传给管道右侧的 sh
curl -fsSL https://raw.githubusercontent.com/Xiaoyangy/novel-studio/main/scripts/install.sh |
  NOVEL_STUDIO_INSTALL_DIR="$HOME/.local/bin" sh
~~~

Windows 用户从 [Releases](https://github.com/Xiaoyangy/novel-studio/releases) 下载对应 ZIP，解压后将 `novel-studio.exe` 放入 `PATH`。

~~~bash
# 从源码构建
git clone https://github.com/Xiaoyangy/novel-studio.git
cd novel-studio
go build -o novel-studio ./cmd/novel-studio

# Docker：仓库提供 Dockerfile 和本地 compose 配置
docker compose build novel-studio
docker compose run --rm novel-studio --version
~~~

### 2. 首次配置与连通性检查

首次直接运行会启动配置引导；后续无参数运行只打印用法。

~~~bash
novel-studio
novel-studio --check
~~~

配置默认读取 `~/.novel-studio/config.json`，项目目录中的 `./.novel-studio/config.json` 可覆盖全局配置。完整示例见 [config.example.jsonc](config.example.jsonc)。

### 3. 启动完整 pipeline

~~~bash
# 从一句话想法新建小说：brainstorm + 默认完整阶段
novel-studio --pipeline --new-novel \
  --prompt "一个返乡程序员得到只能投资家乡的系统，从夜市改造开始重建县城"

# 更适合长期维护：把完整创作契约写入文件
novel-studio --pipeline --new-novel --prompt-file prompt.md

# 先写到第 10 章暂停
novel-studio --pipeline --prompt-file prompt.md --write-to 10

# 中断后在同一项目目录重跑同一命令，按证据续跑
novel-studio --pipeline --prompt-file prompt.md
~~~

默认阶段是：

~~~text
architect -> zero-init -> write -> review -> rewrite -> deliver
~~~

`--new-novel` 会在此之前增加 brainstorm；`cocreate` 是可选阶段，不在默认阶段列表中。

### 4. 打开进度看板

~~~bash
novel-studio service open
novel-studio service status
~~~

默认地址是 [http://127.0.0.1:8765/](http://127.0.0.1:8765/)。pipeline 会尝试复用或后台启动看板；看板启动失败不会中断正文任务。

## 当前系统架构

~~~mermaid
flowchart TD
    CLI["CLI / Pipeline Stage Runner<br/>参数路由、阶段恢复、证据核验"] --> BS["Brainstorm<br/>新书输入哈希与 kickoff journal"]
    CLI --> HOST["Host<br/>项目生命周期、预算、事件、恢复"]
    HOST --> FLOW["Flow Router + Dispatcher<br/>从 Store 推导下一任务<br/>无进展熔断"]
    FLOW --> CO["Coordinator<br/>接收事实化指令并调度子代理"]

    CO --> AR["Architect<br/>foundation、卷弧、大纲、世界 tick"]
    CO --> WS["World Simulator<br/>全角色单世界推进"]
    CO --> PL["Planner<br/>POV structure + details"]
    CO --> DR["Drafter / Finalizer<br/>冻结 render packet 写正文"]
    CO --> ED["Editor<br/>叙事与契约评审"]

    CLI --> REV["Review Engine<br/>同一正文冻结快照"]
    REV --> EBR["Editor branch"]
    REV --> RBR["Reviewer branch<br/>异模型裸正文判定"]

    AR --> TOOLS["Tools<br/>唯一事实写入口"]
    WS --> TOOLS
    PL --> TOOLS
    DR --> TOOLS
    ED --> TOOLS
    EBR --> TOOLS
    RBR --> TOOLS

    TOOLS --> STORE["Store + Domain<br/>原子文件、Progress、Checkpoint、台账"]
    STORE --> RAG["RAG<br/>BM25 + embedding + local vectors + Qdrant"]
    STORE --> QA["Quality<br/>rules + AIGC + review reports"]
    STORE -.-> DASH["Dashboard<br/>只读扫描 data/runs"]
~~~

### 分层职责

| 层 | 目录 | 职责 |
|---|---|---|
| CLI / 阶段编排 | `cmd/novel-studio/` | 命令路由、pipeline 阶段、review/rewrite/deliver、Release 更新和看板控制 |
| Host / Flow | `internal/host/` | 启动与恢复、事件和预算、根据持久化事实生成下一条指令、重复任务熔断 |
| Agent 装配 | `internal/agents/` | Coordinator、Architect、World Simulator、Planner、Drafter、Editor 的模型、prompt、工具和上下文配置 |
| 工具契约 | `internal/tools/` | 规划、推演、写作、校验、提交、评审和 RAG 写入的前置条件与原子语义 |
| 事实模型 | `internal/domain/` | 章节、世界模拟、角色、计划、审核、pipeline、提交阶段和账本 schema |
| 持久化 | `internal/store/` | 原子文件、progress、checkpoints、signals、RAG、角色与世界状态 |
| 召回 | `internal/rag/` | BM25、embedding、本地 GGUF、Qdrant、向量 fallback、facet/stage 路由和重试 |
| 质量 | `internal/rules/`、`internal/aigc/`、`internal/reviewreport/` | 机械 lint、AI 痕迹信号、门禁合并和统一报告 |
| 可导出能力 | `skills/` | 外部 Agent 使用的单一 skill 源目录；写作型入口统一转入 pipeline |
| 看板 | `services/dashboard/` | Python 标准库只读服务，展示全部小说工程的实时状态 |

## Pipeline 执行模型

### 阶段级生命周期

| 阶段 | 主要工作 | 完成证据 |
|---|---|---|
| `cocreate` | 可选的多轮需求澄清 | 定稿创作指令 |
| `architect` | premise、角色、世界规则、分层大纲和写前 foundation | `meta/architect_readiness.*` 与 foundation 指纹 |
| `zero-init` | 第 0 章世界、初始日程、关系/资源/伏笔台账、第一章准备度 | `meta/first_chapter_generation_readiness.*` |
| `write` | 每章世界推演、POV 计划、候选正文、校验和提交 | 正文、commit checkpoint、Progress 与提交 saga |
| `review` | 本地机械/AIGC + Editor + 异模型 Reviewer | 全部报告绑定当前 `body_sha256` |
| `rewrite` | 只处理当前审核认定的 blocking 正文 | 来源 hash、rewrite brief、备份、新正文和复审 |
| `deliver` | 对账最终正文、审核、台账、RAG 与快照 | 非空交付日志、RAG ready、delivery snapshot |

`meta/pipeline.json` 是阶段索引，不是可以手工勾选的完成事实。每个阶段写入 artifact SHA-256；恢复和最终交付都会重新核验。创作指令、模型选择或 prompt 协议指纹变化时，旧完成图会按依赖关系失效。

### 单章关键路径

~~~mermaid
flowchart LR
    S["World Simulator<br/>全部实名角色分批决策"] --> P["Planner<br/>structure + 3 组 details"]
    P --> RP["Frozen Render Packet<br/>只保留正文所需事实"]
    RP --> C["3 个草稿候选并发生成<br/>rewrite 只生成 1 个"]
    C --> PICK["确定性粗糙度初筛<br/>可选 Reviewer pairwise 终选"]
    PICK --> CHECK["draft -> edit -> consistency"]
    CHECK --> COMMIT["Commit Saga<br/>state_applied -> quality_checked<br/>-> checkpointed -> rag_indexed"]
    COMMIT --> REVIEW["Editor || Reviewer<br/>冻结正文并行审核 + 分支缓存"]
    REVIEW --> G{"当前正文是否 blocking"}
    G -->|是| RW["绑定 body hash 的 rewrite"]
    RW --> REVIEW
    G -->|否| NEXT["accept / deliver / 下一章"]
~~~

世界模拟、计划 partial、Progress、Checkpoint、正文换入、review ledger 和 RAG 写入保持单写者顺序。并发只用于同一冻结输入上的草稿候选或独立审核分支，不能用通用并发工具同时修改同一本书的事实线。

## 最新架构基线

当前主干已把长时间空转、上下文放大、同文复评和半提交恢复纳入代码级控制：

| 变化 | 当前行为 |
|---|---|
| 独立 Planner | 新增专用 Planner prompt；全量规划规则不再塞给 Drafter |
| 分阶段上下文 | Planner / Drafter 首选 `64 KiB`，World Simulator 为 `96 KiB`；压缩后按最终 ordered JSON 的真实字节数复验。仅受保护的返工 planning 可在可观测标记下使用 `96 KiB` 硬上限，继续超限则 fail closed |
| 世界模拟收敛 | 实名角色名册不截断，每批最多 8 个；`layered_v1` 将公共 mode policy 提升一次，再按全角色 entry 保留当前因果输入或逐字段 exact blocking contract，已有 partial 决定不可覆盖 |
| 阶段化 authority 投影 | 未完成/invalid world context 保留全名册、gaps 和 exact contracts，但移除 full dossiers 与重复资料；ready 后折叠成 authority receipt，planning 只携带主角投影、事实锁、coverage SHA 与 source-bound 收据 |
| 角色知识独立锁 | `preserve_facts` 中逐角色的未知/不得推断条款进入 `required_knowledge_boundaries`；决定的 `knowledge_boundary` 必须逐条原样包含，不能靠补造证据绕过 |
| 持久化推演复验 | 已落盘 formal simulation 每次复用都按当前 authority/projection guard 复验；旧版 rewrite-only 句界、知识锁、主角投影或 source token 不合格时重新成为 gap |
| 计划收敛 | `plan_structure` 后按因果基础、声口/娱乐性、读者合同 3 组 details 收口 |
| 正文 render packet | Drafter 读取专门的精简渲染包，不再直接吞下整份大型计划对象 |
| 冻结输入并发 | 正常草稿 3 候选并发；Editor 与 Reviewer 对同一正文并发，结果串行落盘 |
| 审核缓存 | cache key 绑定正文、模型、prompt、上下文和审核协议；失败重跑只补缺失分支 |
| 同稿 AIGC 门禁 | 本地 report、外部裸文判定、Editor 和正式 review 都绑定当前正文 SHA；换稿后先进入 `rejudge_pending` |
| 对白识别同口径 | Go/Python 对 ASCII 与中文全角人物对白采用同一统计；明确 ASCII 中文台词在 draft、part、merge、edit、lint/consistency 和 commit 全路径 fail closed |
| 返工计划硬门 | 命名外部复测或 whole-text 结构返工要求六字段 `anti_ai_execution_plan`，并要求有效 `literary_rendering_plan` 至少包含一个 scene mode 和一个有来源 active lens |
| 主观因果反馈 | 低主观报告给出对白段、主观与流程密度；至少要求两条跨场景完整人物链，并删除等量流程/对白原话 |
| Render-only 升级 | whole-text 结构失败按不同正文 digest 记 checkpoint；耗尽迭代预算后使旧 plan 失效，并在需要时先补世界推演再重做 POV plan |
| 指令身份传播 | `rerender-request` 保存指令全文+SHA；live pipeline prompt 生成新 token，simulation、plan、draft 必须连续绑定，artifact 漂移与旧 token 都拒绝 |
| 精确正文 epoch | draft/edit、consistency 与 commit 都绑定当前 artifact、digest 和事件顺序；历史相同 SHA 的旧检查不能跨 epoch 复用 |
| Hard/soft 精准分流 | 精确金额、百分比、知识/授权边界与安全后果自动晋升 hard；镜头、物件、笑点、措辞和技法保持可替换 soft candidates |
| 提交 saga 身份 | `PendingCommit` 固定 canonical payload、正文 SHA、plan/body/consistency epoch 与外判正文 SHA；initial/rewrite 都从缺证阶段幂等恢复 |
| 提交 saga 顺序 | `state_applied`、`progress_marked`、`quality_checked`、`checkpointed`、`rag_indexed` 分阶段记录，全部留证后才清 pending |
| Detector 历史 | 注册平台行按历史语义 digest 幂等；同一组多平台结果重复复审不增 checkpoint，新登记结果才追加 |
| 正文元数据门禁 | 精确 orchestration/RAG/checkpoint 键在 draft、part、lint、consistency 和所有 commit 路径 fail closed |
| 返工恢复 | 正文换入后即使进程中断，也能从 rewrite source 和当前 hash 继续复审 |
| 无进展熔断 | 同一任务在 checkpoint / simulation partial / plan partial 都不变时，第 3 次派发中止 |
| 有界运行 | Planner、Simulator、Drafter、Finalizer 使用独立 turn 上限；rewrite 尝试共享章节总 deadline |
| 写作规则前移 | 项目级 attraction contract 由 `user_rules` 与 `web_reference_brief` 共同决定；plan finalize、Flow 写前路由和 commit 使用同一需求口径 |
| 数量结果前移 | 大纲/brief 的数量目标与 simulation/plan 的最终硬上限不一致时，在 World Simulator、Planner 或 Router 阶段直接退回 |
| Craft 回执 | 自动召回绑定 generation、正文/brief SHA、索引和触发项；Planner 必须消费精确 hit ref 并转成当前场景动作，新返工 plan、draft context、draft tool 与 render-only 共同复验 |

详细性能与恢复依据见 [Pipeline 性能与恢复审计](docs/pipeline-performance-audit-20260711.md) 和 [Pipeline Recovery Audit](docs/pipeline-recovery-audit-20260710.md)。

## 动态世界与主视角边界

novel-studio 把“世界发生了什么”和“正文能写什么”拆成两层：

1. `simulation_characters` 给出当前章必须覆盖的完整实名名册；`simulation_character_authority.format=layered_v1` 按相同顺序逐人给出 entry，并把四种 mode policy 提升为一份公共定义。预算压缩只移除 full dossiers 和重复资料，不截断后半名册。
2. `simulate_chapter_world` 只根据这些权威目标、压力、资源、知识边界和误判给出候选、决定、理由、行动耗时、完成度与蝴蝶效应。`authoritative` 可正常推进；`rewrite_source_only`、`hold_baseline` 必须逐字段精确满足 packet 中的固定 contract；`reuse_saved_decision` 不得重发。
3. `preserve_facts` 中逐角色的未知/不得推断条款被单独固定为 `required_knowledge_boundaries`。rewrite-only 的可见动作则由精确句界 tokenizer 从旧正文提取，连续句末标点与其后的闭合引号/括号不会被切掉，长句也不截断；决定必须原样复用知识锁和 `rewrite_source_evidence`。
4. `chapter_pipeline_instruction` 是本章最高优先级硬合同。simulation 的 `sources` 和 POV plan 的 `context_sources` 必须连续绑定当前 source token，不能在上下文切换时丢掉用户最新先后、资源或知识边界要求。
5. 模拟生成唯一 `simulation_id` 和 `protagonist_projection`。POV plan 必须引用它，不能另造一套世界事实；正式 ready 后只向 planning 投影主角视角、逐条 preserve facts、coverage 身份和 `finalized_and_source_bound` authority receipt，不重复注入离屏决定正文。
6. `hidden` / `delayed` 决策只约束后续世界反馈；除非沿合法传播路径越过信息地平线，否则旁白和主角心理都不能提前知道。缺少 dossier/current state 的离屏角色以 `unknown` 和 `transmission_blocked` 留在基线，不允许模型发明外地工作、借车、联系或救场。
7. formal simulation 不是一次校验永久有效。每次复用都会按当前版本重新检查全角色 authority、知识锁、preserve/projection 不变量、主角决定、隐藏压力隔离、指令 token 和数量结果；旧 artifact 不合格时恢复为 gap，不能凭历史 `simulation_id` 进入 Planner。
8. `commit_chapter` 把实际发生的角色状态、知识、关系、资源、时间线和世界增量回写事实层，下一章模拟从这些结果继续。
9. 弧 / 卷边界由 `save_world_tick` 推进离屏日程、社会情绪、仪式和势力进度钟；世界不会在主角离场后暂停。

这条链保证跨章节因果只有一条来源：

~~~text
世界事实 -> 全角色决策 -> 主角投影 -> POV 计划 -> 正文 -> 提交回写 -> 新世界事实
~~~

## 写作与质量合同

| 合同 | 约束 |
|---|---|
| 章节字数 | `user_rules.chapter_words` 是硬门禁；超出后保留草稿，只允许局部 `edit_chapter`，不能反复整章重抽 |
| 项目吸引力 | 项目要求 trend / entertainment 时，plan 必须包含 active `trend_language_plan` 和完整 `reader_entertainment_plan`；长篇第一章与系统同伴声口按项目条件追加硬约束 |
| 开篇吸引力 | 轻松/爽文项目在 plan 中明确前 200 字冲突、不同机制笑点、即时兑现和流程压缩 |
| 长篇第一章 | `longform_opening` 必须说明开篇钩子、连载发动机、长线承诺、解释预算和第一章页面证据 |
| 热梗与流行语 | 必须来自项目简报并绑定角色、场景和使用预算；它是可选风格素材，不会为了过门禁强塞进每章 |
| 系统/同伴声口 | 若用户定义其会交流、吐槽和支持主角，anti-AI 规则不能把它改写成冷硬菜单机器 |
| 计划范围 | `required_beats` 必须发生，`forbidden_moves` 绝不能发生；计划外重大情节必须退回 Planner |
| 数量结果 | “扩到十家”与“维持五家上限”不能同时成立；摊位和订单分族核验，未来 hook 与已废弃旧上限不算本章终态 |
| 角色知识锁 | `preserve_facts` 中属于角色的“不知道 / 不得知道 / 不能推断 / 不得凭……”条款独立进入 `required_knowledge_boundaries`；决定必须在 `knowledge_boundary` 中逐条原样保留，不能摘要、弱化或用新造证据洗白 |
| 返工原文证据 | `rewrite_source_only` 动作只能逐字引用 tokenizer 提取的当前原文句；句末连续终止符及闭合引号/括号属于证据，超长原句也不截断，任何增补职业、关系、资源、动机或未来行动都会拒绝 |
| 正文渲染 | Render packet 只给 Drafter 页面所需事实；流程说明、离屏心理和 latent context 不得整段搬进正文 |
| AIGC 返工计划 | 当前 whole-text/segment 返工必须补齐六字段 `anti_ai_execution_plan` 与有效 `literary_rendering_plan`；只写资料名或空结构不能进入 Drafter |
| Hard / soft 边界 | `required_beats`、精确数额、知识/授权边界和安全后果是 hard；镜头、物件、笑点、payoff 实现与 craft move 是 soft，可按整章效果重排、替换或省略 |
| 内部元数据 | 小说正文禁止出现 `simulation_id`、`source_refs`、`receipt_id`、`body_sha256`、`checkpoint` 等机器键；自然语言系统面板与普通数字不受影响 |
| 题材声口 | `style_contract` 只约束已命中项目的语域、口述气口、喜剧、成长、关系边界和系统声口；用户最新规则始终优先 |
| 对白与分段 | 禁止“人物：台词”剧本格式、同腔解释对白、连续孤句和无功能文字墙；同章 3 个以上话轮反复用 2—4 汉字句号碎断会触发 `dialogue_micro_period_chain`；人物对白必须使用 `“……”` 或 `「……」`，明确 ASCII 中文台词触发 error 级硬门 |
| 主观人物链 | 低主观/高流程返工至少重建两条分处不同场景的 `刺激 → 主观体验/误判 → 调节 → 改变选择 → 现实余波`，情绪词、微动作和“他意识到”本身不算完成 |
| AIGC / AI 腔 | 机械规则、片段信号、本地 detector、Editor 和异模型判定分层合并；整章单片段 hard gate 不会被低 blended 平均值稀释，Go/Python 对对白引号与整章统计保持一致 |
| 异模型裸文判定 | DeepSeek 裸文判定只有在 advice 完整、非 blocking 且 `body_sha256` 等于当前正文时才参与校准；缺失、过期或建议不完整都 fail closed，带分数的结果仍须严格 `<4%`，`4%` 本身不通过 |
| 注册平台检测 | 人工登记事件按 `detector + mode` 独立追踪；正式稿 payload 必须与 `chapters/NN.md` 精确同字节，受控复测桥只接受实际 `drafts/NN.draft.md` 候选及其精确 checkpoint。每个仍在生效的身份都须严格 `<4%`，一个低分不能掩盖另一个当前高分 |
| 审核新鲜度 | review、AI gate、草稿外判和正式终稿必须绑定当前正文 SHA-256；旧报告不能放行新正文 |
| 返工事实保护 | 预算未耗尽时，render-only 复用当前 plan/simulation；耗尽后只保留既定事实结果与 rewrite source，废弃旧场景/对白投影并重做 plan |

评审维度不仅看语法，还覆盖 plot、character、continuity、pacing、worldbuilding、style/aesthetic、contract 和 ai_voice_detection。外部 Reviewer 的建议会经过本书设定、系统人格和热梗计划过滤，不能用通用审稿偏好推翻用户硬规则。

### 整篇作为单段时，AIGC 值如何解释

检测口径看的是“这次提交给 detector 的输入”，不是 Markdown 里有几个自然段。对 1800—3600 个可见字符的小说章，本地分片代理直接把去标题后的完整正文作为一个 segment，保留自然段和换行；1800—3600 的长度与覆盖率则按去空白后的可见字符计算。短章不超过 5000 汉字时，统一门禁也不会用多片段 `blended_aigc_percent` 稀释整章风险。

| 值或证据 | 含义 | 能否直接放行当前正文 |
|---|---|---|
| 外部值 `0.7866` | 那次提交把整篇作为一个检测片段，平台返回 `78.66%`；记录绑定旧第一章 SHA `873d2a90...b2c37` | 不能放行后续正文；只能作为该旧稿的历史触发信号 |
| 写前值 `0.86 / 0.83` | 2026-07-15 用户对当时第一、二章正式字节做整篇单段检测的结果；分别绑定 `e3b1ef17...31399`、`b798734c...c74c` | 不能；两章都必须返工，换稿后按新 SHA 重测 |
| 本地 whole-text `79.33%` | 三条原始曲线与独立结构证据形成的风险下限；不是疑似句子占比 | 不能；它是 `whole_text_or_segment_risk` 硬阻断 |
| 本地 `blended≈10%`（历史示例） | 多组件或多片段融合诊断值；随正文变化，不是稳定常量 | 短章 whole-text hard gate 存在时不能覆盖 `79.33%` |
| 同哈希外判 `<4%` | 当前正文的 provider-backed 裸文判断 | 只有没有任何 corroboration blocker 时才可参与校准 |
| 同哈希外判 `4%` | 达到而非低于严格阈值 | 不通过；门禁要求严格 `<4%` |

本地 whole-text 下限只在以下条件同时成立时生成：正文按一个 segment 覆盖至少 95%，概率曲率、弱语言模型一致性和局部熵三条原始曲线均不低于 90，并且叙事动力、结构指纹或“突发性 + 跨段一致性”提供独立支持。下限近似为：

~~~text
whole_text_floor = clamp(60 + mean(raw_curve_1, raw_curve_2, raw_curve_3) * 0.20, 76, 86)
~~~

`79.33%` 那次的三条 raw curve 是 `100 / 100 / 90`：均值为 `96.67`，代入后得到 `60 + 96.67 × 0.20 = 79.33`，且未触碰 76—86 的截断边界；独立叙事/结构证据负责确认这个 floor 可以成为硬门禁，不再额外加分。`0.7866` 则只是外部平台对旧 SHA `873d2a90...b2c37` 的整篇提交返回的 0—1 归一化概率，换算为 `78.66%`；它不能归属后续正文，也不参与这条本地公式。

如果只有三条共享底层统计的曲线偏高、没有独立结构证据，该信号保留为诊断，不形成 hard gate。反过来，只要 corroboration blocker 仍在——包括 `whole_text_or_segment_risk`、内容完整性、`legacy_consensus_high`、确定性文本规则，或未经 Editor 明确解除的 structural warning——即使同哈希外判很低，也只能记录外判证据，不能把本地结构阻断降成 warning。

草稿外判是单次授权状态机：

1. 当前哈希被外判 blocking，或本地整章机械门禁写入建议完整的 `full_rerender_required` requirement 后，状态为 `rerender_authorized`，允许一次完整 `draft_chapter(mode=write)`。
2. 显式 `rerender-request` 一旦作废旧候选 SHA，Host 会先跳过该旧字节的 named detector/DeepSeek 预检，再进入换稿；detector/mode 合同不会被删除，而是等待替换候选自己的精确 SHA。
3. 新正文产生不同 SHA 后通常进入 `rejudge_pending`；若该精确新哈希仍被当前本地 whole-text/segment 硬门禁独立复现，则状态保持单次 `rerender_authorized`，先做有界整章重渲染或因果重规划，不把已知失败的中间稿送平台。该例外不能清除任何注册 detector/mode 义务，也不能授权局部编辑或提交。
4. 本地结构门禁不再阻断后，当前候选立即恢复 `rejudge_pending`；新哈希外判严格 `<4%` 且 `blocking=false` 时，外判状态才进入 `approved`。`full_rerender_required` marker 作为 detector/mode 复测合同和审计证据持续保留，不以一次低分删除；它只在当前 SHA 拥有全部同平台通过事件时批准，正文再次变化会自动回到 `rejudge_pending`。
5. `approved` 只表示 current-hash 外判通过，不等于章节最终放行；统一门禁还要求不存在任何 corroboration blocker。需要用人工叙事证据校准本地代理时，还必须有强 narrative human anchor，并由 Editor 精确解除相应 warning。
6. Editor、AI gate 或外判 artifact 只要 SHA 不等于当前正文，就属于 stale evidence，不能参与 accept、commit 或 deliver。

## RAG 与长程记忆

### 召回分层

| 通道 | 内容 | 使用边界 |
|---|---|---|
| 项目事实 | 当前书的 prompt、brainstorm、foundation、台账、摘要和已提交事实 | Architect、Planner、Writer、Review 的连续性依据 |
| Craft | 写法技巧与拆文方法 | 只迁移手法，不成为世界事实 |
| Benchmark | 对标素材的结构、节奏和场景方法 | 禁止复制原文、角色和专有设定 |
| Calibration | 审核样本、规则命中和 detector 校准 | 只服务 review / rewrite 判断 |

项目事实索引默认只扫描当前项目的安全来源，并跳过章节原文库、拆文库、对标库、历史输出和其他书目目录。外部资料必须通过明确的 `--add-source` 或配置通道加入；设计素材和项目事实不会混成一条无来源记忆。

### 项目事实 Top6 的去重与多样性

Hybrid recall 可以先收集多于六条候选，但交给 Writer 的 Top6 不是简单按分数截断：

- 项目事实的向量查询会在 TopK 之前排除 craft、benchmark 与 calibration 等 design-only source kind：Qdrant 使用服务端 `must_not`，本地向量也先过滤再排序，避免方法素材先占满候选、事后过滤却来不及补回事实。
- `output/novel/...` 的绝对路径和相对路径先归一，避免同一文件伪装成两个来源。
- outline、layered outline、accepted outline 合并为一个权威来源族，最多占一槽；当前章大纲已经由 working memory 单独注入，不需要在 RAG 中重复三遍。
- 中文 BM25 指纹相似度达到近重复阈值的 chunk 会被抑制；同一来源真正不同的连续性事实仍可在候选不足时回填。
- 确定性 MMR 同时考虑原始相关度、来源差异和内容差异；分数并列时按规范化来源、chunk ID 和 hash 稳定排序。
- 候选必须达到最高相关度的 30%，宁可少于六条，也不引入弱相关材料制造“用了 RAG”的假象。
- 上下文仍超预算时，Top6 先压到最高相关的 1 条并保留该事实，再裁剪可由当前 plan 重建的宽快照；`_trimmed` 会记录 `rag_recall:top1_preserved`。如果关键任务连同这 1 条仍放不下，工具显式失败，不会让 probe 显示命中而 Drafter 实际收到空 RAG。

### 返工技法 RAG receipt

返工 craft recall 与常规项目事实 recall 分层运行。引擎只从当前返工源同哈希的 review、AI voice 和机械 gate 推导需要，最多选择 methodology、dialogue、scene 中两类；自动通道只接受 plan usage stage、合法 kind/path、安全目录和有实际词法重叠的材料，不使用零分 fallback。自动正文修复的安全语料根仅限 `review-calibration/novel-craft-methodology/` 与 `writing-techniques/novel-craft-methodology/`；更宽的 benchmark、外貌词库、古代资料和法术素材即使伪造 facet 也不能进入。每次结果先写入 `meta/rag/craft_receipts/*.json` 和幂等 `craft_recall_log.jsonl`，再允许 staged plan 建立。

设计索引 schema v4 会把这些专用目录中的旧 design chunk 迁移为 `summary_origin=derived_method_metadata` 的结构化“安全方法卡”。卡片只使用受控词表，包含 `机制 / 适用 / 动作 / 避免 / 验收`，原文仅用于选择技法，不会把原句、人名、标题或路径带给自动返工；人工标记为 `summary_origin=curated_method` 的卡保持原样。`raw_prefix` 或没有可信 provenance 的摘要一律拒绝，安全检索只用方法摘要、类目和 facet 排序；BM25 前既按主机制+主动作折叠近义变体，也要求主技法匹配当前 dialogue / scene / methodology need，次级标签不能把章末钩子伪装成对白方法。Top3 因而提供不同且用途相符的可执行操作。常规 schema 迁移只重算专用安全目录中的方法 chunks；语义 hash 未变化的 benchmark 和项目事实可复用已有向量。需要清除历史二进制污染时则显式重建索引，不能把“可复用”误解成继续保留垃圾块。

有命中的 need 必须由 Planner 写成当前人物、物件和冲突可执行的 `external_reference_plan`，记录精确 hit ref、转化规则和禁用项；`no_material=true` 只留审计，不要求为了形式硬塞素材。单发 `plan_chapter` 不能绕过新返工 preflight，legacy partial 可在 `plan_details` 恢复 receipt。receipt 的索引身份由当前安全方法语料逐 chunk 重新计算，增量写入后即使旧 sanitization marker 尚未刷新也不能复用旧回执。最终 render packet 仅投影 Planner 已转化的 moves / rule / avoid 和来源引用，不携带原始样例文本或摘要。

### 后端与恢复

- 词法召回使用 BM25。
- 语义召回可使用 OpenAI-compatible embedding、本地 GGUF embedding 或其他配置模型。
- Qdrant 是语义主后端；`meta/rag/vector_store.json` 保留本地向量 fallback。
- Qdrant 错误或空结果时依次降级到本地向量和 BM25，不让一次后端故障清空上下文。
- RAG schema hash 覆盖 context、summary、keywords、text 和 metadata；只有语义内容变化才重嵌入。
- 增量写入失败会保存 `pending_upserts.json`，下次启动或 `--rag-ready` 自动回放。
- Qdrant 丢库时可从本地向量恢复，不必重新计算全部 embedding。
- 升级到 4B embedding 不会自动解决重复来源和错误消费。默认先用上述去重/MMR与 receipt 闭环；如果仍需升级，优先把 4B 用作 reranker 或 query rewrite 做同一评测集 A/B，再决定是否替换当前 embedding。Writer 仍由写作模型负责，RAG 模型不替代正文模型。

~~~bash
# 构建当前项目事实索引
novel-studio --build-rag --dir output/novel

# 同时生成 embedding、写本地向量并同步 Qdrant
novel-studio --build-rag --dir output/novel --with-embeddings

# 在默认项目来源上显式追加写法资料
novel-studio --build-rag --dir output/novel \
  --add-source deconstruction-library/writing-techniques

# 只迁移 schema、回放 pending、复用向量并恢复 Qdrant；不启动写作
novel-studio --rag-ready --dir output/novel

# 离线验证 RAG/embedding/vector store 工件
novel-studio eval inspect --cases evals/cases/harness
~~~

## 恢复、返工与证据

- `meta/checkpoints.jsonl` 是 step 级只追加日志。注册 detector 等不可变证据按历史 `scope + step + semantic digest` 幂等；plan/draft/edit 等因果 artifact 只把当前 latest 或指定 causal family 的紧邻同稿视为幂等，因而 A→B→A 会留下新的 A epoch。
- `meta/pipeline.json` 保存阶段状态、输入指纹、artifact digest 和 evidence，但完成状态会在恢复时重新验证。
- `chapter_pipeline_instruction` 通过最近的 `rerender-request` checkpoint 锚定章节作用域，同时校验指令全文与 SHA；live `meta/pipeline.json#prompt` 变化会生成新 source token，使旧 simulation/plan 自动过期。world simulation、planning、draft profile 都保留该合同，不能在 agent 切换时只剩摘要。
- `PendingCommit` 固定 `initial/rewrite` mode、canonical parsed payload 及其 SHA、正文 SHA/字数、plan/body/consistency checkpoint seq+digest、外判正文 SHA 和 strict-AIGC 标记；rewrite 另存覆盖前终稿字节/SHA 与 flow。恢复忽略后来调用的新参数，先复验全部可变门禁和精确 epoch，再按 `state_applied → progress_marked → quality_checked → checkpointed → rag_indexed` 补齐；任何身份变化都拒绝恢复。
- `PendingRewrites` 是返工队列；存在返工时，Flow Router 优先处理队首章节，不继续写新章。
- `draft-structural-block` 只记录当前因果边界之后、命中 whole-text/segment 硬阻断的不同正文；checkpoint digest 绑定正文 SHA 与 causal epoch，同周期同稿重复检查不计次，同一旧稿在真正的新 plan 下再次失败则会在新周期计一次。
- render-only 次数优先读取 `causal_simulation.review_refinement.iteration_limit`，默认 3，并限制在 2—4；达到上限后，旧 plan 不再具备继续渲染资格。
- rewrite brief 的当前顶层修正项在 plan finalize 时确定性写入 `review_refinement`；嵌套证据、代码块、说明和已解决区段不会造成计划膨胀，跨项目负向诊断会先脱敏。
- rewrite craft receipt 先于 staged plan 落盘；索引或正文/brief/触发证据改变会清理旧 token/ref，并在 Flow readiness 阶段退回 Writer。缺索引时也写入 `no_material`，不会静默退成模型自由发挥。
- staged `plan_details` 对 receipt 语境中常见的 `need_id / source_ref / craft_recall_receipt` 别名做确定性归一，分别落到正式 `query_or_need / source_refs / craft_recall`；旧 receipt 清理使用归一后的精确 ref，避免 partial 宽松接收、finalize 却反复报告“need 未消费”。
- `*.plan.partial.json` 与 `chapter_simulations/*.partial.json` 是各自阶段唯一的未完成事实；存在 partial 时，Host 和正文工具都不会拿旧 formal artifact 冒充 ready。正式 plan 必须有同路径、同 SHA-256 的最新 `plan` checkpoint，不能借用旧 checkpoint 建立新 render epoch。
- `simulation_character_authority` 以 `layered_v1` 始终覆盖未截断 required roster：公共 mode policy 只出现一次，每个角色 entry 只带当前模式需要的权威字段或 exact blocking contract，并把已落盘 partial 标为 `reuse_saved_decision`。项目已有 dossier 体系后，档案占位或缺失的离屏角色只能提交逐字段固定的 `hold_baseline` 哨兵；本章可见角色只能逐字引用 tokenizer 提取的 `rewrite_source_evidence`。任何叙事性补猜都会作为 tool precondition 拒绝，而不是写进 partial 后再靠 Planner 修。
- world/planning context 会在应用阶段 profile、分层压缩和关键字段排序后，对最终 ordered JSON 做真实字节复验。invalid world 仍保留全名册、gaps 与 exact contracts，并受 `96 KiB` 硬门；正常 planning 只保留 projection、逐条事实锁、coverage SHA、结构化 brief、RAG/source-bound receipts，并受 `64 KiB` 硬门。只有标记为 `rewrite_critical_overflow` 的受保护返工可上浮到 `96 KiB`，继续超限会显式失败。
- formal `chapter_world_simulation` 每次恢复/复用都会按当前版本重新校验 authority、`required_knowledge_boundaries`、rewrite-only 精确句界、主角投影、preserve facts、hidden-pressure 隔离、pipeline instruction 和数量结果。旧 artifact 若只满足旧 guard 会重新报告 gap；只有复验通过且 source-bound 的结果才向 Planner 暴露 `simulation_authority_receipt.validation=finalized_and_source_bound`。
- 世界推演与正式审稿都使用“当前因果轮次”checkpoint，而不是从全历史中复用相同 digest。即使强制重推演得到相同 `SimulationID`，只要新的 `chapter_world_simulation` checkpoint 晚于 plan，Flow、render-only 和正文写工具都会要求 Planner 重新 finalize；相同字节的 plan 会在新 simulation/review/structural-block 之后建立新的 plan epoch，避免成功返回后仍永久 not-ready。
- review 同样按 registered external detection、simulation、plan、正文交换、commit 与结构阻断组成的因果族去重；同一份 review 出现在新正文轮次之后会取得新序号，恢复逻辑不会把历史复审误当当前证据。
- `registered-external-detection` 对每个 detector/mode 的精确登记行使用历史 digest 幂等；同一正文的多平台结果原样复审不会重复追加，分值、正文 SHA 或其他登记语义变化时才形成新证据。
- draft、edit、正常 commit 与 rewrite commit 必须位于当前 plan 或最新 `rerender-request` 之后，并把当前 artifact 路径和 SHA-256 绑定到最新正文 checkpoint；`draft → edit → draft` 即使回到历史同一哈希也会建立新 epoch。append 不能给旧 plan 正文补一个很小的尾巴伪装重渲染，分片草稿则要求每个 part 的路径、摘要和 checkpoint seq 都属于当前 plan epoch，合并时再次逐片复核。
- `consistency_check` 也必须精确指向当前 draft，且严格晚于当前 body checkpoint；历史同哈希检查不能跨 plan/body epoch 复用。文件写入成功但 checkpoint 追加失败时，Host、显式重渲染状态和 commit 都会因摘要不匹配而拒绝继续。
- review cache 与正式 review artifact 分离。缓存可以避免同文重复调用，正式报告仍按当前正文和项目规则落盘。
- `--restart` 清空 pipeline 阶段索引，不会把缺失的正文事实凭空补出来；通常先运行 `--diag` 判断是否真要重启。
- 不要手工把阶段改成 `completed`，也不要删除 `.pre-rewrite.md`、rewrite source 或 checkpoint 来“解锁”流程。

| Checkpoint / 边界 | 恢复语义 |
|---|---|
| `meta/pending_commit.json` | 在任何不可逆提交写入前冻结 canonical payload 和精确正文/epoch 身份；后续调用参数不能改写当前 saga |
| `rerender-request` | 授权复用现有因果 plan 做一次显式整章重渲染，并保存章节指令全文+SHA；它不是 causal budget 重置边界。它先使旧 draft SHA 失效、跳过旧字节的命名平台预检，但保留 detector/mode 义务给替换候选 |
| `draft` | 记录正文候选 digest；存在新稿而外判仍指向旧 SHA 时，恢复到 `rejudge_pending`，不会继续写第二稿 |
| `draft-structural-block` | 记录一个不同正文哈希的 whole-text/segment 结构失败；正文 SHA + causal epoch 幂等去重 |
| `registered-external-detection` | 每个精确 detector/mode 登记结果历史幂等；多 detector 可并列存在，不把重复 review 当作新平台事件 |
| `chapter_world_simulation` / `plan` / `review` / `commit` | 新推演、新计划、当前复审或已提交正文成为新的因果边界；即使摘要回到历史值也按相邻 causal family 开启新 epoch |
| `state_applied` → `progress_marked` → `quality_checked` → `checkpointed` → `rag_indexed` | initial 与 rewrite/polish 提交 saga；恢复时从第一个缺证步骤继续，rewrite 队列 drain 可重放，全部留证后才清 pending |

当不同正文哈希的 `draft-structural-block` 数达到 `iteration_limit`，Host 会把 `plan_ready` 强制置为 false。若 `ChapterWorldSimulationStatus` 表明推演是 required 且尚未 ready，Flow Router 先派 World Simulator 补齐或重做推演；否则直接派 Writer/Planner，在保留结果事实的前提下废弃旧场景/对白投影并重做 POV plan，之后才允许 Drafter 生成新正文。旧项目如果没有该新 step，系统只有在“当前哈希的 `full_rerender_required` marker 完整 + 本地再次复现 whole-text 阻断”时，才会用边界后的不同 `draft` digest 做兼容升级；该 marker 的来源也可能是 `local_mechanical_gate`，不会仅凭历史草稿数量误判。

常用恢复命令：

~~~bash
novel-studio --diag
novel-studio --architect-check --dir output/novel
novel-studio --zero-init --check --dir output/novel
novel-studio --rag-ready --dir output/novel
novel-studio --pipeline --prompt-file prompt.md
~~~

### 推荐运行与验收链

日常恢复优先重跑原 pipeline 命令，让系统按 checkpoint 推导下一步。只有需要定位或重跑指定阶段时，才使用下面的窄范围命令；同一本书已经有活跃 pipeline PID 时，不要再启动第二条写作链。

~~~bash
# 1. 先验证模型与本地依赖，再做只读诊断
novel-studio --check
(cd data/runs/<书名> && novel-studio --diag)

# 2. 对指定章节重建 current-hash review 证据
novel-studio --pipeline --dir data/runs/<书名> \
  --stages review --restart --from <N> --to <N>

# 3a. 仅在首次显式授权复用现有 plan 重渲染整章时使用一次 --force-rerender
novel-studio --pipeline --dir data/runs/<书名> \
  --stages rewrite --restart --force-rerender \
  --from <N> --to <N> --max-rewrite-rounds 3

# 3b. 已存在 pending、失败历史或卡住循环时沿用原边界；范围必须从队首 pending 开始
novel-studio --pipeline --dir data/runs/<书名> \
  --stages rewrite \
  --from <队首N> --to <目标N> --max-rewrite-rounds 3

# 4. 返工完成后重新生成同稿 review；指定章节全部证据通过后再交付
novel-studio --pipeline --dir data/runs/<书名> \
  --stages review --restart --from <N> --to <N>
novel-studio --pipeline --dir data/runs/<书名> \
  --stages deliver --restart --from <N> --to <N>
~~~

`--force-rerender` 会写入新的 `rerender-request`，保存当前章节指令原文与 SHA，但它只是复用现有 plan 的授权，不会清空 structural attempts；只有新的 `chapter_world_simulation`、`plan` 或 `commit` 才能开启新的因果预算。同一旧 plan 已耗尽时再次 force 会被直接拒绝。该请求会先作废旧候选的预检资格，避免对马上替换的旧 SHA 重做 named detector；外部 identity 仍保留，最终候选必须同模式复测。已有 pending 或失败历史时应使用上面的 3b 命令原位续跑，不加 `--restart`，且 `--from` 不能跳过 `meta/progress.json.pending_rewrites[0]`。连续不同哈希达到 `iteration_limit` 后，系统会使旧 plan 失效；推演有缺口时先补推演，再重规划。其他命令中的 `--restart` 只重置所选 pipeline 阶段索引，不删除章节、review、checkpoint 或提交 saga。

交付前至少核对以下证据链：

1. 若存在注册平台复测合同，`meta/external_detection_log.jsonl` 中每个 detector/mode 都必须对当前 `chapters/NN.md` 精确 SHA 有严格 `<4%` 的后续事件；残留 draft 的低分不能替正式终稿放行。
2. `chapters/NN.md`、`reviews/NN_ai_gate.json.body_sha256`、`reviews/NN_deepseek_ai_judge.json.body_sha256`、`reviews/NN.json.body_sha256` 和统一 Markdown 报告引用同一正文，DeepSeek 裸正文判定也严格 `<4%` 且 `blocking=false`。
3. `reviews/NN.json.verdict=accept`，`meta/progress.json.pending_rewrites` 不再包含该章。`reviews/drafts/NN_full_rerender_required.json` 应保留为长期复测合同，但其全部 identity 对当前正式正文的状态必须是 `approved`；仅让 marker 指向旧 SHA 或只复测另一个平台都不能验收。
4. `meta/checkpoints.jsonl` 已记录对应 commit saga 和 review 证据；`meta/pipeline.json` 的 completed/evidence 经恢复核验，而不是手工标记。
5. RAG pending 已回放、delivery snapshot 非空，相关 Go 测试、文档链接和 `git diff --check` 通过。

## 进度看板

`services/dashboard/` 使用 Python 标准库启动只读服务，默认扫描仓库 `data/runs/` 下的全部书目；`NOVEL_STUDIO_RUNS_DIR` 可覆盖扫描根目录。页面交叉核对 `progress.json`、正文目录、章节字数、评审正文 hash、返工队列、检查点、RAG 和运行事件，而不是只相信一个进度数字。

详情页包括：

- **总览**：主线下一章、实际工作章、实时步骤、资产覆盖、RAG、成本与数据一致性
- **设定**：premise、世界地图、地点与路线、势力进度钟、世界规则和时间线
- **人物**：分层画像、目标/压力/情绪、知识账本、能力边界、群众名册和关系契约
- **成长轨迹**：人物生命线、阶段事实、长弧规划和决策流
- **计划**：卷弧骨架、章节细纲、下一章计划、伏笔和时间线
- **离屏世界**：tick、独立日程、社会情绪、信息传播、进度钟和事件流
- **质量**：逐章评审、AI gate、本地 AIGC、异模型判定、版本新鲜度和写作指标
- **运行**：模型选择、reasoning effort、事件队列、近期错误和日志

~~~bash
novel-studio service start
novel-studio service status
novel-studio service open
novel-studio service url

# 直接启动服务
python3 services/dashboard/server.py --host 127.0.0.1 --port 8765
~~~

主要 API：

| 端点 | 内容 |
|---|---|
| `/api/health` | 轻量健康检查 |
| `/api/novels` | 全部书目摘要 |
| `/api/novels/<书名>` | 项目总览 |
| `/api/novels/<书名>/setting` | 设定与世界规则 |
| `/api/novels/<书名>/cast` | 人物与关系 |
| `/api/novels/<书名>/growth` | 成长轨迹 |
| `/api/novels/<书名>/plan` | 卷弧与章节计划 |
| `/api/novels/<书名>/offscreen` | 离屏世界 |
| `/api/novels/<书名>/quality` | 审核与质量 |

## 命令速查

| 命令 | 用途 |
|---|---|
| `novel-studio --pipeline --prompt-file prompt.md` | 运行或恢复默认完整 pipeline |
| `novel-studio --pipeline --new-novel --prompt "..."` | 从一句话 idea 新建书目 |
| `novel-studio --pipeline --dir data/runs/<书名>` | 从已存在书目的持久化证据恢复 |
| `novel-studio --pipeline --stages review --from 1 --to 10` | 只评审指定章节 |
| `novel-studio --pipeline --stages rewrite --max-rewrite-rounds 3` | 按 blocking 反馈返工并复审 |
| `novel-studio --cocreate` | 多轮澄清创作需求 |
| `novel-studio --check` | 验证 provider、model 与 fallback 连通性 |
| `novel-studio --architect-check --dir output/novel` | 核验 foundation 并生成 readiness |
| `novel-studio --zero-init --dir output/novel` | 生成第一章前置推演资产 |
| `novel-studio --rag-ready --dir output/novel` | 单独恢复/验证 RAG |
| `novel-studio --build-rag --dir output/novel` | 重建项目 RAG |
| `novel-studio --diag` | 只读诊断并导出脱敏报告 |
| `novel-studio --refresh-progress --dir output/novel` | 回填推进、人物变化和下一章台账 |
| `novel-studio --writing-assets list` | 查看写法资产 |
| `novel-studio --simulate` / `--import-sim` | 合成或导入仿写画像 |
| `novel-studio --steer "指令"` | 给下一次恢复排队干预 |
| `novel-studio list` | 查看 `data/runs/` 下全部书目 |
| `novel-studio reader-metrics log ...` | 登记真实读者反馈用于校准 |
| `novel-studio eval inspect ...` | 离线检查项目工件与 RAG |
| `novel-studio skills list` | 列出内置 skills |
| `novel-studio skills export --to <dir>` | 导出 skills |
| `novel-studio update [version]` | 更新到 latest 或指定 Release |
| `novel-studio --version` | 查看版本、commit 和构建时间 |

完整参数以本机二进制为准：

~~~bash
novel-studio --help
novel-studio --pipeline --help
novel-studio --build-rag --help
novel-studio --zero-init --help
novel-studio service --help
novel-studio skills --help
~~~

## 配置

novel-studio 支持 OpenAI、Anthropic、Gemini、OpenRouter、DeepSeek、Qwen、GLM、Grok、MiniMax、Ollama、Bedrock 和兼容代理，也支持通过本机 Codex CLI 接入订阅能力。模型调用是否离线取决于你的 provider；只有使用本地生成模型和本地 embedding 时，正文处理才可完全离线。

配置重点：

- `providers`：凭证、协议、base URL、模型列表和 provider 额外请求参数
- `roles`：`coordinator`、`architect`、`writer`、`editor`、`reviewer` 的主模型、fallback 和 reasoning effort
- `context_window` / `context_windows`：真实模型窗口和压缩阈值来源
- `rag.embedding`：远程 embedding 或本地 GGUF embedding
- `rag.qdrant`：URL、collection、本地进程或 Docker 自动启动
- `rag.craft_library` / `benchmark_library` / `calibration_library`：设计与审核素材的隔离通道
- `budget`：单书 USD 告警和硬停止
- `notify`：桌面或自定义通知
- `aigc.real_lm`：外部 detector 的观测/融合配置

不要提交真实 API key。项目级 `.novel-studio/config.json` 会覆盖全局值；若切换默认 provider，必须同时提供同名的 `providers.<name>` 配置。

## 输出结构

新书通常落在 `data/runs/<书名>/`；直接在普通项目目录运行时，默认输出根是 `output/novel/`。

~~~text
data/runs/<书名>/
├── prompt.md
├── brainstorm.md
└── output/novel/
    ├── premise.md                  # 故事前提
    ├── characters.json             # 核心人物档案
    ├── outline.json                # 章节大纲
    ├── layered_outline.json        # 长篇卷弧结构
    ├── world_rules.json            # 世界规则
    ├── book_world.json             # 地点、路线与势力图谱
    ├── chapters/                   # 已提交正文
    ├── drafts/                     # structure、plan details、草稿和 partial
    ├── reviews/                    # Editor、AI gate、Reviewer 与统一报告
    ├── summaries/                  # 章、弧、卷摘要
    └── meta/
        ├── progress.json
        ├── checkpoints.jsonl
        ├── pipeline.json
        ├── architect_readiness.*
        ├── first_chapter_generation_readiness.*
        ├── chapter_simulations/
        ├── character_stage/
        ├── side_character_journeys/
        ├── chapter_world_deltas/
        ├── rag/
        └── delivery_snapshots/
~~~

所有运行事实都应通过工具或维护命令更新。不要把聊天历史当作项目真相，也不要靠手工编辑 `progress.json` 修复章节状态。

## Skills

`skills/` 是可导出能力的唯一源目录。`story-long-write`、`story-short-write`、`story-douban-long-write` 等写作方法 skill 负责整理输入和方法约束，正文生成必须回到 `novel-studio --pipeline`，避免旁路写作绕开世界模拟、字数合同和审核门禁。

~~~bash
novel-studio skills list
novel-studio skills export --to ./exported-skills
python3 scripts/validate_skill_context.py
~~~

## 开发与验证

要求 Go 1.25；看板要求 Python 3；embedding/Qdrant 按配置选用。

~~~bash
go test -count=1 ./...
go test -race -count=1 \
  ./internal/writer/sampler \
  ./cmd/novel-studio \
  ./internal/tools \
  ./internal/host/flow
go vet ./...
go build -o /tmp/novel-studio ./cmd/novel-studio
python3 scripts/validate_skill_context.py
python3 -m unittest discover -s quality/audit/scripts -p 'test_*.py' -v
python3 -m unittest services.dashboard.test_server -v
python3 -m py_compile \
  services/dashboard/server.py \
  services/dashboard/test_server.py \
  quality/audit/scripts/aigc_value.py \
  quality/audit/scripts/content_lint.py
git diff --check
~~~

本轮 main 候选验证矩阵：

| 范围 | 结果 |
|---|---|
| Go 全量回归 | `go test ./... -count=1` 通过 |
| Go 静态检查 | `go vet ./...` 通过 |
| AIGC / 外部登记 Python 回归 | `42 / 42` 通过 |
| Dashboard Python 回归 | `4 / 4` 通过 |
| Skill context manifests | `20 / 20` 通过 |
| 维护构建快照 | 最终 `cmd/novel-studio` 构建 SHA-256：`ebd462efc7701d5f285eb959fcadb9ba892c535350bef42ec13c3058ccfaea96` |
| 格式与差异 | `gofmt` 与 `git diff --check` 通过 |

以上 `42 + 4` 即 46 项 Python 单元测试，避免把审核脚本和看板测试混成一个不可追踪总数。二进制 SHA 只用于标识本轮维护构建，不表示当前运行中的 pipeline 已经部署或切换到它；摘要只在最终构建后回填，不能把旧运行二进制的值冒充当前源码产物。该矩阵证明项目优化代码与文档基线可合并。第一章当前候选 SHA `c2c1c362...587a0f` 已取得运行时本地 raw `2.54%`（当前 Python 入口复算 `2.75%`）与同稿 DeepSeek `2%`，但朱雀同哈希复测仍待验证码许可，因此不能声称第一章正式通过；第二章 `0.83` 仍是旧正式字节的外部高分返工状态，也必须在后续新候选上重新闭环。

## 文档索引

| 文档 | 内容 |
|---|---|
| [系统架构](docs/architecture.md) | Host、Coordinator、Tools、Store、上下文与 Agent 拓扑 |
| [项目结构](docs/project-structure.md) | 顶层目录与资料归属 |
| [能力清单](docs/capability-inventory.md) | CLI、Agent、Store、RAG、质量和服务能力盘点 |
| [设计阶段工作流](docs/design-stage-workflow.md) | 长短篇规划与写前设计口径 |
| [上下文管理](docs/context-management.md) | 压缩、store summary 和恢复包 |
| [数据生命周期](docs/data-lifecycle-and-progression.md) | 章节、角色、世界与推进台账 |
| [写作审核工作流](docs/writing-review-workflow.md) | 写作、评审、返工与交付 |
| [外部检测同稿协议](docs/external-detector-protocol.md) | 整篇单段 payload、SHA 登记、多平台复测与交付边界 |
| [用户规则运行时](docs/user-rules-runtime.md) | 用户硬规则如何进入工具门禁 |
| [评测系统](docs/evaluation-system.md) | Harness、指标和回归验证 |
| [可观测性](docs/observability.md) | 事件、usage、trace 和诊断 |
| [Pipeline 性能审计](docs/pipeline-performance-audit-20260711.md) | 最新并发边界、缓存、熔断和恢复设计 |
| [Pipeline Recovery Audit](docs/pipeline-recovery-audit-20260710.md) | 全角色模拟、RAG 恢复和返工链问题清单 |
| [工程交付记录](docs/engineering-delivery-20260708.md) | DeepSeek 复审、AIGC 门禁和历史交付证据 |
| [拆文库规范](deconstruction-library/README.md) | RAG 设计素材与拆解工作区约定 |
| [架构总览 HTML](docs/architecture-overview.html) | 浏览器可视化架构说明 |

核心运行时使用 Go 1.25、[agentcore](https://github.com/voocel/agentcore) 和仓内维护的 [litellm fork](third_party/litellm/README.md)；浏览器看板使用 Python 3 标准库；Qdrant 与 llama.cpp/GGUF embedding 均为可配置组件。

## Release 升级

~~~bash
novel-studio --version
novel-studio update
novel-studio update v0.2.0
~~~

长任务运行中不要替换二进制。等待当前 checkpoint 落盘并退出后再升级；升级完成后先运行 `novel-studio --check`，再从原项目目录恢复 pipeline。

## License

[Apache License 2.0](LICENSE)

## 联系

问题反馈优先提交到 [GitHub Issues](https://github.com/Xiaoyangy/novel-studio/issues)。

<img src="docs/assets/wechat-qr.jpg" alt="novel-studio 项目联系二维码" width="180">
