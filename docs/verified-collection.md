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

因此扫描成功只表示扫描结束，不表示已成功获取或入库历史答案。未确认及 reasons 会如实保留只读定位和答案证据的缺口。

工作台为这类任务单独显示“已通过任务采集”，展示扫描课堂、作业、关卡读取尝试，以及新增、重复、跳过、失败和中文原因。没有新增时显示“扫描结束，未新增答案”；关卡读取尝试为 0 时明确提示尚未获取到可入库的题目与答案。未确认是跳过记录的子集，不与跳过重复相加。原始任务 mode 和空作业编号不作为展示标题。

## 队列和接口

同一账号同日登录使用 UTC 日期键 `collection-v1-YYYY-MM-DD`；并发/重复登录复用 job。待执行的同账号同采集目标也会合并。`GET /api/collections` 列出本账号最近任务中的采集记录；`POST /api/collections` 接受空 body 或 `{}`，通过标准 `Idempotency-Key` 头显式创建新重试，不接受外部账号、目标或答案参数。

`GET /api/jobs/{id}` 返回状态、事件和统计，`POST /api/jobs/{id}/cancel` 取消。其他 owner 读取和取消返回 404。任务数据库只存公开参数、事件和统计，客户端和 Cookie 留在内存闭包。重启把未完成采集标记 interrupted，不自动重放。密码、运行密钥、WebSocket URL、签名 URL 不进入 job 或题库。

统计包含 courses/homeworks/levels、collected、duplicates、skipped、unconfirmed、failed、limit_reached 和聚合 reasons。collected/duplicates 按答案条目计（代码为完整文件解答、客观题为单题）；跳过/失败按上下文计，可能是课堂、作业或关卡；unconfirmed 为 skipped 的子集。重复单题不反复追加验证，原有候选不覆盖。

新增 `full_score_levels` 表示分数表中的满分关卡数，`located_levels` 表示详情身份匹配且满分的实际读取关卡数；`objective_questions_read/objective_answers_read` 表示读到的题目和非空实际作答条目（不代表已验证/已导入）；`programming_files_read` 表示读取的源码文件数。不能把成功结束扫描或读到答案直接展示成导入成功。

## 固定上限

扫描顺序执行，全服务只有一个活跃扫描槽，沿用 jobs 有界 worker/queue/per_owner_pending 和每账号串行机制。每类列表 5 页，20 课堂、100 作业、500 关卡、3 分钟；扫描进行中及已结束课堂并去重。超页标记 limit_reached。最多 1,200 请求，单响应 2 MiB，总响应 64 MiB，读取间隔至少 100ms，单请求 20 秒。拒绝重定向、跨源、非 GET 和非允许路径。next_game 和分页循环均去重。本地种子和正式 run 查询最多 500 条。

## 验证

相关包的普通和 race 测试覆盖只读方法与路由、响应/总字节/请求/页面限制、取消、跨关卡误匹配、代码正式哈希桥接及编辑器变化、客观题满分和实际作答、重复采集、owner 隔离、队列满仍成功登录、快速登录和重启不重放。mock HTTP 使用本地 httptest，不登录真实平台、不评测、不调用模型。

## 生产定位缺口与真实协议依据

v0.1.20 的生产结果曾读到 68 作业但 0 关卡：56 个满分作业没有只读 game 种子。该结果不是历史答案采集验收成功。本次补上的是独立于本站种子的已有 runtime 清单。

已检查 HAR 的 homework_commons 列表、header_info、student_works、作业详情和 shixun_challenge_data。列表/详情/header 提供 myshixun 与作业标识，student_works 提供数字 myshixun ID，但都不能直接当 game。公开编译资源中的 `student_works/{work}/query_game_url.json` 为查看学生代码入口，学生本人真实请求返回 API 403，因此未将其加入采集白名单。`myshixuns/{identifier}/challenges.json` 的 GET 服务调用、默认 GET helper 和 runtime 参数来源均已核实，学生请求返回既有 game 列表。没有启用 start/active/reset/version_delete 等有副作用 GET。

真实读取样本和响应保存在忽略目录 `artifacts/collection-locator/`，不提交账号、HAR、响应或数据库。真实课程样本读取12项作业、63个满分关卡，全部63个通过只读清单定位；其中31个代码关卡读到31份当前文件，仍因缺少已提交源码绑定跳过。32个客观题关卡读到324道题目及已提交作答；5关含图片，保持内容证据门槛跳过，其余27关的272个文字题答案已按满分答卷核验写入全新空题库。最终失败数0、未触及限制，已核对数据库272条独立已验证答案和0条编程解答。该样本不是生产截图的68作业账号；不能将其统计冒充该账号重扫结果。完整分项数据见本地采集报告。未调用模型或重新提交、评测。
