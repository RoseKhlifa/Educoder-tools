# 编译静态资源复用与自有 Go 服务

## 当前决策

停止新增或改写前端 UI。直接复用 EduCoder 的 HTML、编译 JS、CSS、字体、图片、worker 和异步 chunk。

从 v0.1.4 起，按用户要求将首页轮播、宣传视频、开屏公告图片、站点横幅及其他确认的装饰位图替换为本地 SVG 和趣味文案。保留原页面结构；原有宣传视频位置显示静态图片，不再请求视频或自动播放。个人和教师头像、表情图集、作业帮助截图、任务编辑器功能资源继续保留。

以用户最新要求为准：**只运行自己的 Go 服务，不使用 Nginx、Caddy、网关或浏览器请求劫持。采用 B，仅修改部署地址常量。**

```text
原站编译前端（本地静态托管）
    /_assets/... -> Go http.FileServer -> 原站静态资源本地副本
    /api/...    -> Go 原站 API 兼容 handlers -> 已验证的 EduCoder 协议模块
```

原入口包的 `newBuild.API_SERVER` 是绝对地址 `https://data.educoder.net`。HTML 选择 `window.ENV='newBuild'`。将 API Base URL 改成空串后，原代码继续请求 `/api/...`，由同一个 Go 服务的 handlers 接收。

同一入口包还将 webpack publicPath 和 Umi publicPath 固定为 `https://www-cdn.educoder.net/`；仅替换 API 地址不能完成本地静态托管。

这些 URL 常量替换不改变页面组件、布局或样式规则。不需要改写官方域名 DNS、MITM 或修改浏览器证书校验。

## 1. 获取资源

当前已经下载并保留原始资源，部署副本也已经生成。在线入口实际为 `umi.c7ec315c.js`，主 CSS 为 `umi.a3dd7499.css`；HAR 当时的入口是 `umi.000c4f23.js`。启动采用本次首页所指向的版本；各 hash 文件名不会互相覆盖。具体数量与缺失资源见 `reports/static-download.json`，逐文件修改前后 hash 见 `reports/rewrite.json`。

### 首选：原构建目录或完整发布归档

如果手中有部署产物，原样复制完整目录及 hash 对应的资源最可靠。HTML、入口 JS/CSS 和 async chunks 要属于同一版本；source map 不是运行必需。

### 没有发布目录：浏览器录制 + 静态资源清单

1. 浏览器开发者工具 Network 勾选 Disable cache 和 Preserve log。
2. 完整走过首页、登录、课堂列表、课堂通知、任务列表、分类切换、任务页和相关弹窗。
3. 导出 HAR，并保证包含响应内容。清除缓存或重新录制以避免只有 304、没有正文。
4. 由 HAR 的 GET 请求生成静态资源 URL 清单；过滤 API、Cookie、认证头和浏览器扩展请求。
5. 下载资源并保留 host/path；解析入口 HTML 的 script/link、CSS url()、worker 和 webpack 按需加载依赖。
6. 逐页验收补齐缺失的 chunk。HAR 记录的是已访问页面依赖，不是全站资源全集。

本次 HAR 已离线提取到 97 个合格的静态资源 URL；原 HAR 有 214 条记录。入口 `umi.000c4f23.js`、主样式 `umi.a3dd7499.css` 等没有出现在这 97 条静态请求清单里，需要从 HTML 引用补齐。工具会跟踪显式 HTML/CSS/绝对资源引用，但不执行 webpack runtime，也不枚举未访问路由的动态 chunk 表。

已提供工具：

```powershell
# 只生成 URL 清单，不发起网络请求。
python .\resource-reuse\tools\static_resources.py inventory --har .\educoder_from_login_to_get_homework.har --report .\resource-reuse\reports\har-static-inventory.json

# 用户执行时下载：输出目录必须不存在；不使用 HAR 中的任何 Cookie/认证头。
python .\resource-reuse\tools\static_resources.py download --har .\educoder_from_login_to_get_homework.har --out .\resource-reuse\original --report .\resource-reuse\reports\static-download.json --resume
```

工具按原始响应字节保存内容、记录 SHA-256，保留以下结构：

```text
original/
  www.educoder.net/
    index.html
    js/...
  www-cdn.educoder.net/
    umi.000c4f23.js
    umi.a3dd7499.css
    p__tasks__index.*.async.js
    static/...
  ali-cdn.educoder.net/
    images/...
```

如果线上入口版本已经变化，不能把新入口和老 HAR 的 chunks 直接当成一个版本发布；应重新录制。下载工具不会把这样的集合标记为完整版本。

GNU Wget 可以作为初始抓取工具（在安装了 GNU Wget 的终端执行）：

```bash
wget --recursive --level=1 --page-requisites --span-hosts \
  --domains=www.educoder.net,www-cdn.educoder.net,ali-cdn.educoder.net \
  --directory-prefix=original-wget \
  --reject-regex='/api/|/accounts/|/logout' \
  https://www.educoder.net/
```

Wget 不执行页面 JS，不能自动发现全部动态 import、webpack chunk、worker 或仅由 API 响应提供的资源。不要把这条命令的成功退出当成“全站资源完整”。`--convert-links` 也不能改写 JS 中的 API Base URL。

## 2. 最小部署地址替换（方案 B）

仅在单独的部署副本上运行：

```powershell
python .\resource-reuse\tools\rewrite_origins.py --source .\resource-reuse\original --dest .\resource-reuse\site --report .\resource-reuse\reports\rewrite.json
```

映射规则：

| 原始地址前缀 | 部署副本前缀 |
|---|---|
| `https://data.educoder.net` | 空串，即 `/api/...` 同源请求 |
| `https://www-cdn.educoder.net` | `/_assets/www-cdn.educoder.net` |
| `https://ali-cdn.educoder.net` | `/_assets/ali-cdn.educoder.net` |

脚本使用精确字节替换，保留原编码和其他内容；同时处理转义斜杠形式。原始目录不变。报告记录逐文件替换次数、修改前后 SHA-256。应审阅报告后再发布，不通过正则重排 JS 逻辑。

特定版本如提供运行时配置优先用运行时配置；当前包虽然有 `window.ENV`，但切换环境会改变多个服务地址，不等于提供任意 API Base URL 配置，不建议靠切换到测试环境实现接入。

检查剩余依赖：

```powershell
rg -n -o 'https://data\.educoder\.net|https://www-cdn\.educoder\.net|wss://[^" ]+' .\resource-reuse\site
```

脚本暂不改 WebSocket 地址、API 响应中的绝对资源 URL、CSP、SRI 或 Service Worker。当前录制的 HTML 未发现 integrity 属性；换版本时重新检查。若有 SRI，更新对应 hash；修改压缩前源文件后重新生成 gzip/brotli，避免旧压缩副本被优先返回。

回滚只需让 Go 服务读取上一版本的部署目录；下载原件始终保留。不会改变 official 域名上的流量。

## 3. 自有 Go 服务

独立入口为 `cmd/reuse`，静态文件与 API 使用同一个 origin：

```powershell
go run ./cmd/reuse
# 默认 http://127.0.0.1:11454，监听 0.0.0.0:11454
```

Windows 推荐独立后台进程启动，避免交互终端结束后服务一起退出：

```powershell
.\scripts\start-reuse.ps1
# 重复执行会验证项目可执行文件路径后重启自己的进程。
# 停止：.\scripts\stop-reuse.ps1
```

顶部全局网络错误横幅已按用户要求停用，仅修改原 HTML 中的 `showNetworkErrorTip` 钩子，不重写页面组件。实际请求错误仍由原业务逻辑处理。

图片补齐工具 `tools/image_assets.py` 获取公开配置与 HAR 引用的真实图片，支持无扩展名、缓存查询参数和实际图片 MIME 核验。Go 将已下载公共图片映射为同源静态地址；API 首页中的图片也会指向本地资源。

用户和教师头像通过专用 `/images/avatars/User/{key}` 读取器按需加载，兼容原编译包对应的 `/_assets/` 地址。读取器只访问固定官方头像源，发送正确 Referer，按实际 PNG/JPEG/GIF/WebP/AVIF 字节返回图片类型；失效头像回落到官方真实默认头像 `User/b`。头像仅在进程内有界缓存，不写入发布资源、磁盘或 Git。其他缺失静态文件仍返回 404，不进行通用网络代理。

```powershell
python .\resource-reuse\tools\image_assets.py --har .\educoder_from_login_to_get_homework.har
python .\resource-reuse\tools\verify_images.py
node .\resource-reuse\tools\verify_banner.cjs .\resource-reuse\site\www.educoder.net\index.html modified
```

图片修复记录见 `reports/banner-images-fix/`；原始静态文件保持原样。

### 装饰媒体轻量化

九张 SVG 位于 `internal/compat/media/`，通过 Go 内嵌提供，合计不足 9 KB。`aliases.json` 列出已删除装饰文件的精确旧地址；这些地址仍返回对应 SVG，避免旧组件中的图片引用变成破图。首页、设置和公告 API 在响应中使用轻量媒体；课堂、作业、用户及任务正文接口不参与此转换。上游没有公告时不会新增弹窗。

首页编译模块还含有内嵌 base64 宣传图片。`tools/media_lite.py` 仅处理原首页及全局展示模块，替换大图与两处宣传视频、保留图片懒加载，并为修改过的 JS/CSS 生成新内容指纹，同步原运行时映射与 HTML 引用。共享任务播放器及编辑器模块不参与修改。重新生成部署副本时，先执行原地址替换，再用明确的旧地址到 SVG URL 清单执行媒体补丁；不要在已发布目录中混用旧入口和新 chunk。

```powershell
# 当前部署树和运行中的本地服务验收，不需要账号或私有录制。
python .\resource-reuse\tools\verify_media.py modified --base http://127.0.0.1:11454
# 原始静态树或回滚副本：
python .\resource-reuse\tools\verify_media.py baseline --site <原始静态目录>
```

### 页面切换性能

JS/CSS/HTML/SVG 等文本资源由 Go 提供 gzip，压缩结果保存在最多 64 MB 的内存缓存中；原文件不变。带内容指纹的静态文件使用一年 `immutable` 缓存，HTML 每次重新验证；ETag 支持 `304`，HEAD 和 Range 仍保持标准行为。首次进入尚未加载过的路由需要获取对应 chunk，后续可复用浏览器缓存。

会话内安全的只读 API 使用短时、有界内存缓存，并合并同时发出的相同请求：用户信息和课堂列表 15 秒、任务列表/课堂通知 10 秒、课堂导航 60 秒、站点设置 5 分钟。缓存按站内登录会话隔离，未登录响应独立；实际错误、任务进入、代码内容、评测相关和有副作用的 GET 不缓存。返回的私人 API 仍标记 `Cache-Control: no-store`，不会进入公共 CDN/浏览器共享缓存。

排查性能时可查看 `X-App-Cache`（`HIT`、`MISS`、`COALESCED`、`BYPASS`）和 `Server-Timing`（上游等待、数据处理、总时间）。请求添加 `Cache-Control: no-cache` 可以强制重新读取。列表在上述短 TTL 内可能保持先前内容；进入任务和代码读取继续使用实时数据。

配置：`REUSE_ADDR` 设置监听地址，`REUSE_STATIC_DIR` 设置部署目录，`EDUCODER_API_BASE_URL` 设置协议模块使用的上游 API 地址。Docker 部署见 `deploy/README.md`；服务直接监听 `11454`，`touge.mom` 的反向代理由用户在 1Panel 配置。版本来自根 `VERSION`，`/version` 和 `/healthz` 提供版本与 commit。

Go 服务本身完成下面三类路由：

```text
GET /                             -> 原 index.html
GET /_assets/{host}/{path}        -> 本地已下载静态文件
/api/明确支持的原站路由            -> 本地 Go 兼容 handler
```

SPA route 刷新返回原入口 HTML；缺失 JS/CSS/font/worker 返回 404。运行时不把缺失静态资源反代到外部站点，也不设置任意 catch-all API 转发。原先的 `gateway/nginx.conf.example` 是被用户撤销方案的留档，不参与启动。

## 4. Go 必须增加原站 API 兼容层

无需修改 UI，但后端必须满足原 UI 的契约。只做 URL 转发不会自动把当前自定义 API 变成原站 API。

| 原编译前端发出的请求 | 当前自定义 Web API | 兼容层需要做的工作 |
|---|---|---|
| `/api/accounts/login.json` | `/api/auth/login` | 接受已加密密码和 autologin；避免第二次 AES 加密 |
| `/api/users/get_user_info.json` | `/api/auth/me` | 返回原用户字段布局，不嵌套 user/data |
| `/api/users/:login/courses.json` | `/api/courses` | 返回 `{count,courses}`，保留 snake_case 字段和分页 |
| `/api/courses/:course/homework_commons.json` | `/api/courses/:course/homeworks` | 返回原 homeworks、统计、分类和分页结构 |
| `/api/shixuns/:id/shixun_exec.json` | 合并在 enter 中 | 独立返回 `{game_identifier}` |
| `/api/tasks/:game.json` | enter 仅返回摘要 | 返回原任务 JSON，以及页面依赖的附属字段 |

页面还依赖 `setting.json`、`home/index.json`、课堂 top_banner/left_banner/informs 和任务 rep_content 等。按实际运行页面逐个建立完整契约，而不是根据 UI 截图写 DTO。

建议目录：

```text
internal/educoder/    可复用协议服务与上游请求
internal/compat/      原路径/原 JSON/原业务状态码/原 Cookie 语义
resource-reuse/      原始静态产物、部署副本、地址替换脚本
cmd/reuse/           自有 Go HTTP 服务入口
```

当前 `internal/web` 的 `{data,error,requestId}` 包装和精简 camelCase DTO 仅适用于先前自写前端，不能作为原站 UI 的返回格式。

原 JS 从 `document.cookie` 读取 `_educoder_session` 生成 Pc-Authorization，还会依赖登录状态。兼容层需要设计本地会话映射及兼容状态；上游 session 仍留在服务端。不能直接把 `domain=.educoder.net` 的 Cookie 发给 localhost；也不需要把上游 Cookie 变成可被 JS 读取的 Cookie。可在确有需要时提供独立的本地兼容状态标记，保持它与服务端会话的一致性。

原包在自己的 origin 下生成 X-Original-*，Go 服务向上游发送请求时需要按已经验证的协议生成官方 Origin/Referer/签名。不能把来自浏览器的代理头无条件当作上游认证依据。

## 验收顺序

1. 原 HTML、CSS、字体、主 JS、当前路由的 async chunk/worker 均从本地成功加载。
2. 浏览器 Network 中业务请求全部发往本地 `/api/`；缺失资源有明确 404。
3. 原站登录表单经兼容层完成登录，刷新后会话保持。
4. 原课堂列表、原任务列表、原进入任务流程分别拿到兼容响应并正常工作。
5. 独立验收编辑器、代码文件、终端及 WebSocket 等额外功能。

资源下载、服务运行和浏览器联调的实际结果以 reports 中的验证记录为准，不将 HAR 静态清单等同于全站完整镜像。整个过程没有新增 UI 组件。
