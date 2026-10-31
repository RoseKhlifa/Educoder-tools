# 异步答题服务

Web 请求只负责校验会话、入队和返回任务 ID。`cmd/web` 启动 `internal/jobs.Manager`，在同一个 Go 进程中运行有界 worker。浏览器不会启动 `cmd/educoder`，服务也不会用 `os/exec` 调 CLI。

创建任务使用 `POST /api/jobs`，请求包含 `course`、`homework_id`、可选 `game`、`scope`、`mode` 和 `idempotency_key`。响应是 `202` 与 `data.id`。客户端通过 `GET /api/jobs/:id` 查询，也可读取 `GET /api/jobs` 恢复历史；`POST /api/jobs/:id/cancel` 只停止尚未完成的执行。如果已经进入保存、提交或评测阶段，服务返回 `uncertain`，不会声称撤销上游操作。

答题提交默认使用整份作业范围：`scope=homework`、`mode=submit`，请求不携带 `game`。服务先读取并严格校验这份作业的完整关卡清单，再按位置顺序处理；已经由当前作业进度精确证明满分的关卡会标记为 `skipped`。每个新提交后都会重新读取对应关卡进度，只有精确满分才进入下一关。结束前还会对全部计划关卡做一次最终满分核验，只有 `completed_levels == total_levels` 且 `failed_levels == 0` 才报告 `passed`。失败、预算耗尽、取消、清单不完整或结果待确认都会停止后续关卡并保留已完成结果。

为兼容已经打开的旧版浏览器页面，服务端也会把缺少 `scope`、但同时携带 `game` 的旧版 `mode=submit` 请求归一化为整份作业任务，并清除单关 `game` 字段；这类请求不会再退化为只执行一关。

仅生成答案或公开样例自测使用 `scope=current` 并携带 `game`，只处理当前关卡，不会继续同一作业的其他关卡。

默认配置为 8 个 worker、128 项队列、单账号 4 项待处理、模型并发 2、单任务 30 分钟。账号内同一目标只保留一个排队或运行任务，不同账号可以并行。模型调用由全局信号量限制。HTTP 请求取消只取消等待，不会取消已接受的后台任务。

任务状态、幂等键和公开进度保存在独立 SQLite 中。服务重启时将未完成任务标为 `interrupted` 或 `uncertain`，不会自动重放可能有副作用的保存或提交请求。owner 只保存为不可逆 hash；Cookie、密码、模型密钥、`sec_key`、WebSocket 地址和模型原始响应不写入任务数据库。

模型密钥通过 `DEEPSEEK_API_KEY_FILE` 指定的 Docker secret 读取。空密钥时服务仍启动，浏览、课堂和任务功能可用；`capacity.model_configured` 为 `false`，创建答题任务返回 `503 MODEL_NOT_CONFIGURED`。配置密钥后重启容器即可启用异步答题。

CLI 仍用于手动验证和运维调试，Web 任务不会为每次答题创建 CLI 进程。任务服务直接复用同一 Go 协议客户端、solver、objective taskservice 和题库连接，在 goroutine 中执行，并把状态写入可恢复任务记录。
