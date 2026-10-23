# 历史讨论与训练资料

2026-10-15 按实际引用核对后，从源码根归档以下目录，文件内容原样保留，用 Git 记录移动：

| 原路径 | 当前路径 | 用途 |
| --- | --- | --- |
| `chat/` | [discussions/](discussions/README.md) | 上游讨论、推演与路线比较，6项文件 |
| `training/` | [training/](training/) | 早期抄图训练脚本和样本，3项文件 |

它们不被当前Go/MCP/Connector运行入口加载。训练脚本只读取调用者传入的两个文件路径；开发指南的入口和源码注释已更新。历史文字内的旧路径/旧结论保留原貌，不作为当前操作指令。原讨论索引引用的“项目方向评估.md”在初始Git快照中就不存在，本次没有补造材料。

`.npmignore`继续排除这类资料。Open完整包的`source/`审计快照仍可能包含它们；这次整理不声称减少运行包大小，也没有重新打包或安装。

以下目录虽可能像资料，仍保留原位：`api-reference`供MCP动态读取；`.agents/skills/easyeda-agent`含CLI资源；`internal/blocks/data`及`internal/apidoc`含Go内嵌数据；`docs/releases/evidence`和部分`docs/reviews`被发布检查或测试引用。默认视图隐藏它们不表示可以删除。
