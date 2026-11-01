# 头歌妈妈 · touge.mom

头歌妈妈提供面向同学的课程执行服务。用户使用头歌账号登录，选择要处理的课堂，或一键处理全部活动课堂中的未完成作业，然后查看服务进度。用户端和管理端采用暖纸色、深棕顶栏、珊瑚强调色与用户提供的头歌妈妈图形 logo；费用、模型配置和内部故障由管理员处理。

头歌账号就是平台账号，无需另行注册、设置本站密码。首次登录由服务端核验头歌返回的数字用户 ID，自动建立平台身份与个人数据分区。登录仅需头歌账号与密码。卡密功能已下架；定价、支付、套餐和周期额度尚未实现，不将模型成本展示成用户余额。

## 用户端与管理端

用户端保留三个主要动作：登录、选择课堂或一键处理全部未完成作业、查看完成状态。遇到执行异常时显示服务处理中，由管理员查看内部原因并处理，不要求用户理解模型、候选、自测和协议错误。

管理端供服务人员查看用户状态、任务详情、模型台账汇总和操作审计；可以管理账号状态（禁用时撤销会话并请求停止任务）、停止任务。管理员权限来自服务端配置与角色记录，不由头歌教师身份或前端页面切换授予。

登录页提供《用户准则》弹窗，并在登录前提示做题数据收集与题目、答案复用用途。

登录后的左下角展示真实的题库概况，包括已验证可复用客观题、编程题和合计数量，并提供服务说明和《用户准则》入口；移动端在主内容下方展示。数量按题目内容去重，不等于课程覆盖率。

实际 API、身份隔离、用户准则和当前限制见 [平台与运维说明](docs/PLATFORM.md)。

## 服务架构

```text
用户端 / 管理端（web/src，TypeScript）
  ↓ 同源应用 API、HttpOnly 站内会话
Go HTTP 服务（cmd/web + internal/web）
  ├─ internal/platform：数字身份、个人分区、角色、审计
  ├─ internal/jobs：持久任务记录、有界并发与重叠目标调度
  └─ internal/taskservice：课程 / 全部课堂 / 作业执行
      ├─ internal/solver、answer、questionbank：求解、模型与题库
      └─ internal/educoder：唯一 Go 协议客户端
          ↓ 动态签名、服务端内存 CookieJar
        Educoder API
```

网页请求返回任务 ID，任务由当前 Go 进程内的 worker 和 goroutine 异步执行，不会为每次请求启动 CLI 子进程。全站、单账号、模型请求分别设并发边界；同账号的课程、整份作业和单关目标存在包含关系时串行调度，避免重复写入。

课程任务首先确认课堂属于当前账号，按完整必做关卡分数跳过已经满分的作业，再执行其他作业。上游明确标记为“选做”的关卡不执行，不计入待完成数量或完成分母；整份作业只有选做关卡时，不列入本次待完成作业。不能单凭关卡分值为零推断选做。某份作业需要人工处理时，保留诊断并继续处理其他作业；单份作业内仍保持逐关身份校验、有限修复与最终满分复核。结果未知的写入不会自动重放。详细语义见 [异步执行](docs/async-jobs.md)。

原站编译 UI、资源复用服务和 Node/TypeScript 协议客户端已经移除。TypeScript 仅用于浏览器，Go 承担协议、AI、题库和应用 API；Node.js 仅用于构建和前端测试。

## 本地启动

需要 Go 1.25+、Node.js 25+、npm：

```powershell
npm ci --ignore-scripts
npm run build
go run ./cmd/web
# http://127.0.0.1:11454/

# Windows 后台启动和停止
.\scripts\start-web.ps1
.\scripts\stop-web.ps1
```

本地 HTTP 使用 `WEB_COOKIE_SECURE=false`；线上 HTTPS 使用 `true`。没有配置模型密钥时，登录和课程浏览仍可用，执行服务由管理员配置后开启。`/healthz`、`/version` 与 `X-App-Version` 返回实际运行版本。

| 环境变量 | 默认值或用途 |
| --- | --- |
| `HTTP_ADDR` | `0.0.0.0:11454` |
| `APP_DATA_DIR` | 本地 `data`；容器 `/data` |
| `WEB_STATIC_DIR` | 本地 `web` |
| `SESSION_TTL` | `8h`，单进程内存会话 |
| `WEB_COOKIE_SECURE` | 本地 HTTP 为 `false`，线上 HTTPS 为 `true` |
| `WEB_ORIGIN` | 可选精确来源；生产可设 `https://touge.mom` |
| `PLATFORM_ADMIN_USER_IDS` | 经核验的头歌数字用户 ID，逗号分隔，默认无引导管理员 |
| `JOB_WORKERS` / `JOB_QUEUE_SIZE` | `16` / `256` |
| `JOB_PER_ACCOUNT_PENDING` / `JOB_PER_ACCOUNT_RUNNING` | `16` / `4` |
| `JOB_TIMEOUT` | `30m` |
| `MODEL_CONCURRENCY` / `MODEL_TIMEOUT` | `8` / `2m`；实际供应商 RPM 由供应商账号决定 |
| `DEEPSEEK_API_KEY_FILE` | 推荐用 Docker secret 的文件路径 |

## 数据与身份边界

`platform.sqlite` 保存已核验身份、角色与审计；`jobs.sqlite` 保存任务记录和幂等键；`questionbank.sqlite` 保存题库、验证证据与模型使用台账。三个数据库都位于应用数据目录，不能进入 Git、镜像或发布归档。

上游密码不落库，上游 Cookie 和站内会话留在 Go 内存；浏览器只持有站内随机 HttpOnly Cookie。重启后需要重新登录，已持久化的个人身份与任务历史保留。未完成任务会被标记为中断或待核验，不会被自动重放。

旧调试版本按登录名 hash 保存的任务不会自动认领到新平台身份。同名登录名、教师标识或客户端传入的 owner 均不能作为迁移依据。当前为单实例 SQLite 服务，不宣称支持分布式 worker、跨节点会话或支付结算。

## 协议与维护工具

可选 `cmd/educoder` 和 `cmd/coursebatch` 继续用于维护、协议验证和显式批次调试；它们复用 Go 协议与求解模块，用户端不依赖这些命令。凭据仅通过进程环境、标准输入或本地忽略配置提供，不能写进源码或命令示例的真实值。

- [代码题协议](docs/programming-protocol.md)：题目、代码读取、自测、提交和进度协议。
- [已通过答案采集](docs/verified-collection.md)：登录后的只读核验、证据门槛与采集限制。
- [架构](docs/architecture.md)：模块边界与旧方案清理情况。
- [平台与运维](docs/PLATFORM.md)：用户、管理员、用户准则、API 与数据隔离。
- [部署](deploy/README.md)：Docker、11454、备份和回滚。

## 验证与发布

```powershell
go test ./...
go vet ./...
npm test
npm run typecheck
python scripts/check-private-data.py
.\scripts\release.ps1 -Message 'Describe the completed change'
```

每个完成的小改动增加 patch、更新 CHANGELOG、通过相关测试和隐私检查，再创建 commit、annotated tag，并通过 SSH 推送到 `git@github.com:RoseKhlifa/Educoder-tools.git`。不修改已发布标签，不 force-push。

服务监听 11454，部署主机通过本机 `ssh main` 连接；`touge.mom` 的 HTTPS 反代由用户在 1Panel 管理。本项目不创建 Nginx/OpenResty 配置。


截止状态：用户端作业列表只展示仍可安排的作业。服务端优先使用头歌返回的 `已截止 / 已结束 / closed / ended` 状态，并按 `end_time / deadline / end_date` 的上海时间兜底判断；缺少日期不当作截止。补交作业只有在上游明确给出有效延长截止时间时才保留。`course` / `all` 任务在规划和每份作业开始前都会复核截止状态，已截止作业不会进入运行实例、模型调用或提交；执行中跨过截止时间时停止后续关卡。历史题库采集仍读取原始列表，以保留已完成作业的核验记录。


管理端任务列表按用户、课程、作业与当前关卡、状态及带单位的进度、具体原因分别展示。姓名通过平台身份在服务端关联，不依赖前端最近100位用户列表；课程与作业名称来自已验证的上游列表及历史执行结果，并保存在仅用于显示的目录中。历史账号缺乏身份关联时明确标记“历史账号未关联”，不按登录名哈希认领。原始任务标识、原因码与诊断放在默认折叠的技术记录中；普通用户任务接口不增加这些管理信息。
