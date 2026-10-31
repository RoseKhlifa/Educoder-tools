# 登录后的已通过答案采集

登录成功后，HTTP 服务在进程内提交 `collect_verified` job，返回登录结果及 `collection.job_id/status/reused`。扫描不依赖模型配置，不运行 CLI，不创建实训实例，不写编辑器、不自测、不评测、不提交、不调用模型。队列满时仍然登录成功，响应 `collection.queued=false` 和错误码。

## 已核实的数据来源和边界

现有接口包括课堂列表 `GET /api/users/{login}/courses.json`；作业列表 `GET /api/courses/{course}/homework_commons.json`；关卡分数 `GET /api/homework_commons/{id}/shixun_challenge_data.json`；任务详情 `GET /api/tasks/{game}.json`；当前源码 `GET /api/tasks/{game}/rep_content.json`。采集作业时使用所有状态 `status=0`，不限定 `type`。

- 当前源码接口没有已提交版本的 digest、revision 或源码快照。满分不能证明当前编辑器代码就是当时通过的代码。代码必须额外匹配本账号、本 course/homework/game、题目与文件契约以及精确源码哈希的本地正式通过记录；候选/保存/提交哈希必须相等，正式测试必须全部通过。当前详情也必须已通过，返回测试全部编译及运行通过，再读分数和文件内容不得变化。满足条件的代码已经在题库，计为重复；外部历史代码缺少提交绑定时记录 `programming_submitted_source_unproven`。未假设一个不存在于已观察协议中的“通过源码下载”端点。
- 客观题只使用目标任务 `choose_test_cases.test_sets.actual_output` 中的实际作答。每题必须有唯一匹配的 position、题型、名称、true 结果，明确正确题数与总数相符。题目 challenge_id 必须匹配目标，选择题 ID 不得重复。目标关卡精确满分，再读任务及分数，题干、选项顺序和答案一致后，按单题入独立 objective 表。不会使用 standard_answer 推断答案，也不会把 had_done、已提交或作业汇总通过数作为证明。
- 只覆盖已观察的 question_type=0、单选/多选 category；其他题型跳过。图片/公式图缺少实际内容哈希时记录 `objective_media_content_unproven`，不拿 URL 代替内容证明，不下载未知资源。
- 作业列表不保证返回 game。`shixun_exec` 虽是 GET，但可能创建运行实例，采集器禁止调用。种子只来自正常用户读取详情后记录的 owner 隔离目标、历史正式通过 run 的目标，或列表已有的明确 `/tasks/{course}/{homework}/{game}` 链接。链接只提取标识，不访问其中的主机和签名查询。没有种子时记录 `no_readonly_game_locator`。
- 部分满分作业必须先知道 game 对应的 challenge_id 且该关卡满分，才能读取详情，不能跟随未知 next_game。只有作业关卡列表全部精确满分时，才允许从种子跟随 next_game；每一跳核对 game、作业、实训、关卡、运行实例及得分。缺失或重复的身份均跳过。

因此扫描成功不等于全部历史答案都已采集。未确认及 reasons 会如实保留只读定位和答案证据的缺口。没有修改前端 UI，正常答题能力继续保留。

## 队列和接口

同一账号同日登录使用 UTC 日期键 `collection-v1-YYYY-MM-DD`；并发/重复登录复用 job。待执行的同账号同采集目标也会合并。`GET /api/collections` 列出本账号最近任务中的采集记录；`POST /api/collections` 接受空 body 或 `{}`，通过标准 `Idempotency-Key` 头显式创建新重试，不接受外部账号、目标或答案参数。

`GET /api/jobs/{id}` 返回状态、事件和统计，`POST /api/jobs/{id}/cancel` 取消。其他 owner 读取和取消返回 404。任务数据库只存公开参数、事件和统计，客户端和 Cookie 留在内存闭包。重启把未完成采集标记 interrupted，不自动重放。密码、运行密钥、WebSocket URL、签名 URL 不进入 job 或题库。

统计包含 courses/homeworks/levels、collected、duplicates、skipped、unconfirmed、failed、limit_reached 和聚合 reasons。collected/duplicates 按答案条目计（代码为完整文件解答、客观题为单题）；跳过/失败按上下文计，可能是课堂、作业或关卡；unconfirmed 为 skipped 的子集。重复单题不反复追加验证，原有候选不覆盖。

## 固定上限

扫描顺序执行，全服务只有一个活跃扫描槽，沿用 jobs 有界 worker/queue/per_owner_pending 和每账号串行机制。每类列表 5 页，20 课堂、100 作业、500 关卡、3 分钟；扫描进行中及已结束课堂并去重。超页标记 limit_reached。最多 1,200 请求，单响应 2 MiB，总响应 64 MiB，读取间隔至少 100ms，单请求 20 秒。拒绝重定向、跨源、非 GET 和非允许路径。next_game 和分页循环均去重。本地种子和正式 run 查询最多 500 条。

## 验证

相关包的普通和 race 测试覆盖只读方法与路由、响应/总字节/请求/页面限制、取消、跨关卡误匹配、代码正式哈希桥接及编辑器变化、客观题满分和实际作答、重复采集、owner 隔离、队列满仍成功登录、快速登录和重启不重放。mock HTTP 使用本地 httptest，不登录真实平台、不评测、不调用模型。
