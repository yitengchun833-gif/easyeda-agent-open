---
name: easyeda-agent-open
description: 通过开放 MCP 读取、分析和修改嘉立创 EDA 专业版，支持局部原理图返工、规则回执、任务续接、原生 eda API、PCB 数据与图面查看。
metadata:
  version: "1.8.1-open.9.6"
---

# 嘉立创 EDA Open

## 强制原生接口路由

EDA 操作固定走 `easyeda-open` MCP 或同包 CLI，经现有 Go daemon、Connector 调用官方 EDA API。禁止为 EDA 截图、切页、缩放、选中、菜单导航或编辑使用 Computer Use、UIA、`sky`、Windows MCP、外部浏览器控制、脚本注入鼠标键盘或窗口激活；原生接口失败也不得转桌面操作。本约束仅针对嘉立创控制，不禁用其他任务的工具，也不限制正常查阅网页文档。

- 已有对应入口时直接使用，不先截图找按钮，也不每次重新遍历全部 API；参数不明才用 `easyeda_actions(exact:...)` 定向查询。工程切换复用 `easyeda_project_transfer(operation:"open",...)`，先核对窗口与工程 UUID、确认已保存并获准切换，再按接口要求明确确认 `allowDiscardUnsaved:true`。
- 新明确授权的操作默认直接调用对应原生动作，不以旧task、remaining、历史unknown或旧检查失败作为启动前置。已采用本次后台后，新请求不继承这些历史锁；窗口/工程身份、在途互斥、请求去重、持久化和真实错误仍保留。旧unknown不改成功、不重放；保存结果未知不等于已保存。`plan`、检查及`runtime finish/resume`是主动选择的验收/续接能力，不是所有日常操作的必经步骤；声称已验证或续接旧请求时仍需相应证据。源码候选未采用前不得假称正式后台已解除阻断。
- 连接：用户要求“连接/重新连接嘉立创”时直接调用 `easyeda_connect()`；它复用健康后台，仅在后台缺失时启动，不强停存活后台、不重放旧请求。`waiting_for_connector` 仅表示后台就绪、尚无EDA窗口连接，不能称已连接工程。只查状态或明确禁止启停服务时仅用 `easyeda_health()`，不可自动转connect；当前工具目录没有connect时准确报告尚未加载，不用桌面操作替代。
- 工程清单：采用支持此参数的Connector后，复用 `easyeda_project(action:"project.find",window,payload:{friendlyName:精确查询名,teamUuid:本次授权团队,includeProjects:true})`；`projects` 返回同次枚举的名称/UUID，`matches`仍为精确匹配，不重复枚举或逐项再查。仅团队根目录，不跨团队或递归文件夹；`enumeration.complete:false` 时清单不完整，不能据缺项认定工程不存在。旧Connector若没有返回 `projects`，报告能力未加载，不转raw脚本或绕过unknown门禁；本条源码更新不等于已安装。
- 看图：使用 `easyeda_screenshot`（原生 `view.capture`）。原理图画面陈旧或捕获失败时，用 `easyeda_artifact(action:"schematic.export.image",payload:{format:"png",scope:"page"})`；局部用 `scope:"selection"` 并传本次完整 `primitiveIds`。导出图与其原生回执按现有视觉验收使用，画布图不能代替逐脚数据。
- 切页：按已核工程身份调用 `document.open({uuid})`，再用 `document.current` 确认目标 UUID；`ready:false` 或目标不符不能视为完成，不重复点击或重放。
- 工程已打开但没有活动图页时，`document.current` 正常返回 `projectOpened:true,documentActive:false,state:"project_open_no_active_document"`，文档UUID为null；这不是图页已就绪。只读 `easyeda_control(operation:"status",window,target:{projectUuid})` 可核对工程级队列并返回同一状态；不要在此状态强填图页UUID。显式图页目标不符和真实API异常仍报错，队列变更仍须完整身份。本说明须对应Connector修复已加载，源码存在不等于在用版本已更新。
- 缩放与选中：复用 `view.fit`、`view.fit_selection`、`schematic.select`，不按固定页面坐标猜操作。
- 失败先区分分发状态：回执明确 `no action dispatched` / `not_executed` 时，仅能认定本次请求未执行，不能据此清除旧 unknown；已分发但结果未知，或无法确认是否分发时，保留 unknown，不因超时、断连或请求取消认定未执行，不重放未知写入、不修改历史证据。
- 身份不符、门禁拒绝、未知结果或持久化失败时，复用 `easyeda_control(operation:"status")`、`easyeda_runtime(operation:"state")` 及必要的原生只读身份/范围回读；只在证据和授权满足时使用下文已有恢复入口，否则保留证据、报告阻塞。原生能力确实缺失（包括安装安全提示）时准确说明缺口，不假装成功、不代为点击，也不把外部点击包装进 MCP。

目标：准确落图、快速局部续接、工程师终审。稳定执行方法随插件源码和本Skill分发；确定性要求由共享检查执行，项目计划仅记录进度，不能用个人记忆或临时脚本代替产品能力。进入绘图或压缩恢复时读取 [工作规则](references/working-rules.md)，连续小改复用。PCB、选型、同步等任务按需读取 [条件规则](references/conditional-rules.md)。用户确定设计，AI 决定功能表达和视觉取舍，程序核对固定事实。

输入准备/交付摘要更新（2026-10-15，已限定实机，分发待采用）：同一 easyeda_plan prepare 从已有 easyeda.plan/1 设计声明和真实空白页快照补齐五阶段、scope/hash/job；设计/布局/确认由调用者提供。finish 保存原始 delivery.json 后自动派生摘要，历史回执可用 summary 重建，原失败与未测量时段保留。原 snapshot-only prepare 兼容。新功能已用独立CLI实机完成三器件模块，当前常驻MCP和全局Skill尚未更新；需采用更新并新建MCP进程、重读本Skill，不能向旧副本发送新参数。详见 drawing-files“输入准备与交付摘要”。

## 正式交付与保存持久性（源码候选，未部署）

final将独立目标/真实引脚网表、原生DRC、实际布局和完整原生快照绑定同一回执，调用同包Go设计差异检查；局部返工额外核范围外对象/网表，未在实际执行回执中的新增独立图元不能混入交付。finish在保存前再读并比对final快照，不一致则不发送保存控制；实际图面必须已审阅。原delivery.json只记录原保存事实，persistence默认unverified，saved:true不等于持久性。

正式验收在已有明确授权的窗口/文档完成保存后，使用原document.close（新鲜uuid+tabId）和document.open按实际保存目标关闭重开，再调用`easyeda_plan(operation:"verify_delivery",deliveryFile,window,closeRequestId,openRequestId,outputFile?)`。此操作本身只读，不自动关闭、重开、保存、重试或清任务；只消费当前daemon原生成功顺序（含关闭前身份/真实closed UUID）、新鲜回读、原生DRC和保存前后完整设计diff。旧daemon缺这些证据、unknown保存、历史回执、重开后又写入或diff不一致均不算持久性通过。可输出独立派生JSON/Markdown，原阶段/交付回执保持不变。

验收结果分别声明page/module/local；模块或局部完成永远不冒充wholePageComplete。engineer acceptance始终交工程师。local_display无final的兼容路径仅交显示局部完成，当前持久性验证保持unknown，不能宣称正式整页签收。多单元/NC/跨页或缺完整测量仍按既有明确能力范围阻断，不猜事实。实际关闭重开未在授权内时报告待验，不自行操作在用窗口。

## 任务必需检查（源码候选，未部署）

新模块prepare自动标记new_module/incremental_module；现有工程局部返工用snapshot-only prepare的taskKind:"local_edit"，自动映射源对象ID，再准备已授权步骤。不要为新任务继续写无taskKind的旧手工plan；该旧格式仅保留兼容，不宣称自动检查覆盖。任务类型驱动同一requirements协议：来源绑定、适用的局部身份/真实引脚/导线/出脚检查、final独立全页目标和原生DRC；纯显示阶段只要求原生适用的显示身份，电气状态在design/final核对。删除自动项会在loadPlan补回，改成warn或替换谓词会拒绝。

有几何变化时和final自动读真实几何，调用同包Go `sch layout-lint --from ... --strict --json`。local_edit比较全页部件，含未修改器件碰撞；新模块在已证空区内检查实际功能组bbox/组间穿插、部件重叠、引脚重合、间距、网格和页界。证据缺失不通过；信号流和文字阅读效果仍由实际图面审阅决定。autolayout默认不画分组框、标题或分隔线；保留显式旧参数仅兼容，不在本工作流启用。

## 中断后的同链恢复（源码候选，未部署）

`easyeda_plan(operation:"reconcile",planFile,expectedHash,window,continuous:true)` 只读现有阶段/交付回执并检查当前目标，保留原字节，分别返回已证完成前缀、未启动阶段和阻塞项。目标当前满足不等于未知动作已完成。`resume` 重新核对同一来源/哈希/前缀，仅执行未启动后缀；连续模块可从已落盘的place回执补既有ID绑定。pending、unknown、损坏或乱序回执、任何既有delivery均阻断；保存应答丢失不得重复finish。无有效原生回执的写入仍需人工修复方案，不能凭当前图面清除原锁。daemon原有paused批次resume继续要求完整成功前缀、当前baseline/observed、同generation和task revision；重启后的陈旧证据不自动变新鲜。

## 日常最短执行

1. 恢复本任务当前状态与必要专业规则，按需读目标资料；普通绘图不复盘研发历史、全量源码或已有效的旧验收。目标变化/证据失效才补读，当前window/target与必要原生核对仍保留。
2. 对空白页或非空页新增模块，完整设计、逐脚网络与布局已确认后，把同一份声明及当前必要快照交给 `prepare`，审阅生成plan；直接消费返回的jobFile，不重拼五阶段、scope、哈希或另做重复inspect/render。非空页用full快照（wiresAvailable及完整pagePrimitives/pagePrimitiveBounds）声明layoutHints.region为空闲区域（仅数据约束，不画框）；prepare冻结原对象/网络并合并全页目标，写前后独立回读，新增线必须留在区域内。增量放置限stored旋转，按同device/subPartName/朝向的已有实际测量确认bbox在区内；必要时每个layout条目指定geometrySourceId引用声明的测量快照，缺源不猜尺寸。完整原几何/网络未知、区域占用或位号重复先阻断。已有页局部返工继续原scoped入口，不清空工程。
3. 一次 `run(continuous:true)` 完成已准备阶段。实际看 `review.imagePath`；可接受才给现有finishInput添加 `visualAccepted:true`，交同一入口finish。
4. 引用自动 `delivery.json` 和 `delivery.summary.json/.md` 直接交付；PWF只补当前结果、边界和下一步。普通任务不再手拼第二份acceptance、重复抄回执或为总结重新检查工程。缺失计时写未测量，不用程序秒级代替整轮等待。

离线布局候选用同包Go `sch autolayout --from measured.json --spec modules.json --json`，复用原确定性求解器，未选器件为固定障碍；结果仅候选，按功能组审阅后作为声明的layoutHints.parts，不执行旧图整页重排。布线仍复用原route-only求解与验证。

失败先核实际范围和已有回执，不重放完成动作、不边画边修改工具。上述是已有接口的用法，没有新增runner、参数或两次调用承诺。

## 读取与执行

symbol实例/库来源未知不得略过：共享plan复用官方项目归档及原解析器，只把有META.source精确证据的符号身份转换到原生检查域，原目标及电气谓词不变。原失败final需显式recheckFinal，仅最后空只读阶段可续，原文件保留且新回执绑定原SHA；无成功写入重放。解析器同包native-identity.cjs无需Connector/共享服务更新。当前ON三器件模块已实际通过来源检查、final续接及保存，限定结论不扩大为任意失败恢复。细节见drawing-files“符号身份域续接”。

连续绘图v0：设计/属性/逐脚网络/功能分组和布局确认后，同一`easyeda_plan(operation:"run",continuous:true,planFile,expectedHash,window,target)`或同包plan-cli消费固定job，不另发runStages/bind/compile_labels。逻辑范围衔接真实ID、新revision/hash、测量、direct落线、真实Name和final。2026-10-15已限定验证新prepare→完整连续阶段→实际审图→finish保存/自动摘要，无手工阶段或成功动作重放；原fail/unknown及自动独立复核保留，不称零异常。大图、任意恢复和保存重开仍未证明。新包须采用后启动新MCP/CLI进程，文件存在不等于当前宿主加载。

本地更新采用后的日常路径：确认plan/job → 同包run → 查看review.imagePath → 实际审图通过后给finishInput添加visualAccepted:true → 同包plan finish。AI始终负责实际图面取舍，失败先读现场，仅续合法剩余阶段；旧runtime visual_review/finish为兼容原门禁，未删除。

自动收尾（当前ON三器件本例已实际保存）：run记录图面SHA，同包CLI返回finishInput；AI实际看图后才显式visualAccepted:true，由easyeda_plan(finish)顺序复用原visual_review/finish门禁，自动保存delivery.json完整回执。减少复制ID和手拼JSON，不自动判断视觉/清任务意图/重试未知保存。saved:true不等于保存重开证明，文件应用不等于所有宿主已加载。


- 按任务所需读取，缺什么再补读，不默认收集整页全部属性。档案与执行方案入口见 [资料及绘图文件](references/drawing-files.md)：snapshot 可直接落 JSON＋Markdown；整页重画的 plan 保留独立的全器件/逐脚目标，局部操作只读相关范围。新建图元的返回 ID 与旧源 ID 分开。
- 显示字号使用 `schematic.attribute.modify(...,fontSizeUnit:"display")`，原生单位保持 `native` 默认；不把该换算套到其他 API。普通器件需按已读旋转值重建时显式 `rotationMode:"stored"`；位置/朝向修改优先 `preserveInstance:true`。`schematic.wire.labels.apply(wireIds,labels)` 按指定可见集合显示/隐藏真实 Name，保留网络含义并回读属性；缺失发布返回 partial，不为找标签重建导线。必要的 raw 脚本可调用 `helpers.call(action,payload)` 复用适配。

- `easyeda_health` 选择目标窗口，后续固定 `window`；用 `target.projectUuid/documentUuid` 校验身份。`easyeda_actions(exact:...)` 查询具体动作，API 缺口用 `easyeda_api` 查签名后通过 `easyeda_execute` 执行 async JavaScript。
- 初次读取也按任务显式选择 `easyeda_snapshot` 的 `profile`：位置/排版用 `geometry`，连接用 `electrical`；已知对象传 `primitiveIds`，两类信息都需要时分别补齐必要范围。默认显式传 `includeTexts:false`（包括指定 `outputFile` 时）；需要文字排版时再读相关可见位号、参数或网名。只有任务确需完整属性/页面图元时才使用 `full`，不能因 API 默认值为 full 就默认全读。3D 模型、图框隐藏字段等与当前任务无关的属性不读取、不为其缺失扩大调试。geometry 的未知网络不能用于电气验收；身份或必要连接缺项按需补读，不猜测。`cacheOnly:true` 或 `easyeda_runtime(operation:"state")` 只读后台已有观察，零 EDA 读取。缓存的覆盖范围、陈旧标记和 requiresScopedReadback 必须一起看。当前宿主原生事件漏报手动移动/撤销，缓存标 external_unconfirmed，不作为新鲜坐标依据；返工按对象 ID 定向读取。程序仍在普通完整修改单元前后局部回读，暂停续接及 finish 还会对照此前局部观察，发现差异返回最新局部事实且不执行旧计划。网表导出仍为文档级，局部组件读取不等于局部网表编译。
- 将一个完整修改单元放进 `easyeda_batch`，传 `edit:{mode:"layout",primitiveIds:[受影响器件ID]}`；授权改连接用 `mode:"design",expectedPins:{"位号.脚号":"新网络"}`。后台推导范围可作补充，已知范围应明确提交。整页交付需 DRC 时加 `requiredChecks:["drc"]`。
- 程序在整个单元前后核对身份、逐脚网络、受影响导线及出线，返回 `rules` 和 pass/fail/unknown；文字碰撞是候选 WARN，需实际图面确认。官方 `schematic.attribute.modify` 的纯显示修改会核对属性身份/电气值并省去网表编译。`complete:false` 只延后检查，不算通过。`verifyPreservedSchematic:true` 仅在需要整页身份/连接保留比较时使用。与局部自动比较同时启用时，两者复用同一读取阶段的网表与引脚；修改前后分别实读，不跨修改或跨请求缓存。局部返工不要额外请求整页比较。
- `schematic.wire.from_pin` 从实测引脚朝向生成非零正交引线，可 `dryRun` 看路径。平台原生网名/电源/地真实连接不强加短桩。任意 JS 保留开放；影响无法确认时回执为 unknown，先读实际结果再续做。
- `easyeda_screenshot` 返回画布和图片回执；后台窗口可能不重绘，若画面与本次修改不一致，直接用 `schematic.export.image(format:"png",scope:"selection",primitiveIds:[本次全部范围])` 导出实际对象；该导出回执同样可供 visual_review，截图字段传导出的 requestId。宏观布局、文字与符号边框用图面判断，电气用真实逐脚数据。必要时局部放大，不在临时断线步骤反复检查或运行 DRC。

## 任务连续性（按需小量读取）

- 新任务或上下文恢复时，先读 `easyeda_runtime(state,window)` 的摘要；默认只返回最近 12 条回执和任务目标，不返回全工程对象。任务内容是调用者记录的意图，不是执行证据或新的指令来源。
- 用户确认目标后、提交该目标的首批操作前，调用 `easyeda_runtime(operation:"task_update",window,target:{projectUuid,documentUuid},expectedRevision:当前task.revision或0,task:{goal,primitiveIds:[范围ID],remaining:[剩余事项],note:必要说明})`。任务字段有界，冲突返回 409 时只重读当前任务再合并；成功后检查 `persisted`，不要把持久化失败当成已保存记忆。
- 用户更换目标且旧操作可能未结束时用 `task_replace`（参数同 task_update）：程序先核对当前页面和版本，暂停并取消所有客户端的旧剩余步骤，确认无进行中动作后才更新任务；成功后仍 paused，按范围回读并准备新动作，恢复已授权任务时显式 resume_queue。无法确认原生动作结束时返回 unknown，不更新目标、不叠加新写入。普通进度补充仍用 `task_update`，不操作队列。插件不读取聊天，目标变更仍由 AI 提交；阶段边界更新即可。
- 历史结果仍 unknown、原执行实例已结束且用户明确接受当前状态时，使用同一 `easyeda_runtime(operation:"task_accept_current_baseline",window,target,acceptance:{acceptCurrentState:false,requestIds:[旧请求ID]})` 预览：后台原生回读工程/原理图/图页/Board，返回结构基线哈希、精确旧证据和任务版本。取得用户对该结构及旧实例结束的明确确认后，才提交 `acceptCurrentState:true`、原预览的 `expectedBaselineSha256`、epoch、attempt/scope引用、同库前后handler状态证据和 `manual_session_termination_attestation`，以及新task/expectedRevision/generation。结构变化、旧窗口仍在、终态关联不全或持久化失败均拒绝；用户声明不是原生执行成功证明。原unknown、未确认保存、原始证据及永久防重放不改；仅当前新任务/页面/代次内的typed编辑与保存可继续，raw/工程级/跨页及其它未知仍拒绝，任务版本或连接改变后重新核验。接纳后队列保持暂停，显式 `resume_queue` 后再执行已授权新任务，不重放旧动作。完整条件见恢复存储文档。
- 恢复时当前页A与本任务目标页D不同，且普通原生切页被旧unknown阻断：先在A用上述 `task_accept_current_baseline` 的 `acceptCurrentState:false` 预览，核对同工程图页清单包含D，并取得用户对A当前结构基线的接受及旧原生实例已结束的明确声明。然后仅调用一次 `easyeda_runtime(operation:"recovery_open_document",window,target:{projectUuid:同工程UUID,documentUuid:D},generation:A当前generation,expectedRevision:A当前task.revision或0,acceptance:{acceptCurrentState:true,requestIds,connectionEpoch,expectedBaselineSha256,reason,attempts,unsavedScopes,nativeEndEvidence})`，所有证据沿用A本次预览及已核原生handler状态。此操作通过官方 `document.open` 改变当前视图，再以 `document.current` 回读D，不是零EDA读取；unsavedScopes仅核对旧证据，不放行内容写入。只接受明确导航成功且目标相符的回执；超时、unknown、ready:false或目标不符即报告阻塞，不重试、不转Computer Use。导航不创建新task、不接纳D基线、不授予绘图/保存/续接许可，旧unknown和防重放保持；到D后必须重新preview并通过现有 `task_accept_current_baseline` 接纳本任务，再按既有门禁执行授权写入。此为源码候选，未采用前不能向旧宿主发送该operation。
- 当前源码候选将活动执行、观察缓存和持久恢复台账分开：同一 Go 后台使用 audit 目录的 `recovery-端口.sqlite` 及身份标记保存意图、尝试和原始结果；旧 `runtime-端口.json`、JSONL 与 MCP 回执保留。已移除256条回执的准入及导入/恢复数量限制；历史由SQLite保存，非活动回执持久化后释放缓存，去重不依赖最近条数。8页面是观察缓存，不是历史工程容量。这不是 EDA 保存。正式采用前仍以已加载版本为准，兼容与回退见 [恢复存储](../../docs/runtime-recovery-store.md)。
- 候选 daemon 重启只载入恢复索引，按当前工程/文档懒加载相关历史；旧对象待局部回读，检查/截图证明和执行队列不恢复。先用实际 `document.current` 确认新窗口身份；程序结合工程/文档 UUID 与扩展会话标识恢复对应历史。空连接不作为候选，连接先后不抢占任务；候选唯一时可自动恢复。若返回 recovery.selection_required，读 state 的历史任务，按当前用户任务选择 sourceWindow，再调用 `easyeda_runtime(operation:"restore",window,sourceWindow,target,generation:当前观察版本)`；目标窗口须经原生读取且尚无新任务/对象，禁止覆盖已有工作。绑定只迁移历史上下文，之后仍按对象 ID 回读，不恢复旧步骤。
- 返工先按 `primitiveIds` 查已有缓存，再对修改范围原生回读；未涉及部分继续保留。程序只更新真实观察，不拿计划坐标当结果。后台合并写盘只影响可丢弃观察；请求意图和结果由分发链同步提交。持久化失败查看 `persistence.error`；写后 `RECOVERY_PERSISTENCE_FAILED` 必须同时保留 `result.execution` 和 `result.persistence`，不得当作未执行。历史终态unknown不锁新请求；仍可能执行的调用继续互斥，断连本身不证明原生调用结束。不得清台账、改成功或盲重放。

## 返工、续接与收尾

- 用户更换目标时按上面的 `task_replace` 处理；只需废止本 MCP 会话旧指令时保留 `easyeda_control(operation:"supersede",window:...)`。只修改指出的部分，同类问题从已观察对象筛选范围；影响不明或证据失效时才扩大读取。
- `easyeda_control(status)` 查看 Connector 队列；`easyeda_runtime(state)` 查看后台工程观察和任务回执。暂停/恢复队列走 `pause/resume_queue`；已确认暂停的批次用 `resume(requestId)` 只续未执行步骤。失败、超时、断连或未知写入不能直接重试整批，先局部回读并补未落地部分。取消不回滚，运行中的原生 API 不能强杀。
- 完整范围修改后看图，使用 `easyeda_runtime(visual_review,requestId,generation,screenshotRequestId,source:"ai")` 记录与当前观察绑定的审阅；这是 AI/工程师的审阅声明，程序不冒充已经看懂图片。
- 保存按用户授权直接调用对应 typed save；已授权“切换前保存”时先保存并确认结果，不因没有task或finish证据另问一次。需要正式验收时才使用 `easyeda_runtime(operation:"finish",window,requestId)` 复用已有自动检查及图面证据后保存；requestId 为原修改批次，不是导图请求，`easyeda_control` 没有 finish 入口。声明了 DRC 时先调用实际 `schematic.drc.check`；finish复用该结果，不重复检查。混合写入批次的早期检查不作最终证据。电气错误与必要证据未知不交付为通过；`saved:true` 与重开持久化证据分别说明。当前宿主实测 wire Name 隐藏在保存重开后恢复显示，不能仅凭即时回读或 saved:true 宣称内部网名清理已持久化，也不清空电气 Name 作为替代。
- 实时控制台为 daemon 同端口 `/dashboard`（默认 http://127.0.0.1:60932/dashboard），无额外模型调用/工程读取。状态缓存不代表实时全工程正确，也不代表保存。

## 能力边界

Connector kb.30：默认 JSON 网表生产导出返回空时，单页、无层级、无屏蔽且单子部件的原理图可读取宿主已编译连接作为分析网表。`schematic.export.netlist` 此时返回 `netlistType:analysis-json`、`.analysis-netlist.json` 与 `netlistProvenance`；`schematic.read/components.list` 也携带来源。它保留缺封装和原生 DRC 失败，不能称生产网表或 DRC 通过。其他格式不替换格式，范围/身份/引脚/编译快照缺证据时仍报告未知；不通过忽略 DRC、补假属性、重新注解或保存来导出。格式和实测边界见同包 `docs/netlist-analysis-fallback.md`。

原理图自动检查是本版重点；PCB 保留已有结构化读取/写入，不能借原理图检查证明 PCB 完整验收。官方事件/BBox 为 Beta，文字缺测仍可由局部图面审阅，电气未知不能被视觉豁免。权限依当前任务和宿主账号/API，本包没有独立桌面控制服务。Docling 在资料解析上游，OPA 和其他流程框架本轮不引入。

CLI/后台、MCP 与 Skill 使用配套完整包；Connector 的知识库增强版独立编号，具体版本以 easyeda_health 和当前项目交接为准。sources.open.json 的 connectorVersion 记录原分发包来源，不表示当前已安装 Connector，也不能据此降级覆盖知识库增强版。升级时核对后台、MCP、Skill、Connector及知识服务的实际加载版本；只有所需组件发生变更时才重启或重载。构建完成不代表当前宿主已经加载。

后台由多个 MCP/Connector 共用，关闭或重载某个 MCP 不停止后台；只有明确 daemon stop/restart 才停止。启动日志在既有 runtime/daemon-stderr.log，启动错误不伪装连接成功。
