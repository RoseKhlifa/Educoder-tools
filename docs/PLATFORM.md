# 平台身份、服务流程与管理运维

本文描述当前代码实现。头歌妈妈使用已选定的 A 方向蓝白界面，用户端只承担登录、选择课程或一键处理全部未完成作业、查看服务状态；管理员负责内部排障、模型配置、用量核对和账号准入。模型用量不是用户余额，当前没有支付结算或付费套餐。

## 身份与个人数据分区

1. 用户提交头歌登录名和密码，Go 服务向上游核验。
2. 上游必须返回正整数 `user_id`；平台以此自动查找或建立本地身份，生成平台用户 ID 与独立 `tenant_id`。
3. 平台存储上游数字 ID、当前登录名、姓名、角色、状态和时间戳；不存储头歌密码、Cookie 或模型密钥。
4. 浏览器取得随机的站内 HttpOnly 会话 Cookie，上游客户端及 CookieJar 只留在服务端内存。
5. 个人任务 owner 从服务器当前身份产生，客户端没有指定 owner 或 tenant 的权限。

头歌账号就是本站账号，没有单独注册、本站密码、邮箱验证或密码重置流程。登录名与姓名只用于展示，不作为身份、租户或任务归属键。当前每人一个独立数据分区，不是包含多成员与组织角色的企业租户体系。

升级前按登录名 hash 保存的任务不自动认领。管理员也不能仅凭同名登录名把旧 owner 改成新身份；当前没有自动 owner 迁移工具。保留原数据库可以供管理员查历史，新的个人任务使用平台身份命名空间。

## 用户 API

所有业务响应使用 `{data, error, requestId}` envelope，下面的路径属于本站 Go 服务，不是上游原站接口。登录与登录配置开放，个人业务读取、任务写入和管理操作需要有效会话。

| 方法 | 路径 | 行为 |
| --- | --- | --- |
| GET | `/api/auth/config` | 返回 `access_card_required`，决定登录页是否显示必填卡密 |
| POST | `/api/auth/login` | 校验头歌账号并自动建档；接受 `login`、`password`、可选 `access_code` |
| GET | `/api/auth/me` | 返回本人公开头歌资料与平台 `id / tenant_id / role / status` |
| POST | `/api/auth/logout` | 删除当前站内会话并清 Cookie |
| GET | `/api/avatar` | 当前用户头像，固定官方来源与内存缓存 |
| GET | `/api/courses` | 本人课堂，默认 `status=processing` |
| GET | `/api/courses/:course/homeworks` | 所选课堂作业摘要 |
| POST | `/api/jobs` | 创建课程、全部课堂或兼容作业任务，202 返回任务标识 |
| GET | `/api/jobs` | 本人的近期服务任务摘要，不包含内部采集任务 |
| GET | `/api/jobs/:id` | 本人的任务状态和公开进度 |
| POST | `/api/jobs/:id/cancel` | 请求停止后续执行 |

课堂执行：

```json
{"scope":"course","mode":"submit","course":"COURSE","idempotency_key":"GENERATE_A_UNIQUE_KEY"}
```

全部活动课堂执行：

```json
{"scope":"all","mode":"submit","idempotency_key":"GENERATE_ANOTHER_UNIQUE_KEY"}
```

`Idempotency-Key` 请求头也可设置幂等键；如果 body 同时提供，二者必须相同。同一键用于不同 payload 会返回冲突；同账号相同目标的活动任务会复用。`course` 不允许 `homework_id` 或 `game`，`all` 不接受客户端指定的课堂、作业或 game。

兼容的整份作业请求为 `scope=homework`、`mode=submit`，需 `course` 与 `homework_id` 且不带 game。平台模式的公共 `/api/jobs` 只接受 `submit` 和 `homework / course / all`；技术用 `generate_only` 与 `selftest` 保留在 Go CLI，不可从普通服务 API 冒充完成状态。缺 scope 且带作业 ID 的旧 submit 请求统一扩展为整份作业。旧版缺 scope 的 submit+game 请求会归一化为整份作业，避免旧浏览器继续只做一关。

课程执行会重新读取账号活动课堂并核实所选课堂归属，读取完整作业列表再开始动作。已完成的判定来自完整、唯一的必做关卡分数清单与精确满分，不采用汇总完成数。上游明确标记为选做的关卡不执行，也不计入待完成清单、进度分母与最终满分核验；整份作业只有选做关卡时从本次作业计划中排除。零分值本身不能代替选做标记。单份作业中的必做关卡仍顺序执行。失败或平台写入结果未知的作业保留给服务人员，其他独立作业继续执行；不自动重放未知写入。

普通用户收到专门投影后的服务摘要，不返回模型次数、成本、预算、内部错误码、原始 events、模型原文或上游响应。需要管理员处理的状态不能被当作已通过；用户无需自行调模型或判断自测错误。具体执行机制见 [async-jobs.md](async-jobs.md)。

课程卡片的作业数是上游课堂作业总数，不代表待完成关卡数。只有打开一门课堂的作业详情时，服务才读取最多 100 份作业的关卡进度，最多 4 个并发读取，并设 12 秒总时限。完整且唯一的进度清单核验成功后返回 `requiredProgressKnown=true`、`requiredChallengeCount`、`finishedRequiredChallengeCount`；必做分母不含选做，全为选做的作业从详情列表排除。进度缺失、不完整、读取失败或超时不会回退到原始总关数猜测完成率，用户看到“安排时核验必做进度”。原始 `challengeCount` / `finishedChallengeCount` 字段仅为兼容保留，不作为必做完成判断。

## 管理员与管理 API

管理员需要使用同一个头歌登录流程。仅经过核验的头歌数字 ID 可通过 `PLATFORM_ADMIN_USER_IDS` 引导为管理员；头歌教师身份、姓名、客户端字段和页面导航都不授予管理权。普通用户访问管理 API 返回 403，未登录返回 401。

首次引导可先保持卡密关闭，使用本人头歌账号登录，从 `/api/auth/me` 的 `data.user.id` 核对真实数字 ID。把经核验的 ID 填入服务器环境，再重启并重新登录。多个数字 ID 用逗号分隔，不能填写手机号、登录名或平台随机用户 ID。

```dotenv
PLATFORM_ADMIN_USER_IDS=VERIFIED_NUMERIC_USER_ID
ACCESS_CARD_REQUIRED=false
```

这里的 `VERIFIED_NUMERIC_USER_ID` 是说明占位，部署时必须替换为实际正整数，或者留空；原样使用会导致配置校验失败。环境列表会在登录时提升并持久化管理员角色，移除环境列表中的 ID 不会自动降低既有角色。当前没有通过 HTTP 任意授予/降低管理员角色的接口；如需立即收回访问，可由另一管理员禁用该账号。

| 方法 | 路径 | 数据或操作 |
| --- | --- | --- |
| GET | `/api/admin/overview` | 用户、运行容量、模型配置、卡密开关与台账汇总 |
| GET | `/api/admin/usage` | 同源使用台账汇总，非用户账单 |
| GET | `/api/admin/users` | 近期用户列表，`data.items`，固定上限 100 |
| POST | `/api/admin/users/:id/status` | `{ "status": "active" }` 或 `disabled`，`:id` 为平台用户 ID |
| GET | `/api/admin/jobs` | 近期跨用户任务与原始受控诊断，上限 100 |
| GET | `/api/admin/jobs/:id` | 单任务详细内部记录 |
| POST | `/api/admin/jobs/:id/cancel` | 按管理员权限请求取消，并写审计 |
| GET | `/api/admin/cards` | 卡密掩码、状态、绑定与有效期，上限 100 |
| POST | `/api/admin/cards` | 签发随机卡密；完整 code 只在本次 201 响应返回 |
| POST | `/api/admin/cards/:id/revoke` | 撤销服务卡密 |
| GET | `/api/admin/audit` | 最近管理及登录审计，上限 100 |

禁用账号会删除该用户当前内存会话，并请求停止其排队和运行任务。重新启用不恢复旧会话或重放已停止任务，用户需重新登录。管理员不能通过账号状态接口禁用自己；没有独立的跨设备会话管理 HTTP 接口。

管理用量来自 `questionbank.sqlite` 的实际 `usage_ledger` 全表汇总，不是把近期 100 条任务费用加起来。字段包括 `model_calls`、`input_tokens`、`cached_input_tokens`、`output_tokens`、`cost_micro_usd` 、`reserved_micro_usd`、`settled_cost_micro_usd` 和 `unknown_calls`。金额单位为百万分之一美元；`cost_micro_usd` 是台账记录值（含未知计费时的估算），`settled_cost_micro_usd` 只汇总已确认计费，`reserved_micro_usd` 只汇总非 settled 记录的预留，`unknown_calls` 是待核对次数。不要将预留与记录成本重复相加。它不是供应商实时余额、发票、用户应付账单或周期额度。

任务详细结果可能包含模型失败原因、自测状态、评测阶段和逐关进度；管理员也不获得用户密码、上游 Cookie、模型密钥或完整模型响应。诊断不提供通用上游请求代理能力。

## 卡密准入的实际语义

`ACCESS_CARD_REQUIRED=false` 为默认配置。此时登录页不要求卡密，提交的 access_code 不构成付费或套餐权益，也不会按当前关闭状态自动兑换。管理员仍可生成卡密，为后续人工分发准备；没有在线购买、自动发货或定价规则。

开启为 `true` 后，普通用户每次登录都要提交有效 `access_code`。管理员免卡密，以便运维。数据库只保存高熵随机卡密的 SHA-256 摘要与掩码，原始完整卡密仅在签发响应出现一次，列表与审计无法读回原文。

签发 body 支持：

```json
{"label":"人工分发备注","reusable":false}
```

也可提供现有平台用户的 `bound_user_id`，或 RFC3339 格式的未来 `expires_at`。它们是准入绑定与有效期，不代表次卡、包月、金额或按课程计费：

- 默认 `reusable=false`：首次成功兑换原子绑定一个用户，同一用户后续可继续登录，其他用户不可使用。
- `reusable=true`：可由多个用户兑换；若另外指定 `bound_user_id`，仍只允许该用户。
- 不设置有效期则没有时间到期；设置后按服务端时间检查。
- 撤销卡密或到期后，该卡不再提供访问资格；如果用户还有另一张有效卡，仍有资格。

每个会话请求都重新检查账号状态与卡密资格。撤销卡密时服务会检查当前连接用户，失去最后有效资格的会话失效并请求停止任务；尚未兑换、未开启卡密校验或管理员账号不会凭此被误禁用。已经被上游受理的操作不能撤销。

## 当前安全措施与边界

- 平台身份必须来自服务端成功认证的数字 ID，任务数据按平台 owner 隔离，管理接口验证当前角色。
- 登录后轮换站内会话；Cookie 为 HttpOnly、SameSite=Lax，线上 HTTPS 配置 Secure。
- API 校验 Origin 与同源 Host，或精确 `WEB_ORIGIN`；不使用带凭据的通配 CORS。
- 登录按账号与 TCP 来源地址做进程内窗口限速；来源使用 RemoteAddr，不盲目信任客户端传入的转发 IP。经代理时来源地址可能共享。
- 请求体和标识有上限，作业清单与任务目标先核验；有界队列、并发、费用与时间边界避免无限执行。
- 禁用、签发/撤销卡密、管理员停止任务与成功登录写入审计；审计不记录密码、卡密原文、认证头或 Cookie。
- 上游 CookieJar、会话、登录限速窗口和执行闭包在内存；数据库与日志不应放在公开静态目录或 Git 中。

这些是当前实现的应用边界，不等于分布式隔离、完整渗透测试或正式安全认证。当前没有自动支付、退款、套餐计费、周期额度、MFA、跨节点会话或分布式 worker。

## 持久化、升级与运维

应用数据目录包含 `platform.sqlite`、`jobs.sqlite`、`questionbank.sqlite` 及 SQLite 运行时可能存在的 WAL/SHM。容器统一放在 `/data` 持久卷，平台数据库以仅属主读写权限创建。备份应停止应用后冷备整个数据卷，或使用 SQLite 一致性备份机制；运行中只复制主文件可能丢失 WAL 数据。

单机服务重启后个人身份、任务历史、卡密与审计保留；内存站内会话与上游客户端丢失，所有用户重新登录。任务记录会改为中断或待核验，不会自动重建客户端并继续上游写入。关闭浏览器或普通退出登录不会自动撤销已接收任务；显式取消、禁用账号和准入撤销才请求停止相应执行。

升级前备份整个数据卷并保留前一镜像。回滚源码不能恢复内存会话，不能撤销已发生的头歌提交，也不自动倒退数据库；如需恢复数据，应使用同一一致性检查点的完整备份，不能只恢复 platform 库而让任务 owner 对应关系丢失。

部署继续使用本机 `ssh main`、Docker、11454 与用户管理的 1Panel 反代。发布与健康检查步骤见 [部署说明](../deploy/README.md)。

后台执行独立于浏览器会话，每 2 秒检查持久化用户状态与卡密资格；退出浏览器后仍继续此检查。授权撤销或卡密到期会协作停止后续动作，已发送的上游提交仍需核验实际结果。内部题库采集繁忙时跳过本次采集，避免占住答题 worker 等待。


截止状态：用户端作业列表只展示仍可安排的作业。服务端优先使用头歌返回的 `已截止 / 已结束 / closed / ended` 状态，并按 `end_time / deadline / end_date` 的上海时间兜底判断；缺少日期不当作截止。补交作业只有在上游明确给出有效延长截止时间时才保留。`course` / `all` 任务在规划和每份作业开始前都会复核截止状态，已截止作业不会进入运行实例、模型调用或提交；执行中跨过截止时间时停止后续关卡。历史题库采集仍读取原始列表，以保留已完成作业的核验记录。
