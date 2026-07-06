# Judge 漂移监控冻结样本（Task 069）

放 3 个冻结样本章 + 期望分数带（manifest.json）。每完成一卷（或每 20 章）用当前
editor/reviewer 配置重评：verdict 翻转或均分偏移 >10 → diag warning + evolution_report
记 proposed（"judge 漂移，建议重校准 rubric 或换 reviewer 模型"）。

重评方式：`novel-studio eval run --cases quality/calibration/golden_reviews/cases`（editor 单角色）
或手动把样本章喂给当前 editor 对照 manifest 期望带。
