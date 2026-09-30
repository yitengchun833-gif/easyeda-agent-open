# two-layer-via

- 来源：TOP 被固定异网铜墙完全切断的人工二层案例。
- 开始状态：端点均为 TOP pad；BOTTOM 空闲；规则给出 16/8 mil 过孔。
- 参数：mil；允许 TOP/BOTTOM；网络预算最多两个过孔。
- 独立期望：使用恰好两个通孔形成 TOP/BOTTOM/TOP 路径；TOP-only 变体必须失败。
- 失败修法：检查过孔站点、跨层连续性和独立复验，不能忽略铜墙。
- 验证状态：`offline-verified`。
