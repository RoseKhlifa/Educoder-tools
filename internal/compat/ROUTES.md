# 已观察只读页面兼容路由

兼容列表以 HAR、原编译前端的实际调用和本地联调已观察的页面请求为依据。登录为唯一发送到上游的 POST；退出只清理本地会话。其余上游请求严格限制为明确列出的 GET，保持上游 JSON 顶层字段、查询参数和分页结构。

作业详情页 `/classrooms/:course/shixun_homework/:id/detail?tabs=1` 已观察到以下辅助读取：

```text
GET /api/homework_commons/:id.json
GET /api/homework_commons/:id/show_comment.json
GET /api/homework_commons/:id/shixun_challenge_data.json
GET /api/shixun_homeworks/:id/student_works.json
GET /api/shixun_homeworks/:id/header_info.json
```

这些 `:id` 仅接受大于零的十进制整数，并要求有效的站内 HttpOnly 会话。列表不授权其他同前缀接口；POST、PUT、PATCH、DELETE 不会转发。

任务页另外观察到仓库过期状态读取 `GET /api/myshixuns/:identifier/version_repository_delete.json`。该接口要求登录，标识只允许字母、数字、下划线和横线；虽然动作名包含 `delete`，本页调用为 GET，仅返回 `delete_expired` 等状态字段，其他方法和同前缀动作不转发。

原编译任务页的一处请求会发出 `/api//shixuns/:identifier/shixun_exec.json`。兼容层只把明确的 `/api//shixuns/` 前缀规范为 `/api/shixuns/`，然后执行同一 GET 白名单检查；其他双斜杠前缀不作通用清理。发送给上游的路径始终为通过检查的规范路径。

完整可执行列表位于 `internal/educoder/compat.go` 的 `RecordedRoute`。函数名保留为内部兼容接口名，列表内容已经扩展为已观察的只读页面兼容路由。未知接口日志只包含请求方法和经过引号转义的 URL 路径，便于下一步联调；日志不包含查询参数、请求体或请求头。
