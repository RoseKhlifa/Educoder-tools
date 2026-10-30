# 当前架构与迁移决定

当前使用手写工作台、Go 协议与业务后端。浏览器 TypeScript 编译成小型 ES modules，Go 同时托管页面、会话及应用 API。

## 唯一服务链路

cmd/web → internal/web → internal/educoder → Educoder API。

每个登录会话持有独立 Go 客户端，上游 Cookie 留在服务端内存。浏览器读取 /api/auth/me、/api/courses、/homeworks 和 /enter 的应用 DTO。Go CLI 直接复用同一协议包。

## 已清理的旧方案

- typescript/：Node/TypeScript 协议库、CLI、HAR 重放工具。
- resource-reuse/：原站编译资源、图片、下载和替换工具、旧网关示例。
- cmd/reuse 与 internal/compat：原站 UI 入口和页面接口兼容层。
- 重复的 Node 协议配置和旧启动脚本。

上述内容已经从当前源码和运行镜像删除，Git 历史与服务器上一发布目录保留恢复能力。

## 模块边界

internal/educoder 保留 Go 登录、课堂、作业、任务、代码读取、保存、自测、提交和状态查询。签名请求辅助方法仍可用于头像资料等 Go 功能，不会启动旧 UI 服务。

internal/solver、answer、problem 和 questionbank 保持独立，当前由 Go CLI 使用。网页 AI 运行入口现在通过 internal/jobs 和 internal/taskservice 接入：HTTP 返回 job ID，Go worker 在进程内异步执行；没有 CLI 子进程。题库和 jobs 数据放在 /data 独立 SQLite，模型密钥使用 Docker secret。已发布的 v0.1.10 选择题协议和 objective CLI 一并保留，选择题题库仍待扩展；UI 迁移不修改对应源文件。

工作台复用早期的蓝色导航、课堂列表、任务列表和详情抽屉。官方实训链接明确在外部站点打开，当前没有代码编辑器或 AI 控制面板。

## 构建部署

Node.js 用于浏览器编译和测试。多阶段镜像运行时仅包含 Go 与 HTML/CSS/JS。端口仍为 11454，服务器为 ssh main，域名反代由用户在 1Panel 管理。

命令与环境变量以根 README 和 deploy/README.md 为准；CHANGELOG 中旧名称属于历史版本记录。
