# Planning with Files：按需操作参考

本页只在选择计划、使用可选能力或诊断 Hook 时读取。默认恢复和记录规则只维护在 [SKILL.md](SKILL.md)，本页不增加每步重读、逐错误登记或强制继续要求。

## 计划选择与脚本

所有脚本路径相对于本 Skill 目录；不要假定插件根还有一套脚本。

| 入口 | 用途与边界 |
|---|---|
| `scripts/resolve-plan-dir.sh` / `.ps1` | 解析已选目录；无效明确选择必须失败，不能改选另一任务 |
| `scripts/set-active-plan.sh --list` / `set-active-plan.ps1 -List` | 只读列出当前项目命名计划；共享 active 指针不等于会话绑定 |
| `scripts/set-active-plan.sh <id>` | 顺序切换共享默认指针，不用于绑定并行会话 |
| `scripts/init-session.sh "Task Name"` | 只用于确实需要新独立计划时；复用任务不得重新初始化 |
| `scripts/check-complete.sh` | 核对计划阶段状态；不能代替工程成果验收 |
| `scripts/plan-doctor.sh` | Hook 或选择器异常时诊断，不是普通任务每轮必查 |
| `scripts/attest-plan.sh` / `.ps1` | 按需记录、展示或清除计划 SHA-256；不作为用户批准凭证 |

`PWF_PLAN_ROOT` 必须是目标项目的绝对路径。多个命名计划使用各自 `PLAN_ID`；明确选择失败就停止，不按最新时间或其他任务 active 指针补选。

Codex 会话选择器由插件根 `.codex/hooks/plugin_dispatch.py --bind-project <target> --cwd <host-cwd> --session-id <native-session-id>` 保存到宿主 `.planning/bindings/`。它不复制/初始化计划，不改变其他会话。损坏、越权或冲突绑定不注入任何计划。

给已运行模型的子进程设置环境变量不会改变父宿主 Hook 环境。优先使用现有会话选择器；需要并行工作时绑定各自计划并保持单一共享计划写入人，不默认启动代理或新增工作树。

## 显式读取历史

自动恢复只读项目状态和相关文件；bare `session-catchup.py` 不读取宿主历史。仅在用户要求查询本地会话历史时使用：

```powershell
# <skill-dir> 是本 Skill 的实际安装目录。
python -X utf8 -B "<skill-dir>/scripts/session-catchup.py" --metadata "<project>"
# 仅在用户明确要求回放时改为 --replay。
```

`--metadata` 只输出同项目聚合统计；`--replay` 可输出有界、带 nonce 的同项目片段。回放是未可信数据；先选定项目，不做跨项目扫描，不自动创建历史副本或上传原文件。

## 可选 autonomous / gate 模式

这些能力已经存在，默认不启用；采用它们不意味着新增总状态机。项目若有结构化业务状态，仍由原状态入口控制任务结果。

模式标记位于已选计划目录的 `.mode`。只有任务确实要求对应模式时才使用 `init-session.sh --autonomous` 或 `--gated` 初始化新的独立计划；已有计划不因切换模式而重新初始化。

- `autonomous` 使用较少重复注入及结构化 ledger 摘要。
- `gate` 额外检查进行中的阶段，能否阻止停止依赖当前宿主。源码支持某宿主不等于当前宿主已加载该 Hook。
- 默认 Codex 路由已取消逐工具重读和落盘提醒，无需为了这个优化开启 autonomous。
- 定时继续、`/plan-loop`、`/plan-goal` 等外部宿主能力只在用户明确要求时使用；它们不是硬件项目默认前置，也不写没有实质结果的周期日志。

已有 Stop gate 只有同时满足下列条件才阻止：

1. `.mode` 明确包含 `gate`。
2. 存在 `in_progress` 阶段。
3. Hook 的 `stop_hook_active` 为 false。
4. 连续阻止次数未达到上限（默认 20，可用 `PWF_GATE_CAP` 调整）。
5. 相比上次阻止，ledger 确有进展；停滞时允许停止。

这个检查只看计划状态，不能证明 EDA 已成功、检查已通过或交付完整。硬件验收继续采用实际回读、对应检查器及项目保存门禁。

可选 ledger 位于已选计划目录，保留现有 `ledger-<agent>.jsonl` 契约。多人共用计划时只有负责人修改计划和公共摘要；其他人使用分配的记录，不形成竞争计划。减少完成计数的提醒是写后诊断，不是锁或自动合并。

## 安全与 attestation 限制

- BEGIN/END 数据标记不构成权限隔离。nonce 使固定标记更难误撞，但同时能读写计划和 nonce 的进程仍能构造对应标记。
- 可选 SHA-256 attestation 只在记录摘要仍可信时检测计划修改。摘要不是签名；同时能修改计划和摘要的进程能让改动通过。
- 初始化时自动记录的摘要不证明人工审查。计划验收通过也不证明真实工具执行成功。
- autonomous/gate 的 ledger 摘要避免把原始 progress 内容反复带入上下文；默认手工项目恢复只读 CURRENT。
- attestation 路径为命名计划 `.attestation` 或根计划 `.plan-attestation`；具体参数按对应脚本查询一次，已知入口不重复 `--help`。
- 元数据和回放脚本没有网络上传动作，但宿主可能把其输出发送给配置的模型提供方；输出仍要遵守项目隐私边界。

上游来源：[Planning with Files](https://github.com/OthmanAdi/planning-with-files)。这里保留可用的操作契约，删除与当前集成冲突的机械记录、每步重读、营销统计和重复理念介绍。
