# pcblayout

社区入口：[问题范围、可认领任务与离线验证](../../docs/pcb-solver-community-design.md)。

固定装配面上的有限、确定性 PCB 布局搜索。输入是 `pcbmodel.Board` 和显式移动组：每组声明
成员、anchor、允许的平移/旋转、固定轴，以及随组移动的内部 track/via/region ID。

`Generate` 枚举有预算的完整组变换，精确检查凹板框、同面器件间距和 keepout。它保留输入
板上未受影响对象的已有 finding；涉及移动对象的问题必须修复，不能按对象对豁免变差的重叠。
候选排序只使用事实向量：
移动组数、总位移、旋转次数；布线指标由 `pcbsolve` 在真实共同路径产生后追加。

`translationSearch` 声明平移步长和相对原始 anchor 的最大距离。`Generate` 给出种子候选，
`Neighbors` 根据协调层传来的实际冲突对象动态生成相邻位置。它返回的中间状态可能有机械
冲突，协调层必须用 `ChangedFindings` 继续修复或拒绝，不能直接布线或 Apply。
邻域在当前位置及相邻位置探索已声明的整体转角，因此可发现“原地旋转碰撞、平移后旋转
才可用”的状态。仍遵守固定轴、原始 anchor 和搜索半径，不做未声明的旋转或翻面。
`Project` 从独立请求复验完整成员、anchor、允许偏移/旋转和固定轴，拒绝重复组及越界变换。

`Check` 只返回机械事实，不把同网名、低飞线长度或布局分数解释为电气连通。公共包不读取
文件、块库或编辑器；宿主需先把 placement hint 和设计意图转换为这些公共结构。

验证：

```bash
go test ./pkg/pcblayout
```
