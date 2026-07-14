# 文学渲染协议：叙事学与小说技法的可执行边界

本参考把英文叙事学、认知叙事研究和大学写作资源转成中文小说渲染约束。它用于规划、起草、重写与审阅，不是评分公式：优先检查信息权限、因果矛盾和证据缺口；叙事距离、场景比例、母题、句法、自由间接话语与潜台词只能作为软诊断，不得设置固定次数、固定比例或统一句长。

每个方法卡都有稳定 `card_id`。计划、推演、审阅和最终 prose 审计统一使用 `literary-rendering#<card_id>` 写入 `source_refs`，不要引用可能随措辞变化的中文标题。

## 中文转译边界（全卡通用）

汉语话题可以用名词、代词或零形式延续成话题链，句子是否自然也常依赖语篇话题、语境和语用功能。因此不能照搬英语“每句显式主语”、英语句型比例或词数阈值；判断视角与衔接时，应同时检查话题持续、零回指、承前连接和上下文可恢复性。连接词的功能还随连接范围与语体变化，不能只按词表计数。

来源：湖南大学汉语语篇与句法讲座纪要 https://wxy.hnu.edu.cn/info/1042/5830.htm ；上海交通大学主办期刊《当代外语研究》话题链研究 https://www.qk.sjtu.edu.cn/cfls/CN/10.3969/j.issn.1674-8921.2015.12.014 ；北京大学出版社《现代汉语连词的语篇连接功能研究》成果页 https://hanyu.pku.edu.cn/xzky/kycg/50176hyxycs366393.htm

## 1. 焦点化：先确定谁能知道什么

`card_id: focalization-boundary`

将“谁在叙述”与“谁在感知、知道、评价”分开。第一或第三人称是叙述形式，内焦点、外焦点或全知才决定信息权限。

执行时至少明确：

- 当前焦点人物；
- 可用信息：亲历、已知往事、当下感官与有依据的推断；
- 禁用信息：未表达的他人思想、人物尚未获知的幕后事实；
- 视角切换位置与提示。

未声明的跨脑读取、知识泄漏可作为硬错误；人物特有措辞、意识形态或感知强弱只能结合上下文判断。第一人称可以回顾并评论过去，第三人称也可以严格限知，不能按人称机械推断焦点化。

来源：汉堡大学《Living Handbook of Narratology》“Perspective – Point of View”：https://www-archiv.fdm.uni-hamburg.de/lhn/node/26.html

## 2. 叙事距离：让远近服务当前功能

`card_id: psychic-distance`

远距适合跨时段背景、群体变化和必要概括；近距适合决定、误判、受伤、发现等需要读者进入人物经验的时刻。近距可使用人物特有称呼、身体感觉、即时注意和被情绪染色的观察；远距则扩大时间与空间范围。

距离变化要有功能：进入关键场面时拉近，跨越重复过程时拉远。突然跳变可能令人脱离，也可能被故意用于惊吓、反讽或疏离，因此只能报告“跳变位置与缺少过渡证据”，不能直接判错，更不能要求全章始终近距。

来源：内华达大学里诺分校写作与演讲中心“Psychic Distance in Creative Writing”：https://www.unr.edu/writing-speaking-center/writing-speaking-resources/psychic-distance-in-creative-writing

## 3. Scene 与 Summary：按叙事价值分配篇幅

`card_id: scene-summary`

Scene 让事件近实时展开，适合重要互动、冲突、决定、揭示与不可逆后果；Summary 压缩时间，适合背景、重复日常、等待、训练过程和过桥信息。场景不是动作清单：结束时至少应有信息、目标、权力、关系或代价的一项变化。

不要把“show, don't tell”绝对化。把通勤、付款、洗漱等全部实景化会制造臃肿；把重大选择只概括为结果又会抽走人物经验。审阅时应问“这一刻为何值得占用实时篇幅”，而不是计算 scene/summary 比例。

来源：

- Purdue Writing Lab，“Summary vs. Scene”：https://owl.purdue.edu/owl/resources/writing_tutors/tutoring_creative_writing_students/documents/working-with-creative-writing-students-09152025.pdf
- The Center for Fiction，“Scenes & Summary”：https://centerforfiction.org/writing-tools/scenes-summary/

## 4. 目标、障碍与转折：维护读者可追踪的因果线

`card_id: goal-causality`

每个关键场面应能回答：人物此刻要什么，什么阻止他，他采取何种策略，结果改变了什么，下一步压力从何而来。转折应由既有行动、信息或关系以“可能或必要”的方式生出，不应只是时间上随后发生的随机刺激。

人物换目标时，正文要给出触发或重新评估的证据；旧目标若仍有效，应说明它与新目标的层级或冲突。目标—尝试—结果可以写入规划台账，但不能要求所有抒情、氛围、荒诞或实验性段落都有显性任务句。

来源：

- Linderholm 等，“Suppression of Story Character Goals During Reading”：https://pmc.ncbi.nlm.nih.gov/articles/PMC4266429/
- Walsh 等，“Stories in Action”：https://journals.sagepub.com/doi/10.1177/15291006231161337
- Aristotle，《Poetics》关于 reversal 与因果连续性：https://classics.mit.edu/Aristotle/poetics.1.1.html

## 5. 情绪评价：情绪必须改变注意或行动

`card_id: emotion-appraisal`

优先构造“事件触发 → 人物按目标、信念和关系作出评价 → 身体或注意反应 → 决策/动作 → 余波”。少写悬空的情绪标签，多写人物认为这件事意味着什么，以及这种判断如何改变下一动作。

这条链允许省略：读者可以从动作、选择或感官偏向推断情绪。不能把目标失败固定映射成悲伤、把目标成功固定映射成喜悦；羞耻、解脱、麻木、嫉妒和混合情绪受人物经历与文化影响。验证器只能追问触发、评价和后果是否有文本证据。

来源：

- Walsh 等，“Stories in Action”关于目标评价与情绪理解：https://pmc.ncbi.nlm.nih.gov/articles/PMC10173355/
- Keith Oatley，“Why Fiction May Be Twice as True as Fact”：https://journals.sagepub.com/doi/10.1037/1089-2680.3.2.101

## 6. 意象与母题：重复时必须带着新语境

`card_id: motif-return`

母题可以是反复出现的图像、声音、词、动作或物件。每次出现先承担场景中的字面功能，再通过人物反应、位置或后果改变既有联想。有效回扣不是复制原句，而是让旧物在新压力下获得差异。

重复不自动等于象征，象征也不必多次出现；文化联想并不普遍。可以记录出现位置、字面用途、前次联想和本次意义变化，但不得规定每章出现次数，也不得给物件锁死唯一释义。

来源：俄勒冈州立大学“What Is a Motif?”：https://liberalarts.oregonstate.edu/wlf/what-motif

## 7. 句法节奏：让结构表达信息重量

`card_id: syntax-rhythm`

句长、从句层级、停顿和标点共同控制速度与强调。追逐、惊慌可缩短感知—动作单位；犹疑、回忆、推理可允许更长的延迟落点；复杂句之后的短句可承担决定或揭示。变化应由场景压力和人物声口触发，而不是随机轮换长短句。

检测连续同长度、同开头或同主谓模板只能提示“单调风险”。英文资源中的句型与词数不能直接转换为中文字符阈值；句长离散度、短句率等统计只供定位，不得单独决定通过或返工。

来源：

- 圣何塞州立大学写作中心“Sentence Variety and Rhythm”：https://www.sjsu.edu/writingcenter/docs/handouts/Sentence%20Variety%20and%20Rhythm.pdf
- Purdue Writing Lab，“Sentence Structure, Variety, and Clarity”：https://owl.purdue.edu/owl/graduate_writing/introduction_to_writing/documents/revising-and-editing/sentence-structure-activity.pdf

## 8. 自由间接话语：让叙述暂时染上人物声口

`card_id: free-indirect-discourse`

自由间接话语通常保留第三人称叙述框架，却省去“他想、她觉得”等引导，让称呼、评价、指示语、感叹和句法靠近当前人物。使用前必须先建立焦点人物与语境，使用后仍要保持思想归属可辨。

它不是内焦点化的同义词，也没有可靠正则：同一句可能是叙述者评论、人物思想或双声反讽。中文不依赖英语过去时规则，第一人称和现在时也可能出现类似效果。可检查思想标签是否过密或人物声口是否可辨，但不得要求自由间接话语出现率。

来源：

- Cambridge Core，“Free Indirect Discourse”：https://www.cambridge.org/core/journals/victorian-literature-and-culture/article/free-indirect-discourse/209B3AB3613BC4834A66F0041E0CCAD5
- 汉堡大学《Living Handbook of Narratology》“Speech Representation”：https://www-archiv.fdm.uni-hamburg.de/lhn/node/47.html
- 同手册“Puzzles and Problems for the Theory of Focalization”：https://www-archiv.fdm.uni-hamburg.de/lhn/node/24.html

## 9. 对白潜台词：让表层话题与真实目的发生压力差

`card_id: dialogue-subtext`

对白可同时承担表层话题、说话者目标、隐瞒事实、社会风险和策略。人物可以试探、施压、转移、安抚、隐瞒或重构问题；动作、停顿、答非所问和措辞选择应给读者留下可推断线索。对白写完后不要再由旁白逐句解释潜台词。

不是所有对白都要隐晦。紧急指令、交易条件、程序信息和直率人物需要清楚表达；无证据的普遍闪避只会制造假深沉。审阅应判断“未说出的目的是否可由上下文推知”，而不是统计潜台词句数。

来源：

- 佛罗里达州立大学写作资源“Dialogue”：https://wr.english.fsu.edu/College-Composition/The-Inkwell/Dialogue
- Penguin Random House 作者访谈《A Dangerous Fiction》reading guide：https://www.penguinrandomhouse.com/books/312361/a-dangerous-fiction-by-barbara-rogan/readers-guide/

## 使用层级

- 硬门禁：未声明的知识越权、跨脑读取；大纲规定的关键转折与前序行动完全无因果关系。
- 结构化复核：当前目标是否可追踪，情绪评价是否改变注意或行动，关键 scene 是否改变状态。
- 软诊断：距离跳变、scene/summary 选择可疑、母题机械复读、句法连续同构、思想标签过密、对白说明化。
- 不可机械化：唯一正确的距离、场景配比、象征解释、句长曲线、自由间接话语用量、潜台词密度与审美优劣。

最终原则：验证器负责指出证据、矛盾和缺口；作者或审阅者决定是否属于有意的艺术选择。
