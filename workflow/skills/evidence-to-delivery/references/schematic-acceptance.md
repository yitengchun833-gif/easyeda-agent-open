# 原理图任务检查与交付衔接

本页只负责业务流程接线；具体谓词、支持范围与恢复条件以当前同包 `easyeda-agent-open` Skill 和 Open MCP/Go 实现为唯一来源，不复制规则引擎或阶段状态。

- 普通新模块由同包 prepare 自动标记任务类型；非空页用完整快照、空闲 region 和独立源测量保护既有工程。局部返工使用 snapshot-only prepare 的 `taskKind: local_edit`，不要为新任务手写遗漏任务类型的旧plan。
- 消费生成plan/job及requirements清单，不手工删自动项或改成warn；按回执区分pass/fail/unknown、未适用、未支持、未执行。布局质量使用原Go谓词；功能信号流与文字阅读仍看本次真实图面。
- 中断先用reconcile只读核对原回执和目标；只有已证完成前缀才resume未启动后缀。目标当前满足不是未知动作成功的证据；pending/损坏/unknown写入、未知保存继续阻断。
- final消费独立网表目标、原生DRC、完整设计diff、布局质量和范围外保护结果。局部与整页分开；任何必要未知不得标verified。
- 实际审图后finish，引用原始delivery和自动摘要。保存前快照变化会阻断保存；saved:true仍只证明保存回执。
- 正式保存持久性要求已有授权窗口的真实保存→关闭→重开→新鲜回读，再用verify_delivery消费原生close/open请求ID并验证同一目标/顺序/设计diff。该接口本身不执行关闭重开或保存。无授权或缺证据记待验，不假造验收。
- verify_delivery可输出独立派生核验结果，不重写原delivery或第二套总流程状态。page/module/local范围按真实回执消费；engineer acceptance由工程师终审。local_display无final兼容路径不可冒充正式整页持久性签收。

结构化业务项目仍仅由workflow.py维护既有.workflow/state.json；将上述实际文件/检查结果挂到现有模块执行证据与required_checks。手工PWF短记同一产物、源commit、必要检查及未测事项，不重复手拼验收报告。

业务 `result` 用 `open_receipt` 指向同一计划最后一个成功 final 阶段，用 `open_persistence` 指向 `verify_delivery(outputFile:...)` 保存的原始 `easyeda.delivery-verification/1` JSON。工作流核对计划、资料、阶段、delivery、run 和图像的哈希链，消费原生核验结果并自动生成 `persistence` 与 `visual_review` 检查，无需再制作旧式 `workflowSave` 报告。`required_checks` 保留 `open_stage_receipt`、`open_target_check`、`open_requirements`、`visual_review`、`persistence`；已有其他必要检查仍须通过。page、module、local 仅在各自范围内完成，工程师终审保持待确认。

上述导入只读取现有证据，不保存、不关闭重开、不重试 EDA 动作。缺少原生核验、保存未知、阶段或来源漂移、设计差异未同步、DRC 未通过均拒绝 verified。旧 `local_display` 的 `easyeda.persistence/1` 与原有 page 验收入口保留。

源码实现、临时离线构建、分发包、安装副本、当前宿主加载、真实EDA验收必须分开记录。只在明确采用任务中同步MCP/Go/Connector/Skill；本升级不自动采用或重启服务。
