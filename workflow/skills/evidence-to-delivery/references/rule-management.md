# 规则管理与实际生效

本页只管理分类、优先级及调用要求，不复制专业规则，不建立第二个规则引擎或任务状态机。所有阶段开始、接续及规则变更时读取；只加载当前范围适用的专业来源。

## 读取层次

| 层次 | 读取要求与复用条件 |
|---|---|
| 短入口与当前任务 | 主Skill、本页、当前完整任务清单必读；接续只恢复唯一CURRENT/结构化check及相关项，历史按需 |
| 当前阶段专业规范 | legacy 调用按其既有契约完整读取相应规范正文。Open 原理图先读短入口、当前任务适用章节和其版本定位；只有范围扩大、冲突或该章节明确依赖全文时才扩读。保存来源版本/指纹，同版同范围会话内复用，变化后重读受影响来源并按既有流程复核 |
| 工具与知识 | 已知入口直接调用；只读当前操作参考、当前问题的知识条目/原文，不加载无关阶段、全部API、样例或知识全文 |

程序核对文件指纹、规则登记与证据，不证明模型已经完整理解。分类/优先级在本页维护；阶段推进在stage-contract，状态字段/检查格式在structured-workflow，专业要求在下表唯一来源。引用不复制正文，过时状态说明移到项目历史，不与当前规则并列。

## Open 与 legacy 边界

Open 原理图执行的专业正文只读取已安装 `easyeda-agent-open` 的 `references/working-rules.md` 与 `references/drawing-files.md`，并通过 Open MCP → HTTP daemon → Connector → 官方 `eda.*` API 取得实际回读。根 `easyeda-agent` 的 SCH-G/SCH-L、`schematic_preflight.py`、`execution_rules` 与 `rule_application` 仍是旧 workflow/MCP 脚本依赖和报告兼容契约；不把它们改名或换链接后称为 Open 适配。若某项仍调用该 legacy 契约，应按其原有脚本和证据完成，不能把旧报告冒充 Open 的运行回执。

### Open 原理图 R1 责任边界

- R1 由 AI 依据工程师已确认的设计明确**适用规则、独立设计意图、最小读取范围和验收方法**。专业正文仍在上述 Open 来源；本页不复制规则条目，也不新增规则表、规则引擎或业务状态。
- 规则来源、资料来源和知识采用记录只说明某要求为何适用、面向哪些对象及建议的检查方法。来源指纹、检索命中或知识采用不等于动作已经执行、现场事实已经读取或规则已经通过。普通规则文件保持普通 pinned source，不能伪装成知识回执。
- 必要规则集合由当前任务、已确认设计和适用章节明确列出；不得依赖 RAG/检索在执行时临时召回来补足 hard 要求。未能确认适用性或方法时保留 `unknown`/待处理，不以默认经验补签。
- `local` 是对声明对象及受影响关联的实际读回；`page` 是独立的整页目标或整页验收。局部读取、局部规则通过和局部图面审阅都不能拼接成整页完成。基线仅在目标、范围和来源仍有效且当前读回证明未漂移时复用；否则重新读取受影响范围。
- AI/工程师图面审阅只记录实际图像范围内的文字、接线表达和可读性判断；它不替代原生电气、身份、范围或持久化证据。独立设计意图来自已确认的资料/用户方案和目标表达，不能从修改后的图反推为自证。
- 保存是一次原生持久化动作；必要的关闭、重开和读回是后续独立证据。两者不得互相提前宣称成功。R1 只扩展必要的声明和校验，不新增自动执行门槛；Open 方案字段以 `easyeda-agent-open/references/drawing-files.md` 的唯一契约为准。
- R2 源码已将固定的已支持检查关联到当前阶段真实回执，并在业务导入时复算；检查名、范围条件和限制仍只维护在上述 drawing-files。此实现不等于当前宿主已经加载，也未替换旧兼容门槛或实现跨阶段证据聚合。

## 分类与唯一维护处

| 类别 | 内容 | 唯一维护处与触发条件 |
|---|---|---|
| 范围与授权 | 当前目标、禁止事项、暂停、允许改动对象 | 当前用户指令及项目既有确认/context；任何目标系统写入前核对 |
| 流程 | 资料校对、规则确认、接续、Jev 声明、交付 | [主入口](../SKILL.md)、[阶段契约](stage-contract.md)、[结构化契约](structured-workflow.md)；所有阶段适用 |
| 资料与电气 | 引脚、网络、型号、极性、工作条件与来源 | 当前确认资料及项目 findings；电气事实不得由排版偏好或 Jev 猜测修改 |
| 原理图（Open） | 功能识别、阅读顺序、摆放/方向、连线/文字、局部修正与保存 | 已安装 `easyeda-agent-open` 的 `working-rules.md`、`drawing-files.md`；Open 原理图执行适用 |
| 原理图（legacy 兼容） | SCH-G/SCH-L、P0～P3、旧预检和报告字段 | 根 [通用规范](../../easyeda-agent/references/schematic-layout-conventions.md)及既有脚本；仅旧调用方适用 |
| PCB | 叠层、工艺、回流、布局布线及物理判据 | 当前项目资料、constraints.json 及 [EasyEDA](../../easyeda-agent/SKILL.md)；仅 PCB 授权范围适用 |
| 工具与检查 | 坐标、API/库引脚、真实读回、ERC/DRC、能力缺口 | 当前 EasyEDA Skill、工具能力及实际回读；使用该工具时适用 |

内部/外部是来源分类；强制/建议是要求强度；原理图/PCB/流程是用途分类，三者不能混为一套优先级。原理图内容按正确性、图面表达、执行过程、项目要求四类理解；上表只说明维护归属，不新增机器分类字段。每条保留稳定 ID、来源版本/定位、适用对象/条件、要求强度、具体判据及检查方法。

## 优先级与冲突

1. 先限制当前授权范围：禁止事项、暂停或停止约束所有执行；确认规则不自行扩大执行范围。
2. 在范围内，用户明确的当前要求优先于内部通用默认。同一事项明确的新要求替代旧要求；其变更须合并进新版完整清单，按已有确认流程生效。
3. 经核对并确认适用的外部资料要求及本任务参数进入执行清单；内部专业通用规则作为默认。用户与资料、资料彼此或强制约束彼此冲突时，列来源、影响及建议，只暂停依赖该冲突的部分，不静默选一边。
4. 建议在上述要求允许的空间内采用；历史样例、候选知识、未确认假设不自动生效。

物理事实、器件极限、工具真实能力和必要证据不因优先级而变成可违反的条件。用户要求与它们不兼容时应报告冲突，不能虚构实现或判成通过。内部例外需要当前用户明确依据，并说明替代条目及检查方法；任务例外不修改通用默认。规则优先级与 PCB 布线先后次序分别管理。

## Legacy 兼容契约的生效链

以下 `execution_rules`、`rule_application` 和保存门禁说明的是根 workflow/MCP 仍消费的 legacy 契约；它们不替代 Open 工作规则、绘图文件或 Open 回执。

沿用“资料登记/解析 → 完整理解校对与确认 → 规则整合 → 展示完整清单 → 当前版本确认 → 执行前校验 → 执行/回读 → 逐条检查 → 交付”。

- 当前任务沿用现有约束 `checks` 结构：`id/rule_id/target/acceptance_criterion/method/basis`，附 `category/origin/strength/disposition`；不适用、被覆盖项保留理由及依据，不能通过删条目规避。内部原理图 SCH-G 与 SCH-L 条目必须全数登记；已采用条目具体化为当前任务判据。排版按P0～P3优先级验收，缺测不能以分数或Jev推荐补签；细则只维护于专业规范。
- `origin` 为 internal/external，`strength` 为 hard/required/advice，`disposition` 为 apply/exclude/override/conflict/needs_input。覆盖或排除内部默认必须有用户决定依据；hard 不允许通过 override 消除冲突。所有 apply 条目均需独立结果检查。
- 原理图在既有来源审查侧表增加 `execution_rules`：version、loaded（本页和当前绘图规范的 SHA256）、checks、confirmation。确认记录含同版 version、sha256、evidence（真实用户确认来源及定位）。执行 `{route, actions}` 计划增加 `rule_binding:{version,sha256}`。用既有 schematic_preflight.py 计算指纹并检查，旧确认/缺少规则/冲突不允许通过。
- 规则指纹绑定任务目标、采用/排除/覆盖记录、来源指纹及加载文件；新增规则、依据或目标变化必须重新审查。相同依据和版本可复用确认，普通坐标修正不自动要求重确认。首次确认的指纹由 Agent 计算，不能把自己生成的记录冒充用户确认。
- 结构化计划新建/修改时必须声明 domain=analysis/schematic/pcb/other；只读分析用 analysis。硬件执行 domain=schematic/pcb 必须声明 `required_checks:["execution_rules","rule_application", ...]`，遗漏由脚本拒绝。开始执行的 result.in_progress 提交通过的 execution_rules 报告；result.verified 还提交 rule_application 报告，绑定同版同指纹，覆盖所有 apply ID，附实际结果证据；相同事实以单个检查的rule_ids列表映射多条适用规则，note/evidence只写一次，未知/重复/遗漏ID仍失败。沿用 workflow.py 的证据指纹和影响复核，不新建规则状态。
- 手工 PWF 项目使用相同预检/检查报告，在已有计划与进度登记版本、确认及结果，不初始化第二套 state.json。typed 写入批次以当下计划再预检；原理图属性文字几何的官方 API 回退按[EasyEDA 主入口](../../easyeda-agent/SKILL.md#官方-api-局部回退)执行其前置核对及回读，明确脚本自动预检未覆盖，不伪造通过。此例外只改变执行入口及验证方式，不免除当前规则确认与任务授权；未确认只做离线准备。
- 同次预检从同一份规范字节计算指纹与登记ID；check/next同一次查询复用状态/证据读取，跨查询或改动后重新核验。未完成硬件项的恢复回读格式只在[结构化契约](structured-workflow.md#执行证据与恢复)维护；读取复用不能代替现场新鲜度。
- PCB 使用当前项目资料与 constraints，按实际工具回读和对应检查验收。当前没有完整 PCB 自动执行门禁适配，须列出未覆盖项，不称已实现。

加载指纹用 schematic_preflight.py 中的 RULE_FILES 对应文件计算 SHA256；rule_digest(requirements) 计算确认指纹，失败报告也返回该指纹供离线整合。confirmation.evidence 引用 sources 中登记的真实用户确认记录，checks.basis 复用现有 kind/id/locator 字段，不让用户手工编写数据文件。被覆盖/排除的默认条目 decision 用 source/locator 定位当前用户决定；该来源指纹也参加绑定。

`execution_rules` 的通过只证明声明/绑定及已支持的计划检查通过；图面、实际电气、最小修改和保存仍须真实证据。原理图属性保护已在预检中拦截；覆盖图面规则、模块语义与物理性能仍由对应检查器或工程师完成。workflow.py 检查声明和证据，工作流MCP强制执行原理图写入契约；直接CLI与平台操作仍在该边界之外。程序不鉴别用户原话真实性。

执行契约由现有MCP入口及workflow.py强制检查：运行身份、计划与动作内容、作用域、回执、新鲜度和恢复。首次准备写入或接口变化时按需读[执行接口](../../easyeda-agent/references/mcp-tasks.md#受控执行契约)，不要每步重复读取或手写同样的检查声明。

### 原理图排版保存的实际门禁

当前工作流 MCP 的 `schematic.save` 必须提供 `payload.verificationReport`（绝对路径），复用 `workflow.py verify-save`；不能直接调用 CLI/raw 保存绕过；raw 写入在本MCP入口拒绝。`edit_scope.mode` 区分 layout（默认，保持原电气关系）和 design（新建/明确授权的电路修改，按独立目标验收）；两者共用规则、范围、图面、保存和重开检查。直接CLI、任意脚本、平台自动保存不在此MCP强制边界内；沿用既有原理图会话启动器关闭daemon自动保存，不擅自更改平台设置。

- 写入前以 `schematic.components.list` 请求 `includePins/includePinNets/includeBBox/includeWires/includePagePrimitives/includeDeviceIdentity=true`。新Connector同时返回完整图元、文字测量、`saveGuardVersion:1` 和页面指纹；采集前后页面不一致则拒绝该快照。旧Connector无能力字段时停止依赖操作，不回退成普通保存。
- 在当前 `execution_rules.edit_scope` 绑定 `baseline_sha256`、`mutable_ids` 和 `acceptance`（page默认／local）。后者按 components/wires/attributes 等图元种类列出允许修改或删除的已有ID，空对象表示不允许改动已有对象。该字段进入已有规则确认指纹；原有用户授权可据实登记，不能伪造确认或扩大范围。
- design模式另需 `target_source`（sources中的独立目标文件ID）及 `authorization:{source,locator}`（真实新建/电路修改授权）。目标复用已有 `schemaVersion:"1.4"` canonical `projectId/documentId/components/nets/connections`，含完整物理引脚、脚名、32位库deviceUuid、实际网名及明确NC；不从修改后的图反向生成目标作为自证。不允许未连接/未知引脚冒充最终完成。目标和授权来源指纹参加同一规则绑定。按位号逐脚检查目标连接，同时检查库身份、声明的名称、已有canonical绑定及来源要求的引脚数；物理器件选型、供电/功能设计正确性仍依赖来源和逐规则工程审查，不宣称网表一致即电路设计正确或PCB就绪。
- 保存前报告沿用 `rule_binding/checks/inputs`，增加 `execution_root`（已有业务状态目录）、`execution_generation`（该状态的当前generation）、`phase:"before_save"` 及 `requirements/plan/preflight/baseline/snapshot` 文件引用。`inputs` 为这些文件、规范来源、逐项证据的路径→SHA256。`checks` 覆盖全部有效ID；保存相关规则此阶段只评价保存前条件，不提前声称重开成功。
- 新建对象运行时ID通过 `created_objects:[{kind,id,plan_index,receipt}]` 绑定完整动作计划和实际回执，回执必须成功、同一目标，实际几何/网名与计划相符。支持 wire.create、已验证的 netflag.create，design模式支持 component.place并按目标库身份核对；原生随新器件/引脚产生的属性、随新导线产生的同网Name属性限定为该新对象的关联内容，不豁免旧对象属性。不接受自由追加范围白名单。计划保留已执行动作用于证据核对，恢复时仍只执行未成功动作，不重放整份计划。
- MCP获取新快照，与已审查快照、范围、逐脚网络及采用的文字检查方式核对。文字可以实际图面审阅，不能把测量缺项改成隐藏/零面积。报告可增加 `visual_review:{status:"pass",snapshot_digest,scope:"local"或"page",note,evidence:[实际图像路径],checks:{text_legibility:true,wire_clarity:true,functional_readability:true}}`；snapshot_digest采用workflow.py的digest(当前完整回执)，图像路径和SHA256纳入原inputs，覆盖本次验收范围。只允许补足绑定原生文字对象的测量/可见性缺项；电气、范围、越界、图元缺失仍阻断。程序绑定检查声明和图像指纹，实际阅读与诚实判断由AI/工程师完成，不声称程序能证明图像语义或真实性。
- 几何候选 `text_overlap/crossing_candidate` 继续沿用 `finding_reviews:[{index,finding,status:"pass",note,evidence}]` 逐对象复核，保留原始finding和实际证据；实际遮挡须修正。visual_review不自动消除这些候选或其他已观测问题。保存前与重开后各绑定对应新快照，不能复用旧审阅声明。
- 通过后Connector再次核对身份/页面指纹，再调用原生保存。原生返回非true或超时保留未知，先回读且不自动重试。该检查缩小检查到保存的间隙，不声称拥有编辑器事务锁或能阻止同时发生的人工操作。
- 保存后用已知目标的 `document.close/open` 和新快照核对，禁止用会再保存的CLI `doc reload`。最终报告使用 `phase:"after_reopen"`；`persistence` 指向 `save_report/save_receipt/close_receipt/open_receipt/reopened_snapshot`，全部列入inputs。保存回执须含MCP生成的 `workflowSave` 绑定；回执顺序、目标、全图内容和逐脚网络必须一致。允许原生重开重新分配属性自身ID，其他属性内容/父对象/可见性/几何按完整多重集合比较。
- 手工PWF项目运行 `workflow.py --root <任务目录> verify-reopen --report <最终报告>`，结构化项目提交相同最终报告到原有 `rule_application`。`before_save` 报告不能满足最终verified。无需另建state或验收框架。

Jev 可用于候选规则匹配、适用条件语义判断或问题分流；硬冲突、未知工程参数和用户确认不得委托 Jev 猜解。已有 request/receipt/skip_reason 机制保持不变。

局部验收的已有检查器先对基线与当前快照分别检查：修改ID及器件→引脚→属性关联纳入受影响范围，原生重开属性ID变化按完整语义状态对应。仅基线中已有、范围外、当前相同且数量未增的已知图面问题/局部未知可继承为preexistingLayout待办；新增/改变/受影响项及全局缺测继续阻断。没有把UNKNOWN改成隐藏或全页pass。原save/reopen报告仍绑定全图电气、身份、范围和持久化证据；局部通过的交付必须说明未完成的全页事项。
