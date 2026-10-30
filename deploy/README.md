# 工作台部署

唯一 Web 入口为 cmd/web，提供手写工作台和应用 API，监听 11454。原站编译资源及兼容服务已经移除，运行时使用 Go；Node.js 只位于 Docker 前端构建阶段。

## Docker

~~~bash
export APP_VERSION="$(cat VERSION)"
export VCS_REF="$(git rev-parse HEAD)"
export WEB_COOKIE_SECURE=true
docker compose --project-name educoder-tools build
docker compose --project-name educoder-tools up -d --no-build
curl --fail http://127.0.0.1:11454/healthz
~~~

构建先按照锁文件编译 web/src，再构建 Go。最终镜像只包含 Go 可执行文件、CA 证书、web/index.html、web/styles.css 和编译后的 web/dist/*.js。没有 Node.js、浏览器 TS 源码、原站资源、账号、题库或抓包。容器使用非 root 用户、只读根文件系统、自动重启和有界日志。

服务器通过 ssh main 管理。用户在 1Panel 将 touge.mom 反代到 HTTP 11454 并终止 HTTPS；项目不创建或修改反代配置。

发布归档从已经推送的提交 git archive 生成，放在 /opt/educoder-tools/releases/<version>，Compose 项目名持续使用 educoder-tools：

~~~bash
bash deploy/install-release.sh VERSION FULL_COMMIT_SHA
~~~

安装器先构建镜像，再替换容器，并核验 /healthz 的顶层 status、version、commit。/version 与 X-App-Version 用于检查运行版本。登录会话在内存中，替换服务后重新登录。

## 配置与检查

HTTP_ADDR、WEB_STATIC_DIR、SESSION_TTL、WEB_COOKIE_SECURE 和可选 WEB_ORIGIN 的说明见根 README。HTTPS 域名设置 WEB_COOKIE_SECURE=true，本地 HTTP 调试设为 false。旧 REUSE_* 变量不再配置当前服务。

个人头像通过当前会话的 /api/avatar 读取，只访问固定官方源，在内存缓存。工作台不包含原站首页广告、轮播大图或宣传视频。

~~~bash
curl --fail http://127.0.0.1:11454/version
curl --fail http://127.0.0.1:11454/ | head
curl --fail http://127.0.0.1:11454/dist/app.js > /dev/null
~~~

首页应为“头歌学习工作台”。业务路径为 /api/auth/* 和 /api/courses/*，未登录请求返回 401 与错误 envelope。旧课堂页面入口回到工作台；旧资源和旧 .json 路由不提供原站界面。

## 回滚

保留前一发布目录与镜像，从所需目录执行其自身配置：

~~~bash
export APP_VERSION="$(cat VERSION)"
export VCS_REF=THE_RELEASE_FULL_COMMIT
# 旧版本的 Cookie 环境变量按对应版本说明设置。
docker compose --project-name educoder-tools up -d --no-build
curl --fail http://127.0.0.1:11454/healthz
~~~

回滚不迁移账号会话，也不修改上游作业。旧发布目录只用于恢复，不挂载进当前容器。当前部署未启动 AI worker 或题库管理，未来接入时再配置运行 API 和独立持久卷。

## 小版本

VERSION 是版本来源。完成的小改动增加 patch、更新 CHANGELOG、执行 Go 与浏览器测试、隐私检查、commit、annotated tag 和 SSH 原子推送。根 scripts/release.ps1 支持在基于最新主线的独立工作区发布。
