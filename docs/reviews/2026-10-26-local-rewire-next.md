# 局部改线下一版源码候选

基线：`dcdf2ad0e1885c347328b7496f9957f9e4911035`。集成来源：原理图
`0ffe89b46026b2360a0c28908724e2181cb20e1f`、`dfc420d03f8e3e0338210803cfb8e91e6af45db5`；PCB
`17cd67afd7ae528118210ea573ea10adb6f0bfd2`、`0732edc4b6a1edd837f6161e5761f0d5f45894b6`。
四个提交顺序应用无文本冲突；共享 Connector action 和 action 契约均保留。

## 支持范围

- 原理图：`easyeda_plan(operation:"prepare_rewire")` 从完整单页快照准备工程师指定器件引脚、错误线 ID 和已有目标网名。仅接受独立、已命名、正交单段出脚短线；保持原路径，修改网络表达。原生 `Line` 与测量几何必须一致，支持平铺四坐标、单条嵌套四坐标、两个嵌套顶点三种单段编码。分支、交叉、其他引脚/标记接触、总线、多段、新网络推断均拒绝。执行仍复用原计划/job、首写前事实核对和完整回读；partial/unknown 不重放。
- PCB：`pcb route replace` 先离线 dry-run 输出 remove/add 预览及 SVG，再按该 plan 和新回执路径 apply。仅接受独立单网、TOP/BOTTOM 单层、实测 SMD 焊盘中心到中心的选定 track 线链；分支、自接触、未选铜接触、过孔、跨层、同网铜区、锁定/封装内线拒绝。原生首删前严格核对整组 ID、解锁状态、网名、层、两端坐标和线宽；任一预期事实变化零删除。原生删除仍非事务；partial/unknown 后读取真实板况，不自动恢复或重试。

工程师负责设计与最终图面审阅。两项能力都不自动保存，也不证明保存重开持久性；PCB 原生 DRC 独立记录，不把局部落铜当整板通过。

## 验证与采用边界

组合验证已通过：MCP 的 local-rewire/local-correction/plan-tools/project-files 共 51 项；Connector 的选定删除、目标约束、迟到原生结果及严格 PCB 删除共 17 项；Go `TestPCBRoute(Replace|Apply)` 定向测试、CLI 编译，以及 TypeScript `--noEmit --incremental false` 检查。测试只使用本地合成资料和 mocks；CLI 编译产物仅供验证，不是发行二进制。
未运行 EDA、未改全局入口、未部署或推送；未用离线测试冒充实机、保存重开或性能验收。
这次只集成源码，不更改版本清单，不生成可发行 EEXT。基线清单仍为 rc3/kb.35，不能据此认定当前运行 Connector 已有新字段。
之后采用必须重新构建并加载新 Connector（预计 kb.36，由主线确定正式版本），并配套同提交 CLI/MCP；当前 rc3 实机采用由主线独占。
源码回退可撤销本集成提交；本候选不触碰原运行目录、已部署或待部署 rc3 载荷。
