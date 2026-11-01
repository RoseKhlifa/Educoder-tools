# 登录后的已通过答案采集

登录成功后，HTTP 服务在进程内提交 `collect_verified` job，返回登录结果及 `collection.job_id/status/reused`。扫描不依赖模型配置，不运行 CLI，不创建实训实例，不写编辑器、不自测、不评测、不提交、不调用模型。队列满时仍然登录成功，响应 `collection.queued=false` 和错误码。

## 已核实的数据来源和边界

现有接口包括课堂列表 `GET /api/users/{login}/courses.json`；作业列表 `GET /api/courses/{course}/homework_commons.json`；关卡分数 `GET /api/homework_commons/{id}/shixun_challenge_data.json`；任务详情 `GET /api/tasks/{game}.json`；当前源码 `GET /api/tasks/{game}/rep_content.json`。采集作业时使用所有状态 `status=0`，不限定 `type`。

- 当前源码接口没有已提交版本的 digest、revision 或源码快照。满分不能证明当前编辑器代码就是当时通过的代码。代码必须额外匹配本账号、本 course/homework/game、题目与文件契约以及精确源码哈希的本地正式通过记录；候选/保存/提交哈希必须相等，正式测试必须全部通过。当前详情也必须已通过，返回测试全部编译及运行通过，再读分数和文件内容不得变化。满足条件的代码已经在题库，计为重复；外部历史代码缺少提交绑定时记录 `programming_submitted_source_unproven`。未假设一个不存在于已观察协议中的“通过源码下载”端点。
- 客观题只使用目标任务 `choose_test_cases.test_sets.actual_output` 中的实际作答。每题必须有唯一匹配的 position、题型和名称。已返回的逐题 false 或正确题数冲突直接拒绝。真实任务的教师隐藏结果模式会同时返回 null 的 `result/choose_correct_num`，此时要求完整已提交答卷（两个提交完整性字段均为 true、题数与记录数准确、每题实际作答非空且符合选项），再绑定当前目标关卡独立读取的精确满分。题目 challenge_id 必须匹配目标，选择题 ID 不得重复。再次读取任务及分数，题干、选项顺序和答卷一致后，按单题入独立 objective 表。提交字段只证明答卷完整性，正确性来自目标关卡精确满分；不会使用 standard_answer 推断答案，也不会把 had_done、已提交或作业汇总通过数单独作为证明。
- 只覆盖已观察的 question_type=0、单选/多选 category；其他题型跳过。图片/公式图缺少实际内容哈希时记录 `objective_media_content_unproven`，不拿 URL 代替内容证明，不下载未知资源。
- 作业列表返回的 `myshixun_identifier` 不是 game。采集器先 GET `/api/shixun_homeworks/{homework}/header_info.json`，匹配 homework、student_work、shixun 和既有 myshixun，然后 GET `/api/myshixuns/{myshixun}/challenges.json?homework_common_id=...` 读取已有运行的 game 列表。这一路径来自任务页编译资源的服务导出 g6 及 GET helper，有学生本人真实响应验证。`shixun_exec` 虽是 GET，但可能创建运行实例，始终禁止调用。
- game 列表返回 identifier、position、name、status，而非 challenge_id。header 有完整 challenges 时按唯一位置和准确名称交叉定位；真实部分 header 没有 challenges，此时仅允许与该作业分数表中唯一同名、精确满分的 challenge_id 匹配。只有 runtime status=2 且名称/类型符合，才读取详情；详情再匹配 runtime identifier、game、challenge ID/position/name、homework、shixun 和正满分。重复/不一致清单整体拒绝。泛化通过标记不授权读取未满分详情。
- 兼容种子仍可来自正常读取后记录的 owner 隔离目标、历史正式通过 run 或列表已有明确任务链接。链接只提取标识，不访问其中的主机和签名查询。新用户不需要本站历史记录即可通过已存在 myshixun 列表发现任务。缺少或不一致的定位证据继续记录具体原因。
- 部分满分作业必须先知道 game 对应的 challenge_id 且该关卡满分，才能读取详情，不能跟随未知 next_game。只有作业关卡列表全部精确满分时，才允许从种子跟随 next_game；每一跳核对 game、作业、实训、关卡、运行实例及得分。缺失或重复的身份均跳过。

因此任务成功只表示本次采集正常结束，不表示扫描已覆盖整个账号，也不表示已成功获取或入库全部历史答案。达到单次扫描上限时，本次已核验入库的内容保留；不会为了让新增数变大而把未证实的编辑器代码或媒体题写入题库。未确认及 reasons 会如实保留只读定位和答案证据的缺口。

当前服务用户端不再显示早期调试工作台的采集统计与原因。登录页的《用户准则》已明确告知已通过任务扫描及题目、答案和代码的收集复用用途；扫描权限与核验门槛保持不变。管理员通过管理任务接口读取扫描课堂、作业、关卡读取尝试，以及新增、重复、跳过、失败和原因。未新增不代表失败，也不能展示成成功获取了答案；未确认是跳过记录的子集，不与跳过重复相加。普通用户任务列表过滤采集任务。

## 队列和接口

同一账号同日登录使用 UTC 日期键 `collection-v1-YYYY-MM-DD`；并发/重复登录复用 job。待执行的同账号同采集目标也会合并。`GET /api/collections` 列出本账号最近采集记录的裁剪摘要；`POST /api/collections` 接受空 body 或 `{}`，通过标准 `Idempotency-Key` 头显式创建新重试，不接受外部账号、目标或答案参数。

当前 `GET /api/jobs/{id}` 返回经过裁剪的本账号服务摘要，不返回原始事件、采集原因和内部统计；管理员通过 `/api/admin/jobs/{id}` 读取详细结果。`POST /api/jobs/{id}/cancel` 请求取消。其他 owner 读取和取消返回 404。任务数据库只存公开参数、事件和统计，客户端和 Cookie 留在内存闭包。重启把未完成采集标记 interrupted，不自动重放。密码、运行密钥、WebSocket URL、签名 URL 不进入 job 或题库。

统计包含 courses/homeworks/levels、collected、duplicates、skipped、unconfirmed、failed、limit_reached 和聚合 reasons。它们使用不同的计数单位，不能相加为“处理题目总数”：

| 字段 | 含义与单位 |
| --- | --- |
| `collected` | 本次新写入的已核验答案条目；客观题按单题，代码按完整文件解答 |
| `duplicates` | 已核验但题库中已有的答案条目；不是新增，也不是采集失败 |
| `skipped` | 跳过上下文的次数，可能是一份作业或一个关卡，不统一按题计数 |
| `unconfirmed` | `skipped` 中因证据不足而未确认的子集，不再与 skipped 相加 |
| `failed` | 读取或采集失败事件次数，不是“答错题数” |
| `reasons` | 分类计数；既可能说明跳过，也可能记录定位或结束事件，不保证总和等于 skipped |

例如，一次合成结果为新增 0 条、重复 18 条、跳过 4 次，说明至少有 18 条已核验内容被识别为已有答案；新增为 0 不能解释成“没有读取到答案”。如果 4 次跳过由 1 份没有满分关卡的作业、2 个缺少通过源码证明的代码关卡和 1 个媒体内容未核验的客观题关卡组成，再附带 1 次达到时限事件，reasons 的总和为 5；时限事件不是第 5 道跳过的题。重复单题不反复追加验证，原有候选不覆盖。

新增 `full_score_levels` 表示分数表中的满分关卡数，`located_levels` 表示详情身份匹配且满分的实际读取关卡数；`objective_questions_read/objective_answers_read` 表示读到的题目和非空实际作答条目（不代表已验证/已导入）；`programming_files_read` 表示读取的源码文件数。不能把成功结束扫描或读到答案直接展示成导入成功。

### 管理端常见原因

| 原因码 | 管理员应如何理解 | 常见单位 |
| --- | --- | --- |
| `no_confirmed_full_score` | 该作业没有任何已确认满分关卡，本次无需继续读取答案 | 作业 |
| `programming_submitted_source_unproven` | 已读取当前编辑器代码，但缺少同账号、关卡及精确源码哈希对应的正式通过记录；不代表代码错误 | 关卡 |
| `programming_evaluation_unconfirmed` | 通过状态、编译或测试结果不足以证明该代码关卡已正式通过 | 关卡 |
| `objective_media_content_unproven` | 题干、选项或说明包含媒体，采集器未核验媒体实际内容；不是图片数量 | 关卡 |
| `objective_answer_evidence_missing` | 实际答卷、完整性或正确性证据不足，不能确认整套答案 | 关卡 |
| `no_readonly_game_locator` | 作业存在满分关卡，但没有足够证据定位已有运行任务；采集器不会创建运行环境 | 作业 |
| `request_byte_or_time_limit` | 本次达到读取或时间上限，记录一次结束事件 | 事件 |

管理端详情保留新增、重复、跳过、未确认、失败及逐项原因；列表使用带单位的统计，不把这些数字合并成“已采集题数”。没有通过源码绑定的外部历史编程解答仍不会作为新答案入库。客观题执行链路能够读取图片并不等于只读采集已核验历史媒体内容，两个链路的证据须分别成立。

## 固定上限

扫描顺序执行，全服务仍只有一个活跃扫描槽；答题任务使用 jobs 有界 worker/queue/per_owner_pending/per_owner_running，但采集不会占用多个扫描槽。每类列表 5 页，20 课堂、100 作业、500 关卡、3 分钟；扫描进行中及已结束课堂并去重。超页标记 limit_reached。最多 1,200 请求，单响应 2 MiB，总响应 64 MiB，读取间隔至少 100ms，单请求 20 秒。拒绝重定向、跨源、非 GET 和非允许路径。next_game 和分页循环均去重。本地种子和正式 run 查询最多 500 条。

达到时间、读取预算或清单数量上限，且没有读取失败的采集任务，以 `succeeded` 表示“本次分批采集已结束”，结果保留 `limit_reached=true`、`scan_complete=false`，并通过 `stop_reason` 区分 `time_limit`、`read_budget_limit` 与 `scan_limit`。这不是“整个账号已采集完成”的承诺，也不会自动放宽上限或重放扫描。尚未覆盖的内容需要后续一次新的只读扫描继续发现；当前没有持久化断点游标。

未知错误、真实读取失败与主动取消仍保留相应失败或取消语义，不能只凭 `limit_reached` 将所有异常转成成功。历史版本曾将正常达到时限的采集记录保存为 `failed / execution_failed`；原始记录不改写。管理端只在采集模式、`limit_reached=true`、`failed=0` 且具有明确限额结束事件等条件同时成立时，将这类历史记录解释为“本次采集达到上限，已保存结果”，不纳入需人工处理的异常计数；其他失败仍正常显示。

## 验证

相关包的普通和 race 测试覆盖只读方法与路由、响应/总字节/请求/页面限制、取消、跨关卡误匹配、代码正式哈希桥接及编辑器变化、客观题满分和实际作答、重复采集、owner 隔离、队列满仍成功登录、快速登录和重启不重放。mock HTTP 使用本地 httptest，不登录真实平台、不评测、不调用模型。

## 生产定位缺口与真实协议依据

v0.1.20 的生产结果曾读到 68 作业但 0 关卡：56 个满分作业没有只读 game 种子。该结果不是历史答案采集验收成功。本次补上的是独立于本站种子的已有 runtime 清单。

已检查 HAR 的 homework_commons 列表、header_info、student_works、作业详情和 shixun_challenge_data。列表/详情/header 提供 myshixun 与作业标识，student_works 提供数字 myshixun ID，但都不能直接当 game。公开编译资源中的 `student_works/{work}/query_game_url.json` 为查看学生代码入口，学生本人真实请求返回 API 403，因此未将其加入采集白名单。`myshixuns/{identifier}/challenges.json` 的 GET 服务调用、默认 GET helper 和 runtime 参数来源均已核实，学生请求返回既有 game 列表。没有启用 start/active/reset/version_delete 等有副作用 GET。

真实读取样本和响应保存在忽略目录 `artifacts/collection-locator/`，不提交账号、HAR、响应或数据库。真实课程样本读取12项作业、63个满分关卡，全部63个通过只读清单定位；其中31个代码关卡读到31份当前文件，仍因缺少已提交源码绑定跳过。32个客观题关卡读到324道题目及已提交作答；5关含图片，保持内容证据门槛跳过，其余27关的272个文字题答案已按满分答卷核验写入全新空题库。最终失败数0、未触及限制，已核对数据库272条独立已验证答案和0条编程解答。该样本不是生产截图的68作业账号；不能将其统计冒充该账号重扫结果。完整分项数据见本地采集报告。未调用模型或重新提交、评测。
