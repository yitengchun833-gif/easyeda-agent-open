# 配置规则回归快照

`pcb-config-web-3.2.203.json` 来源于 260919 AT32F415 Demo 在 Web 3.2.203 的官方接口回读：
`/tmp/260919-pcb-rules-before.json` 的 `result.rules.config` 与
`/tmp/260919-pcb-netclass-after-create.json` 的 `result.classes/netRules`（2026-09-20）。
完整规则原样保留；netRules 只保留 PWR_Class 父子记录和一个无关信号，删除其他信号以缩小 fixture。
不含工程 UUID、账号或个人资料。规则中的单位、浮点数、标签、状态和未知字段均不重构。

这是旧 typed 读取路径产生的真实结构证据；使用它的 `pcb config` 测试只证明离线适配和失败处理，
不代表新命令已在现场保存并重载，也不代表考试设计通过。
