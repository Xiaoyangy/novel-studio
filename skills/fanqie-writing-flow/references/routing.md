# 题材路由规则

本文件用于把用户给的题材、书名、人设和钩子映射到现有写作技能。始终叠加 `fanqie-novel-template`，再选择一个主方向技能。

## 路由优先级

1. 用户明确说的题材最高优先级。
2. 书名和人物代词次之。
3. 人设职业、关系钩子、平台标签再次之。
4. 如果存在多方向冲突，选择更具体的方向，并把假设写入 `flow_state.json` 的 `assumptions`。

## 双男主

选择 `skills/fanqie-shuangnanzhu-short/SKILL.md`。

触发词：

- 双男主、男男、他他、强强、1v1
- CP 营业、对家、顶流、影帝、爱豆、恋综、粉丝嗑 CP
- A/B 都是男性职业或文本出现“他”
- 替身觉醒、穿书反炮灰、京圈豪门、校园成年线且 CP 为男性

示例：

`《和死对头组CP营业后我们假戏真做了》`：对家顶流 + 影帝 + CP 营业 + “他刚踩我脚了”，路由到双男主。

## 百合 / GL

选择 `skills/fanqie-baihe-short/SKILL.md`。

触发词：

- 百合、GL、双女主、女女、她她
- 姐姐、妹妹、大小姐、真千金、同桌、女上司等明确女女关系
- “她”与“她”的感情线、双女主 HE

## 反转爽文

选择 `skills/fanqie-shuangwen-short/SKILL.md`。

触发词：

- 爽文、反转、打脸、复仇、维权、背叛、离婚、重生反杀
- 出轨、论文/提案抄袭、职场抢功、家庭 PUA、男频隐忍反杀
- 主轴是“被轻视 -> 取证/布局 -> 公开翻盘”

## 新题材

如果用户给的是玄幻、古言、悬疑等当前没有专属 skill 的方向：

1. 先使用 `skills/fanqie-novel-template/SKILL.md` 的通用骨架。
2. 若用户提供样本目录，先运行 `skills/deal-paper-summry/SKILL.md` 生成新题材 skill。
3. 若没有样本目录，就用通用模板 + 用户设定临时执行，并记录“缺少题材专属样本”的风险。

## 路由输出格式

在 `flow_state.json` 里记录：

```json
{
  "direction": "shuangnanzhu",
  "selected_skills": [
    "fanqie-novel-template",
    "fanqie-shuangnanzhu-short",
    "review"
  ],
  "assumptions": [
    "根据影帝、顶流、他、CP营业判断为双男主"
  ]
}
```
