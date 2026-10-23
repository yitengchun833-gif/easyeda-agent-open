# MCP 只读任务

## 先发现既有能力

`easyeda_actions` 复用安装 CLI 的 action catalog，支持 search/domain/mutates、limit（默认10、最多50）及 offset。`nextOffset` 非空时还有结果。给定 search 却没有 typed 匹配时自动运行只读 `api search --json`；includeApi=true 可同时查 API 参考。没有增加另一套能力目录或新工具入口。

- `typed_actions_found`：当前 MCP 启动时的 CLI 目录包含此入口，`liveVerified:false`，仍需真实目标回读。
- `native_api_reference_found`：找到原生方法资料，不能证明连接器实现、跨工程支持或现场成功；不允许凭此重建工程或调用任意写入代码。
- `not_found_in_action_catalog` / `apiStatus:unavailable`：明确检索边界，不能宣称软件没有功能。API参考不继承action的只读/领域筛选，不能据此视为可安全执行。

例如查询 `schematic.components.list` 直接返回既有读取入口，不再多查API；查询 `copySchematicPage` 在当前CLI返回原生Beta方法资料，状态保持未接入/未实测。此处只验证发现能力，不执行复制。输出包含发现耗时和 `designMutations:0`；工具目录随新MCP进程刷新，不缓存编辑器电气状态。

## 原有检查/定位任务

以下原有任务状态：live-verified（EasyEDA Pro 4.1.60、Connector 1.6.0、本地 CLI 1.6.0 开发构建）。
已验证一次检查、精确定位、两例真实候选 Jev 选择与关闭 Jev 时的弃权；不外推稳定正确率。

`easyeda_inspect` 和 `easyeda_resolve` 是现有 MCP 的任务入口，内部复用 typed CLI；
不新建、打开、切换、保存或修改设计。仅处理当前已连接的原理图页面。
省略路由时必须恰好有一个已连接窗口且是原理图；否则提供 `window`、`project`
（名称或 UUID）和 `doc`（当前页 UUID）。有多个窗口时不要默认选第一个。

## 一次检查

调用 `easyeda_inspect`，`strict` 默认 true，`includeDetails` 默认 false。
它读取器件/引脚，执行安装版本的 `sch gate --json`，再读回器件与网络并核对工程身份。
当前版本包含 layout-lint、clusters、check、bridge-check、drc；缺阶段或异常返回不算通过。
`verdict` 是五阶段综合结果，必须分别读取 `nativeDrc` 与 `additionalChecks`。
后四项本地规则中的几何、版式和短线告警，不等于嘉立创原生 DRC 错误，也不能据此
断言电气故障。`blocked` 代表没有完成检查；`incomplete` 代表状态变动或证据不足。
`pass` 仅覆盖返回的检查阶段，不代表完整硬件设计验收。
DRC 数量或明细不可读时保留 null 与 coverage，不伪造零。需要定位问题时请求
`includeDetails:true`，原生 finding 中的坐标/位号/图元 ID 原样提供。

## 一次器件定位

调用 `easyeda_resolve`，必填 `goal`。把明确的限定条件放在 `filters`：
`designator`、`primitiveId`、`net`、`role`、`value`、`designatorPrefix`、`pinCount`。
全部是精确条件（前缀按位号约定），不可遗漏目标中的硬条件以人为制造唯一答案。
`goal` 本身精确等于位号、primitiveId 或器件型号时也走确定性匹配；不从长句里随意
提取 U1 作为目标，因为“U1 的旁路电容”不等于 U1。

规则得到唯一候选则不调用 Jev；零候选或超过六个候选则返回 needs_input，收窄条件。
多个合理候选且 `allowJev:true` 时才调用 TypeSafe Choice。调用前用户目标须已授权
向 TypeSafe 发送最小器件候选和相邻引脚网络；默认 false 时停在候选列表，不发网络请求。
API 密钥优先取 MCP 进程环境 `TYPESAFE_API_KEY`；Windows 下进程未继承时，
仅在显式启用 Jev 后读取已存在的用户级同名环境变量，不写入参数、报告或工程。使用环境
`TYPESAFE_MODEL` 或默认 `jev-latest`，记录服务返回的实际模型。

Jev 只选程序生成的候选 ID 或 none，不生成工具参数/代码。默认阈值 0.75；阈值是
工作流策略，未经统计校准，不能当作正确率。低置信度、none、非法分布、超时或 API
错误均返回 needs_input；不执行低信选择，不把预设答案当作回退结果。
由 Codex 根据返回的候选/缺失信息继续判断，硬件连接或封装变更仍须独立工程决策。

回读核对的是工程身份、器件属性/位置与引脚网络的一致性，不是语义正确性，也不是
完整 wire/graphic 几何差异。不能把 verifiedReadback 或 confidence 当作选对的证明。
找不到符合目标的器件时应明确报告；不得强迫从现有列表中选一个。

## 时间与失败

任务执行默认 60 秒（不含排队），`timeoutMs` 可设 1–120 秒；客户端调用超时需大于任务预算。
报告包含任务墙钟、CLI 累计时间、Jev 时间和每步起止时间。任务内未执行 Codex 决策，
剩余时间是本地组织/解析开销，不能叫模型推理时间。
取消会终止后续步骤及本地子进程；已经发往编辑器的只读调用未必能被远程撤销。
同一 MCP 进程串行处理控制请求；不能保证其他独立客户端不会同时访问工程，因此末尾
必须回读，发现观测状态变化就返回 incomplete。
任务不用 CLI 的 `--doc` 自动切页守卫；以 live document.current 与每次 typed 返回的
context 核对页面。检查期间保持当前页不变；gate 内部各阶段未暴露完整上下文，无法
排除其他客户端瞬间切页后又切回的情况，这不是原子快照。
`taskRetries=0` 仅指任务层没有重试；CLI 内部的队列重试未暴露，记为 `cliInternalRetries:null`。

## 受控执行契约

复用已有业务目录的 `.workflow/state.json`，不在维护源码目录初始化业务状态。动作回执写入其中的 execution 字段；task_plan/progress 继续由 workflow.py 生成。

- typed 原理图写入传顶层 `execution:{root,requirements,plan,baseline,action_id}`，路径为绝对路径。plan 增加 batch_id、runtime（daemonVersion/connectorVersion/mcpBuild/capabilities）及动作 postcondition。用 `workflow.py --root <业务目录> execution` 接收 stdin 的 `operation:inspect`、requirements、plan，返回按内容生成的稳定 action_id；排序不影响身份，内容变化使旧ID失效。
- 同一次局部几何修改可传execution.action_ids:[稳定ID]替代action_id，顶层action/payload对应首项。仅layout模式component.modify/attribute.modify几何字段；每项必须独立保持连接正确，不支持临时断线再补线。入口一次核对运行身份，前项已验证回读供后项使用；每项仍持久化并检查范围和连接。失败/未知停止；单动作recover恢复，已验证项禁止重放。快照仅在本次串行请求内复用，不保证外部手工修改的原子性。
- postcondition 是 `{collection,match,equals}` 或 `{collection,match,absent:true}` 数组，针对完整原生图元中的唯一对象；modify 必须覆盖实际目标及全部 patch 字段。创建对象由精确后置条件及实际差异绑定，原有身份、网络和范围检查不因此豁免。没有可靠后置条件的动作不能冒充已验证。
- 入口自动执行运行身份/能力核对、既有 preflight、修改范围、写前持久化 UNKNOWN、typed 调用、实际回读和效果归并。通信状态、落地状态、验证状态分开保存。`execution.recover:true` 只回读归并；已验证动作禁止重放，未知不当作未执行。
- 快照绑定连接实例、daemon写入水位、Connector FIFO与放弃计数、目标、内容哈希。写入尝试即使超时也使旧证据失效；重连或顺序无法证明时不解锁。原始数据在本地，模型接收差异和摘要。当前协议不能证明编辑器中人类操作的原子性，保存仍核对最新内容。
- 默认全页原生回读不进行文字量测；图面审阅与几何量测分开。需要定向量测时显式measureTextIds:[ID]；保存回读只复现报告已采用的测量ID。子集不冒充完整量测，图面审阅可完成实际文字可读性检查，接口与绑定见[保存门禁](../../evidence-to-delivery/references/rule-management.md#原理图排版保存的实际门禁)。没有相同实例/水位/页面哈希的证据不得合并旧测量。
- 排版读取使用 includeDeviceIdentity:false：完整保留原生身份、引脚及页面图元，不逐件查询器件库。受控写入/保存入口按已绑定 edit_scope.mode 自动选择；design及未知模式仍解析库身份。完整采集必须指定已有目录下的新绝对 JSON outputFile，默认只返回计数、缺项、摘要与本地证据路径。
- 模型仍负责功能分组、阅读路径、可读性判断和必要的最小修正选择。以上程序检查只引用输出，不重复逐项手写相同解释。全页专业规则ID及来源保留。
