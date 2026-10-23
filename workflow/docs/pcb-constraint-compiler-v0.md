# PCB Constraint Compiler v0

## 当前交付与证据边界

2026-10-08用户要求直接实操并取消限制：已去除固定测试板UUID白名单和下限/对象对preview-only限制。有效IR返回ready，可显式apply；目标身份、来源数值、不支持类型、schema、冲突和写后回读检查保留。已在SparkFun PCB实际创建4条规则、绑定45项Net并调用原生DRC；保存返回true，详见实操记录（本地历史资料未随公开插件分发）。整板69项Connection Error，未布线/无过孔时Track/Via检查为unknown，不能把规则落地当整板通过。

实现限定 Track、Via Size、Safe Spacing。三类原生能力的既有实机证据见[自动验证记录](pcb-rule-automatic-verification.md)及Track交接（本地历史资料未随公开插件分发）。Compiler 自身最终三类实机回归全部通过：生成规则→成员Net显式绑定→配置/绑定回读→原生DRC负例fail→删除反例后的正例pass→临时对象/规则清理→十一类状态恢复。每项独立证据及原生违规类型、规则名、对象ID、实际值汇总见Compiler结果（本地历史资料未随公开插件分发）。29项Compiler离线检查与原脚本9项检查通过。

首次Track请求超时，后来Connector确认 `settled_after_cancel`；只读恢复比较（本地历史资料未随公开插件分发）证实十一类现场状态与before一致。另两次回执约1MB处截断，未当成功；随后减少无关对象与重复回执、使用紧凑JSON传输，重新读取现场并以新before发起回归。原始超时及写前快照（本地历史资料未随公开插件分发）保留，没有盲目重放或重启服务。具体首个卡住API没有证据。知识库工作继续暂停。

## 输入及调用

[类型定义](../../tools/pcb_constraint_ir.d.ts)保持用户指定的两种 NetSelector 与三种 PcbConstraint；外层 IR 固定版本、毫米及工程/文档 UUID：

```json
{
  "version": "pcb-constraint/v0",
  "unit": "mm",
  "target": {
    "projectUuid": "6bde69a919dc419f98f8b06ec7b318d9",
    "documentUuid": "d260cc249c21359b"
  },
  "constraints": [
    {
      "type": "track_width",
      "target": { "kind": "net", "name": "GND" },
      "min": 0.254,
      "preferred": 0.508,
      "max": 0.762
    },
    {
      "type": "via_size",
      "target": { "kind": "net", "name": "GND" },
      "hole": 0.508,
      "diameterMin": 0.9144,
      "diameterPreferred": 1.016,
      "diameterMax": 1.1176
    },
    {
      "type": "safe_spacing",
      "target": { "kind": "net", "name": "GND" },
      "clearance": 0.254
    }
  ]
}
```

此例用于展示结构，数值不是工程设计推荐。Project Constraint 应按上述字段明确表达；v0 不从自由文本、资料建议或未知单位猜测阈值。`evidenceRef`随约束传递，真实性与适用条件由现有资料采用流程核对，Compiler 不自动批准来源。

### 来源只规定下限时保留未指定原生字段

既有完整范围输入保留原行为。Track允许省略`preferred`或`max`；Via Size可用`holeMin`代替固定`hole`及外径三值，二者不能混用。例如来源只规定最小线宽/钻孔时：

```json
[
  {"type":"track_width","target":{"kind":"net","name":"GND"},"min":0.254},
  {"type":"via_size","target":{"kind":"net","name":"GND"},"holeMin":0.3}
]
```

这两个形式在目标、schema与原生保留值相容时返回`ready`，可显式apply。`requested`保留原约束，`effectiveValues`只列明确数值（Track缺项为null），`unspecifiedFields`列来源缺项，`retainedNativeValues`逐字段列真实原生默认模板的保留值。模板的preferred/max/外径不是来源要求；不会把它们补进IR或改为“已确认”。若下限与模板已有值不相容，返回unknown，不自动抬高其他尺寸。最小值按原生分辨率向上量化。

CLI preview与模块方案继续展示这些缺项及模板保留值，`ready`只说明输入约束可以映射，不说明完整来源设计全部迁移。SparkFun新下限形式已写入并回读，但当前没有走线/过孔，原生DRC对象覆盖仍unknown，不借既有完整范围的实机证据判为supported。三种规则范围未扩展，同层Via-Via特例、环宽公式等仍单列未映射；不能把普通间距下限当完整原规则迁移。

### 按来源对象对生成Safe Spacing规则

`safe_spacing`新增与统一`clearance`互斥的`pairs`形式：例如`{"type":"safe_spacing","target":{"kind":"net","name":"GND"},"pairs":[{"objects":["Track","TH Pad"],"clearance":0.1778}]}`。标签限定当前读取中已识别的`Track / SMD Pad / TH Pad / Via`；相反顺序是同一对象对，重复对、无效数值、额外条件字段及混用clearance均fail。不接收未经验证的sameLayer/sameNet条件。

此形式在schema和绑定检查通过时返回ready，可显式apply。只改选定对象对在每个实际table中唯一可定位的数字单元格；矩阵标签、单元格或方向歧义返回unknown。`changedCells`记录真实字段路径/对象对/before/after，`retainedNativeValues`记录所有未选单元格；未声明的板边/孔/文字/测试点/铜区等保持原值。SparkFun对象对规则已写入并回读，当前69个焊盘上原生DRC未报该规则违规；未制造正反例，不证明全部对象对、同网/跨层语义等价。旧统一clearance输入保留原行为，但不得用于代替只声明部分对象对的来源要求。

现有 CLI/Connector 运行入口：

```powershell
node tools/pcb_constraint_apply.mjs --ir <约束JSON> --window <当前窗口ID> --dry-run
node tools/pcb_constraint_apply.mjs --ir <约束JSON> --window <当前窗口ID>
node tools/pcb_constraint_apply.mjs --ir <约束JSON> --window <当前窗口ID> --review
node tools/pcb_constraint_apply.mjs --ir <约束JSON> --window <当前窗口ID> --review --checks <专业检查JSON>
```

`--dry-run`只读取原生规则配置、NetRules、NetClass和身份，不读取布局对象或调用DRC，不写规则。dry-run按IR的精确target/当前window核对页面，可用于已授权的模块只读准备；apply不再固定测试板UUID，仍必须匹配IR内的工程/文档与显式window；取消白名单不取消单次任务授权。每次运行默认在 `evidence/pcb-constraint-v0-*.json`保存IR哈希、真实before与计划；可用`--output <绝对JSON路径>`将原始回执落在模块项目。apply模式另保存写回执、after、DRC，对象限定目标Net。不导航、不保存、不安装或重启MCP/Connector。

## 模块工作流只读接入

2026-10-09实际配套接入：工作区Open包提供`easyeda_pcb_constraints`，三种operation=`dry-run/apply/review`复用同一Compiler；需要绝对irFile、outputFile、window及expectedIrSha256；checksFile还需expectedChecksSha256，review必须有专业规格。只使用新证据文件，不覆盖已有IR/回执。独立同包PCB CLI保留运行daemon；MCP公共入口检查精确输入字节，CLI再次核哈希，防止读取期间文件漂移。新进程已实际加载工具并经现有Connector返回PCB身份不符，原理图未导航、无DRC/保存/写规则；安装与实际拒绝证据（本地历史资料未随公开插件分发）。当前宿主旧MCP工具列表未重载，完整PCB成功路径尚未新增实操；既有apply和专业回放边界不扩大。

专业要求属于现有模块方案的`pcb_checks`，不扩展三类原生Rule IR。当前规格为`{version:"pcb-professional-checks/v0",unit:"mm",target:与IR一致,checks:[...]}`；新PCB提案必须声明规格及`required_checks:[...,"pcb_professional_checks"]`。每项id/role/evidenceRef非空，evidenceRef对应所选模块source ID。示例判据结构：

```json
{"id":"input-segment","type":"power_path","role":"主输入导体段","evidenceRef":"已确认设计来源ID","net":"明确网名","from":{"designator":"J1","padNumber":"1"},"to":{"designator":"U1","padNumber":"5"},"minWidthMm":0.5,"toleranceMm":0.000001}
```

数值仅说明格式，不是通用设计阈值。完整回路须按器件/外部负载边界分段声明，程序不假设器件导通。`pin_role`声明`pin/expectedNet`；可选decoupling明确powerPin/returnPin/expectedReturnNet/maxDistanceMm及metric=`pad_center_euclidean`。`copper_attachment`声明componentId及baseline：器件x/y/angle/layerId/footprintUuid、明确FILL/POLY对象的type/id/原始data；基线保留原生mil记录，不手工换算这些源字段。姿态、封装或对象漂移需新审阅。身份/来源/结构不明确均unknown。

收尾保存规格哈希、逐项status、实际值、对象ID、覆盖限制和原始快照；专业结果只由同次回读复算，外部自填pass不能替代。旧模块缺规格时新verified请求保持partial。选择已审核知识作为来源复用现有source/plan修订；候选经验不自动批准，也不依赖检索排名决定通过。

2026-10-09收尾改进：`--review`只读复查当前规则/成员Net绑定、整板对象与源记录，调用新鲜原生DRC及现有`pcb check`，随后再次回读。缺规则时返回unknown，不在收尾补写。普通90°/锐角中心线候选及ERROR阻止签收；多分支INFO保留供图面审阅。旧CLI缺少rightAngles/junctionAngles覆盖、数量不一致或现场漂移返回unknown。结构化工作流申请verified自动运行此分支，失败保留partial及原始JSON。该检查不包括全铜轮廓、弧线或电热/EMI验证；安装版本需单独核对。

2026-10-09更新：用户取消仅dry-run阶段约定。以下2026-10-08只读接入与验收保留为历史；当前`result.in_progress`在原预检后调用Compiler真实apply，并回填`pcb_constraint_apply`。另可用`pcb_apply`操作；显式`pcb_dry_run`保持只读。只有身份、ready计划、原始回执及实际配置/成员Net回读共同证明落实时才继续启动；DRC fail/unknown与规则落地分别报告，不能宣称整板通过。当前Pilot真实执行见apply原始回执（本地历史资料未随公开插件分发）：复用4规则45绑定，回读成功，原生DRC仍为13项铜桥提示；并未为证明写入而重复改规则。唯一完整步骤见[硬件执行契约](../skills/evidence-to-delivery/references/hardware-delivery.md#pcb模块布局布线前的只读约束准备)。本项目源码已接通，不等于旧安装插件热更新或全宿主API门禁。

历史只读接入：新增PCB模块计划项须在原有description/acceptance/sources/depends_on/objects/domain/required_checks之外增加`pcb_constraint_ir`，其内容使用本文IR格式。由Codex从已明确设计填写，无法确定的值不补成范例默认值。没有新的模块状态机或自然语言数值推断器。

结构化项目使用既有`.workflow/state.json`，由下面的操作自动导出IR、调用同一CLI的`--dry-run`，把回执加入原有results并生成task_plan.md/progress.md：

```json
{
  "op": "pcb_dry_run",
  "item": "PCB-MODULE",
  "revision": 1,
  "window": "当前Connector窗口ID"
}
```

```powershell
python -X utf8 skills/evidence-to-delivery/scripts/workflow.py --root <模块项目目录> apply <上述操作JSON> --expect-generation <当前generation>
```

项目已有状态通过check/show读取，并通过原有propose/approve添加或修订模块设计；不会手工改生成视图。PCB新提案缺IR直接拒绝，旧记录仍可读取。只读准备成功记录partial/preparation_only，非ready记录blocked，不能标为PCB已完成或已保存。

布局/布线真正开始前的`result.in_progress`须提供window；原有execution_rules预检通过后会自动重新dry-run并附带新回执。冲突、不支持或未知结果使该项保持blocked；旧IR/来源/证据不能冒充本次新鲜规则。已有其他硬件修改未完成时，dry-run记录不会隐藏恢复读回要求。该入口没有自动apply或PCB绘图调用。

手工PWF项目直接复用`node tools/pcb_constraint_apply.mjs --ir ... --window ... --dry-run --output ...`，把同一模块的IR、原始JSON及可审阅结果纳入现有方案；不为此迁移手工PWF。输出preview含拟建规则、成员Net绑定、conflicts、unsupported、unknown及batchIssue；混合未支持批次仍可只读展示支持项候选，但整批不是ready。

真实只读验收使用当前专用测试板及明确标为夹具的模块计划：支持项拟建三条规则，显式绑定GND/TC_4；拒绝项同时报告VCC_DUT既有Track冲突与Region unsupported，并保持blocked。现场规则配置/绑定/NetClass回读一致，数值比较1e-12；保留11处约2.8e-17的原生浮点序列化差异与真实返回。见工作流dry-run证据（本地历史资料未随公开插件分发）与生成的阅览方案（本地历史资料未随公开插件分发）。夹具不是用户真实模块设计，未测试生产或实际布局布线；本轮没有规则/对象写入、DRC或保存调用。

新增11项工作流集成检查及5项dry-run检查通过；原Compiler29项、既有工作流与执行契约等相关回归分别通过。工作流门槛和生成视图来自本目录源码；已安装旧工作流插件未升级，Open MCP没有新增工具。开放raw/API能绕过此入口，当前项目AGENTS要求Codex遵守前置，不能声称全局API强制拦截。

[核心模块](../../tools/pcb_constraint_compiler.mjs)导出 `compilePcbConstraints(ir,before)`、`executePcbConstraints(eda,input,compile,signal?)`及 `buildConstraintScript(input)`。最后一个输出供现有 `easyeda_execute`执行的同一份函数代码，不注册新 MCP 或新增服务。已有 AI 执行步骤可调用这一入口；源码已接到原生 API 调用链，当前宿主没有新增 typed action 或自然语言自动编译能力。

## 固定执行链与冲突策略

1. 核对当前 PCB 的工程 UUID、文档 UUID、documentType；读取配置、NetRules、NetClass及线/过孔/焊盘状态。
2. 校验正有限数、范围顺序、孔径小于外径；解析真实 Net。缺网或名字有歧义即停止。
3. NetClass 用于选择。若同名原生类存在，IR 成员必须与其实际成员一致；若不存在，显式 IR nets 作为逻辑分组。最终写入每个成员 Net，不修改类绑定或依赖继承。
4. 全批先编译：同网同类矛盾约束、已有不同显式规则、规则名碰撞时返回 fail，整批不写。等价显式规则复用；不覆盖用户已有不等价显式规则。默认模板不变。
5. 仅克隆回读到的原生默认模板，修改已知字段，生成确定规则名。缺少/不明确的 schema 返回 unknown。
6. 写前重新核对身份及规则是否变化；按需写 `overwriteCurrentRuleConfiguration`、`overwriteNetRules`，要求原生返回 true。
7. 回读整个配置及 `getNetRules()`核对实际成员绑定，再调用 `pcb_Drc.check(true,false,true)`；保存原始返回。
8. 按规则名与目标对象 ID关联违规，给每项 pass/fail/unknown，并单独保留 `boardStatus`。没有对应几何对象、未知 DRC 结构、无法定位对应对象、写后回读不同均不能 pass。已有其他网络的违规可能使整板 fail，不能因此宣称整板通过。

原生数值在既有实验中按 0.0001 mil 存储；2026-10-08实际写入发现二进制转换可能移动一个存储步长，已规范化发送值并在规则比较时允许一个步长；来源min/clearance下限和max上限另作严格方向检查，不能用容差放过不满足要求的值。配置名称是原生派生元数据，不作为规则内容比较。保留 requested，报告 effectiveValues，最小值/clearance 向上、最大值向下、preferred/hole取最近值。浮点回读比较容差1e-12，不扩大规则许可范围。Via 的单一 `hole`映射到孔径 min/preferred/max 同值；Track 修改模板已有各层宽度项；Safe Spacing 将已有数字矩阵统一为 clearance。Safe Spacing 的既有实机正反例只证明 Track-to-Track，其他矩阵对象组合没有逐项实测证明。

应用入口保留规则供后续画 PCB，不自动删除；回归测试创建临时规则与对象并清理。两次写入不是原生事务，部分失败会留真实 after/unknown，不自动回滚、保存或重试。取消只能阻止尚未开始的后续动作，已进入原生 API 的调用无法强制停止。

## 不支持的类型

Net-to-Net、Differential Pair、Equal Length Group、Region 及其他未声明类型统一返回：

```json
{
  "status": "unsupported_in_constraint_v0",
  "reason": "native mapping not yet verified"
}
```

混合批次出现这些类型时整批拒绝，避免偷偷执行支持的子集。这是 v0 范围结论，不表示官方 API 永久不支持。官方[规则格式](https://prodocs.easyeda.com/cn/format/pcb/rule/)存在 selector 与 ruleOrder 机制，但不能用文件格式推定 BETA 写接口 schema 或实机行为。

## 回归

```powershell
node --test tools/test_pcb_constraint_compiler.mjs
node --test tools/test_pcb_rule_case.cjs
python tools/pcb_rule_verify.py --compiler-v0 --case all --window <当前窗口ID>
```

第三条复用既有工程身份、before/after、正反例、原生 DRC、清理及恢复验证，只选择这三类。清理失败或传输不确定时停止后续 case。本次29项Compiler离线检查覆盖原生映射、显式成员绑定、冲突拒绝、幂等、错误回读/DRC、取消、对象范围及保存的真实 before schema；离线 mock/快照编译与实机 Compiler 闭环分别记录。最终三项实机独立JSON分别为 `pcb-rule-track-width-20261008-172741-838509.json`、`pcb-rule-via-size-20261008-172755-906350.json`、`pcb-rule-safe-spacing-20261008-172810-994878.json`，存于evidence目录；正例pass、反例fail与现场恢复均通过。没有扩大另外四类规则测试面。
