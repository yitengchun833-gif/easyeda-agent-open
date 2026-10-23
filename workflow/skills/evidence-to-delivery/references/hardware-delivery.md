# 硬件交付衔接与规则归属

这是资料到交付的硬件分支。它串联现有能力，不另建状态机，专业要求由项目资料和当前阶段规范确定。

本项目通过 `easyeda-open` 执行时，以已安装 `easyeda-agent-open` 的 `references/working-rules.md` 和 `references/drawing-files.md` 为专业正文，主链为 Open MCP → HTTP daemon → Connector → 官方 `eda.*` API。先读取短入口、当前任务适用章节和版本定位；只有范围扩大、冲突或章节明确依赖全文时才扩读。旧 easyeda-agent 的 project/doc 参数、预检和报告格式不能原样套到 Open 的 window/target/steps；资料事实与已确认方案仍可复用，但必须由实际调用适配和回读证明。Open 复用现有 runtime/batch，不新增业务状态机，也不要求用户重走已完成的确认。

## 唯一归属

规则分类、优先级、版本绑定及门禁统一遵循[规则管理与实际生效](rule-management.md)；下表只定位专业内容，不复制规则正文。

| 内容 | 维护位置 | 其他组件如何使用 |
|---|---|---|
| 用户目标、确认、范围、当前修订、任务状态 | 项目 `.workflow/state.json` | 通过 workflow.py check/next/source/propose/approve/result/review/context 使用 |
| 资料事实、分析结论、矛盾、原文定位 | 项目 findings.md 和 document-intake 输出 | 按当前资料版本定向读取，不复制成通用工程经验 |
| 本板电流、叠层、封装、机械尺寸、验收数值 | 当前项目事实与约束包 | 每条有来源、对象、单位、条件、检查方法、修订 |
| Open 原理图表达、档案和落图方法 | 已安装 `easyeda-agent-open` 的 `working-rules.md`、`drawing-files.md` | 通过 Open 的 window/target/steps 和实际回读执行；缺接口报告能力缺口 |
| 旧预检、SCH-G/SCH-L 与报告兼容 | 根 `easyeda-agent`、既有 workflow/MCP 脚本 | 仅在调用方实际仍消费 `execution_rules`、`rule_application` 或旧报告时使用；不作为 Open 绘图正文 |
| 执行、输入检查、有限候选 Jev、失败处理 | components/easyeda-mcp 及外部 CLI/Connector | 工作流只引用，不复制一套实现 |
| 通用文件化恢复 | [planning-with-files](../../planning-with-files/SKILL.md) | `.workflow` 项目只注入恢复提示，不改生成视图 |

执行规则分为内部通用规范、外部用户/资料要求两类；项目资料中的要求保留来源与条件并参与整合。Open 原理图通用画法仅维护在已安装 `easyeda-agent-open` 的工作规则和绘图文件。根 `easyeda-agent` 的 [通用规范](../../easyeda-agent/references/schematic-layout-conventions.md)仅服务仍调用旧预检/报告的兼容路径；其存在不证明 Open 已适配。绘图前展示当前任务的具体要求与检查方法；用户明确要求优先于内部通用规则。外部要求彼此冲突或与资料中的器件/物理限制矛盾时，明示来源及影响，不自行猜解、不将未知写成 pass；受影响操作等待冲突解决。

Open R1 只把当前任务的适用来源、独立设计意图、对象范围和验收方法放到同一份方案及其实际回执链中：方案的来源与可选知识采用说明为什么适用，不证明规则已执行；普通规则文件不伪装成知识回执。必要 hard 要求在准备写入前明确，不能依赖检索或 RAG 在执行时补齐。局部读取和局部图面审阅只覆盖声明对象及关联范围；完整页面目标、跨模块连接和最终交付另有 page 验收。有效基线可在目标、范围、来源未变且当前读回未漂移时复用，否则读取受影响范围。AI/工程师图面审阅只记录已见范围的文字、接线表达和可读性，不能替代电气、身份、范围或持久化检查；保存与必要重开读回分别记录。

## 从资料到原理图交付

默认在同一任务顺序推进；已有资料、设计和有效确认直接复用。

| 阶段 | 必要动作 | 完成依据 |
|---|---|---|
| 1. 解析资料 | 定向读取原件、关键图表及用户设计，核实型号、引脚、网络和条件；有冲突才询问 | 资料版本、原文定位、已知事实和缺项明确 |
| 2. 确定执行方案 | 形成器件与逐脚连接、功能关系、修改范围及验收方法；设计选择由用户决定 | 现有propose/approve及规则绑定对应当前方案，未变确认复用 |
| 3. 绘制原理图 | 用已有受控typed入口执行当前方案；按功能组织布局，短线直连、远连接用真实网名 | 逐动作回执及实际落地图元；失败或部分成功先回读 |
| 4. 检查并局部修正 | 程序检查身份、逐脚连接和范围；实际图面审阅接线表达、布局与文字可读性 | 当前有效快照、检查结果和图面证据；local/page验收范围明确 |
| 5. 保存与交付 | 检查通过后保存、重开并核对，交代实际修改和未解决项 | 保存回执及重开核对，不能仅用工具success宣称完成 |

PCB模块按下述约束执行段运行。规则apply与布局布线、铺铜、制造输出分别执行，也不作为原理图交付前提；未确认原生绑定时不能声称后续DRC使用了该方案。

PDF/截图复刻和资料驱动原理图通过 Open 规则、绘图文件及实际回读完成；根 [来源图形预检与分项验收](../../easyeda-agent/references/schematic-replication.md)仅在旧 workflow/MCP 调用其预检或报告格式时保留。复用原始工程/BOM可减少查库与猜测；只有图面材料时不得默认确定封装。硬件执行计划声明 required_checks，不能只用 checks_passed=true 把缺少的图面、物料或持久化检查判成完成。工作流维护直接在源码和离线样例验证，不创建业务阶段任务或恢复暂停的绘图。

## 最小数据交接

### Open原理图方案与真实阶段回执

- 本源码已适配`open_plan:{path,sha256}`到原有原理图计划项；`result`传`open_receipt`指向实际`easyeda.execution/1`文件。工作流重新核对方案、来源、工程/页面、阶段及原始执行/回读，自动生成要求、绑定、检查机制、覆盖、证据和结果；调用方不能手填`open_import`或伪造保留检查名。
- 方案/来源变化通过既有basis/impact失效该项及其依赖，不建立第二状态。阶段只证明本次声明范围：纯显示核对实际属性和所属器件，真正几何才测引脚/边界；不得提升为整页 verified。managed 前继从原始回执逐项复算，变化范围的旧要求失效。最终目标仍不自动满足图面、DRC或保存重开。
- 有序阶段、布线及网名编译，以及 requirements 的固定回执映射，只维护在同包`easyeda-agent-open/references/drawing-files.md`。图面审阅使用原runtime的当前图片回执，视觉WARN不变为电气BLOCK。Open业务项通过既有propose/approve明确选择验收；默认page要求open_stage_receipt/open_requirements/open_target_check/visual_review/persistence，完整final和原严格保存报告保持。显式local_display只限官方纯显示属性阶段，要求open_stage_receipt/open_local_rules/open_requirements/visual_review/persistence；旧语义页面资料可作引用，不表示本任务验收了整页。旧pair、PCB保持原契约。
- 局部显示的`open_persistence`接原始`easyeda.persistence/1`索引：固定plan及managed末端stage，输入文件哈希、真实save/close/open/scoped-readback的目标/顺序及器件身份/姿态/显示字段均重新核对。阶段原始局部读回可同时作为beforeSave证据，必须同一原始回执，保存晚于阶段完成。当前只支持原生局部text读回能证明的位置/可见性；字号、颜色等缺原生字段时不能声称持久化通过。局部路径已通过同包新进程实际R2保存重开和业务门禁复算；未执行业务state转换，不代表当前Codex MCP后台进程已经重载，也不证明整页、接线或全宿主门禁。
- 局部图面通过还须原始runtime/image记录绑定本次修改及实际PNG，不能只提交一个存在的文件；业务字段与唯一证据契约见`structured-workflow.md`。程序核归属，AI/工程师核可读性。

### PCB模块布局布线前的只读约束准备

2026-10-09用户已取消仅dry-run约定。标题保留历史链接；当前模块启动自动执行下述apply，显式dry-run仍可用于只读预览。

- Codex为每个PCB模块从已明确的设计约束生成`pcb-constraint/v0` IR，放入现有计划项`pcb_constraint_ir`；缺单位、网名、数值或依据时保留缺口，不填默认值或推测阈值。v0只映射Track、Via Size、Safe Spacing，NetClass只作选择，最终候选绑定列到成员Net。
- 新PCB模块还须声明`pcb_checks`（`pcb-professional-checks/v0`、mm、同一target），并在required_checks加入`pcb_professional_checks`。每项有独立id、明确role、所选模块source ID作为evidenceRef、实际对象与检查判据。知识条目只提供适用条件和依据，选定版本通过现有source/方案审批绑定；pending经验不自动成为正式规则。旧方案可以恢复读取，缺此规格不能通过新收尾验收。
- 显式预览仍用`workflow.py apply`的`pcb_dry_run`，只读取并生成候选。规则执行可用`pcb_apply`，模块`result.in_progress`在原有预检后自动执行同一Compiler apply。IR、before/after、真实绑定/DRC回执进入同一state的results与生成视图，不维护第二个业务状态机。
- Compiler先检查真实身份、当前配置、目标Net、冲突、不支持及unknown；非ready不写。只有实际配置/成员Net回读成功才认可规则落实；等价已有规则复用。绑定无法确认写blocked，不重放未知写入。原生DRC独立记录，已有未连线/违规可在in_progress处理，不能将规则成功或DRC未知/失败改判整板verified。
- PCB提交verified须有同basis的原生apply回读/DRC调用及有效证据哈希，并提供当前window；workflow自动执行同一CLI的`--review`只读收尾复查，不再次写规则。它回读当前绑定，重新调用原生DRC和`pcb check`，再回读确认现场一致。绑定未确认、DRC失败/未知、普通直角/锐角或检查ERROR、旧CLI缺覆盖、现场漂移均保持partial；不会沿用绘图前DRC签收。多分支角度作为INFO候选需图面审阅，不一律当坏转弯。原生DRC与转角中心线检查都不能替代全铜轮廓DFM；保存重开仍按收尾required_checks验收。
- 同次`--review --checks <JSON>`自动复算专业要求，专业fail/unknown也保持partial；不能提交自填的`pcb_professional_checks:pass`代替原始回读。`power_path`逐段绑定同Net的起止真实引脚和明确最小线宽，取最宽线路径瓶颈，细旁支不直接判主路失败；其它铜/孔/并联未知路径保留unknown，不推电流额定。`pin_role`核指定引脚赋网，可核指定去耦端点与已声明中心距离；不从网名猜器件角色，也不把中心距离当完整回路/EMI验收。`copper_attachment`核显式器件姿态/封装与静态铜、阻焊对象基线，变化使旧依赖验收失效；不按邻近自动归属或默认为已联动。未知阈值/原生结构不补值，单位未确认的POURED轮廓不参与载流求解。
- 自动apply仍只支持三类，不猜其他四类schema；该入口不自动摆件/布线/保存。手工PWF项目在已授权模块范围直接运行现有CLI（省略`--dry-run`即apply），将IR/原始JSON及结果链接到同一模块方案；无须为每条规则重复确认，不迁移或初始化第二套PWF。
- 当前接入本源码入口及项目AGENTS，安装插件是否同步另行核对；真实apply和历史只读证据见[Compiler v0](../../../docs/pcb-constraint-compiler-v0.md#模块工作流只读接入)。开放raw/API仍可绕过入口，不能声称底层API全局封锁。AI在输入/依据完整且工具支持时计算和检查，未实测的电热/EMI/装配交工程师，不作为禁止规则apply的笼统理由。
- 同包Open MCP提供`easyeda_pcb_constraints`，直接委派同一Compiler，operation=dry-run/apply/review；传绝对irFile、新outputFile、当前window及expectedIrSha256，checksFile附expectedChecksSha256，review必需专业规格。输入改变或输出已存在时拒绝，不重复写规则；同包独立PCB客户端支持脚本文件及新增转角覆盖，无需替换运行daemon。业务source/确认/basis/required_checks仍由原workflow/PWF负责，MCP结果不是业务verified。当前已同步工作区配套并完成新进程加载及原生身份拒绝验收；当前Codex旧MCP工具列表和全局Skill缓存尚未重载，不能说成全宿主已加载。

局部模块草案优先查现有模块目录。已接入的单词条通过 [离线检索与复用入口](../../easyeda-agent/library/modules/README.md) 运行 `module_reuse.py`，输出候选、缺项/排除原因、草案与差异；每次读取当前词条修订。当前只覆盖 lib.ldo_3v3 的草案复用，candidate 不替代下述工程适用性、required_checks 或 EDA 执行授权。

### 工程师分段审查的执行粒度

- 每个已批准计划项可对应一个小模块、一组器件或一组网络；acceptance 写明对象、边界与本阶段产物。required_checks 只列该范围必需的身份、物料、连接、几何及保存检查，不要求每个局部草案完成整页最终验收。
- 尚未满足本项检查时用 in_progress/partial 交给工程师审查；在 result.note 写出已做、未做及下一批范围。工程师反馈走已有 source/propose/approve，复用已确认授权；新的连接方案仍须确认。
- 局部项满足本项检查可 verified，最终交付另设依赖这些局部项的整体验收项，保留跨模块连接、原理图身份、逐脚连接、图面及保存重开要求。不得将局部 verified 显示为整页通过，也不得为提速删除最终交付门槛。
- 普通局部读回使用对应 typed 只读工具；easyeda_inspect 是整页综合 gate，按整页检查需求调用，不作为每次局部草案的必经步骤。新增修改或冲突只复核受影响范围；未知引脚网络保留 unknown，不用 Jev 补成连接事实。

复用项目现有事实、约束与检查记录，不增加知识库接口或第二套 Schema。

1. EasyEDA 只读快照提供真实项目/文档身份、版本、器件/引脚/网络/几何和工具覆盖。
2. 每项约束注明对象、条件、数值/单位、资料来源及定位；缺失值保持 unknown。
3. 执行适配器将已确认约束映射为实际 EDA 规则与操作；workflow result 核对本地证据和修订。电气/几何正确性由对应检查器及工程验收确定。

## Jev 与执行门槛

先运行 exact match、唯一 ID/net/role、明确数值和已知状态机。规则唯一则 Jev=0；保留至少两个合理候选时才考虑调用。
Jev 只接收目标、必要结构化状态及允许候选 ID；不得输出任意选择器、代码或 MCP 参数。低置信度或验证失败回到明确的 needs_input；不要把 confidence 当成功证据。

全阶段输入/回执/采用机制以[主入口](../SKILL.md#全阶段-jev-加速)和结构化契约为准。workflow_choose接受已收集的合法候选，不生成候选或验证几何；涉及封装/型号必须先证明可制造与电气等价，不能把猜测转成候选选择。

## 经验回流

只在发现稳定可复用经验、用户纠正或实际验证结果时沉淀。项目专属网名、坐标、测试条件留项目；通用条目带来源、适用边界、验证状态和版本，审查后更新对应专业规范或模块条目。不要自动把一次 DRC 通过写成通用规则。

画板纠错可用现有`knowledge_propose`保存待审核候选，带问题/原因/处理/真实证据/条件/验证范围，使用submissionId及候选revision保证续接不重复创建。候选不自动成为正式规则；经审查提炼为专业要求后，关联现有检查器和正反例。热/纹波/EMI未测保持unknown，原生API实测仅证明对应能力。后续项目复用时按型号/条件/版本核对，源变更或对象漂移使相关证据失效。
