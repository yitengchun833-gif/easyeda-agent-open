# 当前插件源码入口

本目录是当前 Open 插件的唯一日常开发入口，已建立独立的本机 Git 仓库。

- 分支：`codex/current-source`。
- 初始源码快照标签：`local/source-snapshot-20261015`，仅表示本机开发状态留档，不是发布或实机全部通过。
- 此仓库没有新增远程地址，本轮不推送。上游出处、原始 LICENSE、NOTICE 和 README-UPSTREAM.md 保留。
- 项目总目录的另一个 Git 仓库管理工作流；两个仓库在同一个 `EasyEDA-开发与版本.code-workspace` 中查看，提交时注意所选仓库。

## 功能目录

| 目录 | 责任 |
| --- | --- |
| `cmd`、`internal`、`pkg` | Go CLI、daemon、状态与校验 |
| `mcp` | Agent 的 MCP 接口、方案准备、连续执行和摘要 |
| `extension` | 嘉立创 Connector 扩展源码 |
| `knowledge` | 知识服务源码 |
| `skills` | 随插件分发的专业规则和操作方法 |
| `tools`、`scripts` | 配套工具、构建和分发脚本 |

## 开发与安装的边界

只在本目录修改插件源码。在用文件位于本目录的父目录，例如父目录的 `mcp`、`bin`、`skills`；Agent 全局安装 Skill 也属于部署副本。它们不是日常开发入口。

Git 提交不会安装插件。先开发、检查和生成包，再在授权的安全检查点采用；不得为了同步源码自动覆盖在用文件或重启服务。

当前开发树保留了尚未发布的候选，包括 `extension/src/actions.ts` 的准备诊断修订，以及 `tools/pcb_constraint_apply.mjs`、`tools/pcb_constraint_compiler.mjs` 的后续候选。初始提交不把这些候选改判为已发布或已验收。判断实际采用版本，仍看项目的包清单、加载记录和目标文件哈希。

当前已采用包为 `EDA-schematic-ready-update-20261015.zip`，SHA256：

`355c3973870a91546dfb02bb2bb04e319495c162e99f543e39ad4f50cd7fe6dd`

旧 `.local/easyeda-agent-open` 保留历史和已发布 PCB 复查的来源，不移动、不合并，也不作为 Open 主链日常修改入口。PCB 专项继续暂缓。

运行历史、安装包、依赖目录及工程数据不进入本源码 Git。目录和版本总览见项目 `docs/项目目录与版本.md`。
