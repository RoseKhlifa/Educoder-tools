# 外部题库导入与复用

本项目可以把公开的 `ppxxforever/fuck-educoder` 仓库作为**未验证参考源**导入本地题库。导入器只读取固定版本的 `solutions/*/*/meta.json`、其声明的答案文件和 `answers.json`；不会读取账号、Cookie、HAR、运行日志或失败尝试，也不会执行外部仓库里的代码。

导入必须固定完整 Git commit，并校验 checkout 的远程地址、HEAD、工作区状态和每个文件的 SHA256。生产环境建议先在临时目录浅克隆指定仓库，再使用与服务相同的题库文件执行导入：

```sh
git clone https://github.com/ppxxforever/fuck-educoder.git /tmp/fuck-educoder-bank
git -C /tmp/fuck-educoder-bank checkout 04540c3249e2d6bfa7ac4e75c46c02d8cfa452e5

educoder-bankimport \
  --source-dir /tmp/fuck-educoder-bank \
  --commit 04540c3249e2d6bfa7ac4e75c46c02d8cfa452e5 \
  --bank /data/questionbank.sqlite
```

导入器输出 JSON 报告。针对上面的固定版本，当前实测结果是 544 条来源记录，其中 271 条具备可尝试的文件契约，273 条只能作为参考（120 条选择题缺少题干/选项身份，150 条代码答案缺少文件契约，3 条绘图答案是非 `.mdj` 文本）。重复执行使用 `(repository, commit, source_path)` 幂等，不会生成重复来源记录。

外部来源永远不会直接变成“已验证答案”。代码答案和 StarUML `.mdj` 答案先进入 `external_entries`，当当前账号读取到的题目具有完全匹配的实验名称、关卡标题、关卡位置和文件契约时，题库才创建一个普通 `candidate`，并把来源绑定到 `external_candidate_bindings`。求解器随后仍按当前账号、当前运行实例执行保存、评测和结果复核；只有平台完整通过证据才会提升为可复用候选。来源字符串本身不能伪造这层绑定。

`answers.json` 中只有选项字母而没有稳定的题干和选项文本的条目会保留为 `objective` 参考，不能自动提交。缺少语言/扩展名的代码答案也只保留参考。这样可以借鉴外部仓库的覆盖范围，同时避免把旧账号、旧关卡 ID 或未经当前平台确认的答案当成正确答案。

题库数据库位于应用数据目录，不能提交到 Git、镜像或发布包。导入命令应在服务停止或由 SQLite 一致性机制保证的维护窗口执行；完成后可以删除临时 checkout。管理员可通过现有题库汇总查看已验证可复用数量，外部来源明细不返回普通用户端。
