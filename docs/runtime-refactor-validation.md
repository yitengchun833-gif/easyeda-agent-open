# 运行时状态重构：源码与离线分发验收

本轮从 `codex/current-source` 的干净 `a0b87d80dd7d77d9d5f3a344eafeff1a1f46a390`
开始，保留 `60a83fc6fbc936d3a0e40dcd88535129406a26d7` 及后续有效修复。
公开快照 `0102967a457e25da01d64c67a5f8152b934085bf` 作为对应审计基准，未强制回退。
实现提交为 `0646432`；本报告与真实分发回归另行提交。

## 实际变化

| 原职责混合 | 当前职责 |
| --- | --- |
| 8 个历史上下文、remaining 和未保存标记占住活动槽 | 历史可靠入库后可释放缓存；不改完成状态或执行许可 |
| 256 条最近回执同时承担去重与恢复 | SQLite 永久请求索引、完整单请求恢复；256 仅为观察缓存上限 |
| 重启全量恢复历史对象 | 启动只读轻索引，按当前目标/原请求 ID 懒加载 |
| 断线与旧窗口清理可能影响替换连接 | 独立 connection epoch 和当前连接指针校验 |
| 关键原生结果依赖 best-effort JSONL | 意图、原始结果和恢复依据同步入台账，JSONL 为镜像 |

同一 Go daemon 继续承接执行、缓存和恢复存储。未增加 Agent、布局引擎、
规则引擎或外部数据库服务。SQLite 事务仅保护本地证据，不宣称 EDA 写入原子化。
父批次的续接消费在子请求发送前持久化；缓存逐出后不能重新使用旧 paused 许可。
同连接已证前缀可续；新连接/重启后的旧依据不能自动恢复执行权。

## 修改文件

- 存储及依赖：`go.mod`、`go.sum`；`internal/daemon/recovery_store.go`、
  `recovery_store_types.go`、`recovery_store_migration.go`。
- 执行和观察：`internal/daemon/runtime.go`、`runtime_execution.go`、
  `runtime_retention.go`、`runtime_task.go`、`dispatch.go`、`autosave.go`。
- 生命周期、恢复与证据：`internal/daemon/daemon.go`、`runtime_checkpoint.go`、
  `runtime_persistence.go`、`runtime_recovery.go`、`audit.go`、`hub.go`、`connect.go`。
- 存储/缓存/连接回归：`internal/daemon/recovery_store_test.go`、
  `audit_checked_test.go`、`runtime_epoch_test.go`、`runtime_checkpoint_test.go`、
  `runtime_recovery_test.go`。
- 真实分发与迁移回归：`internal/daemon/runtime_dispatch_acceptance_test.go`、
  `recovery_migration_acceptance_test.go`。
- 旧测试仅补 Windows SQLite 句柄关闭：`internal/daemon/dashboard_test.go`、
  `response_projection_test.go`、`runtime_replace_test.go`，原断言保留。
- 配套说明：`skills/easyeda-agent-open/SKILL.md`、
  `docs/runtime-recovery-store.md`、本报告。

## 最终必要检查

Windows / Go 1.26.3 / Node 24.16.0：

| 检查 | 实际结果 |
| --- | --- |
| `go test ./internal/daemon -count=1 -timeout=120s`，显式提供授权备份 ZIP | 221 顶层测试、含子项 348 条通过；0 失败、0 跳过；11.500s |
| 其中新增 HTTP/WebSocket 分发与迁移验收 | 21 顶层用例全部通过；未接真实 EDA |
| 原 MCP 回执、恢复及 runtime 契约三个测试文件 | 53/53，通过；0 跳过；4.091s |
| CGO=0 的 Windows CLI 构建 | 通过；临时构建，不是正式采用 |
| `git -c core.whitespace=cr-at-eol diff --check` | 通过；保留原文件换行，未改全局配置 |
| `go vet ./internal/daemon` | 未通过：未修改的 `schematic_geometry.go:230/236` 原有 cancelPublication/lostcancel 告警；未关闭检查或改门禁 |

MCP 命令：`node --test mcp/test/receipt-persistence.test.mjs mcp/test/plan-recovery.test.mjs mcp/test/runtime.test.mjs`。
完整 daemon 最终回归包含最后的 receipt 单独逐出、已消费父请求防重放、
干净文档同窗口重连后连续写入与保存等边界。没有机械重跑 Connector 全套或实机流程。

### 分发链上得到的证据

1. 8 历史文档、256 回执完整保留，新项目写入和保存各分发一次。
2. 未知写所在文档拒绝危险请求；只读对账、无关项目及已知范围外的另一文档可用。
   原始 raw/project-wide 批次仍阻断相关工程。
3. 三次后台重启、每次十轮打开/断连/重连，无历史容量永久耗尽。
4. 同工程并发互斥、未保存窗口所有权、重复 ID、旧注册与迟到回复均有拒绝证据。
5. 请求/结果持久化失败明确分离结果，`public-v1` 保留原生结果且不可自动重试；
   数据库缺失/损坏、迁移事务中断、原生已分发后进程中断均未产生虚假许可。
6. 正常写入/保存、同连接已证后缀通过；跨 epoch、错目标/代次、缺失/损坏依据、
   已消费父请求均拒绝。保存丢应答、partial/error/saved:null 均保持 unknown。

## 既有备份迁移结果

仅使用已存在的 `EasyEDA-runtime-evidence-before-blocker-fix-20261021.zip`，
解压目标为测试临时目录。没有读取或迁移正式在线运行态。

- 原 ZIP SHA256：`50ba91987882f26ea86e8e9d3db1df54f015c05b915ffa17c6a8affd199a2511`。
- 原 JSON SHA256：`8a5ca18071bf43eeea8d9fdaf29fb2a7f96bf637767223c1584306494d8ae025`。
- 8 个上下文、256 条原回执；原状态为 action_finished 157、needs_review 69、
  needs_readback 30，原字节/身份/状态保留；启动活动缓存为 0。
- 缺少原请求、基线等恢复依据的记录仍标记 evidenceUnknown，原 action_finished
  不被当成新鲜验证或重放许可。旧危险未决范围继续阻断，不自动清理或对账成功。
- 重复迁移不重复导入、不覆盖更新后的 SQLite 记录；损坏输入/空图元拒绝。
  迁移中强杀测试子进程后没有半批记录，再导入可完整完成。

## 正式采用与回退

本轮源码分发链的历史容量阻塞已经消除；正式运行版本尚未采用，因此不能据此
宣称真实 EDA 已恢复写入。共享 bin/MCP/runtime/配置文件的受保护哈希未变化，
没有重启服务、安装候选、迁移正式数据或修改任何工程，也没有推送 GitHub。

后续另行批准后：协调共享窗口保存/停任务，完整保全运行证据，采用单一构建，
核对包/进程身份，再做个人测试工程的小型写入、保存回读与已证续接实机验收。
旧 unknown 仅做范围核对，不能盲目重放。

源代码可用 Git revert 回退本轮提交；运行数据不能回滚到旧 JSON。
前 SQLite 版本不理解新台账，必须保持写入停止，保留数据库、身份标记、
JSON 回执、审计及图面材料，直到兼容修复或经审查的证据保全转换可用。
详细状态与兼容边界见 [恢复存储](runtime-recovery-store.md)。

尚未验证：正式环境采用、真实 EDA 写/保存/续接、整机断电、网络文件系统、
长期大量证据增长与并发压力；未知结果没有自动解除 API。
原 Connector 无关历史失败仍单列保留，未放宽其断言，也未在本轮重跑。

## 本次检索来源

仅核对新增存储依赖及持久性契约，没有重新调研绘图架构。

| 实际来源 | 工具与状态 |
| --- | --- |
| [modernc SQLite 包说明](https://pkg.go.dev/modernc.org/sqlite) | 内置网页，原文有结果；用于纯 Go/版本/许可证核对 |
| [Go 官方模块代理](https://proxy.golang.org/modernc.org/sqlite/@v/v1.60.1.mod) | PowerShell，原文有结果；用于版本和传递依赖核对 |
| [SQLite synchronous](https://www.sqlite.org/pragma.html#pragma_synchronous) | 内置网页，原文有结果；用于 DELETE/EXTRA 契约 |
| [SQLite WAL](https://www.sqlite.org/wal.html) | 内置网页，原文有结果；核对副文件/兼容边界，本实现未采用 WAL |

普通网页、论坛及其他参考项目本轮未查询。
