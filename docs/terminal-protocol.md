# Linux 独立终端协议

## 识别与启动

任务必须满足代码题的 `st=0`、`challenge.st=0`、`with_code_file=true`，同时 `code_editor.shixun_environment_id=0`，且只有一个 `tab_type=4` 的非 VNC 环境。拥有代码编辑器并附带终端的普通编程题继续走原来的保存、自测和评测链路。

Linux 录制中的交互终端与自测日志是两个服务入口：

| 用途 | 请求与消息 |
| --- | --- |
| 启动终端 | `GET /api/myshixuns/:instance/start.json`，携带环境 ID、`tab_type=4`、数字 game ID、课堂与作业 ID |
| 连接终端 | `data.link_url`，已观察为官方 WebSocket `/ws` |
| 初始化 | `{tp:"init",data:{启动参数,secret:临时密码,width,height,rows,columns}}` |
| 输入 | `{tp:"client",data:命令文本}` |
| 心跳 | `{tp:"h"}` |
| 输出 | 文本或二进制消息中的 Base64，兼容 URL-safe 与省略 padding |
| 普通编辑器自测 | `log_output` 后连接 `/log`，使用 `{tp:"log",data:运行实例数字ID}`；并非交互终端 |

启动参数、临时密码及连接地址仅存在协议层内存，不进入题目快照、模型输入、任务记录或 Git。连接不复制上游 Cookie、签名或认证头，并拒绝重定向。运行实例必须与当前 task 精确匹配。

## 执行与评测

`Client.RunTerminalCommand` 在初始终端输出到达后只发送一次脚本。脚本以 Base64 分块传输，远端解码后先核对 SHA-256，再执行原始字节，避免交互终端改写引号、换行和控制字符。随机执行边界隔离命令回显与真实输出，完整退出码及远端脚本哈希齐全才生成内存回执。断线、超时、无效帧、身份变化或输出超限保持未确认状态，不自动重放。

默认执行时限 90 秒，最多 10 分钟；脚本最多 32 KiB，输出默认 128 KiB、最多 1 MiB。取消会终止本地连接与心跳。关闭连接不等于已经撤回上游执行。

终端求解使用 `edit_mode=terminal`、`terminal:posix-shell/v1` 等独立执行契约。候选为完整 shell 脚本，保留关卡文件路径作为契约标识；它不替换上游“请使用命令行”的占位源码。一次退出码检查负责执行脚本，随后 `SubmitTerminal` 消费绑定当前客户端、任务、运行实例、关卡、用户、环境与脚本哈希的回执，只发起一次正式评测。

评测前保留并读回原占位文件，按 `tab_type=4` 保存以获取本次评测密钥，再通过既有 `game_build` / `game_status` 链路取得完整正式结果。真实终端保存响应包含评测密钥，但没有 `content` 或 `commitID`，因此该模式按原站行为使用空 commit；普通编辑器模式继续要求有效 commit。正式通过之后，还须重新读取当前任务通过状态与课堂当前关卡的精确满分。命令退出码为 0 或屏幕打印预期文字均不构成通关证据。

终端模式的 `saved_files_hash` 表示已经确认执行的候选脚本哈希，`submitted_files_hash` 表示该脚本回执绑定的正式评测；它们不宣称上游占位文件保存了该脚本。由于工作区可能包含先前操作留下的文件，正式通过只证明当前工作区满足要求；验证记录使用 `formal_workspace`，候选状态为 `workspace_passed`，作业执行结果仍为 `passed`。在尚未证明脚本可在干净环境独立重现之前，终端候选不会晋升为全局可信答案，题库库存与个人贡献也不计入该脚本；正式通过的服务完成量照常统计。外部普通源码候选不自动转换为终端脚本；历史只读采集以 `terminal_submitted_script_unobservable` 跳过占位源码。

## 调试调用

维护工具与 Web worker 复用同一 Go 模块，Web 不启动 CLI 子进程。已认证客户端可先进行只读终端检查：

```go
result, err := client.RunTerminalCommand(ctx, task,
    "pwd\ncommand -v vim\n", educoder.TerminalOptions{})
if err != nil || !result.Completed {
    // 保留未确认状态，先核对上游，避免重复执行。
    return err
}
// result.ExitCode 与 result.Output 只说明这次命令执行情况。
```

本地 HTTP/WebSocket 测试覆盖环境判别、身份与密钥隔离、输出分帧、命令回显、退出码、取消、坏帧、输出上限、重复回执和正式满分核验。真实录制与运行报告保留在受忽略的本地恢复目录，不进入发布归档。
