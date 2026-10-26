# 从资料、工程档案到落图

工程师确认设计，AI 决定表达方式，现有执行层精确落图。这里复用同一个 MCP、队列、runtime 和任务记录；不增加第二个调度器。

## 输入准备与交付摘要（2026-10-15 已限定实机，更新包待采用）

本更新中的MCP/CLI已用独立进程完成真实三器件模块的新prepare、连续落图、审图、保存和自动摘要；当前常驻MCP及全局Skill尚未采用。Go r3已在本机正式加载；其他机器按更新包APPLY在安全检查点采用，不凭版本字符串推断加载。本包不提供新Connector或新服务，旧snapshot-only prepare与显式runStages保持兼容。日常优先按SKILL.md“日常最短执行”，下方旧分阶段/复刻细节仅在实际触发时读取。

### 一次声明，程序补齐执行输入

AI 在 `module-declarations.json` 使用同一个 `easyeda.plan/1` 的设计字段：`schemaVersion`、`goal`、`semanticTarget:{schemaVersion:1,components,nets}`、`layoutHints`、`sources`、`confirmation`，按任务保留 `requirements/missing/limits`。不写 revision、steps、stages、executionReceipts，不复制旧 sourcePrimitiveId/idMap。器件属性、真实 device/symbol/footprint 库身份、全部逐脚网络、内部 members 和接口名称均由已确认设计提供。不能从检查通过或截图反推身份。

`layoutHints` 保留 mode、groups、netPolicies、visibleNetNames、interfaceStubs；`parts` 每器件一项 `{key,x,y,rotation,rotationMode,mirror,subPartName}`。坐标、旋转语义和子部件名须有依据，不自动设计布局。当前只支持已验证 continuous v0 的空白页新模块（少于100件、direct 内部连接及命名短接口）；已有工程返工继续原入口。requirements 引用固定阶段 place/measure/routes/labels/final，未知设计或不支持要求首写前拒绝。

已有确认依据由调用者明确记录 `confirmation:{status:"confirmed",authoredBy:"caller",evidence:"实际确认依据"}`；程序不生成确认、不替工程师审批。原始资料继续保留来源路径，哈希由程序计算并核对已声明哈希。

调用同一入口：

```json
{"operation":"prepare","snapshotFile":"绝对路径/当前空白页.json","planFile":"绝对路径/module-declarations.json","outputFile":"绝对路径/plan.json","window":"已核实窗口ID"}
```

它只生成 plan.json、plan.md 和 plan.job.json，声明新的 plan.execution 目录，不操作 EDA。固定 place→measure→routes→labels→final、逻辑 scope、routeFrom/labelsFrom、target 和 job.expectedHash 均由程序填充；run 内部继续绑定实际实例 ID/新 plan 哈希和真实 wire ID，不预填。结构及共用 continuous 前置在写文件前检查，实际 action catalog 仍在 run 首写前核对。输出必须是新路径，拒绝覆盖原资料、旧 plan/job 或复用回执目录。

先审阅生成 Markdown/JSON，再使用返回的 jobFile 连续执行；job 的哈希绑定该版确认文件。历史快照只能生成不执行的候选输入，不能证明当前页面仍空白或旧 window 仍有效。历史候选演示的旧页已非空，**不得执行旧 job**；正式任务必须使用当前已授权目标与实际快照。

### 保存后自动产生摘要

实际看图后仍由 AI 明确 `visualAccepted:true`，沿原 finish 门禁保存。finish 先原样落盘 delivery.json，再派生同目录 delivery.summary.json/.md；摘要失败单独返回 deliverySummary 错误，已保存的事实和原始回执不变，不能为补摘要重放 finish。

历史交付或摘要重建复用同一入口：

```json
{"operation":"summary","deliveryFile":"绝对路径/delivery.json","outputFile":"绝对新路径/summary.json"}
```

它只读取 delivery 绑定的真实 run/plan、managed 阶段/原失败、图像及编译依据，校验哈希并生成派生视图；不调用 EDA 或知识服务，不改变原件。保存、声明范围、原 fail/unknown、独立 readback 和来源桥复核分别列出，wholePageComplete=false 不改成整页通过。摘要不是业务/PWF 第二状态，也不证明新版本已绘图。

计时按原 JSON UTC 计算，保留毫秒解析精度；自动列 run/控制耗时、阶段边界及 run 文件 mtime 至原生保存的衔接区间（文件复制/触碰会影响 mtime，不能当不可变回执时间）。模型与宿主等待、纯审图、完整 Agent 调用数没有原记录就写 null/未测量。可选 timingFile 只复用本项目历史记录的 timing.dailySampleStart/startBoundary，且必须绑定同一 delivery SHA/target；来源标为调用者历史记录，不能冒充原生或用户消息起点。不计算不同条件提速比例。

同包 `node mcp/src/plan-cli.mjs <绝对 input.json> <绝对新 result.json>` 与 MCP 共用入口；prepare/summary 是纯本地文件操作，不读取 action catalog 或启动后台。输出保留完整回执，控制台摘要给 jobFile/摘要路径，避免临时拼阶段和手工换算时区。

## 文件分工

- **snapshot.json**：本次按任务读取的真实工程观察，原样保留已取得的身份、属性、逐脚网络或几何信息；未读取字段保持未知，不为档案齐全补读无关信息。同名 Markdown 是供人查阅的摘要和逐脚表，不能替代 JSON。全页电气覆盖不等于全部显示/隐藏属性库存。
- **plan.json**：目标工程/页面、器件和逐脚目标、来源文件与哈希、布局意图、具体 typed steps。它表示要做什么，不表示现场已经完成。同名 Markdown 供工程师审阅；修改 JSON 后调用 `easyeda_plan(render,planFile)` 自动刷新当前修订的阅览文件，执行绑定 JSON 的准确 SHA256。
- **原始资料与图片**：保留在用户项目里，通过 sources 的 path/sha256/anchor 引用，不复制进插件。图片负责图形理解及最终排版审阅；JSON 负责身份与连接。
- **局部 patch**：引用固定 baseline 的路径和哈希；缺席对象不等于删除，几何读取的 null 网络不覆盖原来已知网络。当前档案保留 base+patch，不伪装成新的全页快照；现场缓存仍由原 runtime 更新。

## 现有原理图读取、重画与返工（兼容/条件入口）

1. 按目的选取范围和字段，初次读取也不默认全读。例如连接基线用 `easyeda_snapshot(domain:"schematic",window,target,profile:"electrical",includeTexts:false,outputFile:"绝对路径/snapshot.json")`；局部增加 `primitiveIds`，排版改用 `profile:"geometry"`，可见文字按需补读。已有足够证据就直接复用；未确定对象时先取必要清单定位。只有任务确需完整属性与页面图元时才显式用 `full`，与绘图无关的 3D 模型、图框隐藏属性不作为前置条件。快照返回路径、计数、缺项；部分读取明确标局部，默认不覆盖已有文件，cacheOnly 不能生成新鲜基线。
2. 整页重画时，`easyeda_plan(operation:"prepare",snapshotFile,outputFile:"绝对路径/plan.json",goal,sources)` 从覆盖该页器件及逐脚网络的基线生成独立目标与 Markdown；缺少必要身份再定向核实，不为 prepare 补齐无关显示字段。局部返工不强制 prepare 全页目标。 库源UUID与工程内实例UUID分开；几何读取给出的实例ID不能直接当独立库身份。缺库来源时只定向核实已选器件及其封装关联，保留来源回执；不从验收结果抄一个ID替换目标来消除unknown。保留已读器件属性和全部目标引脚，steps 初始为空，confirmation 初始未确认，不替用户签字。
3. AI 结合已确认设计及当前图片编写/修订 steps。布局坐标与线形属于表达，不从网表自动推定最佳画法。新建器件的 primitiveId 由实际返回建立映射，不能重用旧实例 ID；有依赖的新建操作分少量阶段完成。任意脚本可调用 `helpers.call` 复用 typed 适配，raw 脚本整体仍不能自动认定规则通过。
4. `easyeda_plan(operation:"inspect",planFile)` 校验文件结构与所有来源哈希，返回本版 SHA256；旧 `apply(planFile,expectedHash,window)` 提交已写好的完整 steps，并在最后追加一次 `schematic.target.check`。可选 `stages` 按下文指定阶段执行。已准备完毕的连续阶段可用下述 `run`，不得含糊执行全部阶段。除已声明 routeFrom 的接线编译外，不自动生成步骤，不自动重试或主动保存。既有 batch/execute 入口继续可用，阶段执行完也可用 `check(planFile,window)` 单独核对最终目标。

### 已准备阶段连续执行（显式runStages兼容方式）

`easyeda_plan(operation:"run",planFile,expectedHash,window,runStages:[{stage:"measure"},{stage:"connect",edit:{...}},{stage:"final"}])`复用同一planTool/apply和managed回执；也可通过已有plan-cli JSON入口调用。显式给出同一方案的连续阶段及各阶段edit，必须启用executionReceipts。每个阶段仍核原方案哈希/来源、前继和必要回读；成功后直接进入下一项，不逐阶段返回AI组织命令。

`connect` 可以是声明式接线阶段：`check:"geometry"`、`steps:[]`、`routeFrom:"measure"`，其中 `measure` 是同一 `primitiveIds` 范围的较早 geometry 阶段。confirmed plan 必须已有 sources/evidence、完整 `semanticTarget.idMap`、明确 `layoutHints.visibleNetNames` 与策略；该接线范围内所有已连接网络均为 `direct`。同一次 run 先执行并核对 measurement，再将它的实际几何（精确名称网络还包括 pin-net 状态）交给 `compileRoutes`，将受哈希绑定的 artifact 写为 managed `stage-XX.routes.json`，随后只消费 artifact 的 typed steps 进行 apply，最后进入 final。AI 不重新拼写 steps。

Compiler 将同网、共享端点且无分支的连续段合并为完整 polyline，一次原生创建；分支、T 接点和跨网边界保留，匿名网仍不传 net。减少短段中间态不等于原生调用不会失败，未知写入仍先回读。

首次发送前核各阶段 edit 的确定性前置并拒绝范围中已有的回执（成功、失败、pending均不重放），停止/失败/unknown后返回已完成、剩余阶段、原始回执和耗时。design 的身份验收绑定独立 delivery target；局部 after_edit identity 是 layout 保持检查，不能把其 not_applicable 声明为设计修改的硬通过。剩余列表包含失败项，不能直接当重试命令。恢复先核现场和已有回执，按原有方式准备剩余工作；不删除失败回执冒充新执行。run成功只表示所选阶段通过，后续实际图面、保存及交付仍走原入口。

本入口串行执行已准备步骤以及上述受声明约束的 `routeFrom` 接线。它不自动摆放器件、不绑定新实例 ID、不重试、不保存，也不根据图面作布局取舍；需要这些决策仍交回调用者。不能预填坐标/ID或用目标数据冒充实测。members 匿名内部网按独立目标的完整 members 集合验收，不能因自动编号或截图推断通过。运行耗时包含阶段回执落盘，不包含调用前 AI 规划。

已完整执行的 design 单元若唯一必要硬未通过项为 `pin_nets:unknown/fail`，程序最多补一次独立的 `schematic.components.list` 只读复核。其 `verifyDesign` 绑定原 `primitiveIds`、`expectedPins`、`expectedIdentities` 和受影响导线；复用同一 localRules，在完整文档网表上核成员。原写入/逐脚fail或unknown回执保留，新证据另存 `designCheck`，并复用为该阶段 readback。部分执行、写入失败、其他硬错误/未知不走此分支；独立读取仍fail或未知则停止，不循环、不重放、不用同位置修改触发检查。旧扩展未返回绑定字段时仍为 unknown。业务适配重新核对新旧目标/范围/引脚声明，不信调用者自填的通过标志。
5. `target.check` 对完整页面的器件/引脚目标核对少件/多件、少脚/多脚、身份、目标属性及逐脚网络，输出 pass/fail/unknown 和当前实例映射；这不要求调用者另取 full 属性库存。它不承担图面、电路设计合理性、实物封装或持久化验收。整页重建才用全页检查；局部返工继续现有 edit 范围核对，避免每次全页重读。

最后看当前图面、执行任务要求的 DRC，再按工作规则保存。中途 partial/unknown 只回读受影响部分并补剩余操作，不整批重放。

### 局部显示任务的明确验收范围

#### 工程师审图后的批量文字纠错

需要同时修正多处位号/参数排版，并证明范围外图元及逐脚网络保持时，使用 `easyeda_plan(operation:"prepare_correction")`。准备阶段只读取本地快照，输出原有 `easyeda.plan/1`、Markdown 和 `.job.json`，不调用 EDA。输入示例（路径与 ID 使用本次实际值）：

```json
{"operation":"prepare_correction","window":"实际窗口ID","snapshotFile":"绝对路径/snapshot.json","outputFile":"绝对路径/correction.json","primitiveIds":["器件ID"],"corrections":[{"primitiveId":"位号属性ID","props":{"x":100,"y":120}},{"primitiveId":"参数属性ID","props":{"rotation":0,"fontSize":8},"fontSizeUnit":"display"}]}
```

`primitiveIds` 与 `region:{minX,minY,maxX,maxY}` 二选一；区域仅选择边界完全在矩形内的现有器件。每项 correction 必须属于所选器件的原生属性，只支持位置、旋转、字体/样式和可见性，禁止修改 Key/Value、器件位置和接线。该入口的基线需单页完整 `full` 快照、`includeTexts:true`，含已知引脚网络及页面图元；普通局部档案不能冒充完整保留依据。

审阅返回的 plan 和 job 后，将原样 job 交同包 `easyeda_plan` 或 `plan-cli.mjs` 执行。所有属性修改在一个原生批次内，批前核基线，批后复用现有比较器核范围外图元、全部器件/网表以及被改属性的其他字段。一个批次固定进行前后完整保留读取与一次局部显示回读，不逐属性往返。返回现有图面审阅入口，不自动保存、不声明整页或工程师验收。旧基线、partial/unknown、范围外变化或已有执行回执均停止；按实际回读重新准备剩余修正，禁止重放整批。新入口须由采用该源码的新 MCP/CLI 进程提供。

仅修改文字位置/可见性的任务可在同一方案明确`acceptanceScope:"local_display"`，默认仍为page。该路径只允许非空scoped、纯官方attribute.modify显示阶段；禁止final及delivery requirements。原始阶段回执仍核属性owner/Key/Value/字段、器件身份/姿态与范围，末端阶段代表局部验收，不执行整页target.check；完整语义资料仅作引用。当前局部读回包含必要texts，不取引脚、网表、BBox或导线。业务层再用当前图面、受保护finish和固定原始保存/关闭/打开/局部回读证明持久化；原生缺少的样式字段不能宣称保存重开通过。局部范围及stage绑定acceptanceSha256，改成page或改scope不能继承旧通过；page、电气/几何及接线任务保持完整原契约。

局部图面证据须保存runtime的原始修改/截图记录和实际导图回执，绑定本次target、scope、generation与PNG哈希；单独的visual_review成功响应不含这些事实。业务文件字段只维护在资料到交付的structured-workflow.md；程序核图像归属，AI/工程师负责图面判断。

保存重开时先读当前document，close固定uuid/tabId，open显式uuid并保留返回splitScreenId。页面已经关闭时，不把旧documentUuid当成打开前的当前文档断言；open前断言工程，成功后从原生context及局部回读核对完整目标。失败/duplicate先读取请求状态与现场，不能直接重放。

## R1：方案中的要求声明和表达意图

R1 由 AI 依据工程师已确认的设计，在同一份 `easyeda.plan/1` 中填写当前任务要采用什么要求、在何时以何种方法检查，以及表达上的分组和网名意图。声明用于审阅、文件哈希保护和后续实际回执的对应；它是 `declared_only`，不会自动调用检查、执行步骤或把任何要求标为已覆盖。

`prepare` 可接收 `requirements` 及补充的 `layoutHints`；已有 `layoutHints.mode` 和 `layoutHints.parts` 保留。准备后才需要 stages 时，可编辑同一 JSON 加入 stages，再用 `render` 重建 Markdown。所有修改后的 plan 都须重新 `inspect` 并使用新的完整文件 SHA256；`inspect` 对 requirements 仍是 `declared_only`，不凭声明生成通过、执行或覆盖。R2 的 apply 才把已支持的声明映射到当前阶段的真实回执。

范围边界：Open 内部前继阶段按目标、来源、stage 和本阶段适用要求的 `requirementsSha256` 绑定复用；增删或改变已执行阶段的要求使其旧回执失效，修改无关后续阶段要求不会推翻该前继。业务层 `open_receipts.py` 仍按当前完整 plan 哈希接收回执，旧版回执不会自动用于新版业务结果。R2 只核对当前阶段的一次实际修改和当前回读；跨阶段或跨修订的证据聚合仍保守拒绝，留待 R3。既有旧门槛不因 requirements 迁移或取消。

```json
{
  "requirements": [{
    "id": "S10",
    "sourceId": "open-working-rules",
    "when": "after_edit",
    "method": "ai_review",
    "check": "visual_review",
    "severity": "warn",
    "componentKeys": ["unique:U1"],
    "netNames": [],
    "stageId": "labels"
  }],
  "layoutHints": {
    "mode": "preserve",
    "parts": [],
    "groups": [{"id": "power", "purpose": "电源表达", "componentKeys": ["unique:U1"]}],
    "netPolicies": {"VIN": "local_power"},
    "visibleNetNames": ["VIN"]
  }
}
```

- 每个 requirement 只有 `id`、`sourceId`、`when`、`method`、`check`、`severity`、`componentKeys`、`netNames` 和可选 `stageId`。`sourceId` 必须引用同一 plan 的 `sources:{id,path,sha256,anchor?}`；普通规则文件仍是普通 source，不能伪装成 knowledge adoption。
- `when` 只能是 `before_write`、`after_edit` 或 `delivery`；`method` 只能是 `native`、`checker`、`ai_review` 或 `manual_unknown`；`severity` 只能是 `error` 或 `warn`。`check` 是本次任务可审阅的检查名称，不是自动调用指令。
- 同一 `id + when + stageId` 只能出现一次。`componentKeys` 和 `netNames` 必须引用 semantic target 中的实际对象。两个数组都为空时，表示指定 `stageId` 的完整声明范围；没有 `stageId` 时，表示整页范围。给出 `stageId` 必须匹配已有 stage。
- `groups` 只表达 AI 依据已确认设计作出的功能/阅读分组；每项含唯一 `id`、`purpose` 和真实 `componentKeys`，不要求工程师逐组确认。`netPolicies` 只允许已有的 `direct`、`module_port`、`local_power`、`local_ground` 值，且只对已声明的网作表达/编译意图，不替代实际连接检查。`module_port` 是现有路由策略名，不要求绘制六边形端口；跨区域表达默认使用短导线和真实 Name。`visibleNetNames` 是唯一的真实网名数组；字段缺省表示尚未决定，不能当作 `[]`。
- 独立设计意图仍来自确认的资料、用户方案和 semantic target，不能从修改后的图反推。图面 AI 审阅只记录实际所见的文字、接线表达和可读性；它不替代 native/checker 的电气、身份、范围、保存或重开证据。

## R2：已支持要求的当前阶段回执

R2 不增加规则引擎。`apply` 只识别固定映射：`checker/source_binding/before_write` 在发出任何 EDA 动作前重核对来源哈希；`checker/{identity,pin_nets,valid_wires,pin_exit,text_overlap,network_scope,target}/after_edit` 读取一次完整修改单元的 Connector 局部规则回执；`checker/{schematic.target.check,identity,pin_nets}/delivery` 读取当前独立 page target；`native/drc/delivery` 读取一次原生 DRC。其他 `check`，以及 `ai_review`、`manual_unknown`，保持 `unknown`，不因名称或声明变为 pass。

局部 checker 要求 batch 规则 scope 和当前 readback 覆盖声明对象，project/document 与目标一致；声明可为本次完整修改范围的子集。每个映射到的 `componentKey` 还要以 readback 的 `primitiveId`、`designator` 和可用的 `uniqueId` 核对独立 semantic target。实际器件记录缺失、重复或身份不符即为 unknown，只有范围编号没有记录不算覆盖；partial 不能以已成功部分提升为通过。`warn` 可记录但不阻塞；原生或 Connector 返回的 error 不得降级为 warn 或 pass。

有适用的 error 级 `source_binding` 时，来源不匹配会在写前阻止该批；有 error 级 local checker 时，必须先把 scope 绑定到本批 edit。程序为该 scope 加入 `expectedIdentities`。调用方请求 `edit.mode:"design"` 时，程序从 semantic target 中已映射且状态明确的引脚生成 `expectedPins`，调用方给出的值必须逐项相同；映射、目标或引脚状态不充分则拒绝，不能凭调用方的 pins 改写设计目标。

本阶段有尚不支持的 error 级 `after_edit` 方法/检查名，或同时声明 error 级 `after_edit` 与 `edit.complete:false` 时，在写入前拒绝。中间阶段允许临时未完成，必检项应绑定真正完成该修改单元的阶段；不能用 complete:false 跳过已声明的硬检查。业务导入从原始执行、当前回读、独立目标及 DRC 重新判定，不采信调用方填写的 `requirementResults`。

DRC 只在 final stage 有显式 `native/drc/delivery` requirement、该阶段完整执行后运行一次；error/fatal 失败、unknown 保持 unknown，warn/info 不阻塞。它不证明图面可读性、设计合理性、保存或重开。

### 三种工程师场景

1. **纯显示小改**：读取被改属性及其所属对象的最小范围，使用一个 `edit` 完整单元完成显示动作；不为显示动作额外读取 pin 网络。有现成 plan 时关联其中已经声明的适用规则；独立局部任务沿用已有任务记录中的适用规则和证据，不要求完整 plan。它不把显示类动作说成电气或整页通过。
2. **局部重接**：保留当前目标中的相关逐脚网络，先读取受影响器件、引脚和关联导线；在一个 `edit` 完整单元中使用由 semantic target 生成的 `expectedPins` 执行重接。局部 runtime/readback 证明该单元，若需交付级结论再由 final target 和明确选择的方法处理，不能用一次局部回执替代整页验收。
3. **整页重画**：从覆盖目标页面的完整 snapshot 运行 `prepare`，在同一 JSON 的 semantic target、requirements、layoutHints 和 stages 中表达设计与步骤，并由同一 JSON 派生 Markdown。final target 只验证独立语义目标；图面审阅、保存和必要重开仍是分别声明、分别取得的证据。


### 前继要求与表达意图绑定

managed stage 回执的 predecessors 只指向固定前继文件及其 SHA256。适配器从原始执行/读回重新计算具名阶段要求；同目标、来源、独立语义、阶段、要求与 expressionSha256 未变才可复用。后续修改相同范围（含实际 component.modify ID），旧 after_edit 证据变为 unknown，须在相应阶段取得新检查；不复用 caller requirementResults，不重放动作。未具名要求不跨阶段拼接；delivery 仍由最后阶段取得。复用证据标明 post_stage，不声称页面实时锁定或任意外部人工改动自动可知。

业务导入只在单次复算中缓存已核对原始回执，避免逐层重复展开；跨请求不缓存通过结论。编译器必须使用 confirmed plan，表达意图指纹进入同一 stage/route/label binding。groups 和策略不会代替摆件的视觉取舍；module_port 等未生成的连接返回实测端点，由 AI 明确短引线，不猜网络、不生成默认六边形。

## 按实际回执编译下一阶段

`prepare` 仍生成 `steps:[]`，不预编造实例 ID。需要分阶段时保留顶层空 `steps`，在同一 plan 加入 1～32 个 `stages`；每阶段包含 `id`、`steps`、`check`，可加局部器件 `primitiveIds`。阶段 ID 唯一，`check` 为 `geometry` 或 `final`；只有最后一个阶段可为 `final`。空步骤仅 final 或声明 `routeFrom` 的 geometry 接线阶段可以执行；后者必须引用更早、同 scope 的 measurement geometry stage。

```json
{
  "steps": [],
  "executionReceipts": {"directory":"D:/当前项目/execution"},
  "stages": [
    {"id":"place","check":"geometry","steps":[
      {"action":"schematic.component.place","payload":{"libraryUuid":"已核实库UUID","uuid":"已核实设备UUID","x":100,"y":100}}
    ]},
    {"id":"connect","check":"geometry","primitiveIds":["实际新器件ID"],"routeFrom":"place","steps":[]},
    {"id":"labels","check":"geometry","primitiveIds":["实际新器件ID"],"steps":[]},
    {"id":"final","check":"final","steps":[]}
  ]
}
```

这只是现有完整 plan 的字段片段；示例 UUID/ID 必须替换为实际值。调用 `apply` 必须传 `stage:"place"` 和当前 `expectedHash`。中间阶段执行一个原有 batch 后，从 place/modify 的实际回执及已指定的 primitiveIds 得到器件范围，只发一次 `schematic.components.list` 几何读回：普通 geometry 阶段读回含引脚坐标和实测边界，用于后续接线；严格纯显示阶段只读所属器件的身份/姿态，逐属性核对原生 verified、ID、父对象和实际字段，report.readbackProfile 为 display，不读取引脚/BBox/导线。该 profile 由实际动作及显示字段推导，业务适配器独立复算，不信调用方声明。接线/属性等阶段明确填写相关**器件** ID。缺 ID 不退回全页读取，也不把阶段标为完成。

返回的 `execution` 保留原批次回执，`readback` 保留局部读取回执；`stage.completed` 仅表示本阶段动作和所声明读回完成，`wholePageComplete` 始终为 false。partial、未知或取消保留已成功动作；能定位实际 ID 时读回已知范围，不重试写入。dryRun 不读工程、不标完成。中间临时断线阶段按任务传 `edit:{mode:"design",complete:false}` 延后原有完整单元电气检查；这不构成电气通过。

普通阶段由 AI 根据 `execution` 和 `readback` 的真实 ID、引脚坐标编写下一阶段静态 steps，修订同一 JSON 后重新 inspect/render，使用新 hash。`routeFrom` 接线阶段不由 AI 回填 steps：run 只接受已确认的声明，读取前继 measurement 后编译并受 artifact 绑定。开启 `executionReceipts.directory` 后，apply在首个EDA请求前以wx建立固定`stage-01.json`等回执；成功前缀、目标、来源和独立语义目标锁定，后续步骤/revision可更新，idMap新增值必须是前继实际观察到的实例ID。成功阶段不能重放，前继缺失/pending/partial/unknown时拒绝进入下一阶段。dryRun不占用正式回执。

有前继几何且本阶段需要写入时，只定向刷新本阶段相关器件；纯显示比较身份/姿态，真正几何及接线仍读实测引脚/边界，含写入的final阶段也执行；工程/对象范围不符、实测缺失或漂移时保存unknown及原回读，先决定局部修复，不重放整个批次。不含写入的final只运行完整目标检查。partial/unknown恢复使用原实际回执确定已完成动作，再编写仅含剩余动作的局部修复方案，保留原回执；不删除回执强行重试。计划不支持`$ref`；run仅推进已准备的显式连续阶段，不自动生成后续参数或新增第二状态机。本地回执是可审阅的项目文件，不是防篡改签名或用户批准凭据。

最终阶段执行成功后独立调用一次 `schematic.target.check`，只有工程匹配、verified且非partial的pass才完成该阶段。目标、来源哈希与逐步骤 `_target` 绑定始终保留，计划禁止切页、嵌套 batch 或任意 debug 脚本。旧无executionReceipts的手工计划兼容，但不具有上述固定顺序与防重放保护。

新增源码接口：staged apply可传`receiptFile`绝对新JSON路径（不能dryRun）。写入前先落`easyeda.execution/1`的pending；完成、partial或抛错均保留实际方案/来源哈希、target/stage和完整批次/回读，返回文件路径/哈希。已有文件在EDA调用前拒绝，不用覆盖回执来重复已落图步骤；未知写入先局部回读。回执可交给已有业务workflow结果入口，不自动保存、声明视觉通过或提升为整页交付。配套运行MCP未升级前不能使用新字段。

仅测试或使用同包本地新 MCP 源码时，复用固定入口，不再每轮临时编写 import/参数/回执脚本：

```powershell
node mcp/src/plan-cli.mjs "绝对路径/input.json" "绝对路径/new-result.json"
```

输入仍使用 easyeda_plan 原有参数，支持 inspect/render/check/apply 及 compile_routes；apply 必须显式提供 planFile、expectedHash、window、stage，edit 仍按本次影响范围声明。入口读取同包动作目录，沿用现有 planTool/compileRoutes、目标绑定与失败回执，不重启服务、不自动重试、不自动保存。结果文件必须不存在，执行前占用，防止覆盖已有证据。调用摘要给出耗时，完整回执留在该文件，managed stage 仍是原有执行记录。

staged apply 回执自动带 timing：各次调用及本次入口至检查结束的耗时；不包含调用者规划、视觉审阅和末尾回执刷盘。命令行结果另记录入口总耗时。不能拿该耗时代替用户从发出需求到拿到结果的总耗时。

geometry 阶段最后一步若恰好是成功的 components.list，且项目/文档、对象全集、引脚/BBox及所需网络覆盖合格，直接复用其原生结果并记录 readbackSource（原请求和步骤序号）。不复用较早步骤或历史缓存；不满足条件则保留原局部读回。仅当下一阶段包含写入时进行前继几何刷新，纯读取阶段不重复前置读取。器件来源在计划准备时使用独立库源及已验证关联，不能把工程实例ID当库源ID；实际缺项仍按需查询。

## 内部直连网：按完整引脚成员核对

必须长期随插件分发和执行，不能依赖某个Agent的聊天记忆。确定性检查维护在程序，使用顺序维护在本文件，布局取舍维护在working-rules；项目PWF只记本次目标、证据和未完成项。

- 在同一semanticTarget.nets中，内部直连网络显式声明 `match:"members", scope:"local"`，并保留独立的完整members。name作为方案内部引用，不要求等于嘉立创自动编号。该网必须至少两脚、netPolicies为direct，且visibleNetNames显式排除它。缺省或match:name继续严格核对真实名称；电源、跨模块/跨页以及用户明确指定名称的网不能因不显示文字就改为members。
- 程序读取实际原生网表：所有预期引脚必须属于同一非空网络，而且该原生网络不能混入任何额外引脚；漏接、拆分、误并均失败。没有完整原生索引、缺引脚或有歧义返回unknown。NC与明确未接脚继续分别检查。自动编号只作为实际证据，不反写独立目标、也不建立另一套绑定状态。
- 编译器为members网生成不带net的真实导线，允许从无命名锚点的引脚开始；仍通过原路由与线形核对，只生成一个相接线路岛。落线阶段必须是完整design edit；expectedPins由独立方案派生，members形式为 `{"members":["R3.2","R4.2"]}`，涉及的整组器件须在本次范围。Connector复用已导出的文档网表执行局部检查，最终target.check使用同一比较函数，业务工作流从同一目标复算预期值。
- 功能需要kb.22 Connector及配套MCP源码；旧版本不会因此自动升级。网表通过不证明图面、保存或重开；仍在完整修改单元结束后查看实际图面。旧的精确名称方案保持原约束，不根据截图猜成员、不事后把实际误连当作目标。

## 固定位置布线和落线前核对

大量接线优先调用同一`easyeda_plan`的离线编译入口：

以下命名锚点限制只适用于match缺省/name的精确名称目标；members方式按上节执行。创建时的名称策略直接消费`layoutHints.visibleNetNames`：显式声明后，每个可见direct网优先继承实读匹配的原生锚点；没有原生锚点时才在首条导线指定一次真实net，其余导线按相接顺序省略net并继承；不在可见集合中的direct网只生成继承步骤，必须由原生回执中`netlistAvailable:true`且逐脚net已匹配的端点提供真实依据，再沿已规划相接线路传播。只读几何不能证明继承网络，需要时在同一局部measurement读取`includePinNets:true`，不另读无关属性。没有依据或出现无锚点线路岛时拒绝产出可执行artifact；不会降级成全部命名后隐藏。字段缺省保留旧方案兼容，不代表已决定名称策略。

实机创建样例已验证：指定网名仅首段传`net`，后续相接段省略；孤立内部连接省略`net`，由原生网表生成自动编号且无非空Name文字。此证据不代表独立目标的命名网可静默替换成自动编号；精确名称目标对无真实锚点的内部网仍拒绝，members目标已接入无名创建。几何修改必须保留实读的电气命名：当前宿主直接`sch_PrimitiveWire.modify({line})`省略`net`曾使指定Name丢失，已有非空Name时连同其真实`net`提交并核对逐脚、Name和线段。已有匿名线使用仅line修改的实测路径；本宿主匿名线传`net:""`时曾即时返回新坐标但后续实际线路不变。只认可后续真实线段回读，不能用原生修改即时返回值代替；这些是本宿主观察，不推定全部SDK行为。

刚放置、尚未连接的内部网通常没有上述真实命名依据；当前不得让它自动改名后冒充独立目标的精确网名通过，精确名称模式继续保留该限制；members新画接入后仍须通过当前宿主整链实测。新生成的继承导线也不默认追加隐藏阶段；名称位置确需调整时，仅处理实读确认存在的Name属性，不对没有Name的线套显示清理。编译输出与真实创建、当前图面及保存重开仍是分别取得的证据。

1. 完成摆件geometry阶段后，将实际实例ID写入semanticTarget.idMap；在尚未执行的connect阶段填写本次参与实测布线的器件primitiveIds，前继几何回执必须覆盖同一明确范围。调用`compile_routes(planFile,expectedHash,geometryReceiptFile,stage:"connect",outputFile,netPolicies)`，复用现有Go路由器生成并验证direct线路。局部编译只消费本范围的器件与网络策略，不要求其他模块的实例映射或实测；direct网有范围外端点时拒绝，不能漏连后称完成。非direct的范围外成员只返回逻辑引用及measured:false，不猜坐标。不从旧实例ID或net:null推断连接；没有明确目标/完整局部实测时拒绝。
2. 编译器返回新的 artifact 文件及 hash，不写 EDA、不改计划。当前 stage schema 不支持 `compiledSteps`，apply 也不消费或验证 artifact；不得把 artifact 路径/hash 写进 stage 后声称已有受控绑定。AI 核对 artifact 与当前目标、范围和已有授权后，将 typedSteps 作为静态 steps 写入尚未执行阶段并重新 inspect/render；只有改变设计含义或遇到真实歧义时才询问工程师。这仍只是待执行方案，不是已验证路线。自动路线所用的全部器件必须在写前局部范围内。
3. 只有已观察到的必要名称需调整显示时，才调用`compile_labels(planFile,expectedHash,wireReceiptFile,stage:"labels",outputFile,labels)`。已有导线改用`wireReadbackFile`＋明确`wireIds`，原生components.list局部回执须包含真实分段和器件身份；两种证据入口互斥，不为获得创建回执重建导线。已确认 layoutHints.visibleNetNames 时可省略 labels，为实际观察的同网相接导线岛保留一处既有 Name；未决定可见集合时必须显式 labels（明确 [] 才请求隐藏）。该编译器保留旧式隐藏兼容能力，不作为新画或冗余名称的默认处理；新画内部direct网不生成非必要名称，已有名称须先确认无连接/跨区域引用作用再按原生能力移除，不能直接清空有电气作用的Name；本阶段缺失的可见网列为 uncoveredVisibleNetNames，不能称已完成。标签仍需靠近实际导线并由当前图片审阅位置。AI 核对 artifact 与当前目标、范围和已有授权后，可将输出 typedSteps 作为静态 labels 阶段 steps；只有改变设计含义或遇到真实歧义时才询问工程师。当前不以 `compiledSteps` 绑定或宣称 apply 已验证该 artifact。复用`schematic.wire.labels.apply`不改网名或重建导线。当前宿主隐藏在保存重开后恢复显示，尚未证明持久化；即时规则pass与持久化fail分别记录，不反复尝试同一隐藏方法，不清空真实网名或删除导线来掩盖限制。
4. 非direct的电源/地或跨模块短线仍由AI按当前功能关系明确编排；有额外路线时使用下述validate-wires核对完整计划。编译器生成新增导线步骤，不负责清除既有导线；局部重接须将已确认的旧线处理与新线放入同一完整修改单元，不能直接叠加重复导线。完成本次全部修改后统一看当前图面、核对独立目标和所需DRC，最后通过`easyeda_runtime(operation:"finish",window,requestId:原修改批次)`保存。文字靠近/重叠、连接表达及阅读顺序保持图面审查，不以编译成功代替。

整页重建或大量接线时，先由 AI 结合功能关系和图面确定摆件，再读取真实器件 bbox、引脚坐标和方向。将独立目标中的逐脚网络映射到这些实际引脚，不能把尚未接线的 `net:null` 当成 NC，也不能用旧页面坐标冒充当前测量。

已有 Go 布线器支持保持位置的路径生成；不引入第二套执行服务：

```text
easyeda sch layout-plan --route-only --from measured.json --out routes.json --report routes-report.json
easyeda sch layout-plan --route-only --from measured.json --validate-wires planned-wires.json --out checked.json --report checked-report.json
```

`measured.json` 使用现有 schemaVersion:1 / components[].measurement / netPolicies。measurement 包含实测 designator、x/y、rotation、mirror、bbox、pins[{number,net,x,y,rotation}]，真实文字框可放 textBboxes；没有文字测量就保留最终图面审查，不用推测文字框阻塞人工布局。确认的 layoutHints.netPolicies 可直接供 compile_routes 消费；显式输入只补充未声明项，不能覆盖确认策略。功能 groups 只返回对象覆盖及跨组提示，不强制固定排版。每网声明 direct、module_port、local_power 或 local_ground。route-only 只连接 direct；其他网仍保留引脚障碍，由调用方编排电源/地或跨模块短引线。程序不移动器件，不自动生成网名和模块框。

`planned-wires.json` 严格使用 `{ "routes": [{ "net": "目标网", "points": [[x,y],[x,y]] }] }`。可先审查自动生成的路径，再合并电源/地和已规划的导线，统一验证：非零、正交、引脚出线方向、本体/真实文字障碍、异网接触及 direct 网络连通。失败不输出可当作完成的部分线路；按精确位置修改受影响器件或路径后重新核对该方案。通过是计划几何证明，不能替代落图后的原生逐脚网络检查。

新画只为确有用途的网络生成名称；接线后如需调整这些名称，按实际 wire/attribute ID 一次处理。已有冗余名称先核连接和引用，再使用已验证的原生修改能力；不默认追加隐藏阶段，也不把隐藏成功当成删除或持久化成功。标签意图来自表达方案，不能默认每段线显示一个网名。文字与线的相对放置遵守 working-rules 第7条：器件位号/参数就近成组且绝不重叠，网名基线/锚点贴到所属导线，不另加悬空偏移；先用本次已有的实际边界/图片选择相邻空位，不套案例固定偏移表。最后用当前整图核对文字、折返和可读性，再保存。局部小改继续使用已知对象与引脚，不为了形式重复整个布线流程。

## 手册、图片、文本与表格入口

知识库保存可复用的资料依据：原件及其版本、解析事实、AI解读分开关联；已确认解读注明适用型号/封装/条件、原文页码或图表锚点、未确定项。沿用现有Markdown卡片、metadata及evidenceRef，不为一次性小资料强制入库。AI解读不能替代原厂引脚/封装依据，导入可检索与工程师确认解读不同。

实际采用知识时，将完整pinned knowledge_get的JSON回执作为现有plan.sources中的本地来源（id/path/sha256），增加knowledge:{adoptedFor,conditions,componentKeys,netNames,checkMethod}。componentKeys/netNames必须对应semanticTarget；至少指定一类目标。checkMethod为native/checker/ai_review/manual_unknown，表示拟采用的检查方式，绝不是检查通过。Markdown由同份JSON生成并展示该绑定。loadPlan在任何EDA请求前核对文件哈希、冻结引用、当前来源版本/启用/批准状态及identity/scope/applicability/verification；变化或缺完整回执时先补读相关依据、修改方案，不执行旧依据。语义适用性及用户授权仍由AI/工程师确认，回执文件不证明AI理解正确。

资料建议先转成适用本项目的约束，再优先交给能表达该部分的EDA原生规则，其次既有确定性Checker、AI图面/语义审阅，最后人工/unknown。不得编造定性要求的距离；不得把原生失败降级成通过；同一要求不同部分可分工核对。当前仅绑定依据/目标/检查方式，未实现任意资料到原生PCB规则的自动编译或完整约束执行，不把knowledgeChecks.status=current当成工程检查通过。

局部续改优先沿用既有阶段：仅含preserveInstance:true的component.modify坐标/旋转/镜像修改、且能通过sourcePrimitiveId/idMap明确映射目标器件时，程序只复核绑定这些器件或关联网络的知识来源；其余返回deferredKnowledgeSources/not_checked_outside_stage，绝不标当前通过。最终检查、inspect/render、创建器件、物料/接线变更或无法映射的动作仍核对全部绑定依据。普通文件来源仍按原哈希保护。这里缩小的是资料依赖复核，不替代EDA局部readback或修改范围授权；型号/封装/网络变化应先更新plan目标及相关依据。

先按当前目标搜索/查看sources定位，仅读取命中的必要章节与图表；不要先读全库或整本手册。knowledge.11搜索的metadataOmitted列出未重复发送的卡片正文字段，完整内容仍在同一evidenceRef的knowledge_get中。搜索节选始终是partial，不能因为有适用条件摘要就当全文已读；真正采用前补齐相关条件/脚注。相同冻结版本已读内容可复用，换型号、封装、版本或条件只补相关依据。任务经验自动生成/回流本轮暂缓，保留已有知识及审核功能。

knowledge.11支持直接导入PNG：只保留原图与固定哈希，不自动OCR或推断引脚/网络，parseStatus=partial、qualityStatus=unreviewed；用evidenceRef＋includeVisuals读取冻结PNG，再由AI与工程师解读。无可靠出处不编造页码。此类原图不能冒充complete的已解读证据回执；其后确认的Markdown解读与原图分别作为plan来源关联。既有PDF/Docling结构资产路径继续复用，不重新解析未变原件；JPEG、DOCX、BOM等仍按document-intake和原件来源处理，本轮未扩展直接导入。

知识库检索后用返回的 evidenceRef 调用 `knowledge_get(scope:"section",includeVisuals:true)`，同时读取冻结原文、表格/明确脚注和对应图像；可用 assetIds 选择当前读取范围内登记的图片 chunkId。默认只读文字。旧引用无冻结图、缺图、过大或归属不匹配均为显式状态，不用新版图替代旧证据；id-only 要先取得 pinned 引用。续读保持原 evidenceRef/scope/maxChars/limit，邻近内容仍不能证明条件归属。知识服务未加载支持时按升级错误报告，不宣称已看图。

### 经验候选提交与返工（knowledge.9）

沿用 `knowledge_propose`，完整保留条件、限制、证据和实际验证范围。首次提交生成稳定 `submissionId`，与请求内容一起记入已有任务记录，`expectedCandidateRevision:0`；返回的 candidateId/revision 是后续返工依据。超时或响应丢失只用原 submissionId 和完全相同的请求重试。新的有效修改使用新 submissionId、原 candidateId、当前 expectedCandidateRevision 和完整候选正文，不创建无意义副本。

`IDEMPOTENCY_CONFLICT` 表示同 ID 内容变了；`CANDIDATE_STALE` 返回 currentCandidate，先读取并合并变化，再以新 submissionId 提交，不自动覆盖。replayed 返回第一次回执，currentRevision 如不同只能说明此后发生过修改，不能拿旧回执当当前正文。已审核候选禁止通过 propose 覆盖。旧无 submissionId 的调用仍可创建，但没有幂等保证。

批准/拒绝仍由用户通过现有管理 UI 操作，审核绑定用户看到的 candidate revision；变化后刷新并重新审阅。资料解析和已审核知识保持分离，不能因重新解析自动覆盖正式经验；unknown 不升级成硬件验证通过。

knowledge.10：引用 sourceIds 时，用实际已读 evidenceRef 的 sourceId/sourceRevision/sourceHash 组成完整 sourceVersionSet。省略只建立提交时基线，不能声称读时版本已核实。审批会核对来源版本、提交时元数据/条件和启用状态；SOURCE_VERSION_STALE/REVIEW_SOURCES_STALE/REVIEW_SOURCES_UNPINNED 表示必须重新读取依据并修订 pending 候选，不换新候选绕过冲突。

用户审核请求以 candidateId＋所见 revision 为稳定键，同决定重试返回原批准回执/sourceId/jobId。超时先刷新看真实状态，不重新提交或重新发布；冲突不能自动改审核决定。审批回执、正式来源和 lesson 索引任务同事务提交；服务重启只续索引，不重放审核。模型未就绪时 deferred 不等于向量完成。旧已批准卡保持，后台专题整理仍未实现。

复用已安装 document-intake：PDF/DOCX 等先 Docling，已有解析可用就复用；失败或格式不适用才 MarkItDown。表格单元格/公式使用 spreadsheet-intake；关键图形与数值回看原件。不把“提取出 Markdown”当成资料完全理解，也不把所有转换器都运行一遍。

Docling 的 `OUTPUT.md.assets/{document.json,manifest.json,index.json,images/}` 可直接作为 plan.sources，格式为 `{id,path,sha256?,anchor?}`；prepare 自动计算/核对哈希，inspect/check/apply 再核对来源是否变化。anchor 使用现有 library find/show 得到的位置，不编造页码。转换器不进入实时 EDA 执行链。

从零设计时，由 AI 将工程师已确认的资料结论写成同一 `easyeda.plan/1` 结构；prepare 是已有原理图的便捷入口，不是手册到电路的自动设计器。未知引脚/封装仍保留 unknown，只暂停依赖该缺项的决定。


## 日常操作的耗时边界

准备好当前操作单元的目标、对象和必要参数后，复用plan-cli的runStages连续执行；模型在资料/设计/视觉判断或真实失败边界介入，不逐条指挥已确定步骤。已有同范围实机证据不为形式重做。测量用只读入口，避免通过原值写回制造基线；原生写入未确认时停止并定向读取缺项，不自动重试或用固定睡眠猜完成。

CLI完整回执继续落文件，默认只读摘要；失败摘要包含动作、请求ID、mismatch/unknown和实际属性，只有摘要不能回答原因才定向打开原始回执。已知文件先定位符号，不猜文件名反复试路径、不整份打印嵌套MCP结果。runtime state局部查询默认返回最近12条回执，receiptCoverage说明省略量；includeObjects:true可读取完整缓存证据，不能把省略的回执当作不存在。

一次任务计时分别记录用户等待/开发总时段、成功执行段、失败返工段及未计时部分。图面审阅绑定当前结果；保存重开按实际交付需要执行。源码研发的调查、修改和打包耗时不冒充画图耗时，API的毫秒数也不能证明端到端速度。


代表模块接线：可见的命名直连网由现有route compiler在首段建立真实Name，其余线段继承连接；不要为此另建不完整锚点阶段。单脚外部接口不会由多端路由自动连出，应依据实测引脚向外方向明确短线，并与编译出的内部线路合并为一个完整design修改单元。创建同请求检查fail必须保留；先独立回读实际引脚，确认已落地后只做剩余工作，不重画已成功导线。路由输出在现有坐标容差内归一化旋转尾差，保留分支边界，拒绝退化路线。


## kb29：放置后的自动衔接

绘图方案一次准备好器件、完整逐脚目标、布局、单脚接口与后续阶段。放置步骤可明确传uniqueId，与位号同次落下；不能从实际连接反推设计目标。后续measure/connect阶段预留geometry、steps:[]，不填猜测的原生ID。

放置阶段实际完成后，固定plan-cli调用：operation:bind，planFile、原expectedHash、stage:place、bindStages:[measure,connect]、routeStages:{connect:measure}。程序重新核managed回执链、创建结果、原生位号/身份/脚号及独立库来源，然后把实际ID写入同一计划idMap与后续scope，revision递增并返回新sha256；已执行阶段或未知回执拒绝绑定。bind是确定性补充执行ID，不是新的设计审批，也不会发EDA写入。继续用返回哈希执行同一run入口的measure/connect；禁止重放旧place。

layoutHints.interfaceStubs可声明[{componentKey, pinNumber, length}]。只允许已明确的单脚、visible、direct网络；length需正且有限。路线编译器用同一测量回执中的引脚坐标/方向生成接口短线，和内部线路一并validate-wires、一并作为完整design单元落图。接口显示名从独立目标取得，不传另一套net值、不由模型手算旋转坐标；跨区多脚关系继续按其他既有表达处理。

kb29身份读取将同请求重复输入去重，并最多四路并行查询独立库数据，共用本请求新鲜项目来源；不跨请求缓存、不缩小封装/型号核对。identityTiming记录身份子段，只有安装版对照结果才能宣称提速。


## 日常交付与研发计时（2026-10-13）

本项用于已经确认设计或明确局部返工。修订源码、诊断、回归用例、构建、打包和加载核对先作为研发完成；日常任务的计时从接收该项任务起，到图面/必要检查及保存可交付为止。两者分别记录；同一用户轮次若混有研发，其完整等待不能用局部API时间或日常样本替代。

日常执行只复用当前配套包已经加载的入口。目标/窗口/必要范围确认后一次准备完整修改单元；确定步骤由既有batch或plan-cli连续执行，图面需判断时再交给AI。完整回执直接留本地，模型读取摘要和实际图片。成功后一次更新关键进度，交付前不临时写源码、补测试或重打安装包。必要研发缺口出现时记明已落地范围与原因，将当前任务状态保留，不用一次试写冒充日常完成。

失败或未知先定向回读；已有成功动作不重放，不重新准备完整工程。汇报日常样本总时间、顶层工具调用数、EDA调用数、返工及重复次数；研发及用户等待另列。未归因区间明确保留，不以不同任务/不同条件计算提速比例。

日常导图使用现有 `easyeda_artifact(action:"schematic.export.image")`，不能临时猜成schematic域入口。selection必须覆盖本次实际对象；返回的selectedPrimitiveIds缺少请求ID时不登记图面通过，先核对实际对象/返回范围再补图。finish被拒绝时不重画成功部分。

局部网名排版固定执行：按对象ID实读所需事实；已知属性ID直接实读Parent/Key/Value及几何，核实后一次batch提交显示修改和器件scope；只将本次实际器件及属性ID提交artifact域selection导图，不塞入旧来源导线ID；查看实际图面后以当前generation登记visual_review，再checked finish保存。正常分支不查整套API目录、不重读整页，不在中间写代码或打包；错误/unknown仍停止并局部恢复，不把未知当通过。

## 新增模块的页面占用依据（2026-10-13）

新增模块或跨区域重排前，先复用仍适用的整页图面/图纸范围，识别已有模块、边框和图签占用；没有页面依据就先取得一次页面图面。局部selection能证明本模块内部，却不能证明没有压到图签或范围外对象。先选页面可用区域再测量/放置，不按孤立坐标猜空白。普通局部文字返工不因此重读整页。

交付页级任务时，最后查看包含图签和全部修改范围的图面；冲突只修受影响模块，不重画未受影响部分。分组平移沿用现有group.move：其导线会重建并返回新ID，必须刷新后续网名/导线引用，不能拿旧ID继续修改。视觉仍由AI审阅，这条方法不是已实现的原生图签碰撞Checker。

计时诊断先用已加载入口的现有identityTiming及实际目标检查区分身份来源、读取和判断；连续读不冒充冷启动，不为复现旧时长重启共享宿主，不在当前秒级通过的路径叠加未验证查询优化。必要身份来源/封装/逐脚核对继续保留。

## 模块命名接口与续接边界（2026-10-13）

模块路由范围内只有一个引脚的已声明命名接口，可以用当前实测方向的短线接入同页既有同名网络。interfaceStubs必须是可见命名网、明确引脚、正长度；编译时核对本范围只含一个该网端点。范围内有多个端点、按members核对的内部网、没有显式接口声明，仍不得遗漏范围外端点。最终独立目标检查保留全网成员，不能用短线存在替代真实连接。内部直连按members生成匿名线，跨模块接口才显示Name。

使用库放置前，比较独立目标中的非库默认实例属性（例如试验标记、显示名），将必要补写作为已准备的完整修改单元，不等final拒绝才发现。原始失败回执保留；实际写入成功、只签收失败时，先回读并准备剩余动作。组件文字的managed attribute阶段目前只接受组件父ID；导线网名优先走现有compile_labels及wire.labels.apply，不把导线ID伪装成组件scope。只读batch不能假定生成修改后的规则回执，应使用明确verifyDesign或独立target.check。

已知后续缺口：新检查覆盖旧阶段失效证据的自动衔接、导线标签完整范围接入仍需完成现场验证。这次ON模块交付包含恢复，不能表述为连续无返工或已证明提速。

## 首版默认执行入口（2026-10-13）

日常路径固定为：确认设计与本次范围 → 准备一次plan和job → `easyeda_plan(operation:"run", ...)`（或同包 `node mcp/src/plan-cli.mjs <job.json绝对路径> <新result.json绝对路径>`）→ 查看返回的`review.imagePath` → 图面可接受时依次调用 `easyeda_runtime(review.visualReview)`、`easyeda_runtime(review.finish)`。这仍是现有MCP/CLI入口，没有第二个调度器。

job需包含已确认planFile、expectedHash、实际window/target和连续runStages。新画时原生ID只能由成功放置回执经已有bind取得；不能猜ID。首次绑定仍按现有bind入口完成，后续已确定阶段统一run。完成final或local_display阶段后，run自动导出当前页，检查图面与最近成功修改的观察版本一致，并返回审阅/保存参数；程序不代替AI审图，不自动签署视觉通过。图面导出失败只停在审阅准备，已经完成阶段不可重放。

证据有效性：较晚阶段有同要求、同工程文档、覆盖原对象范围的原生新检查时，沿用现有判据重新核算；其中fail仍fail、unknown仍unknown，后续若再次改动则重新失效。旧阶段失败/时间/执行记录保持原样，不把新结果填回历史。

导线Name显示修改：属性回读必须证明ID、Key、Value及指定显示字段；当父对象是导线时，当前原生导线几何必须连接本范围的实测引脚且网名值一致。当前支持直接触及范围内引脚的线段；更远的同网线段不能只凭同名推定归属，先用既有compile_labels/wire.labels.apply及明确范围。为此只在导线父对象场景补必要引脚/导线几何，不重复编译显示任务的网表。

异常续接：保留原失败，先按失败属性/对象回读，确认已经落地的动作不重发；剩余工作仍使用同一run入口。宿主不接受的坐标不可当作成功，也不可循环重试；本次Name y=685回读仍687.5，故保留实际合法锚点。复杂剩余方案仍由AI准备，尚不声称任意失败全自动续接。

计时须列清任务确认/方案准备、运行到保存、研发/打包。29154ms仅本次冻结局部job调用到保存，不是用户发指令起的完整等待，也不是整页新画速度。

### 连续绘图v0本地更新（2026-10-14，限定实机闭环）

符号身份域续接：原生target.check只有symbol的library_identity_domain_unknown时，共享plan读取当前官方project.export_source，复用有长度/CRC边界的原归档解析器，核对实例SYMBOL DOCHEAD/META.source与独立目标库UUID及library。只有证据精确对应才将临时原生比较输入中的该symbol UUID转换为实例域，再调用原schematic.target.check；原plan/source/网表及其它谓词不改。完整原unknown、原生导出/当前读取和后续原生比较均保存，不手填成功。解析器复用源码编译为同包native-identity.cjs，源入口native-identity.ts，构建命令在源码mcp/package.json的build:native-identity；没有新Connector或服务变更。

仅对最后空steps的只读final，显式`recheckFinal:true,runStages:[{stage:末阶段ID}]`可消费原settled fail/unknown；原stage-XX.json不改，新stage-XX.recheck.json用recheckOf绑定其真实文件/哈希，同plan/revision/target/sources/binding必须一致。原成功、带写入、未settled、已有recheck或跨目标均拒绝，不是通用retry。续接final通过后额外复用既有verifyDesign独立实读和原规则，以当前读取请求绑定图面/visual_review/finish，避免用旧图面/旧代次保存；此读取不冒充再次写入，原最后写入回执仍保留。当前三器件模块本例已通过这一续接、实际审图和finish保存；它不证明任意失败恢复。

目标：工程师与AI确认完整设计、必要实例属性、逐脚网络、功能分组、器件坐标/朝向和表达方式后，只准备一份plan与固定job。调用现有`easyeda_plan`的`operation:"run",continuous:true`，不传runStages；plan-cli仍读同样的job。不是让工程师填写API，也不是程序重新设计电路。

plan保留当前easyeda.plan/1目标/独立语义/source哈希/confirmation/executionReceipts契约；五个阶段按顺序：placement（只包含普通component.place）；measurement（空steps，componentKeys声明新模块逻辑身份）；routes（空steps，相同componentKeys，routeFrom指向measurement）；labels（空steps，相同componentKeys，labelsFrom指向routes）；final（空steps，check:final）。阶段名称可自定，前继必须引用实际名称。新模块阶段不预填primitiveIds，不把旧source ID当实际ID；已存在页级器件仍保留其真实idMap，作为独立final目标的一部分。

placement逐器件声明实际库Device/library UUID、唯一身份/位号、x/y/rotation/mirror。独立目标含明确脚号/状态/Net/成员、必要properties、真实footprint身份。properties只支持name/manufacturer/manufacturerId/supplier/supplierId/addIntoBom/addIntoPcb/otherProperty；库放置后与实际测量有差异的声明值，作为同一次完整接线单元的固定component.modify执行，不用AI临时补属性。不填写无关3D字段；程序不因运行方便删除设计给定的必要值。

layoutHints声明groups覆盖模块、每条受影响Net的direct策略、visibleNetNames。内部直连至少两个端点，使用match:members、scope:local且不可见Name；单端命名接口需显式正长度interfaceStub，其他端点在独立全页目标保留。当前只支持一个新模块、少于100个普通器件、五阶段、direct/internal与已有命名短线接口；NC标记、混合既有器件重排、任意自动布局或复杂失败规划先明确返回不支持，不静默省略。

连续run首写前检查固定声明/必需action及硬要求支持，缺项返回missing路径、executionState:not_executed。放置及实际回读成功后调用已有bind，仅填未执行阶段，revision/hash变化保存在bindings返回中；旧放置回执及前继哈希不重写。后续按新plan哈希测量、编译/执行route，labels编译直接消费已验证接线回执真实wire ID；匿名内部线依据执行steps识别，无需猜$网名。实例属性和标签由同一共享实现/唯一业务适配签收，不添加第二状态机。

失败/partial/unknown返回当前plan/revision/hash/idMap、已完成及剩余阶段和原始回执路径。没有自动retry；continuous不能重放已有阶段。放置成功但bind失败先读保存回执和当前plan，用已有明确阶段入口只续已核实剩余工作；复杂剩余方案仍由AI决定。final通过不等于视觉通过：返回真实图面和SHA，AI实际看图、批准后使用finishInput完成原visual_review/finish及自动delivery.json。任务/PWF一次摘要链接即可，不手拼ID或重复原始返回体。

当前限定验收（2026-10-15）：真实空页的新prepare自动生成plan/job，连续五阶段、真实ID绑定、实际图面、finish saved:true及自动摘要完成；内部直连匿名，只显示3.3V/!CTL接口。原逐脚fail→独立pass、符号来源unknown→有证据复核pass保留。程序CLI run9587ms，清理/准备至保存650417ms，不能宣传整轮秒级或已提速。新功能仅独立CLI实机、常驻MCP未更新；本包还携带已采用的Go r3文档生命周期修复。无新EEXT。大图、任意恢复、DRC/保存重开及Python业务导入未验；旧包及原始失败证据保持。

### 自动收尾：复用已审阅run结果（当前模块限定实机通过）

新run在review写入实际imageSha256；配套plan-cli写结果后返回resultSha256及finishInput。AI先查看review.imagePath，确认可接受才向finishInput添加visualAccepted:true，用现有easyeda_plan或同包plan-cli提交operation:finish。MCP直接使用时，runResultFile是已保存的原始run返回体（或CLI的{elapsedMs,result}文件），expectedRunResultHash取该文件实际SHA256，不能用plan的SHA代替。不手拼requestId/generation/screenshotRequestId或复制原始返回体。

finish按结果字节哈希、原plan哈希、完整managed阶段前缀、最新实际写入和图面字节绑定，再顺序调用原visual_review与finish。编译生成的路由也从实际batch动作认定写入，不回退至较早的pass。门禁失败/unknown不保存为通过；新delivery.json先以wx保留，再写完整审图/保存回执。文件已存在先读，不能重放run/finish；保存后写证据失败仍返回原始保存回执和未完成标记。

阶段回执、delivery.json及其路径/哈希作为交付依据，task_update/PWF仅补一次任务状态和证据指针；不再人工重复组装一份包含全部返回体的JSON。新增模块仍先准备独立设计、放置并bind真实ID；本候选只合并已有run的固定收尾，不自动准备任意新设计、绑定新器件或清除任务目标。

计时边界：R22的123338ms是消息后首次工具计时至保存，215375ms至deliveryReady；保存后整理92037ms。消息到首工具、交付记录后到最终回复未计，不能称完整用户等待已缩至3分35秒。后续入口耗时仍只代表程序区段，实际改善须另测同条件日常任务。
