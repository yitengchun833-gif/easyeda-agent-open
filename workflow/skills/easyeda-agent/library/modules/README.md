# Reusable Lib Modules

`catalog.json` is the public, sanitized registry. Records begin as evidence-backed candidates and
advance only when their topology, EasyEDA identities and measured geometry are independently proven.

Run `python3 ../../scripts/modules-audit.py`. See
[`../../references/reusable-module-library.md`](../../references/reusable-module-library.md) for the
data boundary and contribution workflow.

`ams1117-3v3.layout-input.json` is the first executable asset. It contains independently verified
part identities and official-API symbol measurements; run it through `sch lib-layout`, then pass the
generated JSON to `sch compose`. Its PCB and hardware maturity remain explicitly unverified.

The eleven `*.topology.json` files are independent role-based modules extracted directly from a completed
live schematic. They are deliberately split so the public library does not retain an original whole-board
mapping. Project identity, original designators and anonymous source-net names were removed; 32-character
Device UUIDs, every physical pin and connected/unconnected state were kept. They are `topology_ready`, not
`compose_ready`, because the editor timed out when all pin coordinates were requested with geometry in one call.

This directory intentionally contains no copied training PDFs, BOM rows, board names, scoring text,
absolute board coordinates or per-board mappings.

## 单词条离线检索与复用

当前只有 `lib.ldo_3v3` 接入可运行试点。目录中的 `knowledge` 链接到
[机器可读词条](ldo-3v3.knowledge.json)，保留原有资产和成熟度。别名仅在 catalog.json 维护。
从仓库根目录运行：

```powershell
python -B skills/easyeda-agent/scripts/module_reuse.py skills/easyeda-agent/library/modules/ldo-3v3.request.example.json
python -B skills/easyeda-agent/scripts/test_module_reuse.py
```

[需求样例](ldo-3v3.request.example.json) 是合成输入，不是实际工程或器件额定条件。
传入 query（模块 ID、标题片段或别名）及既有 target profile 格式；事实用 value 和
provenance.artifact/locator 表达。字段名中的 `_v`、`_a`、`_c` 分别表示 V、A、摄氏度，
不接受带单位字符串替代数值。调用方须先核对真实资料，出处字段存在不证明内容真实。

| 输入 | 输出 |
|---|---|
| 型号、输出与词条一致，需求工况及出处齐全 | candidate，可复用草案，电气仍 unknown |
| 已知工况变化且未触及词条排除条件 | 草案带 requested_parameters、differences 及待复核检查 |
| 缺少、未知或无出处的必要条件 | needs_input，需要补信息，不给执行草案 |
| 已知型号或输出与词条冲突 | excluded，不适用；其他条件缺失也不改变已知冲突 |

candidate 只表示**草案复用条件**匹配。当前没有经手册核实的输入、负载、环境数值阈值，
不会计算允许最大电流或宣称热/稳定性通过；明确越界指超出该词条资产范围，例如用固定
3.3V 模块请求 5V 输出。超出目录知识的数值工况仍需人工审查。
草案包含原资产的器件/网络、需求参数、来源定位与哈希、修订号、差异及 required_checks，
不改原资产、不生成 Apply 动作，也不绑定真实工程。无匹配、多候选、尚未接入的模块均要求补信息。

工程师审查后修改同一词条的 review_notes/required_checks/适用条件并更新 revision，
下一次运行重新读盘并输出 knowledge_sha256，不依赖进程缓存。原资料、工况和硬件验证
依然分别核对，修改词条不会自动升级成熟度。`verification` 输出保守保持 pending/unknown。
测试在隔离临时副本中模拟修订，两次独立进程确认第二次采用新说明、新检查项和相同原资产；
不把模拟反馈当成工程师真实批准。当前 Jev=0，PCB 与实物验证未执行。
