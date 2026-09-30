# EasyEDA Agent Open

**让 Codex 等 AI Agent 直接读取、查看和修改嘉立创 EDA 专业版工程。**

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

默认本机通信端口为 `60932`。open.9.4 包含 Connector、MCP 与后台读取优化；升级时需导入匹配 `.eext`，切换 MCP 与 Skill 到完整包，正常停止旧后台后重载 Codex。
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
