---
name: PCB 算法贡献 / PCB algorithm contribution
about: 用可复现的数学问题、反例或基准改进公共布局布线包
title: "feat(pcb): "
labels: ''
assignees: ''
---

<!-- 可以直接向 dev 提 PR，不要求先开 Issue 获准。
贡献入口：docs/pcb-solver-community-design.md
详细任务：docs/pcb-solver-community-design-detail.md
本地检查：make pcb-solver-test（只需 Go，无需 EasyEDA）
-->

### 要解决的问题

<!-- 可选择 A1 独立精确参照、A2 同层替代路线、A3 拥塞感知布局、A4 多层协调，或描述一个新反例。
说清输入、待求变量、硬约束和想改善的指标；说明是否只改测试/算法，还是涉及宿主适配。
-->

### 可复现输入与独立期望

<!-- 附公共 pcbmodel.Board、pcbsolve.Request、参数/单位和来源许可；移除私有客户信息。
也可先给最小几何示意，再补机器输入。已有案例在 pkg/pcbsolve/testdata/。
期望用合法性、已知 witness、指标或明确有限模型的证明描述，避免把当前输出坐标当标准答案。
-->

### 当前结果与改进建议

<!-- 基线 commit、实际命令、预算、found/pass/incomplete/error、输入哈希和原始诊断。
有限搜索失败不证明全局无解；替代算法或数学模型可以在这里解释。
-->

### 如何验收

<!-- 独立 Check、必须保持的正反例、同预算前后比较、状态/耗时/内存、随机种子。
离线成功与真实 EDA 写入/保存重载/DRC 分别记录；算法贡献不要求安装 EDA。
-->
