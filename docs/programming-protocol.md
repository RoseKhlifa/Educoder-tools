# 代码题协议与录制证据

本次协议来自本地 `openwork_to_finishwork.har`。录制保持原样且不进入 Git。本文使用路径占位符；认证材料、执行密钥、WebSocket 地址和账号信息不写入文档或测试。分析按 `startedDateTime` 排序，不能按 HAR 数组顺序执行：该录制按连接/路径组织，共 152 条记录，其中 Educoder 相关 68 条（数据接口 62、页面 4、自测 WebSocket 2），其余 84 条排除。

## 支持的题型与上下文

此次仅证实代码题：任务顶层 `st=0`、`challenge.st=0`、`challenge.with_code_file=true`。模块保留题型值，只有符合这组特征的任务进入代码题动作；其他或缺失特征保留为未支持类型。作业 `homework_type=practice` 和 `is_shixun=true` 只表明实训作业，不能单独作为代码题分类。

| 字段 | 用途 |
| --- | --- |
| `homework_common_id` | 串联课堂作业、题目、保存、自测和评测；保存及 extras 中为字符串 |
| `game.identifier` | `/api/tasks/:game` 的路径标识 |
| `game.id` | `update_file.game_id` 的数字 ID |
| `challenge.id` | 保存/评测 extras 和轮询 `challenge_id` |
| `challenge.task_pass` | Markdown 题干，保留原文 |
| `challenge.path` | 本关已知可编辑文件路径，以全角分号分隔，去除空项 |
| `myshixun.identifier` | `/api/myshixuns/:identifier/update_file.json` 路径 |
| `myshixun.id` | 自测 WebSocket 订阅中的数字 `data` |
| `shixun_environments[]` | 运行环境 `shixun_environment_id` 与 `tab_type` |
| `user.user_id` | 保存与正式评测的 `extras.currentUserId` |

作业只有两关，任务响应 `game_count=2`；整个实训 `shixun.challenges_count=12`，两关原始 `challenge.position=3/4`。展示与遍历本作业时应使用作业关卡数据和 `next_game`，不能将整个实训数量或原始 position 当作本作业的关卡数量/序号。

## 已观察的请求顺序

下列 entry 均为原始 HAR 的 **0-based** 索引。第一关缺少自测前保存记录，应直接重放已观察的自测，不能补造该次请求。

| 阶段 | 方法与路径 | 第一关 entry | 第二关 entry |
| --- | --- | --- | --- |
| 作业详情/关卡清单 | `GET /api/homework_commons/:homework.json`、`/shixun_challenge_data.json` | 146 / 144 | 共用 |
| 开始挑战 | `GET /api//shixuns/:shixun/shixun_exec.json` | 149（304，无响应体） | 使用 `next_game` |
| 读取题目 | `GET /api/tasks/:game.json` | 120 | 109 |
| 读取代码 | `GET /api/tasks/:game/rep_content.json` | 110 | 97 |
| 自测前保存 | `POST /api/myshixuns/:identifier/update_file.json`，`evaluate=0` | 未记录 | 134 |
| 自测启动 | `POST /api/tasks/:game/log_output` | 111 | 99 |
| 自测输出 | WebSocket `/log` | 0 | 1 |
| 提交前保存 | `POST /api/myshixuns/:identifier/update_file.json`，`evaluate=1` | 133 | 135 |
| 正式评测 | `POST /api/tasks/:game/game_build.json` | 118 | 106 |
| 评测轮询 | `GET /api/tasks/:game/game_status.json` | 113 / 114 / 115 / 116 | 101 / 102 / 103 / 104 |
| 计时上报 | `POST /api/tasks/:game/cost_time.json` | 119 | 107 |
| 离开时保存 | `POST /api/myshixuns/:identifier/update_file.json`，`evaluate=0` | 136 | 137（空响应体） |
| 返回详情确认 | 作业详情、`shixun_challenge_data.json` | 148 / 145（两关汇总） | 共用 |

此次 `shixun_exec` 只有 304，无响应体；沿用现有客户端已验证的进入入口，但本次离线重放从实际录制的 task 响应开始，不为该 304 编造 `game_identifier`。

## 题干与编辑器文件

`rep_content` 的 query 为 `path`、`homework_common_id`，并由已有认证层补充登录提示。路径正常 URL 编码一次，不重复编码。响应结构：

```json
{
  "content": { "content": "BASE64_UTF8_CURRENT_CODE", "size": 0 },
  "language": "cpp",
  "file_type": "txt",
  "filename": "example.cpp"
}
```

`file_type=txt` 不代表文件扩展名为 `.txt`。扩展名应从精确 `filename` 或路径中获取。客户端返回题干、文件路径、文件名、扩展名、语言及解码后的代码。

| 本作业关卡 | 文件路径 | 本次读取到的代码 |
| --- | --- | --- |
| 基于栈的回文字符序列判断 | `242/242基于栈的回文字符序列判断1.0.cpp` | 已含填写的函数，和提交前保存内容一致 |
| 入栈和出栈的基本操作 | `243/243入栈和出栈的基本操作1.0.cpp` | 待填写函数模板，后续保存为编辑后的代码 |

本次只读到这两个 `.cpp` 文件，没有目录树、`.h` 或多文件读取记录；不主动重置仓库来寻找原始模板。模块能保留路径中的后缀及多个已列路径，但多文件保存/评测顺序仍待其他录制核实。

## 保存与自测

保存发送明文代码，保留换行和空格。`evaluate=0` 表示普通保存，`evaluate=1` 表示正式评测前保存。请求包含 `path`、`content`、数字 `game_id`、`tab_type`、`exercise_id:null`、字符串 `homework_common_id`，以及录制中出现的 extras 上下文。

保存响应 `content` 是对象：`{commitID, content, size}`，其中 `content` 为 Base64；`sec_key`、`resubmit`、`content_modified` 要原样关联到本次评测。`content.commitID` 放入 `game_build.extras.commitID`。普通保存可能返回非空 `resubmit`，不能将其固定为空字符串。

自测请求为：

```text
POST /api/tasks/:game/log_output
{test_case_content, shixun_environment_id, tab_type,
 extras:{homework_common_id}}
```

HTTP 响应只给 `data.wss_url`。连接其 `/log`，客户端发送明文 JSON `{tp:"log",data:myshixun.id}`。服务端帧内容采用应用层 Base64 编码，文本帧和二进制帧均按同一编码处理。原始 HAR 及原编译任务页的 `Base64.decode` 证明这不是 HAR 额外编码；本次针对故障任务的线上流核验还确认首个 stdout 帧含 URL-safe 字符 `-`，结束帧仍是 Base64 编码的 `ProcessFinished`。仅使用标准 Base64 解码器会把该合法输出误判为坏帧。

客户端接受标准与 URL-safe Base64、有 padding 或无 padding 的形式，并忽略编码文本中的 ASCII 空白。解码后累积 stdout，只有精确的 `ProcessFinished` 才表示输出完整。普通明文不会作为解码失败后的兜底输出，未编码的结束标记也不当作成功；运行器 JSON 错误帧、无效编码、提前关闭、超时或超出输出限制均保留未完成状态。错误诊断只记录受控原因及帧类型/长度，不保存原始帧或运行地址。完整输出在结束标记后统一校验 UTF-8，以允许一个多字节字符分布在相邻消息中。

两次自测输入均与该关公开 `test_sets[0].input` 一致。stdout 比较先把 CRLF 归一为 LF，再允许一次可选的终止换行，以兼容题目样例缺少最后换行而程序正常输出换行的情况。内部空格、额外空行和输出内容仍严格比较。自测输出相等不等于正式通关，必须继续取得正式评测和当前关卡满分证据。

公开样例仅取 `is_public=true` 的测试。输入与预期输出同时为精确空字符串的条目视为无可观察输出的占位项，不用于自测；只缺一侧、或者包含空格和换行的样例仍有效。未公开测试输入/输出以及没有可运行公开样例都不阻止 `submit` 模式继续正式评测；单独请求 `selftest` 模式时，无公开样例返回 `selftest_skipped`。这项规则不会把隐藏的测试结果或不完整的通过证据直接认定为通过。

## 提交、评测和通过状态

正式评测请求包含保存响应提供的 `sec_key`、`resubmit`、`content_modified`，以及 `first:1`、环境 ID、`tab_type`、含 `commitID` 的 extras。保存失败则不启动 build。写请求不做自动重试，评测轮询次数/时间有界。

`game_build.status=1` 是本样本中的启动状态，不能由通用 API 层按“非零即失败”处理；本样本的 `res.success=false` 与 `res.code=0` 同时出现，也不能据前者直接判定失败。

轮询携带保存返回的执行密钥、build 返回的 port，以及 `time_out=false`、`resubmit`、`challenge_id`、`subject_id`、`homework_common_id`。观察到的顺序：

```text
running_code_status=1 → 304 未更新 → running_code_status=2
→ 最终 status=2 + test_sets[].result=true + sets_error_count=0
```

`running_code_status=2` 仍只是环境启动完成，不代表最终通过。最终响应提供 `last_compile_output`、预期/实际输出、编译状态、耗时和内存。第一关最终 `had_done=0`，第二关为 1，故该字段也不能用来判断单关是否通过。客户端仅对这两个已观察的执行接口采用业务状态解释，其他接口仍保留原有 API 错误处理。

线上故障记录曾出现 `game_status.json: API status 0`，说明通用 API 层过早拒绝该值；它本身没有证明该次评测已结束或属于编译错误。当前针对评测端点单独解析 `status=0`：仍处于 `running_code_status=1/2` 时继续轮询；否则仅在 `sets_error_count>0`，或某条测试明确 `result=false` / `compile_success=0` 时返回“已结束、未通过”，保留反馈供 solver 在原有尝试与费用预算内修复。裸 `status=0`、只有计数/元数据、只有编译输出而没有明确失败判定，都保留未知错误；不能靠放行状态码认定失败或通过。这些失败组合由本次本地合成契约测试覆盖，原始 HAR 只录到了上述成功评测序列。

成功仍需正的完整测试数量、对应每条结果均明确编译成功且通过，随后按精确关卡得分复核。隐藏测试输入/输出不影响这些结果元数据的核验；缺少结果详情或数量矛盾时不会直接判为通过。正式保存或 build 后结果未知，不自动重放写请求；已有只读复核仅在能够绑定原候选源码、正式评测证据及当前满分状态时恢复通过。

返回详情后，`challenge_settings[].passed_status` 从两项 0 变为两项 2，`passed_count` 从 0 变为 2，`evaluate_count` 从 0 变为 2。课堂关卡得分在 `game_score`；评测返回的 `grade` 不作为本题得分。`can_submit=false` 属作业层上下文，在本样本中不妨碍关卡 `game_build`。

用户自测后约等待 30 秒才点击提交；录制没有额外等待令牌或等待接口，不将这个操作间隔写成协议前提。`cost_time` 为页面计时上报，读取评测结果不依赖它。

## 验证与后续录制边界

当前只维护 Go 协议客户端，复用其签名及内存 CookieJar。单元/集成测试使用虚构上下文和本地 HTTP/WebSocket 服务。早期曾用独立 TypeScript HAR 工具验证录制；该工具已按用户要求删除，本文保留字段结论和录制证据。当前回归命令为 go test ./internal/educoder。

此次可以验证题型分类、两关题干/文件、代码解码、普通保存、两次自测输出、提交前保存参数传递、两次 build、304/运行状态/最终结果和详情通过状态。没有生成答案。

该原始 HAR 未覆盖其他题型、完整目录树/`.h`/多文件、多环境、失败/编译错误/排队、实际非空 resubmit 的重新 build。本次线上另核验了 URL-safe 自测帧，并结合线上 `status=0` 错误迹象补充本地失败组合测试；这些合成响应、超时和异常测试不等于原始 HAR 或线上完整录到了相应失败分支。

## 选择题链路（独立题型）

随后对同一课程中的 `客观题测试-第5章树和二叉树` 实测确认：选择题不属于代码题的文件编辑协议。任务详情响应使用 `st=1`、`challenge.st=1`，并提供非空 `chooses` 数组；本次一关含 10 道题。每项包含 1-based `position`、`subject`、`question_type`、`category` 和 `challenge_question` 选项数组；选项保存 0-based `position` 与 `option_name`。`category=1` 表示单选，前端提交一个字母；其他已观察 category 使用排序去重后的多字母字符串。

选择题提交请求为：

```text
POST /api/tasks/:game/choose_build.json
query: homework_common_id（以及页面存在时的题目上下文参数）
body: {answer:["A", "BC", ...], challenge_id, subject_id, exercise_id,
       competition_entry_id, smart_plan_page_item_bank_id, ip}
```

响应包含 `choose_correct_num`、`challenge_chooses_count`、`test_sets`、`next_game` 和游戏状态字段。部分作业关闭公开正确数时，`choose_correct_num` 可能为空；客户端无论是否公开正确数都重新读取目标关卡进度，只有非负 `game_score` 与正的 `challenge_score` 精确相等才报告通过。JSON 数值 `13.3` 与字符串 `"13.30"` 均按原始十进制值比较，不经过 float64。公开正确数或题目总数若与满分证据矛盾，则仍保留未确认状态。`game_original_status=2` 和 `passed_status=2` 只是上下文，不能替代满分比较；部分得分不得进入下一关。它不调用 `update_file`、`log_output`、`game_build` 或代码 WebSocket。

模型使用单独的 objective prompt 和 `{kind:"objective",answers:[...]}` JSON 契约，选项题干与选项文本进入白名单快照。选择题题库候选和正式验证记录仍需单独扩展，不能把源码 `solution_files` 当作选择题答案。

满分核验结果包含 `score`、`max_score`、`score_known`、`score_check` 和脱敏诊断。`full_score` 为已确认满分，`not_full_score` 为分值明确但非满分，`unconfirmed` 为分值缺失、读取失败或证据矛盾。JSON 模式保留报告，并与普通模式一样对未通过结果返回失败；输出 JSON 本身不等于答题通过。
