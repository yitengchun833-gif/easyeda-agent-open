# 完整仓库与开发入口

本公开快照包括Open本体和 `workflow/` 配套工作流。来源提交、匿名化和非产品资料排除项见PUBLISH-PROVENANCE.json；版权见NOTICE和workflow/THIRD_PARTY_NOTICES.md。本机私有Git历史没有并入公开仓库。

根目录包含Go CLI/daemon、MCP、Connector、知识服务、PCB算法、Open Skill和构建测试；workflow/包含独立插件清单、Hook、九个Skill、状态/回执及解析。tools/为唯一PCB compiler及其类型、专业检查和离线测试。

安装和连接见README.md，构建见README-OPEN.md，工作流见workflow/README.md。完整Windows包附运行载荷、workflow目录和source源码快照。Node/Python、大型解析依赖及知识模型按各组件说明准备。

源码、安装、新进程加载和实际EDA验收分别核对，不因打包而覆盖用户运行数据库。
