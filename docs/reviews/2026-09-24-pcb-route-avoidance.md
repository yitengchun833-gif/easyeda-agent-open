# 组合移动与路线避让开发验证

基线 `2d43d4b`，用户要求判断组合移动方案能否达成路线避让，成立后继续开发。结论是架构
成立：固定布局共同试布 → 路径与排网回溯 → 实际冲突 → 完整组平移/旋转 → 全部声明连接
重算 → 独立复验。只有完整移动动作、没有真实共同通道检查时，不足以证明布局可布通。

## 本轮修复与红绿证据

### 固定器件的路线避让

四个固定单焊盘器件；A 横向跨越板内，B 纵向短连接。A/B 单独直连都通过，但同时直连
相交。独立手工共同路径 witness 证明 A 绕 B 的端部可行，不要求 solver 复制这些坐标。
原 solver 每层只提供一个候选，MRV 平局选择 A 先布后无法让 B 通过；118 状态后
`incomplete`，未耗尽预算。只互换 demand ID 就成功，暴露了次序依赖。

先新增真实几何回归：原输入和输入重排失败，重命名通过；然后修改公共 `SolveJoint`，
保持 MRV/ID 优先，在当前网络所有候选失败后尝试其他未完成网络先布。原网络仍调用原来的
路径求解器，只是障碍上下文换成已选共同路径；所有回溯共用预算、取消和状态去重。
修复后三种输入均通过独立 Check，固定器件不动、0 新过孔。相交直线 witness 仍被拒绝。

输入、独立期望、witness 与命令见
[fixed-route-order](../../pkg/pcbsolve/testdata/fixed-route-order/README.md)。不声称已经支持
所有同层候选路线的完整枚举；多网次序搜索也可能先耗尽预算。

### 路由反馈后的完整组旋转

原实现仅在初始候选中枚举旋转，反馈邻域只改 DX/DY。若原地旋转机械碰撞而被淘汰，之后
即使平移腾出了空间，也不会再次尝试该朝向。先加入两个失败回归：公共邻域缺目标状态；
实际双网场景在允许平移/90°转角时仍 `incomplete`。

修复后，邻域在当前位置和有限相邻位置探索全部声明转角。每个投影都从原板 anchor 重建，
完整成员与内部铜几何同步，仍受固定轴、允许转角、最大距离和总预算约束。两个回归转绿；
禁止旋转时依然无法打开该场景的通道，固定对象保持，最终双网独立检查通过。

对应测试：`TestFeedbackCanRotateAfterMovingAwayFromFixedObject`、
`TestRouteAvoidanceNeedsTranslationThenWholeGroupRotation`。它们证明有限声明范围内可行，
不给连续布局空间的最优或无解证明。

### 两个已有相对布局的组递归让位

初始没有器件重叠，但实际 TOP 路线撞 M1；M1/C1 整组让位又影响 M2/C2。求解仅收到
10mil 步长、移动半径和固定轴，没有成功偏移。最终两个完整组保持内部几何，三条需求
共同通过，BOTTOM 外部连接终点随 M1 移动重新计算；固定器件、板框、规则与铜清单保持。
固定第二组、遗漏外部连接都不能误报通过。C1 两异网 pad 宽度为 4mil，间隙 6mil，避免
合成输入夹带不相关的异网 pad 接触。

输入和独立要求见
[multi-group-clearance](../../pkg/pcbsolve/testdata/multi-group-clearance/README.md)。该场景
限制 `maxDetour=0`，用于隔离“线路受阻后组让位”的行为；不证明任意绕行都失败，也不证明
未声明 X/Z 内部网络已导通。真实带铜电源块、整板完整需求和现场持久化仍待验收。

## 预览与复现

预览检查发现请求中的机械禁放区没有绘出，递归移动原因被两行上限截断。新增失败断言后，
solve/check 的整板与局部 SVG 加入独立请求禁放区，完整原因按实际行数扩展标题空间；几何
坐标不因标题变化而改变。独立 `render` 增加可选 `--from request.json`，核对板/请求与候选
来源后显示同一禁放区；不同请求必须失败并保留先前预览。无 `--from` 的旧调用保持兼容。

```bash
go test ./pkg/pcblayout ./pkg/pcbrouting ./pkg/pcbsolve -count=1
EASYEDA_PCB_SOLVE_ARTIFACT_DIR=/tmp/pcb-route-avoidance \
  go test ./internal/app -run '^TestPCB(AutomaticChannel|MultiGroupClearance|FixedRouteOrder)Artifacts$' -count=1 -v
```

预览与报告保存在上述本地目录，参数、独立要求和测试源码在版本库，可重新生成。规划预留
只存在于候选数据，不写实际铜。本轮未访问/修改现场 EDA，没有新增现场清理动作。

## 验证记录与限制

最终 `go test ./... -count=1`：当前工作树 **3612 项 / 17 包通过**；
`make layout-calibrate`、`make skill-check`、`make agent-check`、`git diff --check` 通过。
新/旧三个 artifact 场景和 CLI solve/check/render、篡改候选负例均通过；SVG 已 XML 解析，
并完整栅格化观察固定路线及双组最终图。

| 场景 | 观测（本次资源配置） |
|---|---|
| 固定布局路线回溯 | 798 总状态、1 个布局尝试、0 移动；双网共同通过 |
| 双组递归让位 | 377 总状态、21 个路由尝试、两组总位移 120mil、三条共同连接、0 新过孔 |
| 原自动窄口通道回归 | 5117 总状态、3 个路由尝试、移动 10mil；共同检查通过 |

顺序回溯增加搜索工作：同配置的原自动窄口例从 3946 增至 5117 状态，换取对先占通道的
路径重新求解。该代价受原共享预算限制，不称性能已改善，也不是大板性能承诺。
文件场景按要求找到首个候选就停止时，`searchComplete=false` 可以与合法候选同时成立。

其他原理图/daemon 工作树改动保留，以上总数不能冒充本提交隔离检出的数量。新增测试
属于离线开发验证，不替代 ESP32 原始需求完整回归和连续两轮现场 save/reload/readback。
