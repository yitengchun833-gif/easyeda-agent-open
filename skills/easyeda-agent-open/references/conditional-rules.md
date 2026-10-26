# 按任务启用的条件规则

只读取当前任务涉及的段落；通用执行规则见 [工作规则](working-rules.md)。PCB来源绑定见下段。

## 原理图到PCB的来源绑定（Connector kb.34起）

首次建立PCB，先核对Board绑定的源原理图UUID。当前许可下官方原生导入可用时，核对实际导入器件/来源后继续；不能只凭importChanges返回true。当前import_changes默认自动确认仍含私有DOM点击，原生接口限定任务不得调用该确认路径；不为此强制重绑旧板。

首次逐件建立或向既有板增量补件，复用`pcb.add_component`，传真实源制造网表的位号、UniqueID、Channel ID、逐脚网络与`schematicUuid`。Channel ID从同一源网表原样复制（可含层级），不由位号/UID推算。typed输入新增`channelId`与`schematicUuid`，成对使用且匹配当前Board；新CLI可用`pcb add-component --channel-id '<真实源值>' --schematic <源原理图UUID>`，旧rc.2 CLI可用原有`call pcb.add_component --payload ...`直传这两个字段，无需更换后台。现场source→PCB的切页按任务边界一次完成，不为每颗件重复导入或全板DRC。

正常AI调用按此短链执行，不能把新增字段留给用户填写：

1. 在已确认源页，AI复用已读的真实JLCEDA制造网表；尚未读取才调用`schematic.export.netlist(netlistType:"JLCEDA")`，同时核对源原理图root UUID。只按已确认UniqueID选本次器件，不从同位号的其他页猜配。对每件直接取`props["Unique ID"]`、`props.Designator`、`props["Channel ID"]`和`pinInfoMap`的number→net（包含显式空网络）；缺真实字段时只补读该来源，不能悄悄转legacy或请用户手抄ID。
2. 首次空板默认优先官方原生导入；**仅在其当前许可及确认机制可用时**执行，随后核对实际器件与来源。当前默认DOM自动确认不合本任务原生约束时，明确说明这一步未用，先建立正确Board来源，再直接用下述来源完整的逐件路径，保留已有板不重绑。现有板增量补件直接走逐件路径，不对整板重复导入。
3. AI把步骤1各字段直接组成`pcb.add_component`的`uniqueId/designator/channelId/nets`，`schematicUuid`取同一已核root；其余device/位置来自已确认放置输入。通过原有`easyeda_batch.steps[].payload`或同包CLI `call pcb.add_component`透传，无额外用户ID输入、无第二编排框架。一次源读取后批量传给现有串行执行入口，返回实际ID与局部回读；工程师指出问题后按该ID立即局部修正。

读取回执的`sourceBinding.verified`、`bindingVerified`和`netMapping.verified`，缺字段表示旧Connector未支持；不能把原字段verified当完整来源同步。新Connector合并保留原属性，只补来源Channel ID并独立回读；返回partial保留新primitiveId，先对账，不能盲重建。旧无来源字段的调用继续兼容，明确sourceBinding.requested=false/verified=false，不替调用者补造来源。既有器件修复走定向属性修改，保留真实原属性图与身份，避免删除重放。

## 仅在 PCB 布局任务启用（P02–P11、P13）

下一版源码候选的指定旧线换线用同包 CLI `pcb route replace`：完整铜快照与独立两焊盘单网单层请求，先 `--dry-run --ids` 审阅 remove/add 与 SVG，再同输入 `--plan --window` 执行到新回执。仅独立 TOP/BOTTOM 单层 SMD 焊盘中心线链，无分支/未选铜接触/过孔/跨层/同网铜区，锁定和封装内线拒绝。首删前必须由新 Connector 按 expectedTracks 整组核对 ID/锁定/网/层/端点/线宽；旧运行 Connector 不具有该保证，不能采用旧副本执行。删除非事务，partial/unknown 后先读真实铜状态，不重放或自动恢复；不自动保存，DRC 与持久性另报。本能力尚未打包采用，版本由主线另行确定。

- 机械尺寸、孔位、接口朝向与禁区以用户给定图纸为准；未指定时可依据封装占地和布线空间自定板框，不默认圆角。区分 anchor 与封装 bbox 中心，旋转后重读 bbox。封装不得越板或与其他封装／已知禁区冲突；间距按封装和当前制造要求判断。（P02–P04、P10）
- 依据真实网络和功能关系聚拢器件，关键路径、专属外围优先，不靠位号或固定模板分区。去耦按真实供电／地引脚和回流路径放置；板边接口只在插拔、RF 或人手可接触等要求成立时考虑空间。（P05–P07）
- 模拟／数字／RF／高压／隔离及开关电源／晶振／差分对的特殊约束仅在该设计确有对应条件时按资料处理，不凭网名臆定安全距离或拓扑。制造交付时审丝印、极性和接口可读性。局部摆放任务如实报告未布线与网表差异，不能因全板未连接就称摆放失败。（P08、P09、P11、P13）

## 仅在布线、铺铜或制造交付任务启用（R01–R08、C10）

PCB 路径候选可使用同包 Go `pcb route apply` 原生执行；以同包 CLI 的 `pcb route apply --help` 查询输入和回执，独立安装本 Skill 时无需访问仓库外文档。限单网单层、零新过孔、仅增加铜且不显式保存；同机共享 temp 的 apply 锁不约束 GUI/其他命令/其他主机，双快照不是全局原子事务。先核新鲜目标与既有铜，未知/部分完成不盲重放，持久性仍待保存重开验证。

- 走线图元存在不代表物理连通；用新鲜焊盘、走线、过孔、铺铜数据及适用 DRC 核对。线宽、间距、过孔、板边约束来自当前工程、电气要求和现行制造能力，不把旧范例数值当永久默认。（R01、R02）
- 先布真实关键网络；差分阻抗、等长、换层、参考平面、地／电源铜和缝合过孔只在设计要求成立时处理，不默认分地或加电源层。（R03–R05）
- 走线、过孔、铺铜改变后按需重建铜区并核真实连通；轮廓不等于已灌铜。若当前宿主无可用重灌接口，报告旧铜状态待处理。自动布线及美化是可选工具，成功回执不等于电气／制造通过。制造交付时按任务审丝印压焊盘、铜到板边、悬空铜、锐角等 DFM 项。（R06–R08、C10）

## 器件、同步及恢复的条件规则（L01–L05、C03、C05、C07–C09）

日常AI规则模板导入用原有easyeda_pcb(action:"pcb.drc.rules.set",window,target,payload:{fromFile:"绝对JSON路径",module:"ruleConfig",dryRun:true})；已有batch可放入同一规则步骤，预览必须用step.payload.dryRun:true，不能用只列动作的batch顶层dryRun。MCP复用同包CLI离线解析后仍发原生action，一次原生请求返回before/requested，ruleImports给出JSON字段差异、未处理模块及previewed/verified/unconfirmed；实际apply显式传dryRun:false并要求verified:true，partial/unknown不重试。只需采用匹配MCP与CLI，无需新Connector协议或重启后台。模板来源与完整覆盖范围由工程师决定。独立CLI入口仍可用 `pcb drc-rules-set --from <文件> --module ruleConfig --dry-run`。外部easyeda-eda-rule-config-set/schemaVersion1只提取完整data.ruleConfig；多模块必须明确选择，其他模块列为未处理，不能连带写入网络类/网络规则。先检查before/requested和真实单位；这条入口是完整规则覆盖，不是与当前规则自动合并。原生覆盖可能把当前预设转为自定义配置；恢复规则内容不等于恢复配置名称，不能通过修改新建PCB全局默认来冒充当前板回退。仅在本次授权允许时移除dry-run实际写入并独立回读；模板来源或工程正确性仍由工程师判断。旧裸配置、{name,config}及本项目导出格式保留，配置集不能藏进旧包装绕过格式检查。文件存在不代表当前MCP/CLI已加载新能力；旧副本缺参数时报告需采用匹配候选，不绕过格式检查。

PCB布线收尾须用当前快照重新检查规则绑定、原生DRC及转角，不能复用布局前DRC签收。现有`pcb check`报告普通90°/锐角中心线候选，多分支及线身T节点单列INFO；须审阅实际铜几何，不据此宣称电气失效或完整DFM通过。项目Compiler的`--review`只读执行这些检查并核对复查前后快照；缺规则或旧CLI缺覆盖返回unknown，不在复查时写规则。

专业要求放在同一模块pcb_checks并由`--review --checks`复算：完整功率回路明确分段端点及依据，不把同网细旁支一律当主路瓶颈；真实引脚角色/去耦按指定对象、网和指标核对，不从Net名称猜；板级静态铜区及阻焊开窗显式登记器件归属，姿态/封装/关联对象变化后旧验收失效。程序不能确认的铜轮廓/孔载流、回路干扰和实物性能留unknown供工程师处理。已审核知识提供带条件的判据，pending经验不能自动变为正式规则。

同包MCP的`easyeda_pcb_constraints`复用上述Compiler，operation为dry-run/apply/review；指定当前window、绝对irFile/outputFile及expectedIrSha256，传checksFile时还要expectedChecksSha256，review必须有checksFile。outputFile必须是新文件，身份/哈希不符停止，不导航、不保存、不重放。它使用独立同包PCB客户端，运行daemon不需重启；安装代码、新进程实际加载和当前宿主工具列表分别核对。业务模块方案/来源审批仍由原workflow/PWF负责，MCP只执行已明确授权的约束与检查。

- **执行文件准备阶段：**型号、厂商料号、LCSC 号、器件 UUID、封装与逐脚定义分别核对；指定器件按用户设计，重新选型依据数据手册；多符号／器件／封装变体有歧义时不默认选第一个。不为补属性、丝印或禁区重绑正确器件／封装／3D 模型。落图时只核实际库身份与已确认选择一致。（L01–L04）
- 用户明确要求贴装或成本选型时，核 Basic／Extended、商城可买、JLC 可贴装、价格、时点库存与贴装方式；不同库存池不能互证，不自动换料，只查任务涉及的料。（L05、C08）
- 连接重建、状态不明或超时后，用实际轻量往返确认编辑器可回答；进程存在不等于请求通路可用，正常连续调用不逐步 ping。（C03）
- **批量**原理图→PCB 同步诊断时，可只读比较实例 ID 缺件、PCB 独有件及逐焊盘网差异；缺失／不匹配 Unique ID 单列。单件补齐复用上文来源绑定路径，批量诊断不替代实际落图回读。（C05）
- 多步任务中断时沿用现有 PWF／执行状态保存已完成动作与剩余范围，失败／取消证据不当作完成；单件小改不建 checkpoint 或第二状态机。（C07）
- 明确要求离线生成或编辑 `.eprj3` 时按对应文档类型 schema 与真实记录核对；格式检查不能代替 EDA 打开后的图面、电气验证，不强制安装额外工具。（C09）

