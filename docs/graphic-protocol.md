# 绘图题协议

绘图题不走浏览器自动化，也没有单独的 HTTP 文件上传接口。头歌任务详情会给出 `tab_type=3` 的 VNC 运行环境和题干要求的唯一 `.mdj` 路径。Go 协议客户端按下面的链路完成一次读取或提交：

1. `GET /api/myshixuns/{myshixun}/start.json`，携带 `shixun_environment_id`、`tab_type=3`、`game_id` 和 `homework_common_id`，取得临时 `ssh_address`、端口、用户名和密码。
2. 通过受限 SSH/SFTP 连接 `/data/workspace/myshixun`，只读或原子写入题干明确的相对 `.mdj` 路径。路径拒绝绝对路径、`..`、`.git`、`pingce`、`secret`、符号链接和超大文件。
3. 写入前后做内容哈希的 compare-and-swap 校验；临时文件原子替换后只对目标文件执行 `git add -- <file>` 和 `git commit --only`，返回提交 SHA。不会执行仓库 hooks，也不会把临时密码、地址或签名令牌写入任务、模型输入、日志或错误文本。
4. 先读取一次 `game_status.json` 作为旧结果基线；随后按头歌 Web 客户端协议 `POST /api/myshixuns/{myshixun}/update_file.json`，使用 `evaluate=1`、`game_id`、`tab_type=3`、`homework_common_id` 和当前题目 extras 获取本次评测准备返回的 `sec_key/resubmit/content_modified`。图形 `.mdj` 已通过受限 SSH/SFTP 保存，评测准备请求不携带占位文件的 `path/content`，避免覆盖代码编辑器占位文件。
5. 将评测准备返回的密钥、`resubmit` 和 `content_modified` 连同提交 SHA 放入 `game_build` 的 extras，再 `POST /api/tasks/{game}/game_build.json`，其中 `tab_type=3`；然后轮询 `game_status.json`，区分排队中的 `status=0`、明确失败和完整通过，直到终态或有界超时。通过后再次读取任务详情和课堂进度，确认用户、作业、关卡、运行实例、目标路径、`game.status=2` 与满分证据全部一致。

当前 `internal/educoder/graphic.go`、`graphic_ssh.go` 和 `internal/solver/graphic_adapter.go` 已实现这条协议，并带有本地 HTTP、SSH/SFTP、路径、主机密钥、CAS、身份变更和凭据不泄露测试。StarUML 文件要求 UTF-8 JSON 且顶层 `_type` 为 `Project`；外部题库中的流程图、数据流图、实体联系图、类图、用例图、活动图、状态图和时序图统一按 `edit_mode=graphic`、单个 `.mdj` 文件契约复用。

外部仓库提供的是参考答案，不代表平台已验证。只有当前任务的标题、实验、位置和文件契约精确匹配，才会绑定为未验证候选；提交后仍必须取得当前账号的完整通过结果。当前只读“已通过答案采集”对绘图题保守跳过，不把无法从平台确认的 `.mdj` 自动写入正式题库证据。
