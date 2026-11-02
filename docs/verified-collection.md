# 登录后的已通过答案采集

登录成功后，HTTP 服务在进程内提交 `collect_verified` job，返回登录结果及 `collection.job_id/status/reused`。扫描不依赖模型配置，不运行 CLI，不创建实训实例，不写编辑器、不自测、不评测、不提交、不调用模型。队列满时仍然登录成功，响应 `collection.queued=false` 和错误码。

采集与正式答题的整关满分缓存分别保存。只读采集当前仍跳过缺少图片内容核验的含图关卡；正式答题已经下载并验证图片时，则可用当前图片字节与 SHA256 引用匹配整关历史满分答卷。该路径不要求登录采集先成功，也不把“已完成”标记直接当成可复用单题答案。图片内容或题目快照改变时重新匹配，不能仅凭作业同名复用。

## 已核实的数据来源和边界

现有接口包括课堂列表 `GET /api/users/{login}/courses.json`；作业列表 `GET /api/courses/{course}/homework_commons.json`；关卡分数 `GET /api/homework_commons/{id}/shixun_challenge_data.json`；任务详情 `GET /api/tasks/{game}.json`；当前源码 `GET /api/tasks/{game}/rep_content.json`。采集作业时使用所有状态 `status=0`，不限定 `type`。

- 当前源码接口没有已提交版本的 digest、revision 或源码快照。满分不能证明当前编辑器代码就是当时通过的代码。代码必须额外匹配本账号、本 course/homework/game、题目与文件契约以及精确源码哈希的本地正式通过记录；候选/保存/提交哈希必须相等，正式测试必须全部通过。当前详情也必须已通过，返回测试全部编译及运行通过，再读分数和文件内容不得变化。满足条件的代码已经在题库，计为重复；外部历史代码缺少提交绑定时记录 `programming_submitted_source_unproven`。未假设一个不存在于已观察协议中的“通过源码下载”端点。
- 客观题只使用目标任务 `choose_test_cases.test_sets.actual_output` 中的实际作答。每题必须有唯一匹配的 position、题型和名称。已返回的逐题 false 或正确题数冲突直接拒绝。真实任务的教师隐藏结果模式会同时返回 null 的 `result/choose_correct_num`，此时要求完整已提交答卷（两个提交完整性字段均为 true、题数与记录数准确、每题实际作答非空且符合选项），再绑定当前目标关卡独立读取的精确满分。题目 challenge_id 必须匹配目标，选择题 ID 不得重复。再次读取任务及分数，题干、选项顺序和答卷一致后，按单题入独立 objective 表。提交字段只证明答卷完整性，正确性来自目标关卡精确满分；不会使用 standard_answer 推断答案，也不会把 had_done、已提交或作业汇总通过数单独作为证明。
- 只覆盖已观察的 question_type=0、单选/多选 category；其他题型跳过。图片/公式图缺少实际内容哈希时记录 `objective_media_content_unproven`，不拿 URL 代替内容证明，不下载未知资源。
- 作业列表返回的 `myshixun_identifier` 不是 game。采集器先 GET `/api/shixun_homeworks/{homework}/header_info.json`，匹配 homework、student_work、shixun 和既有 myshixun，然后 GET `/api/myshixuns/{myshixun}/challenges.json?homework_common_id=...` 读取已有运行的 game 列表。这一路径来自任务页编译资源的服务导出 g6 及 GET helper，有学生本人真实响应验证。`shixun_exec` 虽是 GET，但可能创建运行实例，始终禁止调用。
- game 列表返回 identifier、position、name、status，而非 challenge_id。header 有完整 challenges 时按唯一位置和准确名称交叉定位；真实部分 header 没有 challenges，此时仅允许与该作业分数表中唯一同名、精确满分的 challenge_id 匹配。只有 runtime status=2 且名称/类型符合，才读取详情；详情再匹配 runtime identifier、game、challenge ID/position/name、homework、shixun 和正满分。重复/不一致清单整体拒绝。泛化通过标记不授权读取未满分详情。
- 兼容种子仍可来自正常读取后记录的 owner 隔离目标、历史正式通过 run 或列表已有明确任务链接。链接只提取标识，不访问其中的主机和签名查询。新用户不需要本站历史记录即可通过已存在 myshixun 列表发现任务。缺少或不一致的定位证据继续记录具体原因。
- 部分满分作业必须先知道 game 对应的 challenge_id 且该关卡满分，才能读取详情，不能跟随未知 next_game。只有作业关卡列表全部精确满分时，才允许从种子跟随 next_game；每一跳核对 game、作业、实训、关卡、运行实例及得分。缺失或重复的身份均跳过。

当前默认会持续遍历完整清单，不因扫描耗时或累计数量提前结束。只有完整遍历且没有读取失败才返回 `scan_exhausted=true / scan_complete=true`；完成遍历与核验全部历史答案是不同概念。已核验入库的内容持续保留，未证实的编辑器代码或媒体题不会被标为已验证答案。未确认及 reasons 如实记录只读定位和答案证据的缺口。

当前服务用户端不再显示早期调试工作台的采集统计与原因。登录页的《用户准则》已明确告知已通过任务扫描及题目、答案和代码的收集复用用途；扫描权限与核验门槛保持不变。管理员通过管理任务接口读取扫描课堂、作业、关卡读取尝试，以及新增、重复、跳过、失败和原因。未新增不代表失败，也不能展示成成功获取了答案；未确认是跳过记录的子集，不与跳过重复相加。普通用户任务列表过滤采集任务。

## 队列和接口

自动登录采集不再使用固定的同日键：同账号仍在排队或执行的采集合并，先前任务结束后新的已认证登录使用当前客户端安排新扫描，避免复用旧版限时、失败、繁忙跳过或中断记录。`GET /api/collections` 列出本账号最近采集记录的裁剪摘要；`POST /api/collections` 接受空 body 或 `{}`，通过标准 `Idempotency-Key` 头显式创建新重试，不接受外部账号、目标或答案参数。

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

## 完整扫描与执行调度

- 默认 `CollectionLimits{}` 表示不设置整次扫描时限、页数、课堂数、作业数或关卡数上限。进行中与已结束课堂分别翻页，课堂、作业和运行关卡去重；读取到真正空页才结束。上游提供总数时，提前空页、缺失列表或重复非空页会明确记录读取/分页异常，不把不完整清单当作扫描完成。
- 默认 `CollectionBudget` 的累计请求数、累计响应字节均为 0（不限累计量）。保留单响应 2 MiB、单请求 20 秒及读取间隔至少 100ms；只允许既定同源 GET 端点，不跟随重定向。临时网络、429、5xx 错误采用可取消的有限次请求重试；这不是整次采集的耗时上限。
- 每个 Collector 顺序读取；jobs 默认使用 1 个独立采集 worker，与答题 worker 分开。其他采集留在有界队列等待，不再返回 `collector_busy` 跳过。账号任务去重、队列容量、取消、禁用账号及服务关闭的协作停止保持有效。
- 本地目标按当前账号和当前作业查询，正式源码证据按准确目标核验，不再只查询全账号最近 500 条。没有创建运行环境或放宽答案验证条件。
- `scan_exhausted` 只表示已走到全部清单末尾；`scan_complete` 还要求没有失败或中断。新增、已有、跳过、未确认和失败各自统计，不把已读取内容或缺少证据的答案当成入库成功。

代码中的显式正数限额仍供主动要求有限扫描的内部调用和回归测试使用，生产默认不会设置。历史 `time_limit / read_budget_limit / scan_limit` 和 `collector_busy` 记录保留原始状态及解释，不改写为本次全量完成。服务重启后不保存客户端闭包或 Cookie；再次认证登录会安排新扫描，已经保存的题库结果保留并去重。当前没有持久化分页断点，重新扫描从清单开始。

## 验证

测试覆盖默认完整分页、超过旧课堂/作业/关卡/请求数量、累计流量不限但单响应受限、分页异常、单请求重试、无整次任务 deadline、采集独立队列、答题不被占用、同账号去重及登录后重新采集、取消与权限撤销、只读路由及身份/源码/答案核验。显式有限扫描保持相应终止语义。测试使用本地 httptest 和合成题库，不登录真实平台、不评测或调用模型。

## 生产定位缺口与真实协议依据

v0.1.20 的生产结果曾读到 68 作业但 0 关卡：56 个满分作业没有只读 game 种子。该结果不是历史答案采集验收成功。本次补上的是独立于本站种子的已有 runtime 清单。

已检查 HAR 的 homework_commons 列表、header_info、student_works、作业详情和 shixun_challenge_data。列表/详情/header 提供 myshixun 与作业标识，student_works 提供数字 myshixun ID，但都不能直接当 game。公开编译资源中的 `student_works/{work}/query_game_url.json` 为查看学生代码入口，学生本人真实请求返回 API 403，因此未将其加入采集白名单。`myshixuns/{identifier}/challenges.json` 的 GET 服务调用、默认 GET helper 和 runtime 参数来源均已核实，学生请求返回既有 game 列表。没有启用 start/active/reset/version_delete 等有副作用 GET。

真实读取样本和响应保存在忽略目录 `artifacts/collection-locator/`，不提交账号、HAR、响应或数据库。真实课程样本读取12项作业、63个满分关卡，全部63个通过只读清单定位；其中31个代码关卡读到31份当前文件，仍因缺少已提交源码绑定跳过。32个客观题关卡读到324道题目及已提交作答；5关含图片，保持内容证据门槛跳过，其余27关的272个文字题答案已按满分答卷核验写入全新空题库。最终失败数0、未触及限制，已核对数据库272条独立已验证答案和0条编程解答。该样本不是生产截图的68作业账号；不能将其统计冒充该账号重扫结果。完整分项数据见本地采集报告。未调用模型或重新提交、评测。

## 用户端题库数量

用户端题库概况来自已登录只读汇总 `/api/question-bank/summary`，按客观题、编程题、绘图题统计库存，包含尚未验证的完整候选；这些库存量不等于可信答案数量。`total_questions` 为三类之和，个人归因另由 `contributed_questions` 返回。求解复用仍依赖正式通过证据，不因入库即自动信任。具体准入与缓存行为见 [PLATFORM.md](PLATFORM.md)。

终端操作题的 `rep_content` 通常只是“请使用命令行”的占位文件，无法从只读接口还原真正执行的命令。因此采集以 `terminal_submitted_script_unobservable` 跳过，不将其作为缺失源码的错误答案。本站执行终端题时，绑定当前任务与脚本的执行回执、正式评测与课堂满分核验齐全后，保存当前工作区通过证据。脚本可能依赖之前操作留下的文件，未经干净环境独立复现，不作为共享可信答案或题库贡献。
