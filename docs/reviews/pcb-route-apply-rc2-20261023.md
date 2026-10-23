# PCB route apply：rc.2 独立候选

本轮把已有离线路径候选接到原生落铜与新鲜回读。基于 rc.1 提交 `8a7d35a3de7f07a70720b25b8b8a72dd3a79e21a`，核心版本为 `1.8.1-open.9.7-rc.2`。工程师仍决定电路与布线约束。

`pcb route apply --board board.json --from request.json --plan report.json --window WINDOW --project PROJECT --doc PCB --out new-receipt.json` 消费既有 solve/check 数据。先核来源、当前身份和几何，按段添加铜并回读，核对原对象、实际连通性和原生 DRC。`--dry-run` 仍读取当前目标，但不落铜。输出文件必须新建，超时、部分完成或未知结果不能盲重放。

范围限定为单网、TOP/BOTTOM 单层、零新过孔，仅增加铜；不移动或删除已有对象，也不显式保存。候选落铜、原生 DRC、保存持久性分别报告，`persistence: unverified` 不表示保存重开验收通过。

互斥锁只协调同一机器、共享同一临时目录和锁协议的 route apply 进程。它不约束 GUI、其他命令、不同临时目录或其他主机。双快照用来发现读取间漂移，不构成全局原子事务；外部编辑仍可能发生，必须保留实际部分结果和错误。

来源为 `fadc12c31e087ba0ca02c52925d409a43c45cc6d` 和 `88615c79f508f27483d545d3355149904a2887a4`，公开映射见根目录 PUBLISH-PROVENANCE.json。第二提交补齐同机跨进程互斥、每段覆盖回读及全部内部快照的目标绑定。没有导入私有 Git 历史或运行数据。

Connector 源码未变，复用 `1.8.1-open.9.7-rc.1-kb.33` EEXT，SHA-256 为 `921be9ee29be13b053fa49c26a51ac94d07d59e15b7dca191471d6df4f0385c7`。已安装 kb.33 无需再次导入；新功能由 rc.2 CLI 提供，单独安装 EEXT 不会升级 CLI。rc.1 包保持不变，切回原 rc.1 CLI 即可回退程序版本；回退程序不会撤销已落铜内容。

本候选尚未在真实 EDA 执行 route apply，也未安装或替换常驻后台。构建与定向回归结果在候选交付说明中单列，不能作为实机稳定性证明。

定向回归：`go test ./internal/app -run '^TestPCBRoute' -count=1 -json` 通过，15 个顶层测试（含 1 个锁子进程辅助入口）、49 个子场景，包执行 2.163 秒。覆盖新增 apply、跨进程锁和原有 solve/check；使用模拟原生 API，不是实机验收。CLI 父命令描述同时更新，solve/check 保持离线。
