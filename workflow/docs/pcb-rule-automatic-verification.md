# PCB Rule 自动验证：2026-10-08

已建立运行器 [pcb_rule_verify.py](../../tools/pcb_rule_verify.py) 和原生测试脚本 [pcb_rule_case.js](../../tools/pcb_rule_case.js)，使用现有 easyeda-agent CLI → daemon → Connector → eda API；没有安装依赖、切换 MCP、要求人工点击或操作生产工程。

目标固定为 P1 / PCB1：projectUuid `6bde69a919dc419f98f8b06ec7b318d9`，documentUuid `d260cc249c21359b`。每次原生操作核对活动文档 UUID、工程 UUID 和 PCB 类型；不通过项目名猜选目标，不自动切页。窗口 ID 取当前 health，旧 ID 失效时停止。

```powershell
python tools/pcb_rule_verify.py --window <当前测试PCB的windowId>
# 可单独运行：--case via-size / safe-spacing / net-to-net-safe-spacing /
# differential-pair / equal-length-group / region-rule
```

默认依次运行六项。每项写入前先把规则和对象基线保存至独立 JSON；请求失败/超时或清理回读不一致会停止后续项，不自动重放。临时对象有独立 ID，临时规则、类、组、网络都有独立名称；finally 清理后比较十一类状态。恢复配置前核对无其他配置变化，防止旧基线覆盖并发变更。状态比较仅允许 `1e-12` 数值浮点差，同时保留原始值与严格比较结果。不要在测试运行时编辑此测试板。

## 实测结果

`supported` 仅表示该最小正反例链路通过；`partial` 表示部分 API/步骤成功、完整链路未通过；`unknown` 表示缺少结构或证据；`unsupported` 需有明确 API 否定证据。本轮没有把未报错或文档不完整标为 unsupported。

| 规则 | 状态 | 实际依据 | 独立 evidence |
|---|---|---|---|
| Via Size | supported | 显式绑定新网络；孔径均 0.508 mm，合规外径 1.016 mm，违规外径 0.508 mm；要求 0.9144–1.1176 mm。DRC `Physical Error / Via Size / RadiusError` 命中违规 ID、临时规则名及实际外径，合规对象没有对应规则违规。 | JSON（本地历史资料未随公开插件分发） |
| Safe Spacing | supported | 四个网络逐个显式绑定；线宽 0.254 mm，合规边距 0.508 mm、违规边距 0.127 mm，要求 ≥0.254 mm。DRC `Clearance Error / Track to Track / ClearanceError` 命中两个违规 ID、规则名、实际间距及门限；合规组没有对应规则违规。 | JSON（本地历史资料未随公开插件分发） |
| Net-to-Net Safe Spacing | unknown | 原生 getter 返回 `Safe Spacing: []`；覆写接口只给 Record 结构，没有条目 schema。未猜写选择器，未创建伪装成网络间规则的普通网络规则正反例。执行了原生 DRC 并保存 before/after；该项创建、绑定、正反例步骤未完成。 | JSON（本地历史资料未随公开插件分发） |
| Differential Pair | partial | 两个差分对及四条正反例走线创建成功，临时差分规则创建回读成功；成员 `Differential Pair` 显式覆写返回 true，回读仍为 default。保留真实规则写入/回读及失败后 DRC，不认定绑定生效。 | JSON（本地历史资料未随公开插件分发） |
| Equal Length Group | partial | 两组网络创建成功，组及四成员显式绑定 0.254 mm 容差并回读。合规组长度均 2.54 mm；反例长度 2.54/5.08 mm，差 2.54 mm。原生 DRC 没有返回对应长度错误；不能称验证通过，也不能推出全部等长检查不受支持。 | JSON（本地历史资料未随公开插件分发） |
| Region Rule | partial | 原生创建带 `[5]` 禁布线类型的区域及内外两条同网正反例走线成功，`getRegionRules()` 仍为 []，区域规则条目/网络选择器结构没有得到确认。DRC 没有对应区域错误，仅有连接错误。禁布区域图元不等于已完成区域设计规则绑定。 | JSON（本地历史资料未随公开插件分发） |

每项 JSON 保留 before、原始操作返回、创建对象 ID、after、详细 DRC、绑定回读、清理返回与 restored；支持项还有独立实际值/对象几何检查。差分异常回读失败后的原始 DRC 在 `result.drc`；不能因外层 ok 或通用 rules unknown 替代内部 evidence 判定。所有未完成步骤在 `result.steps` 独立标明。

## 验证与限制

- 最终六项清理回读均恢复；旧 Track 违规线 `ec26e4c6297004a5` 保留。全部测试前至最后测试后的整体范围比较另见 汇总（本地历史资料未随公开插件分发）。本轮没有显式保存、关闭重开、导入网表或修复历史 DRC。
- [9 项离线检查](../../tools/test_pcb_rule_case.cjs)覆盖错误对象 ID、规则名、类型、数值、合规对象几何、绑定拒绝、清理失败及错误工程身份；仅验证脚本判定/停止逻辑，不代表新增实机规则支持。
- 临时图元位于 `(5000,5000)` mil 附近，避开现有布线；不是交互布线自动取规则、差分阻抗、完整区域规则或制造验收。等长/差分/区域的具体缺口仍需公开结构或进一步有依据的原生能力，不要求用户点击 UI 来补成成功。
- 官方相关接口为 BETA，原始结构和量化差异都保留；最早失败回执未覆盖或删除。规则坐标/尺寸 API 使用 mil，配置和 DRC 展示使用 mm；不推测 DRC `errData` 内部数值的单位。
- 来源：[官方规则选择器格式](https://prodocs.easyeda.com/cn/format/pcb/rule/)、本机 `easyeda_api` 的 PCB_Drc/PCB_PrimitiveVia/PCB_PrimitiveRegion/PCB_PrimitiveLine 参考，以及用户提供的 Track 交接（本地历史资料未随公开插件分发）。格式文档没有定义上述任意 Record 条目的运行时 schema，Track 结论未推广。

未完成：网络间规则完整绑定、差分绑定生效、等长 DRC 反例命中、区域规则完整绑定及 DRC 命中。脚本框架已建立并实跑，六类规则并未全部验证成功。
