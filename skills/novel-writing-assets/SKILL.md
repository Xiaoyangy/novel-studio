---
name: novel-writing-assets
description: "查看、启用、停用、组合、绑定、试写和重新编译 novel-studio 的长期写法资产。触发：「列出写法特征」「启用/停用某个写法」「组合写法」「绑定写法」「试写写法」「重编译写法规则」「维护写法资产」。"
---
# novel-writing-assets：维护长期写法资产

写法资产保存在 `output/novel/meta/writing_assets.json`，Markdown 镜像在
`output/novel/meta/writing_assets.md`。它们是可编辑、可复用的长期资产，不是一次性 prompt。
内置人工感基准来自《同桌是只假装高冷的猫》：物件回扣、主观误判、短对话/动作拍、现实支架和可复核因果链。你可以把本书实践中有效的物件链、误判链或对白手感继续沉淀为 feature，再启用、停用、组合或绑定。
内置写作技巧总纲来自 `data/reference-library/writing-craft` 19 篇逐篇提炼：前台故事、时间线、阶段爆发、人物反差、单章目标/阻力/代价/新增信息、事件铺垫/过程/余波、改文层级和中文标点功能。维护写法资产时，可以把本书验证有效的这些规则沉淀为 feature 或 preset。

## 执行

```bash
# 列出当前特征池
novel-studio --writing-assets list

# 首次为当前项目注入人工感 / 单章可读性 / 去 AI 味基础写法资产
novel-studio --writing-assets seed-defaults

# 启用 / 停用某个特征
novel-studio --writing-assets enable prose:xxxx
novel-studio --writing-assets disable taboo:xxxx

# 手工编辑 JSON 后重新编译 compiled 结果
novel-studio --writing-assets compile

# 组合多个特征，形成可复用 preset
novel-studio --writing-assets preset horror_trade prose:xxxx pacing:yyyy anti_ai:zzzz

# 绑定单个特征或 preset 到全书、卷、弧、章节或试写范围
novel-studio --writing-assets bind book horror_trade
novel-studio --writing-assets bind arc 1 2 horror_trade
novel-studio --writing-assets bind chapter 18 prose:xxxx
novel-studio --writing-assets bind trial horror_trade

# 生成可编辑的试写任务单，落到 output/novel/meta/writing_trials/
novel-studio --writing-assets trial "写一段便利店债务交易"
novel-studio --writing-assets trial chapter 18 "测试第十八章的压迫感和对白"

# 试写人工感规则：物件回扣 + 主观误判 + 短对话/动作拍
novel-studio --writing-assets trial "用门牌、欠费单和猫眼写一段误判后修正的开场"

# 试写 refer 技巧规则：目标/阻力/代价/新增信息 + 事件余波 + 标点声口
novel-studio --writing-assets trial "写一段过渡章, 要有结算、下一目标、人物反应和章末新钩子"
```

## 注意

- 弧摘要保存的 `style_rules` 会自动沉淀为写法特征。
- `seed-defaults` 只补缺失的默认特征 / preset / book 绑定，不覆盖用户已有特征，也不会重新启用用户已经关闭的同 ID 特征。
- `enabled=false` 的特征不会进入 `reference_pack.writing_engine.active_rules`；绑定到其他范围的特征也不会误进当前章节。
- 原文样本只能作为节奏和取景参考，正文生成不得搬运样本原句。
- 人工感特征应写成本书可复用的规则，例如“每章至少两个物件承担新信息”“误会必须有前文证据”，不要写成样本文校园桥段或原句。
- refer 技巧特征应写成可执行规则，例如“每章回答目标/阻力/代价/新增信息”“核心事件必须有铺垫/过程/余波”“标点按声口和条款层级使用”，不要写成“文笔更好”这类空泛目标。
