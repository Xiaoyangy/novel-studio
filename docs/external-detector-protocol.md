# 外部检测器人工抽检协议（Task 062）

朱雀等检测器是网页工具，**只人工触发、人工登记，不做自动化提交**（合规边界）。

## 抽检流程
1. 每卷抽 3-5 章（含 1 章返工稿），复制正文到腾讯朱雀 https://matrix.tencent.com/ai-detect/ ，选**小说版**模式
2. 记录：分数、判定、截图存 `quality/calibration/external_screenshots/{book}/{NN}.png`
3. 登记：`python3 quality/audit/scripts/register_external_detection.py --project <output/novel> --chapter N --detector zhuque --mode novel --score X --verdict ai_like|human_like|mixed --note 截图路径`
4. 跑 `calibration_report.py --external-log <project>/meta/external_detection_log.jsonl` 看本地 blended 分与外部分相关性；相关性差 = 本地代理失真 → 提议复核词表/权重（proposed 语义）

## 平台口径提醒
平台专项整治（语言僵硬/内容空洞/同质化/批量占流量）：申诉只有一次复审机会、重证据——
保留 reviews/（八维评审+机械门禁 ai_gate）、返工前后 diff、外部检测登记与 prompt_manifest 作为创作过程证据（见 docs/platform-alignment.md）。
