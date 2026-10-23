# 原理图与 PCB 功能集成记录（源码候选）

工程师负责电路方案，本轮改善 AI 落图、放置验证和返工执行。未改工程设计、部署或运行版本。

Open 集成分支为 `codex/eda-native-integration`，基线 `76d0b2c0332fb975ff36d0a0e6b18deade1133d5`。原源码工作区保留。提交按功能分离，未重写原历史。

| 原专项提交 | 本分支 cherry-pick | 内容 |
| --- | --- | --- |
| `7917e881aef09ed56318770940817b2e27f9479f` | `d8823362dd6d0a32a60f6add7b84623063021ddc` | 原理图网名候选选位、功能组包围区域与 ModuleGap |
| `d05b56c5d7e6156da7c0a9889dcded1572cc8002` | `e1c4da02484f163f867827d4db3856cf055abc2c` | 真实焊盘赋网回读、批量放置的锁定保护与逐件结果 |

集成后修复：

- `6bb3681ac62b236946581110b52ef139aeb6cad6`：网名候选同时避让斜线与线段边界接触。
- `0e2f2c77e703c820882baecdc36d874b703aba83`：Go 的 dispatch/capture/request/apply 消费 move/arrange 完整回执；加件 partial/verified=false 同样传递失败。当前未核实写入不被 Retry、Verify 或 continue 掩盖，原结果保留在现有 journal。该处理不读取旧任务或阻止新授权任务。
- 动作目录列出 add_component、move、arrange 的实际返回字段；旧加件回执未提供 verified 时保留既有兼容行为。

必要验证：

| 范围 | 结果 |
| --- | --- |
| 原理图专项原始记录 | 8 项 Node 与布局定向 Go 通过；连续模块 2 项因未配置候选 CLI 跳过 |
| PCB 专项原始记录 | 15 项新 Node、4 项已有加件 Node、TypeScript 类型检查通过 |
| 本分支斜线与网名纯计算 | `node --test --test-isolation=none mcp/test/label-placement.test.mjs`，6/6 通过 |
| 本分支 PCB Go 入口、部分完成与 silk 回归 | `go test ./internal/app -run 'TestPCBComponentPlacement\|TestSilkAlignmentRequiresFreshVerifiedResultAcrossEntryPoints\|TestSilkApplyCannotRetryVerifyOrContinueAfterUnverifiedPlacement' -count=1`，通过，1.847s |
| 本分支加件兼容与防重放 | `go test ./internal/app -run '^TestPCBAddComponentPlacementVerification$' -count=1`，通过，1.492s |
| 范围及格式 | gofmt、git diff --check 通过 |

Go 使用任务局部缓存与过程级遥测目录，关闭依赖下载；未改全局设置或复制大型依赖。Node 默认测试子进程曾被 `spawn EPERM` 拒绝，该启动操作停止；上表采用支持的同进程模式，仍受原沙箱限制。未做真实 EDA、保存重开或图面验收，不将离线结果扩称为工程验收。

配套工作流属于另一 Git 仓库，基线 `58d6189448290d1ce0d68755944b623bc0eb9f9c`，分支 `codex/native-delivery-integration`，产品提交 `216922daec7f845cf863a93ef5e831d1ff563954`。它接入已有 native delivery verification，支持 page/module/local；纯内存 2 项通过，CLI 初始化的 `.workflow` 创建仍受权限阻碍，功能用例未通过运行验证。不要将这些提交 cherry-pick 到 Open 仓库。

发布前继续复用现有打包、来源清单、版权和哈希校验。`scripts/pack-open.py` 要求干净提交及匹配该提交的 Go/Connector/MCP 产物，只输出新 ZIP，不安装或启停。历史 `prepare-public-snapshot.py` 固定旧公开父提交与原目录，还会删除未列入快照的旧公开文件，不能原样用于本分支；须先按当前两仓已审阅提交准备有界公开增量。

当前只形成可回退的源码提交。原分支与现有发布包保留，后续如需撤回，用 `git revert` 逆序撤回相关功能提交；不 reset 或强推。发布、安装、新进程加载和真实工程验收分别记录，不复制恢复数据库或覆盖已有用户改动。

公开候选在现有公开 main `aab485e` 上应用本轮有界增量，版本 `1.8.1-open.9.7-rc.1`，Connector `1.8.1-open.9.7-rc.1-kb.33` 沿用既有 UUID。公开独有的 workflow、工具、版权及脱敏文档均保留，未导入私有 Git 历史、运行回执或工程身份。来源登记追加于 PUBLISH-PROVENANCE.json。

此版本是候选，不是实机验收完成的正式版。工作流 CLI 功能断言仍受初始化权限错误阻碍；既有后台和 Connector 的只读连通性不证明候选已加载。Knowledge 功能未改，其服务版本保留，候选包仅同步 package 版本声明，不要求重启现有知识服务。Connector 的安装和禁用旧扩展暂无经核实的官方 EDA API，不能用开发期 IndexedDB 注入代替安装。

候选 Connector 已生成，清单/UUID/版本、bundle 字节、logo 与 ZIP CRC 校验通过。标准 npm 构建的 esbuild 子进程启动被当前运行环境拒绝；停止该路径后，在相同沙箱内直接调用已安装 esbuild CLI，参数逐项对应 config/esbuild.common.ts，再使用原 sync-knowledge-ui.mjs 和 build/packaged.ts。未降级沙箱或修改安全设置。公开增量的 13 个产品/测试 Open 文件及 4 个工作流文件按换行规范化后与各自已审阅集成提交完全一致，未删除公开跟踪路径。
