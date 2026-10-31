# 头歌妈妈部署

唯一 Web 入口为 `cmd/web`，同时提供用户端、管理端和应用 API，监听 `0.0.0.0:11454`。Go 负责协议、身份、任务、AI 与题库；Node.js 仅位于前端构建阶段。服务器通过本机 `ssh main` 连接，用户在 1Panel 将 `touge.mom` 反代到 11454 并终止 HTTPS。本项目不创建或修改 Nginx/OpenResty 配置。

## Docker 与配置

`deploy/.env.example` 只包含无敏感值的示例。把实际部署参数放在服务器当前发布目录的 `.env` 或启动进程环境，不能提交账号、密码、Cookie、卡密原文或模型密钥。

```bash
export APP_VERSION="$(cat VERSION)"
export VCS_REF="$(git rev-parse HEAD)"
export WEB_COOKIE_SECURE=true
# 推荐在部署 .env 中设置精确外部来源 WEB_ORIGIN=https://touge.mom。
# PLATFORM_ADMIN_USER_IDS 填经核验的头歌数字用户 ID，不能填手机号。
# ACCESS_CARD_REQUIRED 默认 false，计费和支付尚未启用。
docker compose --project-name educoder-tools build
docker compose --project-name educoder-tools up -d --no-build
curl --fail http://127.0.0.1:11454/healthz
```

多阶段构建按照锁文件编译 `web/src`，再构建 Go。最终镜像包含 Go 可执行文件、CA 证书、入口 HTML/CSS 和编译后 ES modules，没有 Node.js、头歌原站资源、抓包、账号或运行数据库。容器使用非 root 用户、只读根文件系统、丢弃 Linux capabilities、`no-new-privileges`、自动重启和有界日志。

Compose 使用命名卷 `app-data` 挂载 `/data`。该目录包含：

| 文件 | 内容 |
| --- | --- |
| `platform.sqlite` | 数字用户身份、个人数据分区、角色、卡密摘要、兑换与审计 |
| `jobs.sqlite` | 异步任务、幂等键、内部状态与进度 |
| `questionbank.sqlite` | 题目、候选、验证证据、模型调用与费用台账 |

SQLite 使用期间还可能出现 WAL/SHM 文件，备份时必须保持一致性。不要把持久卷文件复制进新发布目录或镜像，也不要随升级删除该卷。

模型密钥保存在发布目录外，例如 `/opt/educoder-tools/secrets/deepseek_api_key`，通过 Docker secret 只读挂载为 `/run/secrets/deepseek_api_key`。安装器在没有密钥时创建空 secret：登录与课程浏览仍可使用，普通用户执行操作得到统一服务不可用提示；配置情况只向管理员展示。实际密钥由受控本机/服务器操作写入，不能放到 `.env.example`、Git、构建参数或日志。

## 首次管理员引导与卡密

默认无引导管理员，且 `ACCESS_CARD_REQUIRED=false`。先在平台使用本人头歌账号登录，从 `/api/auth/me` 的 `data.user.id` 核对头歌数字 ID，再写入服务器 `PLATFORM_ADMIN_USER_IDS`，重启并重新登录。多 ID 使用逗号分隔；错误的非整数配置会阻止启动，防止用登录名误授予权限。

环境列表在成功登录时提升并持久化管理员角色，移除列表中的 ID 不自动降低旧角色。没有 HTTP 角色授予接口。管理页面不能自行把普通用户变为管理员，头歌教师身份也不构成管理资格。

当前不收款、不扣用户模型额度。卡密只作为可选服务准入，管理员可生成、人工分发、绑定用户、设到期时间或撤销；完整随机卡密只在签发响应出现一次。启用 `ACCESS_CARD_REQUIRED=true` 后普通用户每次登录需要有效卡密，管理员免卡密。先完成管理员引导与卡密分发，再开启校验，避免普通用户缺卡无法进入。卡密不代表已设计好的次数、包月或按课程定价规则。详细语义见 [PLATFORM.md](../docs/PLATFORM.md)。

## 发布安装与核验

发布归档必须从已推送提交使用 `git archive` 生成，放在 `/opt/educoder-tools/releases/<version>`。Compose 项目名持续使用 `educoder-tools`，保持命名卷一致。从该发布目录执行：

```bash
bash deploy/install-release.sh VERSION FULL_COMMIT_SHA
```

安装器先构建镜像，再替换容器，核验 `/healthz` 顶层 `status`、`version`、`commit`。部署参数来自当前 shell 或该发布目录 `.env`；新的发布目录不会自动继承旧目录的 `.env`。每次升级均须保留管理员数字 ID、卡密开关、外部来源与模型 secret 路径等实际配置。

```bash
curl --fail http://127.0.0.1:11454/version
curl --fail http://127.0.0.1:11454/api/auth/config
curl --fail http://127.0.0.1:11454/dist/app.js > /dev/null
docker compose --project-name educoder-tools ps
```

还应从域名检查版本、登录页和浏览器同源访问。普通账号访问 `/api/admin/*` 应返回 403，未登录业务请求应返回 401。管理员登录后应能查看自己的角色、用户列表、任务、真实台账汇总与审计。健康检查成功只证明进程可响应，不证明模型、上游账号或真实任务一定可执行。

个人头像经 `/api/avatar` 使用当前会话读取，只访问固定官方源并在内存缓存。页面不加载头歌首页广告、轮播大图或宣传视频。旧课堂页面入口回到本站首页；旧编译资源和原站 `.json` 路由不提供原站界面。

## 并发与运行状态

默认配置为全站 16 个 worker、256 项等待队列、单账号 16 项待处理与 4 项并行、8 个模型并发。`course`、`all` 与包含的作业目标会串行调度，避免同账号重复写入；不同账号或独立课堂可并发。供应商 RPM 不由本地并发参数提高。

课程执行当前限制 20 个活动课堂、100 份作业、30 分钟，以及 $2 内部模型费用边界；单份作业 $0.50 上限仍有效。这些是服务运行边界，不是用户付费额度。预算和详细错误仅在管理端处理。

任务状态持久化，站内会话、上游客户端和执行闭包保存在单进程内存。升级或重启后所有用户重新登录，旧未完成任务标为中断或待核验，不能自动重放。禁用账号会撤销会话并请求停止其任务；已经到达头歌的提交不能撤销。

## 备份与回滚

升级前保留上一发布目录、镜像、实际部署配置，并对整个应用数据卷做一致性备份。可停止 app 后冷备卷，或使用 SQLite 一致性备份工具；运行中只复制 `.sqlite` 主文件可能丢失 WAL。备份包含身份和题库数据，应限制权限并置于发布归档之外。

恢复旧镜像时，从已保留的发布目录执行其自身配置：

```bash
export APP_VERSION="$(cat VERSION)"
export VCS_REF=THE_RELEASE_FULL_COMMIT
docker compose --project-name educoder-tools up -d --no-build
curl --fail http://127.0.0.1:11454/healthz
```

回滚代码不回滚头歌作业、不恢复内存会话，也不自动倒退 SQLite 数据。需要恢复数据库时应使用同一检查点的全量备份，不能只换 `platform.sqlite` 导致任务身份对应关系变化。旧调试版按登录名 hash 保存的任务不会自动分配给新平台用户。

当前是单机 SQLite 部署，没有共享会话、分布式 worker、支付、套餐、周期额度或跨实例调度。不要通过同时启动多个相同数据卷实例扩容；并发调整与容量诊断由管理端完成。

## 小版本

`VERSION` 是版本来源。完成的小改动增加 patch、更新 CHANGELOG、执行 Go 与浏览器检查、隐私审计，再创建 commit、annotated tag 并通过 SSH 原子推送。根 `scripts/release.ps1` 支持基于最新主线的独立工作区发布，不能夹带其他会话未完成工作或运行隐私文件。

服务器持久配置使用 `/opt/educoder-tools/config/service.env`（仅服务器保存，权限 0600）；安装脚本自动作为 Compose env-file 读取，使后续版本更新保留 `PLATFORM_ADMIN_USER_IDS / WEB_ORIGIN / ACCESS_CARD_REQUIRED`。模型密钥继续使用单独 secret 文件。

部署脚本在构建前要求至少 1 GiB 非特权用户可用空间；构建后用镜像内应用用户检查实际 `/data` 挂载可写，再替换在线容器。检查失败会停止切换。Docker 构建缓存需要另行维护，根用户保留块不能作为应用可用空间。
