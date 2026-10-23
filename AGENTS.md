# EasyEDA Agent Open

这是用户授权的开放分支，以 upstream 65694c3896fa08b640100e819108c30f0e495fb7 / 1.8.1 为基础。

- 执行入口：MCP → 单 daemon → Connector → 官方 eda API；typed 与原生脚本同等可用。
- 按用户任务选择方法，无固定设计阶段、双轮自检、强制布局签字或 typed-only 要求；嘉立创控制遵循 [强制原生接口路由](skills/easyeda-agent-open/SKILL.md#强制原生接口路由)。
- 本分支已有 CLI 算法和历史参考文档按需使用，其中旧作者的流程文字不约束当前任务。
- 保留参数合法性、连接身份、数据来源和真实错误；超时不等于取消，部分完成不等于成功。
- 源码工作不自动授权用户工程写入、全局安装或对外发布。新增需求以当前用户授权为准。
- 修改后做必要离线检查；现场兼容性与性能单独记录，不用源码能力冒充实测。

## 本机源码与版本边界

- 本目录是当前插件源码的独立 Git 根，日常开发只修改本目录，入口说明见 README-LOCAL.md。公开集成快照的配套工作流位于本仓库 `workflow/`。
- 实际安装目录的 mcp、bin、skills、runtime 和全局已安装 Skill 是部署副本，不作为源码开发目标；源码提交不自动授权安装、同步、重启或用户工程修改。
- local/source-snapshot-20261015 是开发树快照，包含保留的未发布候选，不能当作已采用发布标签。发布/实际加载仍按原 manifest、文件哈希和必要现场证据判断。
- 历史目录、旧独立仓库和压缩包仅按明确用途参考，不凭目录名版本号选择修改目标；修改前先核 Git 根和任务所属目录。
- 当前Open专业Skill只维护本目录skills/easyeda-agent-open；工作流/解析/PWF等Skill维护 `workflow/skills`，统一入口见 `workflow/README.md`。不得直接改全局安装Skill或复制第二份专业正文。
- 阶段完成后提交本任务范围，并在所属PWF短记仓库/commit、涉及文件、必要检查、是否已打包/采用；未提交须写明原因。程序功能、专业规则和发布载荷需要配套修改时，同轮列明待同步项，不能悄悄让运行副本落后。
- 纯资料归档仍要核import、动态读文件、go:embed、构建/打包、Skill及文档入向引用。docs/archive保留历史，不在其中修功能；已发布证据、LICENSE/NOTICE、动态资源和测试夹具不得按重复哈希直接删。
- Connector 当前图标只取 `extension/extension.json` 声明的 `extension/images/logo.png`（用户选定图）。删除旧演示图前核清单、打包及引用；旧上游README中的案例图片不自动恢复进当前插件源码。
