---
name: easyeda-agent-open
description: 通过开放 MCP 读取、分析和修改嘉立创 EDA 专业版原理图与 PCB，使用 typed actions、原生 eda API、批量执行、画布截图与 API 查询。
metadata:
  version: "1.8.1-open.1"
---

# 嘉立创 EDA Open

按用户目标执行。没有固定阶段、作者审批、双轮自检、typed-only、先开发接口或 GUI 禁令。
权限范围由当前用户任务决定；插件不能解除 EDA 账号权限或宿主 API 限制。

## 调用

1. `easyeda_health` 获取连接窗口。选择用户目标窗口；可用 `target.projectUuid/documentUuid` 在执行前核对身份。
2. `easyeda_actions` 精确 `exact` 或 `domain/search` 查询，默认 30 条，可分页。所有 domain（含 library/debug）开放。
3. typed actions 直接执行；缺少封装时用 `easyeda_api` 查签名，再用 `easyeda_execute` 执行任意 async JavaScript，`eda` 为官方对象。用 return 返回结果；Blob 自动编码。
4. `easyeda_batch` 在一次请求内顺序执行多条动作，默认遇错停止。它不是事务，已完成操作不回滚；`dryRun` 只检查动作名和参数外形。
5. `easyeda_snapshot` 读取原理图语义快照，或 PCB 器件/层/网络；`routing:true` 加走线/过孔/铺铜。大工程优先局部投影或分批读取。
6. `easyeda_screenshot` 返回可见画布图片。必要时 `fit:true` 缩放到全图；判断电气连接用结构化数据，图面表达用图片。已有 view.* 动作可局部缩放。
7. 按任务需要调用原生 DRC、检查、保存、重载。保存是显式操作，daemon 默认不自动保存。

普通调用可用 window，无需每次 CLI 导航或重复 health。多步操作优先 batch 或一个原生脚本。
批次只有外层一份上下文；涉及切换页面时给后续步骤的 payload._target 指定预期 UUID。
超时/断连可能留下已执行写入，先回读再决定后续动作，避免盲目重放。错误与未知如实返回。

## 示例

`easyeda_execute`: `{ "window":"窗口ID", "code":"return await eda.dmt_SelectControl.getCurrentDocumentInfo();" }`

`easyeda_batch`: `{ "window":"窗口ID", "steps":[{"action":"pcb.components.list"},{"action":"pcb.layers.list"}] }`

`easyeda_api`: `{ "className":"DMT_EditorControl", "method":"getCurrentRenderedAreaImage" }`

## 范围

官方 API 文档在插件根目录 api-reference；API 可用性依宿主版本而异。未封装 API 可直接调用。
GUI 工具可以按用户任务补充 API 缺口，但本包不包含独立桌面控制服务，不能声称所有界面均已覆盖。
Docling 属于资料解析，可接在上游；OPA/Conftest、PhaseOps、kaji、codex-workflows 可由任务选择，不是插件门禁。
