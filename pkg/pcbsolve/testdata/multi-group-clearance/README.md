# 两个已有相对布局的完整组递归让位

- 场景 ID：`multi-group-clearance`。
- 来源：由 `TestAutomaticRecursiveDisplacementAndFixedGroupBacktrack` 的合成板扩展，
  增加移动组到固定器件的显式外部连接；不是实测板或真实功能模块。
- 开始状态：`blocker = M1 + C1`、`dependent = M2 + C2` 均已有合法相对布局；
  两组没有器件重叠，U1/J1 固定，M1 的焊盘挡住 A/B 的 TOP 直线路径。
  下方机械禁放区限制 blocker 向下移动，向上让位又会碰到 dependent。
- 数据：`board.json` 是公共 `pcbmodel.Board`，`request.json` 是求解需求；
  不属于宿主 `pcb dump` 格式，不能直接作为 CLI `--board` 输入。
- 独立期望：通用 `expect.json` 保存结果类别；`scenario.json` 单独声明完整成员、
  固定器件、外部端点和指标边界。没有保存成功坐标或标准答案路径。

所有参数单位为 mil。线宽 4、铜间距 5、机械间距 5、搜索步长 10。
blocker 仅可沿 y 轴在 80 范围内搜索，dependent 仅可沿 x 轴在 60 范围内搜索；
均固定旋转与装配面，输入只给 `translationSearch`，没有 `offsets`。
A/B 限 TOP；EXT 从固定 U1.3 到移动 M1.3，限 BOTTOM，端点为通孔焊盘。
本场景设 `maxDetour=0`，用于隔离“必须移动布局才能在声明路径范围内通过”的行为；
不把初始失败当成允许任意绕路后的全局无解。X/Z 仅用于组内焊盘障碍建模，
没有既有内部铜，也未把相同网名当作组内已经电气导通的证据。

运行：

```bash
go test ./pkg/pcbsolve -run 'TestMultiGroupClearanceScenario|TestFileCases/multi-group-clearance' -count=1 -v
```

检查顺序：

1. 原板机械合法；禁止移动时路由失败，记录到具体 M1 焊盘与被拒绝线段，预算未耗尽。
2. 使用相同初始板启用自动搜索，观察路由反馈和 M1/M2 间机械碰撞，递归移动两个完整组。
3. 从结果板独立计算每组位移，将所有成员的 anchor/bbox/焊盘平移归一后与起点逐项比较；
   组内相对位置、角度、层、网络和尺寸必须保持，固定 U1/J1 和板框/规则必须保持。
4. A/B/EXT 三条声明需求共同通过 `pcbsolve.Check`；EXT 必须接到移动后的 M1.3，
   规划路径只存在于候选 reservations，不能变成 Board 的 tracks/vias。
5. 将 dependent 锁定后，声明搜索范围内不能产生成功候选；删除 EXT 需求必须报外部连接缺失。

2026-09-24 离线运行通过：总状态 377、布局状态 121、路由尝试 21、机械拒绝记录 139；
结果移动 2 组，总位移 120，3 条需求，新增过孔 0，`exhausted=false`。
这些数值是本次观测，测试只验独立边界，不将具体位移或搜索顺序写成答案。
`searchComplete=false`：达到一个合法候选即停止，未证明最优或穷尽所有布局。

状态：`offline-verified`。没有现场 Apply、保存重载或 DRC 证据；没有验证真实模块的
电气归属、组内局部铜随动、多组同时旋转、任意形状封装与大板规模。
若失败，保存完整 report，区分输入缺测、路径阻挡、机械限制和预算，再修参数/算法；
不可通过给定成功偏移或删除外部需求将场景改成通过。
