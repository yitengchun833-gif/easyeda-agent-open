# 离线验证与边界

在仓库根运行工作流 unittest、tools 下 PCB 单元测试、Open Go/MCP/Connector/知识库测试。Windows 建议 Python -X utf8；MCP集成测试须将 EASYEDA_BIN 指向新构建的 bin/easyeda.exe。测试使用隔离临时目录和模拟EDA，不授权真实工程写入。

公开版不分发私有工程和原始运行台账；依赖私有PCB回放的单项用例保留在本地，公开工具保留其他独立检查。当前实测结果与跳过条件见根 VALIDATION.md 和Release说明；历史数字不代替当前测试。
