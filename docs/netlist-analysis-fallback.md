# JSON 网表读取兼容修复（Connector kb.30）

问题：嘉立创生产网表接口在原理图 DRC 阻塞时返回 undefined；插件此前把所有引脚网名置为未知，无法用于测试点覆盖等分析。

改动位于 extension/src/schematic-netlist-file.ts 的唯一共享函数。优先保留原生成功文件；仅 JLCEDA/EasyEDA JSON 返回空时，尝试 schematic-analysis-netlist.ts 的只读适配。不重新注解、不设置假封装、不关闭 DRC、不保存工程。该适配复用宿主编译后的 purepins.nlNet、originPin.wire 及原生自动网名索引，不自行按几何求连接。

输出是 `easyeda-analysis-netlist/1`，含真实 projectId、schematicUuid、components（真实 Unique ID、属性与逐脚网络）以及 provenance。文件名为 `.analysis-netlist.json`，动作回执的 netlistType 为 `analysis-json`、requestedNetlistType 保留请求格式、netlistProvenance.manufacturingExportAvailable=false。不得把此文件作为生产导出或导入 PCB 成功的依据。非 JSON 请求保持原来成功/失败语义。

兼容边界：当前仅支持单图页、无层级/屏蔽、单子部件；不支持的范围拒绝兼容读取。要求已编译、归属唯一、原生元件清单与 AST 完全对应、位号/实例 ID 唯一、引脚编号和网络索引有效、读取前后修改标记/文档对象一致；外层保留原有事务就绪和身份检查。当前验证宿主 pro-sch/4.1.54.bfdf9a4d（桌面 4.1.60）。未来宿主结构变化仍须重新验证。

验证：37项定向回归测试和tsc通过，覆盖生产导出成功不改行为、空返回兼容、未知字段/范围/身份/变化拒绝、网名和组合脚号、来源传播。当前工程源函数读取 530 元件、1254 脚、267 网，与宿主 getCompareNetlist 的 1177 已连接脚及77未连接脚零差异，修改标记不变。kb.30安装版正式导出analysis JSON及schematic.read成功；实际加载JS SHA256为99501f331687ba5dd525947e7fcf377c7a72a2bf521c1e8c2742ffdc1df9004e，与构建一致。当前health可能仍有旧Connector会话，后续须选择kb.30窗口，不能声称所有旧会话热更新。

分发：导入同包 kb.30 EEXT，保持扩展 UUID de2aa2ac3bd74c00b6701e3adf3ee723 和既有知识库资源。只重载连接器，不重启共享 daemon。回退使用保留的 kb.29 EEXT。源码测试：`node --require ts-node/register --test src/schematic-analysis-netlist.test.ts src/schematic-netlist-file.test.ts src/netlist-availability.test.ts src/read-performance.test.ts`，另执行 `tsc --noEmit --incremental false`。
