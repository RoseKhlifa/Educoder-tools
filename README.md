# Educoder 学习工作台

唯一 Web 界面是 web/ 中的手写 TypeScript 工作台，唯一服务入口为 cmd/web。Go 同时提供页面、应用 API 和上游协议，默认监听 0.0.0.0:11454。

按用户最新决定，原站编译 UI、下载资源、页面兼容服务和 Node/TypeScript 协议客户端已经移除。TypeScript 仅用于浏览器；Node.js 仅用于构建和前端测试。线上最终镜像只含 Go 与轻量页面。

## 架构

~~~text
浏览器工作台（web/src → web/dist）
    ↓ 同源 /api/auth/*、/api/courses/*
Go Web（cmd/web + internal/web）
    ↓ 每个用户独立的服务端会话
Go 协议客户端（internal/educoder）
    ↓ 动态签名、内存 CookieJar
Educoder API

Go CLI（cmd/educoder）
    └─ solver → answer / questionbank → 同一 Go 协议客户端
~~~

工作台支持登录、查看课堂、选择课堂加载任务、搜索任务、任务详情和退出。详情中的官方实训链接会在外部平台新开标签。AI 自动答题当前通过 Go CLI 使用，尚未接入网页按钮、运行查询或后台 worker。

## 本地启动

需要 Go 1.25+、Node.js 25+、npm：

~~~powershell
npm ci --ignore-scripts
npm run build
go run ./cmd/web
# http://127.0.0.1:11454/

# Windows 后台运行：
.\scripts\start-web.ps1
.\scripts\stop-web.ps1
# 可选其他端口：
.\scripts\start-web.ps1 -Address '127.0.0.1:8081'
~~~

后台启动器先编译浏览器代码，再编译 Go；重启前验证监听进程确属当前工作区。/healthz、/version 和 X-App-Version 提供当前版本及提交号。页面静态文件使用 gzip、ETag 和重新验证缓存。

## Web API

| 操作 | 接口 |
| --- | --- |
| 登录 | POST /api/auth/login |
| 当前用户 | GET /api/auth/me |
| 退出 | POST /api/auth/logout |
| 课堂 | GET /api/courses |
| 作业 | GET /api/courses/:course/homeworks |
| 进入任务 | GET /api/courses/:course/homeworks/:homework/enter |
| 当前用户头像 | GET /api/avatar |

业务响应为 {data, error, requestId}。浏览器持有站内 HttpOnly 会话标识，上游认证只留在 Go 内存。头像按当前登录会话从固定官方来源读取并缓存，不落磁盘。旧课堂与个人页面地址回到工作台首页；旧静态资源和原站 .json 接口不作为新 UI 接口。

| 环境变量 | 默认值或用途 |
| --- | --- |
| HTTP_ADDR | 0.0.0.0:11454 |
| WEB_STATIC_DIR | web，包含 index.html、styles.css 和 dist/*.js |
| SESSION_TTL | 8h |
| WEB_COOKIE_SECURE | HTTPS 部署为 true；本地 HTTP 为 false |
| WEB_ORIGIN | 默认同源；分离开发时指定精确来源 |
| EDUCODER_API_BASE_URL | 默认官方 API 地址 |

## Go 协议终端

基础协议只维护 Go。凭据通过进程环境或 --password-stdin 输入；trace 只输出方法、路径及状态码。

~~~powershell
$env:EDUCODER_LOGIN = 'your-login'
$env:EDUCODER_PASSWORD = 'set-in-process-only'
go run ./cmd/educoder login --trace
go run ./cmd/educoder courses --json
go run ./cmd/educoder tasks --course COURSE --json
go run ./cmd/educoder enter --course COURSE --homework HOMEWORK_ID --json
go run ./cmd/educoder read --course COURSE --homework HOMEWORK_ID --game GAME --json
go run ./cmd/educoder self-test --course COURSE --homework HOMEWORK_ID --game GAME --path 'SERVER_FILE.cpp' --source 'LOCAL_FILE.cpp' --stdin-file 'INPUT.txt' --expected-file 'EXPECTED.txt' --json
go run ./cmd/educoder submit --course COURSE --homework HOMEWORK_ID --game GAME --path 'SERVER_FILE.cpp' --source 'LOCAL_FILE.cpp' --timeout 2m --json
go run ./cmd/educoder progress --course COURSE --homework HOMEWORK_ID --json
~~~

题干、精确文件名/后缀、当前代码、自测输出、正式评测及作业进度分别建模。请求顺序、字段和录制证据见 [代码题协议](docs/programming-protocol.md)。

## AI 与题库

Go 的 internal/solver 编排题目快照、双指纹、题库候选、模型生成、结构校验、自测、有限修复、正式评测及验证记录。SQLite 保存题目与候选源码，评测证据独立追加。模型输入包含精确文件路径、文件名、后缀和语言。

~~~powershell
$env:DEEPSEEK_API_KEY = 'set-in-process-only'
go run ./cmd/educoder solve --course COURSE --homework HOMEWORK_ID --game GAME --bank .\data\questionbank.sqlite --mode submit --idempotency-key run-1 --json
~~~

模式 generate_only 仅生成，selftest 保存并运行公开测试，submit 继续正式评测。编程题已验证编排范围是单文件。v0.1.10 新增独立选择题协议、题型路由及模型提示词，objective CLI 支持 generate_only 和 submit，使用 choose_build，不走代码文件、自测或代码评测流程。选择题题库和验证证据仍待独立扩展；UI 迁移保留这批已发布的 Go 实现。网页运行接口、持久卷、后台任务及题库管理另行接入。

## 验证与发布

~~~powershell
go test ./...
go vet ./...
npm test
npm run typecheck
.\scripts\release.ps1 -Message 'Describe the completed change'
~~~

npm test 编译并执行浏览器工作台行为测试；协议测试统一由 Go 执行。已移除 TypeScript HAR 重放命令，原始录制仍保留为本地私有材料。

每个完成的小改动增加 patch、更新 CHANGELOG、测试、commit、annotated tag 和 SSH push。发布器支持基于最新 origin/main 的独立分支，原子推送 HEAD 与版本标签；其他工作区的未提交修改不会被包含。工作中的开发任务应在发布前同步已更新的主线。

部署继续使用 Docker、11454、ssh main，以及用户管理的 1Panel 域名反代，详见 [部署说明](deploy/README.md)。账号、密码、Cookie、HAR、签名 URL、题库与日志不进入 Git、镜像或归档。

## 目录

~~~text
cmd/web/              唯一 Web 服务
cmd/educoder/         Go 协议与 AI CLI
web/src/             浏览器 TypeScript
web/dist/            构建输出，Git 忽略
internal/web/        API、DTO、会话、静态服务
internal/avatar/     真实头像读取与内存缓存
internal/educoder/   唯一协议客户端
internal/solver/     答题编排
internal/answer/     模型调用与输出校验
internal/problem/    题目快照和指纹
internal/questionbank/ SQLite 题库与验证证据
docs/                协议与架构文档
deploy/              Docker 部署
~~~

[架构与迁移决定](docs/architecture.md) 说明当前模块边界。CHANGELOG 中旧入口名称属于历史版本记录。
