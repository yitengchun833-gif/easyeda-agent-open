# 工作流使用的Skill与适配入口

这里是本项目维护的工作流Skill源码。默认工作区的“02 工作流Skill”直接打开本目录；“03 EDA专业Skill”打开Open插件的唯一专业源码。它们由各自Git仓库管理，修改源码不会自动覆盖已安装副本。

## 项目内维护

| Skill | 作用 | 适配位置 |
| --- | --- | --- |
| [evidence-to-delivery](evidence-to-delivery/SKILL.md) | 阶段、输入、方案确认、执行和交付衔接 | 本目录；配套`references`和`workflow.py`保持同包 |
| [planning-with-files](planning-with-files/SKILL.md) | CURRENT、计划、发现与续接 | 本目录；不另建总状态 |
| [document-intake](document-intake/SKILL.md) | 文档解析路由、Docling和回退策略 | 本目录及其运行路径说明 |
| [pdf-intake](pdf-intake/SKILL.md) | PDF原页、关键图表核对 | 本目录 |
| [spreadsheet-intake](spreadsheet-intake/SKILL.md) | 表格值、公式和来源读取 | 本目录 |
| [information-collection-orchestrator](information-collection-orchestrator/SKILL.md) | 必要时选择检索渠道 | 本目录；专业来源名单仍沿用户配置 |
| [research](research/SKILL.md) | 有边界的资料研究 | 本目录 |
| [project-architect](project-architect/SKILL.md) | 项目宏观判断与职责分工 | 本目录；跨对话指挥另以用户授权为准 |
| [easyeda-agent（兼容）](easyeda-agent/SKILL.md) | 旧流程及现有规则/脚本依赖 | 保留必要兼容，不作为Open主执行规范 |
| [easyeda-agent-open（当前EDA专业入口）](../../skills/easyeda-agent-open/SKILL.md) | Open MCP落图、局部回读、规则与保存流程 | **插件source仓库**；不在这里复制第二份Open Skill |

“源码可适配”不代表当前宿主已读取改动。采用后还需核实际安装位置、重读Skill及必要加载结果。

## 外部能力：引用原入口，不复制全局安装树

| 能力 | 当前入口 | 本项目的适配方式 |
| --- | --- | --- |
| Ponytail | 当前宿主提供的 `ponytail:ponytail` Skill；代码任务适配见 宿主外部能力 | 不固定插件缓存版本路径；项目差异写入项目AGENTS/工作流规则，保持宿主插件独立 |
| Archify | 宿主外部能力 | 图在项目docs/architecture维护；工具继续独立 |
| workbench-projects | 宿主外部能力 | 按任务使用；项目衔接写入本地工作流 |
| workbench-router | 宿主外部能力 | 仅选法不明时定向使用 |

Docling、MarkItDown等是解析后端，接入由document-intake维护，不把它们各复制成一个Skill。知识库当前通过Open MCP的知识工具与knowledge服务接入，本次不新造独立“Knowledge Skill”。其他文档/搜索能力按任务读取，完整历史登记仍见[组件清单](../EDA工作流组件清单.md)，其中版本记录需要使用前核实。

需要改外部Skill行为时，先确定改动应落在本项目适配层还是上游；确需项目分支时再保留出处/许可建立明确的源副本。不得直接改全局安装目录或插件缓存，不把仅添加入口说成已启用、已安装或已验证。
