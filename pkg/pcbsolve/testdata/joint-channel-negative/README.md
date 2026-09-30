# joint-channel-negative

- 来源：两条正交单网各自可达、共同路径必相交的人工反例。
- 开始状态：四个端点固定，TOP-only，`maxDetour=0` 把两条路径限制在各自直线上。
- 参数：mil；4 mil 线宽；5 mil 线间距；无过孔。
- 独立期望：不能同时选择两条路径，报告 `incomplete` 且无候选。
- 失败修法：修共同候选占用或回溯，禁止把两次单网成功拼成联合成功。
- 验证状态：`offline-verified`。
