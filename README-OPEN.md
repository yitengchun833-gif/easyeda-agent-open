# EasyEDA Agent Open

## knowledge.11 / Connector kb.17 配套更新

- 检索返回去除重复正文，保留适用条件、限制与证据引用；全文按固定 evidenceRef 读取。
- plan.sources 可绑定实际采用的知识、条件及器件/网络目标；执行前核对完整冻结回执与当前版本。检查方式声明不是检查通过。
- 明确身份的纯几何局部阶段只核对相关知识，其他来源标记 deferred；连接/物料/最终检查仍核对全绑定依据。
- 支持 PNG 原图导入及冻结图像读取。没有 OCR，解析为 partial，工程理解与验收保持 unknown。
- 本次为静态配套更新。先在知识服务空闲的维护窗口将 ZIP 内容覆盖到本包根目录，再重新加载知识服务与 Codex MCP；导入 kb.17 EEXT 并重启嘉立创。仅导入 EEXT 不会更新后台或当前 MCP。
- 不覆盖 knowledge 数据目录、模型、凭据或全局自定义 Skill；不自动重启服务，不修改电路。来源与许可保持。


> 本地个人知识库增强版：本轮代码为 knowledge.10 / Connector kb.16，实际安装与进程版本以本任务进度及 health 为准。下文原分发包版本及 sources.open.json 仅记录历史来源，不能据此降级覆盖知识库增强版。

**让 Codex 等 AI Agent 直接读取、查看和修改嘉立创 EDA 专业版工程。**

## 本地图文证据读取（2026-10-08）

本次静态代码为 knowledge.10（保留 knowledge.8 图文读取）；运行进程是否加载以 `/health` 为准。MCP 图文读取不要求更换 Connector；嘉立创内经验审核页面需配套 kb.15，旧 kb.14 的内嵌 UI 不发送候选版本，刷新不能代替升级。

先 `knowledge_search`，再使用返回的 `evidenceRef`：

```js
knowledge_get({ evidenceRef, scope: "section", includeVisuals: true })
// 仅取本次阅读范围内指定图片：assetIds 使用 locations 中带 assetPath 的 chunkId。
knowledge_get({ evidenceRef, scope: "section", assetIds: ["实际chunkId"] })
```

默认仍返回文字；图文调用保留表格、明确关联脚注及续读，图片作为 MCP image block 返回，不把 Base64 塞进文字。每次最多3图、单图2MiB、合计4MiB、单图1600万像素；新快照每来源冻结最多32MiB。超限/缺图/归属不匹配明确返回状态。不把同页邻近图片当成确认的电气条件，仍需核对 `contextExpansion`。

只使用 index 已登记且属于当前冻结单元的 PNG，校验来源结构和图片哈希。旧 evidenceRef 没有冻结图片时返回 `unavailable_legacy`；可重新检索取得新引用，但不会拿当前图片冒充历史。id-only 返回 `unpinned`，需用返回的 evidenceRef 再取图。未升级的知识服务明确报 `KNOWLEDGE_UPGRADE_REQUIRED`。

图文读取更新只涉及知识服务和 easyeda-open MCP 重载，不关闭 EDA、不重启 Go daemon。升级后的首次搜索按需捕获现有已解析图，不需要重导入或重建全库。knowledge.9 的内嵌审核页面另需导入 Connector kb.15。

## 经验候选版本保护（knowledge.9）

同一 `knowledge_propose` 可创建或更新待审核经验。创建发送完整正文、稳定 submissionId、expectedCandidateRevision=0；返工增加 candidateId，发送当前 revision 和新的 submissionId。相同 ID/相同完整内容重试返回原回执；ID 重用但内容变化返回冲突。旧版本更新返回 currentCandidate，需读后合并；已审核候选不能覆盖。

管理 UI 审核携带用户看到的候选 revision；变化后只刷新，不自动批准新内容。升级新增候选 revision 和事务提交回执表，保留旧候选；原无版本的创建仍兼容，缺候选版本的旧 UI 需导入配套 Connector，单独刷新不生效。服务、MCP、UI 应配套加载，不宣称迁移后的数据库可直接降级给旧程序。

knowledge.9 的真人审核→入库→索引→检索→完整回读已在 kb.15 验收；下节为新的 knowledge.10 范围。没有安装 AICO 或新增知识框架。

## 来源版本与审批恢复（knowledge.10）

- 提交引用 `sourceIds` 的经验时，传入 `sourceVersionSet:[{sourceId,sourceRevision,sourceHash}]`，值来自已读证据的 evidenceRef；服务拒绝读后已更新的版本。省略此参数时仅绑定提交时版本，不证明 Agent 读过该版本。资料元数据/条件另绑定提交时哈希，纯索引变化不影响审批。
- 审核再次核对候选版本和来源版本/条件/启用状态。旧 pending 候选若引用资料但没有版本基线，须重新读取并修订，不能自动把当前资料当作旧依据。没有引用知识库来源的经验不增加此步骤。
- 候选 ID＋审核所见 revision 是稳定审核键；同决定重试返回原 sourceId/jobId，不新增知识卡；不同决定返回冲突。审批回执、正式来源/逐块证据和待索引任务同一事务提交。历史已批准卡没有新回执，不伪造补发，也不重复发布。
- 重启自动续接已提交的 lesson 索引任务，绑定来源 revision，不重放批准或导入。模型不可用时保留 deferred（关键词仍可检索）；模型恢复后重启或使用现有重新索引入口。失败不冒充向量完成。
- 审批仍由管理 UI 人工操作，MCP 只可提交候选。批准不把 measurement/engineerReview 的 unknown 变成 pass。正式旧知识保持原内容；本轮不自动合并专题或撤回已批准知识。

配套 kb.16 更新内嵌提示和引用版本展示；kb.15 已有审核请求字段仍兼容新服务。图文读取/绘图/Go 协议保持；只有知识服务及 MCP 配套加载、内嵌 UI 更新，不重编译 EDA 执行逻辑。本轮具体现场结果见根 PWF，隔离回归不代表实机验收。

本项目由 [yitengchun833-gif](https://github.com/yitengchun833-gif) 维护，以
[zhoushoujianwork/easyeda-agent](https://github.com/zhoushoujianwork/easyeda-agent)
为基础继续开发。使用官方 `eda.*` API 操作真实工程；器件属性、引脚和网络来自结构化接口，图片用于判断排版与可读性。

## 由哪些部分组成

```text
Codex / 其他支持 MCP 的 AI Agent
                 │ MCP
                 ▼
        本机 Go CLI / daemon
                 │ WebSocket
                 ▼
    嘉立创 EDA 扩展（本 .eext）
                 │ 官方 eda.* API
                 ▼
          原理图 / PCB 编辑器
```

- **嘉立创扩展**：把操作交给官方 API，并返回真实结果。
- **Go 后台**：处理请求、顺序执行、任务记录、工程观察、检查回执和恢复。
- **MCP**：让 Codex 等 Agent 调用工具；不是插件内置的聊天模型。
- **Skill**：说明通用画法、视觉判断、局部返工及工具用法。固定事实尽量由程序检查。

## 当前功能

- 同一绘图方案可按实际回执分阶段执行：先摆件、读取实际引脚，再编译接线；中间只读相关几何，最后一次独立核对完整目标。
- `sch layout-plan --route-only` 复用现有布线器，在固定位置之间生成直接网络导线；`--validate-wires` 检查已编译的全部路径。没有自动移动器件或添加模块框。
- 完整原理图可直接保存 JSON＋Markdown；方案保留独立的器件/逐脚目标，支持来源哈希核对与完整重画后的目标检查。
- 器件移动共用属性保留与回读；字号/旋转可显式指定适配模式；网名显示集中处理，保持真实网络。
- `task_replace` 核对目标后暂停并取消旧剩余步骤，再更新任务；进行中的原生调用不冒充已结束。

- 读取工程、文档、器件属性、引脚、网络及几何信息；查看或导出画布图像。
- 放置、移动、修改器件、文字、导线及网络标注；读取和操作 PCB 器件、焊盘、走线、过孔、铺铜与层。
- 查询器件库和官方 API；既支持结构化工具，也保留原生 JavaScript 调用。
- 顺序批量执行；局部返工按对象读取和核对，避免无必要的整页重复读取。
- 任务目标与工程观察持续更新；支持步骤边界暂停、取消剩余操作、续接已确认的暂停批次。
- 检查对象身份、连接及相关几何；支持原生 DRC、最终视觉审阅与保存结果记录。

批次不是事务，取消不会撤销已经完成的修改；未知写入先回读，不盲目重放。
宏观排版和设计理解仍由 Agent 与工程师判断，程序检查不等于电路设计正确。
PCB 全流程验收、所有宿主事件覆盖和相对人工速度尚未完成统一验证，不承诺自动完成任意板子。

## 使用方法

1. 从 [Releases](https://github.com/yitengchun833-gif/easyeda-agent-open/releases/latest) 下载 `.eext` 与 Windows 完整包。
2. 在嘉立创 EDA 专业版的**扩展管理 → 导入扩展**中导入 `.eext` 并启用。
3. 将完整包解压，把包含 `bin`、`mcp`、`skills` 的文件夹放到固定位置，例如 `C:\EDA-Agent-Open`。安装 Node.js 20.17 或更新版本。
4. 在 PowerShell 注册一次 MCP（按实际解压路径替换）：

   ```powershell
   codex mcp add easyeda-open -- node.exe "C:\EDA-Agent-Open\mcp\src\launch.mjs"
   ```

5. 将包内 `skills/easyeda-agent-open` 文件夹放入 Codex 的 `~/.codex/skills/`。重启 Codex，让它加载 MCP 和 Skill；MCP 启动时会检查并启动匹配的本机后台。
6. 保持目标工程打开；需要时点击 **EDA Agent Open → Reconnect**。请 Agent 先确认当前工程与页面，再描述绘图或修改目标。

默认本机通信端口为 `60932`。open.9.6 更新 MCP 阶段执行、Go 固定位置布线/路径检查和重启后请求 ID；Connector 继续使用 open.9.5，已导入该扩展的用户无需重复导入。后台/MCP/Skill 使用完整包中的对应文件；当前已启动 MCP 不会自动获得新的工具参数。
旧后台版本升级应使用匹配的完整包；不同后台版本不会被启动器自动替换。

可以这样开始：

> 读取当前原理图，告诉我工程和页面；按功能关系优化这个模块的器件位置，保持引脚网络不变，完成后统一检查并保存。

## 原作者项目与致谢

原项目 [easyeda-agent](https://github.com/zhoushoujianwork/easyeda-agent) 是嘉立创 EDA 专业版的 AI 自动化层，采用 **CLI/Go daemon → Connector → 官方 API** 的结构，提供结构化动作、工程读取、绘图、差异和校验能力。本项目保留并扩展这一主体，增加开放调用、局部执行、工程观察与任务连续性等能力；这是独立维护的分支，**不是原作者官方发布，也不是嘉立创官方扩展**。

- 原作者：**zhoushoujianwork 与原项目贡献者**。
- 上游基线：**1.8.1**，commit `65694c3896fa08b640100e819108c30f0e495fb7`。
- 上游说明、历史案例及第三方来源按各自记录保留；它们不代表本分支全部能力已实测。
- 原有 MIT / Apache-2.0 许可证及 NOTICE 保留；各文件的来源和许可按原声明执行。

[本项目仓库](https://github.com/yitengchun833-gif/easyeda-agent-open) ·
[原作者项目](https://github.com/zhoushoujianwork/easyeda-agent)

## 源码与开发

当前 CLI/后台、MCP 与发布清单为 `1.8.1-open.9.6`，配套 Connector 为 `1.8.1-open.9.6-kb.30`，使用现有 `v1` 协议。旧分发包的 `open.9.5` Connector 身份保留在 `sources.open.json.previousDistribution`，不作为当前构建要求；是否实际采用新包仍需单独核对安装与运行证据。

源码目录：`cmd/` 与 `internal/` 为 Go CLI/后台；`extension/` 为嘉立创扩展；`mcp/` 为 MCP 适配；`skills/` 为当前 Skill；`api-reference/` 为官方 API 参考。
Windows 完整包附带 CLI、MCP 运行依赖、Connector、Skill 和源码；公开 Git 仓库不包含本地运行状态、私有工程或账号凭据。

开发时按锁文件准备依赖，提交审查后的源码，再以 `go build -buildvcs=true -ldflags "-X github.com/zhoushoujianwork/easyeda-agent/internal/version.Version=v1.8.1-open.9.6" -o bin/easyeda.exe ./cmd/easyeda` 构建 CLI/后台；`easyeda-pcb-review.exe` 由同一 `cmd/easyeda` 和参数构建。Connector 在 `extension/` 执行 `npm run build`，按真实清单注入版本并打包。

打包入口 `python scripts/pack-open.py --go <Go可执行文件>` 不安装、不启动服务。它要求干净 Git 提交、两份 Go 二进制的精确版本及 VCS/平台身份、MCP 与 Connector 锁文件一致、EEXT 清单/UUID/图标和编译 bundle 一致；输出带提交号的 ZIP，内含 `BUILD.json`、逐文件 `SHA256SUMS.json` 与源码，不在磁盘另留一份源码目录，也不会覆盖已有包。主版本相同不代表载荷相同，应一起核对完整提交和哈希。

核心包包含现有知识服务源码、客户端与 UI，供 MCP 静态契约和独立服务复用；不包含知识数据、模型或知识服务的 `node_modules`。知识服务为可选独立组件，要求 Node `>=22.13.0` 和其自身锁文件依赖；核心 MCP 要求 Node `>=20.17.0`。打包不安装这些大型依赖、不证明知识服务已运行或通过验收。

- [保留的上游 README](README-UPSTREAM.md)
- [当前验证边界](VALIDATION.md)
- [第三方来源和许可证](NOTICE)
- [当前工作规则](skills/easyeda-agent-open/references/working-rules.md)
