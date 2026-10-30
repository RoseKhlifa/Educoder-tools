# 已观察页面与代码题兼容路由

兼容列表以 HAR、原编译前端的实际调用和本地联调已观察的页面请求为依据。保留上游 JSON 顶层字段、查询参数和分页结构；退出只清理本地会话。代码题新增下列明确方法/路径，其他同前缀动作不转发：

```text
GET  /api/tasks/:game/game_status.json
POST /api/myshixuns/:identifier/update_file.json
POST /api/tasks/:game/log_output
POST /api/tasks/:game/game_build.json
POST /api/tasks/:game/cost_time.json
```

这些请求要求有效的本地 HttpOnly 会话，使用服务端已登录客户端生成签名和认证头；不使用浏览器传来的上游 Cookie/Authorization。代码 POST 只接受不超过 2 MiB 的 JSON 对象，保留代码字节和字段；已观察的 `cost_time` 可为空请求体。所有代码/评测动作和进度读取保持缓存 BYPASS。轮询 304 和离开页面时保存的空响应体按原状态返回。`log_output` 返回的 WebSocket 地址由原编译前端连接；协议客户端自测使用同一 `/log` 订阅和 Base64 帧解码。完整证据见 `docs/programming-protocol.md`。

作业详情页 `/classrooms/:course/shixun_homework/:id/detail?tabs=1` 已观察到以下辅助读取：

```text
GET /api/homework_commons/:id.json
GET /api/homework_commons/:id/show_comment.json
GET /api/homework_commons/:id/shixun_challenge_data.json
GET /api/shixun_homeworks/:id/student_works.json
GET /api/shixun_homeworks/:id/header_info.json
```

这些 `:id` 仅接受大于零的十进制整数，并要求有效的站内 HttpOnly 会话。这些作业详情路径仅支持列出的 GET。

任务页另外观察到仓库过期状态读取 `GET /api/myshixuns/:identifier/version_repository_delete.json`。该接口要求登录，标识只允许字母、数字、下划线和横线；虽然动作名包含 `delete`，本页调用为 GET，仅返回 `delete_expired` 等状态字段，其他方法和同前缀动作不转发。

原编译任务页的一处请求会发出 `/api//shixuns/:identifier/shixun_exec.json`。兼容层只把明确的 `/api//shixuns/` 前缀规范为 `/api/shixuns/`，然后执行同一 GET 白名单检查；其他双斜杠前缀不作通用清理。发送给上游的路径始终为通过检查的规范路径。

完整可执行列表位于 `internal/educoder/compat.go` 的 `RecordedRoute`。未知接口日志只包含请求方法和经过引号转义的 URL 路径；日志不包含查询参数、代码、执行密钥、请求体或请求头。未增加替代前端组件。
