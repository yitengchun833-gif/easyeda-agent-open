# 资料到交付工作流

这是与Open插件配套的可选插件，包含资料解析、方案/确认、执行回执、PCB约束衔接和PWF恢复。目录内 `.codex-plugin/plugin.json` 是独立插件入口；Open MCP和EDA专业规则由本仓库根插件提供，不重复注册旧兼容MCP。

需要 Python 3.11+、Node.js 和已连接的Open插件。Docling/MarkItDown及Office解析依赖按 `skills/document-intake/references/runtime.md` 配置；知识模型、凭据、Jev以及Ponytail等宿主外部能力不随本包复制。

在业务项目目录维护 `.workflow/state.json`，不在源码目录初始化业务状态。示例（按实际路径替换）：

```powershell
python -X utf8 -B workflow/skills/evidence-to-delivery/scripts/workflow.py --help
python -X utf8 -B workflow/skills/evidence-to-delivery/scripts/workflow.py --root <业务项目目录> check
python -X utf8 -B -m unittest discover -s workflow/skills/evidence-to-delivery/scripts -p "test_*.py"
node --test tools/test_pcb*.mjs tools/test_pcb*.cjs
```

PCB compiler唯一实现位于Open根 `tools/`，工作流通过嵌套目录适配复用它。原生动作仍绑定窗口、工程和文档；测试不授权真实工程写入。来源与匿名化范围见根PUBLISH-PROVENANCE.json，工程资料、运行台账和个人配置不随插件分发。
