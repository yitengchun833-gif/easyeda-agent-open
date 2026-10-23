# 结构化资料、方案与接续契约

本文件用于本工作流已明确启用的项目。日常入口为聊天；Agent 维护数据并生成只读 Markdown 总览。专业执行经已接入适配器和具体授权进行，工具返回成功不等于验收。

## 统一运行契约

本节是所有专业分支的共用执行约定；具体检查算法仍由专业模块维护。契约说明目标行为，不代表每个入口已实现。源码开发不初始化业务state；手工PWF任务复用专业计划和回执。

| 责任 | 唯一归属 | 现有实现与边界 |
|---|---|---|
| 目标、来源、业务依赖、交付状态 | workflow.py / 项目state | impact、basis、status已实现任务/来源/共享对象级传播；检查级细粒度复用尚未普遍实现 |
| 专业计划、阶段推进与编译 | 专业适配器；Open为plan-tools、route-tools、plan-cli | 复用已审阅计划、实测几何及managed阶段回执；参数/身份未知时停在依赖边界 |
| 原生执行、排队和现场事实 | daemon / Connector / 官方API | 文档写入串行、部分结果回读；运行缓存和回执不替代项目业务状态 |
| 专业检查及工程判断 | 专业checker、AI、工程师 | 程序核固定事实，AI核实际图面，设计缺项由工程师决定 |
| 阶段签收和交付 | 原有result与证据校验入口 | 动作完成、检查通过、保存及交付分别成立；不由连续执行器自动签收 |

### 工作单元及推进

AI一次准备目标、范围、必要输入、完整操作单元、依赖和停止条件。复用现有plan.stages/steps、required_checks及专业目标字段，不另建执行语言。程序在授权和前置满足时按依赖推进；需要实际放置结果才能计算接线时，先测量再编译，不猜引脚坐标。依赖不全、图面需审阅、设计待定或外部操作待完成时，返回明确原因和所需输入。

同一文档有副作用的步骤通过既有队列串行；独立且无共享可变状态的读取/计算才并行。完整修改单元结束后执行受影响检查，临时断线不能当最终目标验收；同一批次不具备原子回滚保证，失败仍立即停止依赖步骤并保存实际回执。简单修改使用单个单元，不机械经过资料接收等无关阶段。

### 检查输入、复用及失效

Open原理图S2已接入检查级的首个边界：仅经真实属性/身份回读证明的纯显示阶段保留前序电气/身份检查，图面相关证据失效；其他写入或未知结果保守失效，跨模块网络不能仅按器件ID划分。专业实现位于Open plan-rules.mjs，业务导入由open_receipts.py重算同一原始回执；详细方法随Open working-rules分发。保留项是历史阶段证据，不替代当前交付检查。


| 变化 | 应失效的证据 | 可以保留的依据 |
|---|---|---|
| 资料/要求更新 | 使用这些事实的解析、约束及下游检查 | 仅在相关事实重新核对且未变时保留不受影响结论 |
| 位号或参数文字的位置变化 | 相应图面/显示与持久化证据 | 电气属性已确认未变且现场新鲜时，可保留连接结论 |
| 器件、引脚、连接或名称含义变化 | 相关身份/网表/引用和实际依赖的下游项 | 不受影响且依赖完整的其他检查 |
| 规则/检查器实现变化 | 使用该版本的相关检查 | 原工程不因此自动重画；不受影响规则仍可复用 |
| 人工修改、断连或未知外部写入 | 无法证明仍新鲜的现场证据 | 历史回执保留为历史，待原生确认后再判断复用 |

专业适配器负责声明动作的真实读写事实与检查输入。通用层按依赖传播；输入投影包括目标身份、对象范围、相关属性及依赖版本、规则/检查器版本。只有输入充分、证据范围覆盖且现场新鲜度可确认时复用；不能把“没有事件”当作“没有修改”。局部判定仍可能需要整页网表采集。一次观测服务多个检查，同一检查结果可映射多条要求；不按规则数量重复读取。

检查结果pass/fail/unknown、严重性及fresh/stale分别解释，缺失输入不当pass。下游只依赖其实际需要的有效检查；必要检查fail/unknown阻止依赖动作，不阻断无关工作。交付核全部所需覆盖，复用仍有效证据。图面证据需真实审阅，内容哈希只绑定版本。

当前workflow.py仍保守按任务basis和共享objects失效；不能仅根据本表跳过现有门禁。细粒度判定接入代码并完成验证后，才收窄对应路径。

### 失败与续接

执行前保留原有输入/目标绑定和回执占位；结果及时进入同一动作记录。已经完成的副作用不重放；回执缺失/超时/部分成功先回读目标，确定已完成与待补部分。只读或已证明幂等的失败可使用有界重试；写入不因调度器支持重试而自动获得幂等性。已有成功结果但检查失败时，先补检查或纠正实际问题，不重做成功动作。

阶段出口返回已完成、未执行、结果未知、证据路径和下一步阻碍；不将pending或unknown自动转成通过。旧计划/输入已改变时拒绝继续原批次。业务状态仍通过原有result更新，调度器缓存不成为第二权威状态。

耗时记录至少区分阶段总时长、原生调用与检查；无法测量的模型/宿主等待明确未归因。以同范围总耗时、重复调用、返工及必要检查覆盖评价优化，不用单个API速度证明端到端提速。

机制来源：[Bazel依赖及失效](https://bazel.build/reference/skyframe)、[Dagster产物检查](https://docs.dagster.io/guides/test/asset-checks)、[Temporal幂等与恢复](https://docs.temporal.io/activity-definition)、[in-toto证据绑定](https://github.com/in-toto/attestation/blob/main/spec/v1/statement.md)。复用思想与既有实现，不要求安装这些框架。

## 数据归属

- 原始资料保持原位，不例行复制或建立历史副本。
- 解析阅读版 OUT.md；同级 OUT.md.assets 内保存 document.json、manifest.json、index.json、index.md、images。它们是当前派生物，不是历史库。
- .workflow/state.json 是计划、来源登记、待讨论变更、执行记录和交接的唯一可编辑权威来源。使用 workflow.py apply，不手工并行修改。
- findings.md 保存分析结论、已核对范围、矛盾和质量缺口；不重复维护计划状态。
- task_plan.md / progress.md 是生成视图；由 render 重建，不手改、不再复制旧模板。
- .workflow/plan-history.json 仅由 checkpoint 生成并定向提交 Git，保存当前方案与变更历史。原件、解析数据、实际工程与执行证据不由工具加入版本历史。
- 旧方案中的来源定位仍可追溯其标识；原件被覆盖/删除后，不能保证重新打开当时的内容。Git 回退方案不恢复外部工程。

已有手工计划不能被 init 覆盖。接续原项目先读旧正文，将真实决定和状态明确映射为结构化操作；向用户说明迁移范围后再替换旧入口，不直接初始化覆盖。新项目使用 init。

## Agent 调用入口

以下路径均相对本 Skill。用户不需要记忆命令。Python 3.12+；工作目录用绝对路径。

    python scripts/workflow.py --root PROJECT init --goal "用户目标"
    python scripts/workflow.py --root PROJECT check
    python scripts/workflow.py --root PROJECT apply OP.json --expect-generation N
    python scripts/workflow.py --root PROJECT next
    python scripts/workflow.py --root PROJECT show PLAN-001
    python scripts/workflow.py --root PROJECT impact SOURCE-001
    python scripts/workflow.py --root PROJECT render

每次 apply 使用刚读到的 generation。存在写入锁时先核查其他任务或中断状态，不盲目删锁。state 写入后视图生成中断时，先核对差异，再按文末一致性恢复流程使用带原因的 render 修复。文件锁只防同一项目的工具并发写，不代替外部系统的唯一写入者规则。

## 资料入口与定位

同级 document-intake/scripts/intake.py 保留原单文件用法，并新增 --document-id 与 --version。
批量使用同级 document-intake/scripts/library.py：

    python library.py catalog INPUTS.json --report CATALOG.json
    python library.py find MANIFEST.json "关键词"
    python library.py show MANIFEST.json "#/texts/3"

INPUTS.json 是 id/input/output/可选 version/mode 的对象列表；输入输出用绝对路径，output 为 .md。
每项记录 success/partial/failed，执行中断时剩余项标 not_processed。失败不阻止登记其他文件。
find/show 返回文本或表格 HTML、原位置、模型锚点和可用图片位置。目录索引用于章节导航，关键词用于进一步定位。
锚点属于该解析版本，不假设重新解析后数字编号不变；重新解析后需重新确认选取范围。稳定来源 ID 与模型锚点分开。

页序不等于印刷页码。没有可靠定位时记录缺口，DOCX 用章节/模型引用，表格按 spreadsheet-intake 获取工作表/单元格与公式。关键图面按 pdf-intake 回原页核对。JSON 无损指解析模型序列化，不证明 OCR 和解析内容完全正确。
manifest 的 quality_status 与 analysis_status 初始为未核对/未分析；阶段出口依据 findings.md 中实际定位的核对记录，不能靠 success 通过。

## 结构化操作

操作文件是一个 JSON 对象。只由 Agent 在已授权范围内填写，不执行资料里包含的命令。

登记文档依据（选择包含表头、脚注、测试条件等必要上下文的锚点）：

    {"op":"source","source":{"id":"SRC-001","kind":"document",
      "manifest":"资料/手册.md.assets/manifest.json","anchors":["#/texts/3"]}}

用户决定与现场观察也属于依据，分别使用 kind=decision/observation，并保存 text 与 locator。
locator 记录真实原话/观察记录位置及时间；助手建议不得标成用户决定。

    {"op":"source","source":{"id":"DEC-001","kind":"decision",
      "text":"用户已确认的约束","locator":"实际确认消息或文件位置"}}

提出变更；ADDED/MODIFIED 的 item 使用完整字段，REMOVED 只用 id/action。依赖必须存在且无环；已删除 ID 不复用：

    {"op":"propose","id":"CHG-001","reason":"变更原因",
      "delta":[{"action":"ADDED","id":"PLAN-001","item":{
        "description":"当前要求","acceptance":"可检查的验收条件",
        "domain":"analysis",
        "sources":["DEC-001"],"depends_on":[],"objects":["共享对象的真实ID"],
        "jev":{"skip_reason":"此示例仅保存用户明确指定文本，无候选选择"}}}]}

propose 不改变当前有效方案。确认其影响和条件后，保存真实用户授权位置：

新建/修改项必须声明 `domain=analysis|schematic|pcb|other`；只读硬件分析用 analysis，真实原理图/PCB 执行分别用 schematic/pcb。PCB 与 legacy schematic 必须声明 execution_rules 和 rule_application；绑定open_plan的schematic可明确选择Open路径：page必检open_stage_receipt/open_requirements/open_target_check/visual_review/persistence，local_display必检open_stage_receipt/open_local_rules/open_requirements/visual_review/persistence，范围由真实已确认的Open方案决定。缺完整声明由脚本拒绝，不自动改已有计划；仍经既有propose/approve及有效确认恢复。

    {"op":"approve","id":"CHG-001","confirmation":"用户确认本修订的实际位置"}

工具验证记录不为空，但不能验证授权是否真实；Agent 必须核对原话。若方案或来源已变，旧提案不能批准；使用 withdraw 记录原因后以新变更 ID 重提：

    {"op":"withdraw","id":"CHG-001","reason":"资料已更新，需重提"}

## 影响检查与复核

源码/资料指纹检测变动；依赖和共用对象反向查询产生保守候选范围。它不保证找到未登记的隐含关系。
原文件版本变化先要求重解析/刷新来源，即使选中片段未变也需一次有理由的复核；不按文件哈希直接断言全部设计错误。
来源刷新、计划变化后 affected 项标 needs_review，未关联项保留。对循环、缺失依据、缺失依赖报错。

    {"op":"review","item":"PLAN-001","outcome":"reverify","reason":"变化影响验收，需重新执行/检查"}
    {"op":"review","item":"PLAN-002","outcome":"unchanged","reason":"已核对关联，原结果仍适用于相同计划修订"}

unchanged 只允许沿用相同计划修订且证据未改的结果，并追加复核记录；不会把旧记录改写为新结果。不同修订需要新验收。
review 不代替要求变更；需要修改要求时走 propose/approve。有未登记关系的风险时定向查实际对象。

## 执行、证据与恢复

### Open分阶段回执导入（新增源码接口）

当前源码的Open staged `easyeda_plan apply`可传`receiptFile`（绝对、新JSON路径、非dryRun）。写入前先占用该文件并记录pending；返回或抛错后记录settled或unknown及原始批次/局部回读/targetCheck。已有文件会在任何EDA调用前被拒绝；未知写入不自动重试。运行包未同步/宿主工具契约无此字段时不可声称已启用，不另启服务绕过。

业务原理图计划项可增加`open_plan:{"path":"plan.json","sha256":"当前审阅文件的64位SHA256"}`，经既有propose/approve保存；不能用于PCB。方案改动仍走原修订/确认规则，普通未变确认复用。`open_plan`本身不豁免旧要求。经同一 propose/approve 明确选择 Open required_checks 才切换规则证据路径；仍声明旧 pair 的项继续旧检查。新路径开始前需要 confirmed 方案、非空 requirements 和当前来源，硬 before_write 仅允许已接通 source_binding。

执行回执按`easyeda.execution/1`保存：实际planFile/sha256/revision/target/stage、固定sources的路径与哈希、状态/时间、完整原始receipt。结果直接交给原入口：

```json
{"op":"result","item":"已批准原理图计划项","revision":1,"status":"partial",
 "open_receipt":"实际阶段回执.json","note":"完成本模块摆件，等待接线与图面审阅"}
```

工作流重新核对当前方案/来源与目标，将回执和所有输入自动纳入证据哈希，重新从实际批次完成数、新ID、动作推导的 display/geometry 覆盖和 native context 推导`open_stage_receipt`；final阶段另导入`open_target_check`。若有Connector local-rules，程序推导`open_local_rules`的硬检查状态，WARN原样保留，不因为图面候选生成硬阻塞。Open 具名要求还从 managed 前继原始回执重新计算，派生 open_requirements；同一复算内复用已核对数据，后续修改的范围失效。上述检查均由程序写入，调用方不能手工填同名checks或open_import摘要。

每项结果带要求ID、目标/修订/哈希绑定、实际检查机制、覆盖/未覆盖范围、回执证据和pass/fail/unknown。局部geometry不证明电气；full target不证明设计合理性、视觉或保存重开；pending/unknown、缺真实ID/geometry/native目标时不能pass。导入partial不会自动变成verified、saved或业务依赖已完成。默认page请求verified仍须对应final实际target pass及完整既有持久化报告；before_save输入钉住当前Open plan/final、目标及时间顺序，不能以局部geometry验收该全页目标。

明确`plan.json.acceptanceScope:"local_display"`时，只允许非空scoped官方属性显示阶段，不允许final或delivery requirements；全部写步骤均重新分类，禁止借局部标记降级电气/几何操作。该业务项的必检为open_stage_receipt/open_local_rules/open_requirements/visual_review/persistence。`result`同时给`open_receipt`末端managed阶段及`open_persistence`原始持久化索引，程序生成persistence检查与open_persistence_import，调用方不得手填；图面仍须当前实际证据。索引schema为easyeda.persistence/1，固定planFile/planSha256/revision/target/acceptanceScope，stageReceipt/beforeSave/saveReceipt/closeReceipt/openReceipt/reopenedReadback路径及inputs哈希。仅证明对应显示字段保存重开、器件身份/姿态未变；不产生open_target_check pass或整页/电气证明。缺样式原生字段不通过；旧page与PCB分支不变。

局部`visual_review`检查另外给`runtime_state`（原生runtime.state返回的完整记录，可用primitiveIds作局部投影）和`image_receipt`（真实导图回执）文件路径。程序核末端修改请求的saved记录及visualReview，图像请求的target/window/generation/范围、审阅晚于修改与截图且早于保存，以及原生artifact哈希和实际PNG；AI/工程师用note记录图面判断。文件存在或调用方自报generation不替代该绑定。新open_persistence只用于纯Open契约，不能与legacy execution_rules混合。

`check/next`定向核对绑定方案与来源：发生变化会让该项及已登记的关联项待复核，不关联项保留。回执导入只接已有事实；尚不自动编写后续steps、保证所有调用都经过同一入口，或执行下一阶段/保存。新接口尚未部署/实机验收时只能报告源码/离线结果。

PCB模块在完整计划项中增加`pcb_constraint_ir`，使用`pcb-constraint/v0`的mm/精确target/constraints。新PCB提案缺IR会被拒绝；旧记录可读取，但开始前需通过既有propose/approve补齐设计中已明确的约束。只读准备操作为`{"op":"pcb_dry_run","item":"模块ID","revision":当前修订,"window":"实际Connector窗口"}`，仍通过`workflow.py apply ... --expect-generation N`运行。工具生成IR、调用Compiler dry-run并把原始回执写进同一results，生成方案展示规则、成员Net绑定及冲突/未支持/unknown。

2026-10-09用户取消仅dry-run约定。`result.in_progress`对PCB项要求显式window，在原有预检通过后自动调用Compiler apply，程序写入`pcb_constraint_apply`；调用方不能手填该字段。另可显式用`{"op":"pcb_apply","item":"模块ID","revision":当前修订,"window":"实际窗口"}`落实规则，结果仍partial/preparation_only，不代表绘图完成。Compiler非ready不写，实际身份/配置/Net回读无法确认时blocked；规则成功但原生DRC fail/unknown分别保留，已有问题在in_progress修复，不冒称verified。IR/来源/证据变化及硬件恢复读回要求继续生效。该入口不摆件/布线/保存；AI做有依据且能力支持的计算，样板性能/工艺未知交工程师。详见[PCB约束执行](hardware-delivery.md#pcb模块布局布线前的只读约束准备)。

规则门禁按[规则管理](rule-management.md)执行。legacy 硬件 result.in_progress 必须带 id=execution_rules、status=pass、report=当前预检报告路径及 evidence；报告的 execution_rules 需包含 confirmed=true、version、sha256、effective_ids，所有 inputs/evidence_files 指纹均须当前有效。不能只填 checks_passed=true。

仍声明 legacy pair 的 result.verified 还需 rule_application 报告：`{"passed":true,"rule_binding":{"version":"当前版本","sha256":"同一指纹"},"checks":[{"id":"实际采用条目ID","status":"pass","note":"实际观察","evidence":["相对报告文件的实际证据路径"]}]}`。覆盖全部 effective_ids；重复事实可在同一检查对象以rule_ids列出它实际证明的多条ID，程序展开映射，note/evidence只写一次。不同事实不得靠泛化说明合并；缺项、未知、版本不符或依赖文件变化时拒绝验收。report.passed 不替代逐条覆盖，也不鉴别证据或用户确认是否真实。

硬件开始动作前记录in_progress以检查当前规则报告；部分成功用partial，阻碍用blocked，实际验收后用verified。其他领域沿用既有记录方法：

    {"op":"result","item":"PLAN-001","revision":1,"status":"verified",
      "artifact":"实际工程/文件/对象标识","note":"真实动作与读回结论",
      "evidence":["检查记录.txt"],"checks_passed":true,"saved":true}

verified 要求证据文件存在、明确目标标识、检查通过和保存确认。工具记录证据指纹，不能替 Agent 判定专业正确性。
计划项可增加 required_checks 字符串数组；硬件执行项按专业分支声明必需检查。其 verified 结果须提供 checks 数组，每项含 id、status（pass/fail/unknown）、note 和非空 evidence 文件列表。缺项、重复 ID、fail/unknown 均不能 verified。可选 report 指向顶层含 passed 布尔值的 JSON 自动检查报告；失败报告不能标 pass，报告的 inputs/evidence_files 文件哈希映射必须仍匹配。报告及其输入自动纳入既有结果证据；旧计划无 required_checks 时保持兼容，不能据此声称旧任务满足新硬件门槛。
局部功能验证只写入 result.note 及对应证据，不把较大的未完成计划项标成 verified。note 写明检查范围、通过项、未运行项、未解释警告和真实保存状态；用户接受局部图面另记带原话来源的 decision。暂停与恢复范围使用现有 context.authorization/next/blockers 维护，不新增状态机。
结果绑定方案修订、来源与依赖基线；旧结果保持原样。证据被改/删除会使当前验证待复核。用户验收是单独事实，写入有来源的决定/记录，不由 verified 推断。
check与next提供相同的一次只读状态快照，含view_issues；恢复先调用一个即可，不连续重复同一查询。需要完整项再show；现有来源/证据哈希在同次查询内复用，下一次调用重新读取，不跨修改或宿主会话缓存。

next的ready只列pending且依赖已验证、无未完成硬件回读的项；派生recovery列出in_progress/partial/blocked的当前修订、原记录修订、最后说明、readback_required及下一步，不写入第二套状态。计划修订或review.reverify不会清除尚未读回的旧中断。

未完成硬件项再次提交result.in_progress前，checks必须含`{"id":"recovery_readback","status":"pass","note":"真实目标身份、已成功对象/动作及剩余范围","evidence":["本次实际回读文件"]}`；unknown/fail、缺note、缺文件不能启动。legacy 仍须通过当前 execution_rules 报告；Open 迁移项须满足当前绑定方案前置和实际回执要求。规则/来源或证据变化先按已有impact/review处理；partial/in_progress的已登记证据变化也标needs_review。

回读后把已成功操作从待执行批次剔除，只生成剩余动作或最小局部修正，并对当前批次重做专业预检。恢复检查验证回读声明及文件存在/指纹，不证明现场数据真实、新鲜或操作者绝不重放；实际工具轨迹/回读仍要对账。本脚本不发EDA写入，不能拦截绕开工作流的CLI/MCP。每批结束记录真实保存状态并生成总览。

    {"op":"context","value":{
      "stage":"execution","coordinator":"真实任务ID",
      "tasks":{"1":"实际阶段任务ID"},"authorization":"阶段创建/接续授权来源",
      "questions":[],"blockers":[],"next":"明确下一步"}}

生成总览只展示最近记录；完整结果在 state.json。新任务先 check，按 show 定向读取项，查看 findings 中相关证据；不要载入全部 state 到模型上下文。
钩子识别 .workflow/state.json 后提供恢复提示并绕开旧 PWF 状态编辑；不保证应用关闭时后台运行，也不强制执行业务验收。
当前运行中的桌面任务仍需遵守其工具权限；只能在用户明确授权新任务创建时派发，不把 Skill 当作授权。

## 历史

    python scripts/workflow.py --root PROJECT checkpoint --message "方案已确认：变更摘要"

仅使用已有 Git 仓库，定向提交 .workflow/plan-history.json，不初始化嵌套仓库，不提交其他已暂存文件，不推送。若项目无 Git 或身份未配置，报告明确缺口，不修改全局 Git 配置。
在方案首次确认、重要纠错批准后提交即可，不逐工具调用提交。执行记录及原件不进入该历史文件。
不要对项目执行 git add -A 来代替此命令。

## 一次最小自检

    python scripts/test_workflow.py

验证文件级真实写入和读回、提案门禁、共享对象/依赖传播、旧修订隔离、证据变化、循环和并发版本冲突、独立进程恢复、钩子路由与限定 Git 提交。
资料解析另用代表性文件核对索引和原件；通过这些不等于真实 EDA、扫描 PDF 或全新桌面任务端到端验收。


## 生成视图一致性与冲突恢复

check/next 比较两份Markdown与当前权威状态应生成的内容，返回 view_issues 和 recovery_blocked。有差异时清空 ready，next 退出码为2，禁止据旧摘要继续执行。差异可能来自手写内容、缺失文件、状态被绕过工具修改，或外部证据变化；必须核对实际原因。

apply/render 默认拒绝覆盖不一致视图。先读取冲突内容，把其中尚未登记且经核实的决定、执行证据通过现有操作写回状态；对已过时内容说明依据。完成核对后，才在本次 apply 或 render 附加 `--reconcile-views "具体核对依据与保留/舍弃原因"`。该参数允许重建视图；工具把理由、时间、受影响文件及观察文件指纹和恢复前状态对应的期望文本指纹（pre_reconcile_expected_hash，UTF-8/LF；不是恢复后文件哈希）写入现有changes并增加generation，不另存整份历史副本。render与apply使用同一写锁。恢复记录不替代用户批准或原文核对；不得在通用脚本中无条件附加。无差异时沿用原命令。每次修复后check确认view_issues为空。普通工具调用成功不代表状态内容真实。

此检查能发现生成内容不一致，但不能阻止拥有文件写权限的Agent绕过全部工具，也不能识别被同步伪造的状态与视图。现场读回和真实用户确认仍必须执行。


## Jev 自动调用与阶段门禁

只在有真实语义判断时登记以下requests；纯确定性任务在任务摘要说明一次，计划项可省略jev，不产生调用或逐项跳过回执。旧skip_reason仍兼容，真实语义选择不得用省略代替。

```json
{"jev":{"requests":{"source_priority":{
  "goal":"优先核查哪个证据来源", "stage":"analysis",
  "candidates":[
    {"id":"official","label":"官方资料","evidence":"对应型号完整，包含引脚表"},
    {"id":"catalog","label":"目录记录","evidence":"仅有系列名称"}
  ]
}}}}
```

无语义歧义的步骤可省略jev，统一在任务摘要说明确定性依据；有新的真实决策时更新其requests，不以遗漏登记或“忘记调用”充当跳过理由。脚本保证已登记决策被调用，不能自动识别未登记的隐含语义问题。

进入决策步骤先提交普通 result（status=in_progress）。workflow.py 自动通过固定 Node 入口调用 Jev，传递最小请求；不执行输入中的代码，不读取 EDA。回执、输入指纹及调用次数随结果保存在原 state.json，不新增状态机。需要 Node 与现有 TypeSafe 密钥。原始文件、私有路径和无关信息不得装进候选 evidence。

- 同一 basis/请求沿用结果，标 `reused:true`、`calls_this_run:0`；来源/计划修订改变后重新调用。
- 无密钥、错误、超时、none 或低置信度使该结果成为 blocked。重复 result 不会无限重试；显式 `retry_decisions:true` 才重试失败调用。
- verified 除原有分项检查外，还需 `decision_selections:{"source_priority":"official"}` 与真实推荐一致；未记录采用则 blocked。若工程师否决推荐，应以真实工程师意见更新依据/计划并说明确定性选择理由，不能篡改旧回执。
- 用户提交的 result 不能自行填 `jev` 回执。check/next 是只读状态查询，不触发服务；自动调用发生在 result 执行入口。
- 推荐不是硬件验收；选中优先核查对象不表示其引脚或封装合格。回执防漏调用不保证助手登记完整，也不对人工直接修改 state.json 提供防伪证明。

验证：`python scripts/test_jev_dispatch.py` 覆盖确定性省略不调用、自动调用、失败拦截、采用记录、缓存、修订失效和显式重试；`python scripts/test_workflow.py` 回归已有状态流程。
