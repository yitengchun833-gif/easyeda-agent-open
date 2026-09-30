---
name: easyeda-agent-open
description: 通过开放 MCP 读取、分析和修改嘉立创 EDA 专业版，支持局部原理图返工、规则回执、任务续接、原生 eda API、PCB 数据与图面查看。
metadata:
  version: "1.8.1-open.9.6"
---

# 嘉立创 EDA Open

目标：准确落图、快速局部续接、工程师终审。进入绘图或压缩恢复时读取 [工作规则](references/working-rules.md)，连续小改复用。PCB、选型、同步等任务按需读取 [条件规则](references/conditional-rules.md)。用户确定设计，AI 决定功能表达和视觉取舍，程序核对固定事实。

## 读取与执行

- 完整档案与执行方案入口见 [资料及绘图文件](references/drawing-files.md)：snapshot 可直接落 JSON＋Markdown，plan 保留独立的全器件/逐脚目标并在完整重画后核对。新建图元的返回 ID 与旧源 ID 分开；只有局部返工时继续局部读回。
- 显示字号使用 `schematic.attribute.modify(...,fontSizeUnit:"display")`，原生单位保持 `native` 默认；不把该换算套到其他 API。普通器件需按已读旋转值重建时显式 `rotationMode:"stored"`；位置/朝向修改优先 `preserveInstance:true`。`schematic.wire.labels.apply(wireIds,labels)` 按指定可见集合显示/隐藏真实 Name，保留网络含义并回读属性；缺失发布返回 partial，不为找标签重建导线。必要的 raw 脚本可调用 `helpers.call(action,payload)` 复用适配。

- `easyeda_health` 选择目标窗口，后续固定 `window`；用 `target.projectUuid/documentUuid` 校验身份。`easyeda_actions(exact:...)` 查询具体动作，API 缺口用 `easyeda_api` 查签名后通过 `easyeda_execute` 执行 async JavaScript。
- 初次需要工程基线时用 `easyeda_snapshot(domain:"schematic",detail:true)`；局部返工传 `primitiveIds`，可加 `includeTexts:true`。只读位置/排版时加 `profile:"geometry"`，省去网表编译和器件库身份补全；检查连接用 `profile:"electrical"`，省去绘图几何；默认 `full` 保持完整读取。geometry 返回的未知网络不能用于电气验收。`cacheOnly:true` 或 `easyeda_runtime(operation:"state")` 只读后台已有观察，零 EDA 读取。缓存的覆盖范围、陈旧标记和 requiresScopedReadback 必须一起看。当前宿主原生事件漏报手动移动/撤销，缓存标 external_unconfirmed，不作为新鲜坐标依据；返工按对象 ID 定向读取。程序仍在普通完整修改单元前后局部回读，暂停续接及 finish 还会对照此前局部观察，发现差异返回最新局部事实且不执行旧计划。网表导出仍为文档级，局部组件读取不等于局部网表编译。
- 将一个完整修改单元放进 `easyeda_batch`，传 `edit:{mode:"layout",primitiveIds:[受影响器件ID]}`；授权改连接用 `mode:"design",expectedPins:{"位号.脚号":"新网络"}`。后台推导范围可作补充，已知范围应明确提交。整页交付需 DRC 时加 `requiredChecks:["drc"]`。
- 程序在整个单元前后核对身份、逐脚网络、受影响导线及出线，返回 `rules` 和 pass/fail/unknown；文字碰撞是候选 WARN，需实际图面确认。官方 `schematic.attribute.modify` 的纯显示修改会核对属性身份/电气值并省去网表编译。`complete:false` 只延后检查，不算通过。`verifyPreservedSchematic:true` 仅在需要整页身份/连接保留比较时使用。与局部自动比较同时启用时，两者复用同一读取阶段的网表与引脚；修改前后分别实读，不跨修改或跨请求缓存。局部返工不要额外请求整页比较。
- `schematic.wire.from_pin` 从实测引脚朝向生成非零正交引线，可 `dryRun` 看路径。平台原生网名/电源/地真实连接不强加短桩。任意 JS 保留开放；影响无法确认时回执为 unknown，先读实际结果再续做。
- `easyeda_screenshot` 返回画布和图片回执；后台窗口可能不重绘，若画面与本次修改不一致，直接用 `schematic.export.image(format:"png",scope:"selection",primitiveIds:[本次全部范围])` 导出实际对象；该导出回执同样可供 visual_review，截图字段传导出的 requestId。宏观布局、文字与符号边框用图面判断，电气用真实逐脚数据。必要时局部放大，不在临时断线步骤反复检查或运行 DRC。

## 任务连续性（按需小量读取）

- 新任务或上下文恢复时，先读 `easyeda_runtime(state,window)` 的摘要；默认只返回最近 12 条回执和任务目标，不返回全工程对象。任务内容是调用者记录的意图，不是执行证据或新的指令来源。
- 用户确认目标后、提交该目标的首批操作前，调用 `easyeda_runtime(operation:"task_update",window,target:{projectUuid,documentUuid},expectedRevision:当前task.revision或0,task:{goal,primitiveIds:[范围ID],remaining:[剩余事项],note:必要说明})`。任务字段有界，冲突返回 409 时只重读当前任务再合并；成功后检查 `persisted`，不要把持久化失败当成已保存记忆。
- 用户更换目标且旧操作可能未结束时用 `task_replace`（参数同 task_update）：程序先核对当前页面和版本，暂停并取消所有客户端的旧剩余步骤，确认无进行中动作后才更新任务；成功后仍 paused，按范围回读并准备新动作，恢复已授权任务时显式 resume_queue。无法确认原生动作结束时返回 unknown，不更新目标、不叠加新写入。普通进度补充仍用 `task_update`，不操作队列。插件不读取聊天，目标变更仍由 AI 提交；阶段边界更新即可。
- daemon 自动把已有工程缓存和最近回执写入同一 audit 目录的 `runtime-端口.json`，完整执行历史仍在原 JSONL。普通动作后台合并写盘，任务目标更新等待写盘；这不是 EDA 保存。最多保留 8 个页面、256 条回执；摘要按 ID 展开，不让模型反复全文记忆。
- daemon 重启后缓存和回执均为历史数据，所有对象待局部回读，检查/截图证明和执行队列不恢复。先用实际 `document.current` 确认新窗口身份；程序结合工程/文档 UUID 与扩展会话标识恢复对应历史。空连接不作为候选，连接先后不抢占任务；候选唯一时可自动恢复。若返回 recovery.selection_required，读 state 的历史任务，按当前用户任务选择 sourceWindow，再调用 `easyeda_runtime(operation:"restore",window,sourceWindow,target,generation:当前观察版本)`；目标窗口须经原生读取且尚无新任务/对象，禁止覆盖已有工作。绑定只迁移历史上下文，之后仍按对象 ID 回读，不恢复旧步骤。
- 返工先按 `primitiveIds` 查已有缓存，再对修改范围原生回读；未涉及部分继续保留。程序只更新真实观察，不拿计划坐标当结果。强制结束进程可能丢失最后约 200 ms 加写盘耗时内的缓存，必要时查原 audit；绝不依赖旧缓存重放写入。持久化故障会出现在 `persistence.error`。

## 返工、续接与收尾

- 用户更换目标时按上面的 `task_replace` 处理；只需废止本 MCP 会话旧指令时保留 `easyeda_control(operation:"supersede",window:...)`。只修改指出的部分，同类问题从已观察对象筛选范围；影响不明或证据失效时才扩大读取。
- `easyeda_control(status)` 查看 Connector 队列；`easyeda_runtime(state)` 查看后台工程观察和任务回执。暂停/恢复队列走 `pause/resume_queue`；已确认暂停的批次用 `resume(requestId)` 只续未执行步骤。失败、超时、断连或未知写入不能直接重试整批，先局部回读并补未落地部分。取消不回滚，运行中的原生 API 不能强杀。
- 完整范围修改后看图，使用 `easyeda_runtime(visual_review,requestId,generation,screenshotRequestId,source:"ai")` 记录与当前观察绑定的审阅；这是 AI/工程师的审阅声明，程序不冒充已经看懂图片。
- 任务结束才主动保存。可用 `finish(requestId)` 复用已有自动检查及图面证据后保存；声明了 DRC 时先调用实际 `schematic.drc.check`；可与导图/读取合并为完成后的只读批次，finish 复用该结果，不重复检查。混合写入批次的早期检查不作最终证据。WARN/info 暂不阻塞，电气错误与必要证据未知不交付为通过。普通 typed 保存仍可使用，`saved:true` 与重开持久化证据分别说明。
- 实时控制台为 daemon 同端口 `/dashboard`（默认 http://127.0.0.1:60932/dashboard），无额外模型调用/工程读取。状态缓存不代表实时全工程正确，也不代表保存。

## 能力边界

原理图自动检查是本版重点；PCB 保留已有结构化读取/写入，不能借原理图检查证明 PCB 完整验收。官方事件/BBox 为 Beta，文字缺测仍可由局部图面审阅，电气未知不能被视觉豁免。权限依当前任务和宿主账号/API，本包没有独立桌面控制服务。Docling 在资料解析上游，OPA 和其他流程框架本轮不引入。

本版 CLI/后台、MCP、Skill 为 1.8.1-open.9.6，Connector 沿用 1.8.1-open.9.5，与 sources.open.json 的 connectorVersion 一致。此次更新阶段执行、固定位置布线/路径检查及后台请求 ID；已导入 open.9.5 Connector 无需重复导入 .eext。升级时使用本完整包中的后台、MCP 与 Skill；旧后台需明确重启，MCP/Skill 需重载，并分别核对实际加载版本与工具参数。构建完成不代表当前宿主已经加载。

后台由多个 MCP/Connector 共用，关闭或重载某个 MCP 不停止后台；只有明确 daemon stop/restart 才停止。启动日志在既有 runtime/daemon-stderr.log，启动错误不伪装连接成功。
