# 原理图工具调用审计与 gate 验证（2026-08）

本页仅保存 2026-08-03/04 的统计与现场结果，不代表当前命令数量或失败率。
原提案中的三档命令收敛计划已移除；当前能力见 [FEATURES.md](../FEATURES.md)，
当前设计流程见 [架构](../architecture.md)。原始提案可从 Git 历史追溯。


| 指标 | 数值 |
|---|---|
| `sch` 唯一子命令数 | **42** |
| 其中在当时的 `skills/` 目录 **0 引用** | **12**(`align` `distribute` `connect` `wire` `netflag` `delete` `list` `modify` `select` `set` `open` `pages`) |
| 连接器 `extension/src/actions.ts` | **8308 行单文件 / 88 handler**,测试 786 行 |
| Go `internal/app` | 45781 行 / 66 测试文件 / 449 用例 |
| 唯一端到端回归基准 | `esp32MiniRequire.md`,**42 分钟人工真机** |


## 调用日志证据


`~/.easyeda-agent/audit/*.jsonl`,**76121 条真实 dispatch,2026-06-25 → 07-30,33 天**。
原理图侧 **28 个 typed action / 31155 次调用**(CLI 却暴露 42 个子命令)。

**调用极度集中在头部:**

| 档 | action | 调用占比 | 失败率 |
|---|---|---|---|
| 头部 8 | `connect_pin`(13128) `pages.list` `component.place` `components.list` `component.modify` `save` `check` `read` | **93.3%** | **2.5%** |
| 长尾 15 | `primitives.delete` `drc.check` `titleblock.get` `wire.create` `page.create` `titleblock.modify` `library.get_by_lcsc` `export.bom` `page.delete` `page.rename` `component.delete` `export.netlist` `pin.set_no_connect` `page.open` `group.move` | **1.55%**(484 次) | **12.6%** |

> **长尾的失败率是主干的 5 倍,而它只占 1.55% 的调用量。**
> 这不是「用得少所以没优化」,是「用得少所以坏了没人知道」—— 每一次调用都是一次踩雷。

**活标本:`schematic.titleblock.modify` 调用 32 次,成功 0 次。**
20 次 `EDA_CALL_FAILED: Failed to modify schematic page title block.`(2026-06-26)+
12 次 `NO_CONNECTOR`(2026-07-23)。**这个命令从未成功过一次**,却仍在 skill 文档里被引用。

其余长尾故障画像:`group.move` 60% 失败(陈旧 id,"Pull fresh ids first")·
`page.open` 28.6%(多页场景打不开)· `page.create` 23.5% · `wire.create` 17.5%
(`Failed to create wire.` 无细节)· `export.bom` 28.6%。

**错路回退实测(agent 失败后 60s 内改调他者):**

| 次数 | 回退路径 |
|---|---|
| 146 | `components.list [NO_CONNECTOR]` → `snapshot` |
| 114 | `connect_pin [EDA_CALL_FAILED]` → `components.list` |
| 46 | `connect_pin [EDA_CALL_FAILED]` → `save` |
| 39 | `connect_pin [EDA_CALL_FAILED]` → `check` |
| 38 | `connect_pin [EDA_CALL_FAILED]` → `pages.list` |

top1 的 146 次是 **agent 在连接器根本没连上时瞎试别的命令**,而不是先 `health`。
后四条同源:`connect_pin` 失败 442 次后,agent 分别改调 `components.list`/`save`/
`check`/`pages.list` —— **同一个失败,四种不同的猜法**,没有一种是被规定的。
这正是「无数据判据、只有散文描述」的代价。


## gate 实现与现场验证


### ✅ 第一刀:`sch gate`(2026-08-03 落地)

选它先行是因为**它零破坏**:纯新增聚合命令,四个单命令原样保留,却直接消灭了
「跑哪个检查、什么顺序、谁的退出码算数」这个每次都要重做且没有判据的决策。

- `internal/app/cmd_sch_gate.go` — 固定流水线 `layout-lint → check → bridge-check → drc`
  (顺序理由写在代码注释里:几何最便宜且解释力最强,DRC 最慢且需前台故垫底)
- **`blocked` verdict 是本刀的核心设计**:把「检查器没跑起来」和「板子有问题」分开。
  前述日志里 146 次 `components.list [NO_CONNECTOR] → snapshot` 的盲试,根因就是二者混同 ——
  agent 把 infra 失败当板子失败,于是去试别的命令。现在第一个 stage 报 `error` 就停,
  后续 stage 标 `skipped` 而不是继续撞同一堵墙,报告直接指向 `health` / `doc switch`。
- **每个失败 stage 自带规定的下一步**(`gateAdviceFor`)—— 修法跟着失败走,不靠 skill 散文。
- `--only`/`--skip` 拼错 stage 名**直接报错**,绝不静默少跑一关(少跑一关的绿灯比红灯更危险)。
- 复用现有 `parseCheckReport`/`parseBridgeReport`/`parseDrcReport`,只从 `runLayoutLint`
  提取了 `collectLayoutLint`(纯提取,行为不变)——**没有重写任何检查器逻辑**,回归面最小。
- 13 个单测钉住:流水线顺序、`--only` 按流水线序而非参数序、未知 stage 报错、
  三态判定(含 `blocked` 优先于 `fail`)、error stage 不产出告警、渲染带「下一步」。
- Skill 同步:`SKILL.md` 停点表 ②、`design-flow.md` S5/S6、`schematic.md`、
  `auto-layout-sop.md` 最终验证门 —— 主干路径全部改走 gate。

**✅ 真机已验(2026-08-04,web 编辑器 `pro.lceda.cn` + 官方「示例工程_快速入门」,只读)**:
默认档 **0.86s 四关全过**;DRC **没有**卡后台(窗口前台时正常返回),此前担心的
「DRC 前台要求会让 gate 默认 blocked」未发生。

**但 `--strict` 档当场暴露 3 个会误导 agent 的缺陷,已修并回归**:

| # | 缺陷 | 症状 | 修法 |
|---|---|---|---|
| 1 | layout-lint 的 summary 不含 strict 判据字段 | 报告写「0 overlap, 0 pin-coincidence, 0 tight…」却 FAIL,**自相矛盾** | summary 补 no-bbox / unchecked-pin / unproven-pin / invalid-geometry |
| 2 | `Errors` 计数与 `Status` 判据脱节 | blocker 行写「**0 个阻塞项**」却判失败(strict 失败在被提升的告警上,不在 error 计数里) | 新增 `BlockingReasons[]`,status 与 blocker 都由它决定;Errors/Warnings 降为纯展示用的严重度统计 |
| 3 | 建议按 **stage 名**给,不看实际失败项 | 0 bridge 却教「拆掉真短路」,0 overlap 却教「重排几何」 | 建议表改为按 **reason 关键词**匹配(`gateAdviceRules`) |

第 3 个最恶劣 —— **把 agent 引向不存在的问题**,正是本文档要根治的病;它在单测里没被抓到,
因为我构造的 stage 都是 `Errors>0` 才 fail,没覆盖「strict 提升告警致 fail 但 Errors=0」这个组合。
**真机验证的价值就在这里**:纯几何/纯逻辑的单测证明不了「报告读起来对不对」。

修复后同一块板:
```
• layout-lint: 34 unproven pin geometry (--strict;连接器未给 pinsAvailable 契约)
• check: 23 个 warn/info finding (--strict): wire-crossing×13, dangling-wire×8, zero-length-wire×2
• bridge-check: 11 orphan-stub (--strict)
→ 引脚几何未经证明:连接器太旧没给 pinsAvailable 契约 —— 升级连接器,或本轮去掉 `--strict`(不是电路问题)
```
新增 8 个回归测试钉住(含两条「建议不得指向板子没有的问题」的负向断言)。

**副产物**:34 个 unproven-pin 的真因是**市场版连接器 0.17.3 滞后 CLI 0.18.3**,不发
`pinsAvailable` 契约 —— 印证了 CLAUDE.md 记的市场版滞后问题,且现在 gate 会**直接说出来**
而不是让 agent 去挪器件。
