# 原理图复刻：资料、执行和验收

本分支用于按图复刻与资料驱动绘图。复用现有 typed action / compose / 检查器，不建立新的控制层。
只做工作流维护时不触发阶段任务创建，也不自动恢复暂停的 EDA 写入任务。

## 先确定交付深度，再索取必要资料

| 交付 | 优先资料 | 缺失时如何处理 |
|---|---|---|
| 忠实复刻图面 | 矢量 PDF 或原生工程，用户指出的正确局部图 | 原页可读即继续；未知符号保留未知，不替换成外观近似端口 |
| 可编辑且联网正确的原理图 | 上述资料 + 位号/引脚/网名表，或原生工程/网表 | 源图与网表交叉核对；仅凭网名相同不证明真实线段及节点正确 |
| 可用于 PCB 的器件 | 位号对应完整 MPN/厂商/C号；封装图、焊盘映射和机械要求 | 缺封装信息允许完成图面实验，禁止称为 BOM/封装已核准 |
| 可制造板 | BOM、器件手册、板厂/叠层/电流/机械约束 | 进入既有 PCB 规则分支，不能从 PDF 符号尺寸猜实物封装 |

优先复用用户已有的 BOM/原生工程，不要求为了工作流重新填一套大表。需要追问时只列缺失字段与受影响位号。
来源事实与执行选择分开：原件可能没有封装；搜索到的 0603 是候选，不能倒写成源图事实。

## 最少步骤，先把目标写完整

### 执行效率与保存前提

- 复用已确认资料、规则和候选；未变内容不重新检索/确认。不检查无关工程或 PCB，仅内部绑定当前目标页面。
- 首次写入前核实 daemon 自动保存已关闭。使用 scripts/start-schematic-session.ps1 指定实际 CLI，以 --autosave-debounce 0 --auto-update-skill=false 启动；核对新进程参数、健康状态及目标页面。未证明关闭不得声称符合 SCH-G06；不要修改编辑器自身自动保存设置。
- 正在绘制时需重启 daemon，先保留只读对象/导线快照，停止发新动作，重启后核对快照；不刷新或重载编辑器，不为重启提前保存。
- 纯放置批次使用 schematic_preflight.py --phase placement，只检来源、目标、选型与坐标，报告 deferred_assertions；不以尚未画出的网络/标签阻塞放置。完整计划仍用默认 --phase complete，完整性检查不得省略。
- 相同器件/接口只探测一次；后续从参数化数据生成有界顺序批次，批末集中回读。失败先查实际落地，禁止全批盲重放。图面/ERC 完整验收在全图完成后进行，局部只查本次新增风险。
- 图片/报告文件落盘应称“导出图片/记录”，不得称“原理图已保存”。保存统一遵循[通用 SCH-G06 与 local/page 验收边界](schematic-layout-conventions.md)：整页复刻默认 page；明确授权的局部修改采用 local，不被无关且未变的既有排版问题牵制。

1. 从原页独立记录器件/引脚/网名，以及每个功能块的分支、连接点、无节点交叉、特殊符号和文字。每项附来源页/区域。
2. 按唯一 C号/完整 MPN/明确参数先去重，复用精确库身份。同一规格只解析一次，多个实例共享身份，库缓存按站点与版本失效。不以搜索首项作为正确性证据。
3. 读真实目标文档、纸张 bbox、坐标约定和引脚几何。原理图 y 向上；先做局部布局预览再生成整页，避免负 y 出图框。
4. 用既有 typed actions 的 JSON 计划做预检。几何保真约束独立于执行器生成；不能从错误执行计划反推出一份同样错误的“期望”。
5. 对新画法只验证一个代表块：三通支路、同网多针主干、特殊符号与标注。实际读回和导出局部图符合源页后，再复用到相同模式。
6. 已确定动作放进同一有界批次顺序调用 easyeda-agent MCP；同窗口禁止并发。对独立文件解析、公开资料检索才并行。批末记录实际完成对象 ID、计划哈希、读回和批次时长；批末回读不自动触发保存，保存按已确定的验收范围及 SCH-G06。
7. 中断后读回现场，与 journal 核对，只执行确实未完成部分。超时不代表未落地，不自动重放整个批次；出错后先检查状态。
8. 全页核对器件身份、逐脚网络和范围外保护；图面检查按 local/page 范围执行，不以局部视觉复核代替整页复刻验收。对应范围检查通过后保存一次，再重开只读核对持久化；失败、未知及既有问题的处理均按通用 SCH-G06，不在本分支另设保存条件。

既有 bulk-place.py / bulk-connect.py 属于旧示例：包含泛化重试、坐标复用或全网口连接策略，不能直接用于忠实复刻。
准确复刻优先使用现有 compose 的完整几何与实例保留流程；若当前 MCP 没有对应入口，使用明确的 typed action 列表分批执行，不用 GUI 或 raw JS 代替。
使用 CLI 的批处理入口须符合当前会话工具许可；本分支不会自动扩大许可。

## 执行前的可运行检查

所有原理图写入批次按[规则管理](../../evidence-to-delivery/references/rule-management.md)登记 execution_rules 与 rule_binding，再运行下述既有预检；没有当前版本确认、漏内部规范、来源变更或硬冲突时不得执行。旧侧表无规则字段将返回失败，须按已有授权补齐真实记录，不伪造用户确认。预检报告不替代逐条图面/电气/保存验收。

脚本 [schematic_preflight.py](../scripts/schematic_preflight.py) 只读文件，不调用 CLI/MCP、不选择器件、不写 EDA。
输入是已有 `{route, actions}` typed 计划，以及带原件哈希的审查侧表。它只补现有检查缺少的来源图形断言：

- `junction`：源图中指定支路必须在目标坐标上几何相接；同名但有间隙不算接上。
- `text`：源图指定文字必须在计划声明中出现；不证明渲染可见或未重叠。
- `named_wire_budget`：限定区域显式命名导线的次数，发现逐引脚重复赋名风险；不冒充实际显示标签计数。
- 实测纸张范围、y-up/raw、目标项目/文档、正交非零线、选型约束与来源存在性。
- PCB 阶段要求厂商、完整 MPN、封装、引脚到焊盘映射与尺寸有来源；缺项不能自动用默认值补齐。尺寸和映射仍需专业核验。

```powershell
python skills/easyeda-agent/scripts/schematic_preflight.py --requirements PROJECT/source-review.json --plan PROJECT/actions.json --stage schematic --out PROJECT/preflight.json
# 准备 PCB 时再检查同一份资料的物理证据是否齐全：
python skills/easyeda-agent/scripts/schematic_preflight.py --requirements PROJECT/source-review.json --plan PROJECT/actions.json --stage pcb --out PROJECT/pcb-readiness.json
```

侧表的最小示例（占位哈希须由真实文件计算；坐标来自已确认的源图到目标映射，不是从原 PDF 直接读取的 raw 坐标）：

```json
{
  "sources": [{"id":"PDF","path":"source.pdf","sha256":"REPLACE_WITH_ACTUAL_SHA256"}],
  "target": {"project":"ACTUAL_UUID","doc":"ACTUAL_UUID","axis":"y-up","units":"raw","sheet":[0,0,1170,825]},
  "parts": [{
    "ref":"J1",
    "required":{"pin_count":3},
    "selected":{"pin_count":3},
    "evidence":{"pin_count":{"source":"PDF","locator":"page 1, connector detail"}}
  }],
  "depiction": [{
    "id":"INPUT_BRANCH","kind":"junction","at":[200,300],"net":"SIGNAL",
    "directions":["left","right","down"],
    "evidence":{"source":"PDF","locator":"page 1, input branch"}
  }]
}
```

`required` 仅写源材料确实证明的值，`selected` 写选中库器件实际读回属性。需保留独立选型记录/手册页；通过脚本不能证明这两组数据的人工抄录正确。
预检通过仅允许进入该范围的执行准备，**不能直接用于“图面已通过”“封装已通过”或“交付完成”**。

## 分项验收接入唯一状态

原理图交付计划使用现有 workflow.py 的 `required_checks`：
`["source_preflight","part_identity","connectivity","appearance","native_drc","persistence"]`。
PCB 交付另加 `footprint_review` 与适用板级约束检查。源图复刻范围内的 part_identity 可以明确记录“按源图识别，部分物料暂选”，但这不能充当 footprint_review。

`result.checks` 为每项提供 `id/status/note/evidence`；status 仅为 pass/fail/unknown。自动检查可附 `report`：
报告顶层 `passed` 必须为布尔值，标为 pass 的失败报告被拒绝；有 `inputs/evidence_files` 哈希映射时必须仍匹配。
报告和依赖文件纳入已有证据指纹；证据变化使既有验收失效。脚本不能证明人工评价诚实，最终仍需读原文与真实输出。

appearance 的证据至少包含同版本源页/局部图、当前导出图以及逐块比较记录；不得用网络表或预检报告代替。
检查分支进线、节点/交叉、符号类型、全部可见文字、重复标签与遮挡。点的图形表现必须看导出图，几何方向正确不自动等于红点已显示。
未解释的严格 DRC 警告不抹零；未知物料不改成已核准。旧项目加入 required_checks 走既有方案修订，不直接编辑 .workflow/state.json。

## Jev 与计时

唯一料号、唯一合格候选、坐标计算、去重、模板应用、执行和检查都不调用 Jev。
存在两个以上已通过硬性参数的候选且偏好确有歧义，才允许窄范围候选选择。资料缺失属于缺证据，不能当成让 Jev 猜答案的选择题。
当前 resolve 能力不等于封装选型已接通；confidence 不证明尺寸、引脚映射或物料等价。

按解析/选型/规划/工具执行/验证/返工记录 wall-clock 与次数。批量执行时间不代表端到端时间；预检速度也不代表 EDA 速度。
提速优先消除整页返工、重复解析与重复查库，其次减少 Codex 的逐动作决策。不给未经对照实测的倍数承诺。

## 核查依据与可复用能力

2026-09-24 核对以下官方资料；实际接口能力仍以当前宿主和 typed action 读回为准：

- [导线 create](https://prodocs.lceda.cn/cn/api/reference/pro-api.sch_primitivewire.create.html)：网名可省略并依连接继承；显式赋名的行为不等于只画一个可见标签。
- [网络标签](https://prodocs.lceda.cn/cn/schematic/place-net-label/)：标签与导线名称属性关联，逐段命名会影响图面表达。
- [器件](https://prodocs.lceda.cn/cn/schematic/place-device/)：实例关联符号、封装和工程库模板；库绑定不能证明符合当前设计要求。
- [原理图常见问题](https://prodocs.lceda.cn/cn/faq/schematic/)：符号引脚号和封装焊盘号需要匹配；可导出网表做差异核对。

复用本包的 parts-select / canonical 连接与 design-diff / compose / schematic.check / bridgeCheck / 原生 DRC；
其中现有比较选型器仍需手册核验，连通/几何检查仍需图面核验。本迭代不新增通用执行引擎。
