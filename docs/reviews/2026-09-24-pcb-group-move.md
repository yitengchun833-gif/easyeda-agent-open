# LG-01 / LG-02 五成员组合移动离线验证

来源：2026-09-23 用户要求保留已有相对布局，先列开发验证场景，再逐项完成。
待办入口为 [PCB 开发验证清单](../pcb-layout-validation.md)，可迁移输入与实际命令为
[公开样例](../../.agents/skills/easyeda-agent/references/examples/pcb-group-move/README.md)。

## 版本与范围

基于 `dbbf93b` 加本轮文档、公开合成输入与 `pcb_group_move_scenario_test.go`。
本轮没有修改生产算法、daemon、connector，没有现场写入。工作树同时包含其他未提交的
原理图/daemon 工作；完整工作树测试结果不能称为只有本提交的隔离验证。

输入为七个虚构器件的已布局快照：五成员组和两个固定对象，二层、无铜，U1 anchor 非
bbox 中心。偏移是输入要求，不验证自动让位搜索。后续现场仍要替换为新鲜实测几何与身份。

原始文件 SHA256（由实际 CLI manifest 记录）：

- board：`549c62c1605952f34c0552c090c746f8b85989a6838b43a35bff5942f4b410b2`
- layout：`eaef2d24debf4ace78879cd1ee186482aa7f49026810ee2c33a85a19031730b7`

## 实际执行与观测

```bash
bin/easyeda pcb layout-plan \
  --board .agents/skills/easyeda-agent/references/examples/pcb-group-move/board.json \
  --from .agents/skills/easyeda-agent/references/examples/pcb-group-move/layout.json \
  --module established-group --candidates 3 --out /tmp/pcb-group-move
go test ./internal/app -run '^TestPCBEstablishedGroupScenario' -count=1 -v
go test ./pkg/pcbmodel ./pkg/pcblayout ./pkg/pcbrouting ./pkg/pcbsolve -count=1
```

- 生成两个候选：整体偏移 `(150,100)mil`，分别旋转 0°/90°；0 个拒绝。
- 自动检查分别核对 CLI 候选与公共组变换；独立 quarter-turn 公式从原始输入计算期望，
  不调用生产变换生成答案。五个成员、全部 pad 身份/网络/坐标、非对称 bbox、角度与装配面
  符合要求，两个固定对象保持，输入未改变；Apply 与候选一致且恰好五个 modify 加 save。
- 最小组内 bbox 间隙均为 20mil；所属焊盘距离在两个候选之间不变。几何不同的候选允许
  具有不同的外部间隙，不要求相同位置或唯一答案。
- 负例删除输入中的 C2 及其 pin assignment 后仍几何可行；独立五成员表拒绝生成的四成员
  候选。证明业务所有权需要独立输入，不宣称工具已自动识别所有未声明业务成员。
- 首个场景回归 2 项通过；四个公共包回归 62 项通过。
- 原布局（相同输入改偏移为 0、旋转为 0）、平移、旋转三份 SVG 已完整渲染观察。旧
  `layout-plan` 图仅显示器件 bbox，真实 pad/丝印仍靠数据检查；该渲染边界单列为待补项。

本地临时产物是 `/tmp/pcb-group-move` 与 `/tmp/pcb-group-move-baseline`，可按公开参数重建。
未将临时绝对目录当长期证据依赖；输入与独立要求在版本库保存。

本轮最终检查：`go test ./... -count=1` 为当前工作树 **3596 项 / 17 包通过**；
`make layout-calibrate`、`make skill-check`、`make agent-check` 和 `git diff --check` 通过。
该总数包含其他未提交工作的测试，不作为本提交单独检出的测试数量。没有生产检查内核改动，
本轮没有新增现场端到端结论。

## 未覆盖

本例为 `offline-verified`。未执行 typed Apply、两轮现场自检、保存重载、DRC 前后差异或
测试对象清理；未验证内部铜移动、外部连接、多个功能块的完整联合求解、位号/绑定以及
完整 ESP32 原始需求回归。对应待办继续保持未完成，旧双网通道实证不替代它们。
