# dashboard — 创作进度看板

统一读取仓库根 `data/runs/` 下全部书目工程的**只读**实时看板：每本书一张卡片，展示阶段 / 章节进度 / 字数 / 成本 / 卷弧位置 / pipeline 阶段 / 每章字数分布；点开卡片看章节明细（标题、字数、评审 verdict、AI 门禁与警告数）、按角色用量和运行日志尾巴。前端每 4 秒轮询自动刷新，正在写作的书带呼吸灯标记。

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
| `/api/novels` | 全部书目进度摘要（progress / pipeline / usage / 评审统计） |
| `/api/novels/<书名>` | 单书详情：章节表、按角色用量、交付沉淀次数、日志尾 80 行 |
