# PCB 整板图片能力边界

官方 API 请求：[easyeda/pro-api-sdk#41](https://github.com/easyeda/pro-api-sdk/issues/41)。

EasyEDA Pro 4.1.60 的 PCB 右键菜单提供“复制为 SVG / 复制为 PNG”。对官方类型包
`@jlceda/pro-api-types@0.4.25`、`pro-api` 包装层和 Web PCB bundle 交叉核对后，结论如下。

- 公开 `eda.*` 只有 `dmt_EditorControl.getCurrentRenderedAreaImage(tabId?)`，它返回当前渲染
  视口的 Blob；没有格式、选中图元、板框范围或对象级导出参数。
- `pcb_Document.zoomToBoardOutline()` 可以先把视口适配到板框。本项目据此提供
  `pcb snapshot --fit-mode board`，并明确回传 `board-fitted-viewport-png` 与
  `objectLevelExport:false`。
- PCB 制造输出没有 PNG/SVG。`sch_ManufactureData.getPngFile/getSvgFile` 只适用于原理图；
  `lib_Footprint.getRenderImage` 只适用于库封装预览。
- Web PCB bundle 内部确实存在 `exportPCBPng` / `exportPCBPngForArea`，会按板框计算 bbox、暂时
  移除选中高亮并由 PCB 场景生成图片；这与菜单中干净整板 PNG 的表现一致。但官方 `pro-api`
  和 `eda.*` 没有包装它们。直接调用内部 message bus 会绕过 typed 公开接口契约，因此不能接入
  connector，也不能称为公共 API 能力。

当前降级路线是“公开 board-fit + viewport capture + 数据包”，用于 Layout 的两轮视觉证据；
连接、几何与 DRC 仍由 typed 数据回读判定。将来官方若为对象级 PCB PNG/SVG 增加公开
`eda.*` 包装，再替换 capture 后端，保持现有 `captureKind/objectLevelExport` 字段用于能力区分。
