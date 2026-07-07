# dashboard — 创作进度看板

统一读取仓库根 `data/runs/` 下全部书目工程的**只读**实时看板：每本书一张卡片，展示三层进度（目标 / 规划 / 完成 / 落盘）、当前章步骤链、字数 / 成本 / 卷弧位置 / pipeline 阶段 / 每章字数分布；点开卡片进入七页签抽屉。前端每 4 秒轮询自动刷新，正在写作的书带呼吸灯标记。

## 详情页签

- **总览** —— 章节状态表（规划 ∪ 落盘：标题 / 字数 / 评审 verdict / AI 门禁与警告数）+ 当前章进展 + 按角色用量
- **设定** —— premise.md / book_world.md Markdown 渲染、故事日历、势力进度钟、世界规则、物理公理、背景时间线
- **人物** —— 按核心 / 重要 / 次要分层的画像卡（OCEAN 大五人格条形、依恋类型、DNA 三层、当前目标 / 压力 / 情绪评价 / 可能行动）+ 群众 NPC 名册 + 关系契约
- **成长轨迹** —— 人物生命线（章节出场时间轴 + 首末跨度 + 计划回归节点）、三段弧向（前 / 中 / 后期）、当前事实、长弧规划；决策流（state_changes 画成 old→new + 「因为…」理由的时间线，按人物筛选）
- **计划** —— 卷 / 弧骨架树、已细化章节细纲、下一章计划、伏笔台账、时间线
- **离屏世界** —— 世界推演游标、模拟分层色条、社会情绪、离屏日程、SVG 环形进度钟（Blades 式）、离屏事件流
- **日志** —— 运行日志尾 80 行，自动滚动

## 启动

```bash
novel-studio service start    # 拉起并打开 http://127.0.0.1:8765/
novel-studio service status   # 健康检查
novel-studio service stop     # 停止
# 或直接：python3 services/dashboard/server.py --host 127.0.0.1 --port 8765
```

## 数据源与 API

数据源固定为 `data/runs/<书名>/output/novel/`（可用环境变量 `NOVEL_STUDIO_RUNS_DIR` 覆盖扫描目录）。零依赖（Python 标准库），不写任何文件。

| 端点 | 内容 |
|---|---|
| `/` | 看板页面（自包含单文件，无外部资源） |
| `/api/health` | 健康检查 |
| `/api/novels` | 全部书目进度摘要（progress / pipeline / usage / 评审统计 / 当前章步骤） |
| `/api/novels/<书名>` | 总览详情：章节表、按角色用量、交付沉淀次数、日志尾 80 行 |
| `/api/novels/<书名>/setting` | 设定：premise / book_world MD、势力进度钟、世界规则、时间线 |
| `/api/novels/<书名>/cast` | 人物：分层画像 + 群众名册 + 关系契约 |
| `/api/novels/<书名>/growth` | 成长轨迹：人物出场生命线 + 弧向 + 长弧规划 + 决策流 |
| `/api/novels/<书名>/plan` | 计划：卷弧骨架 / 细纲 / 下一章计划 / 伏笔 / 时间线 |
| `/api/novels/<书名>/offscreen` | 离屏世界：tick 游标 / 日程 / 进度钟 / 社会情绪 / 事件流 |
