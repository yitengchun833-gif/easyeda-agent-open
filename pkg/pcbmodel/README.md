# pcbmodel

社区入口：[问题范围、可认领任务与离线验证](../../docs/pcb-solver-community-design.md)。

编辑器无关的 PCB 公共数据模型。坐标为 y-UP，默认单位为 mil；运行时 primitive ID 只保留为
来源证据，算法使用稳定的器件位号和 `REF.PAD` 端点。

模型覆盖精确多边形板框、有序铜层及 signal/plane 角色、器件装配面、局部尺寸与旋转明确的
焊盘、导线、过孔、禁布区、复合铜区、规则和模块所有权。`Transform` 围绕 footprint anchor
对器件、焊盘及显式归属的铜执行同一刚体变换；装配面不会被改变。

`Board.Validate(true)` 是当前二层求解入口的 fail-closed 校验：要求精确板框、完整实测规则和
恰好两个已声明铜层。既有板边连接器可以跨出板框，合法性由 `pcblayout.Check` 作为基线事实
处理；重复逻辑 pad number 的多个物理铜岛保留为障碍，但不能作为不唯一的 `REF.PAD` 端点。
四层及更多层可由 `Stackup` 表达，但公共二层 solver 会明确拒绝，不能从单一层数推断内层用途。

包只依赖 Go 标准库。验证：

```bash
go test ./pkg/pcbmodel
```
