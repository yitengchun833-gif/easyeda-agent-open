# 2026-07-16 新器件调研存档(CAN / WS2812B / I2C EEPROM / 蜂鸣器)

## 这份文档是什么

四颗新器件的**一手源调研结果存档**,不是 block。当天原计划把它们做成四个新 block,
在读到 `chat/2026-07-16-blocks-data-model.md` 后**中止**——该文档的暂停清单明确包含
「把更多 PCB/手册知识继续拆字段、扩 schema」,而当务之急是先用 `block-apply` 证明
数据模型的价值。

调研已完成且全部落在原厂 PDF / 官方 API 上,**丢掉是纯浪费**,故存档于此。
将来若 block-apply 实验成功、确需扩库,直接从这里取值,不必重跑。

> **状态:未落地。** 以下器件**均未**进 `standard-parts.json`(没有 deviceUuid),
> 也**没有**对应 block。C 号与 libraryType 为 2026-07-16 实时查询值,会漂移。

---

## 0. 跨器件的共同结论(最贵的一条)

**立创基础库里没有 CAN 收发器、没有蜂鸣器、没有 I2C EEPROM、没有 WS2812。全是扩展料。**

三个 agent 各自独立查 JLCPCB parts API(`componentLibraryType`: `base`=基础库 /
`expand`=扩展库,并用已知基础料 C1525/C25804/C14663 做对照验证编码),结论一致。
→ **「优先基础库」这条约束在这四类功能上不可满足**,一次性扩展料工程费省不掉,
选型只能按工程优劣判断。

**顺带发现的仓库 bug**:`standard-parts.json` 里 `tvs.sm712_sot23`(C32677)标了
`"basic": false`,但 API 说它是 `base`(**基础库**,库存 23 万+)。标反方向了——
把便宜料记成了贵料。**未修**(与当日任务无关,单独处理)。

---

## 1. CAN 收发器

**推荐:TJA1051T/3(C38695,扩展,SOIC-8,库存 15.5 万)**

### 决定性事实:总线耐压
本项目是 **12–24V 车载**节点,这一条直接淘汰候选:

| 器件 | 总线脚耐压 | 结论 |
|---|---|---|
| SN65HVD230 | **−4 ~ +16V**(SLOS346O §8.1 Abs Max) | ❌ 24V 短到电瓶即毁;12V 系统对 13.8V 充电电压也零余量 |
| TJA1051 | **−58 ~ +58V**(Rev.7 Table 5) | ✅ 两种系统都扛短到电瓶 |

### 3.3V vs 5V 供电之争(原始疑问的正解)
两者都符合 ISO 11898-2 显性差分电平(1.5–3.0V @60Ω),能互通。真正的分野是
**隐性共模电平**:SN65HVD230 偏置到 VCC/2≈**1.65V**,而车上每个 OEM 节点都是 5V 供电、
偏置在 **~2.5V**。3.3V 收发器挂上车 = 与总线共模错位,每次跳变产生共模阶跃 → EMI 恶化。
**TJA1051T/3 = 5V 供电给出匹配车规的总线电平 + 独立 VIO 脚吃 3.3V 逻辑**,正是双轨板该用的。

**TJA1050 不可用于 3.3V 逻辑**:pin5 是 Vref 不是 VIO;TXD 方向能吃(VIH min 2.0V 固定),
但 **RXD 是 VCC 参考的推挽输出**,会往 3.3V GPIO 灌 ~5V。

### 关键topology(NXP TR1014 应用提示,Fig 19 就是"TJA1051/3 + 3V 单片机"这一模式)
- VCC 去耦 **47nF~100nF**(§7.1.1)
- **VIO 去耦 100nF 接到 VCC,不是接 GND**(§8.9 原文:『Decouple the VIO pin by a capacitor
  **to VCC (instead GND)**… to achieve a high-frequent short of the supplies and thus
  improve the electromagnetic immunity』)—— 反直觉且关键
- **S 脚(pin8)**:低=Normal,高=Silent;内部下拉,浮空即 Normal;可接 GPIO。
  建议仍贴 10k 下拉(ESP32 GPIO 复位期高阻),且**别放 strapping 脚**
- **终端**:末端节点 120Ω 或**分裂终端** 2×60Ω + 中点电容 **4.7nF~47nF** 到地(TR1014 §8.5,
  Fig 19 用 4.7nF);NXP 称分裂终端「highly recommended」
- **TXD 内部已上拉到 VIO**,无需外部;TXD 显性超时 `tto(dom)` 0.3/1/5ms → **最低速率 40kbit/s**

### 🚩 设计判断:终端默认不贴
ESP32 设备插进**既有车辆总线 = stub 节点**,车上已有两个 120Ω 末端终端。
再贴 120Ω → 60Ω‖120Ω = **40Ω,直接破坏总线**。
→ 终端做成 **2×60Ω + 4.7nF 的跳线/DNP 位,默认不贴**。

### 🚩 SM712 是错的 TVS(推翻原设想)
仓库已有 `tvs.sm712_sot23`,但它 **专为 RS-485 设计**(PSM712 datasheet 05094.R13,
Applications 列表无 CAN),两处硬伤:
1. **标称电压太低**:VWM **12.0V** / V(BR) 13.3V —— 12V 车充电时 13.8~14.4V,**它会直接导通**
2. **75pF/线 吃爆容量预算**:NXP TR1014 §8.2 要求 500kbit/s 下总线电容『lower than 100pF』

→ CAN 应选 **NUP2105LT1G(C14486,24V 标称,350W,$0.04,库存 37.8 万)**;
容量紧张时用 **PESD1CAN(C15771,Cd 典型 11pF)**。

### 共模电感
**「51R/100R @100MHz」的单位是错的** —— CAN 共模电感按 **µH** 标称。
TDK ACT1210 表:`-510-`=**51µH**、`-101-`=100µH,阻抗在 **10MHz** 标定。
选 **ACT1210-510-2P-TL00(C95572)**,关键参数 **杂散电感 0.09µH**
(TR1014 §8.1 要求 stray inductance **<500nH**)。

### 未确认
- TJA1051 读的是 **Rev.7(2015)**;NXP 官网 PDF 已 404,检索显示存在 **Rev.9(2017)**
  (据称补 ISO 11898-2:2016 / SAE J2284-5),**未取到**。冻结 block 前须对 Rev.9 复核。
- VIO 电容 100nF 是 Fig 19 的「e.g.」示例值,非规格;只有 VCC 有 47–100nF 明确区间。

---

## 2. WS2812B

**推荐:WS2812B-V5/W(C2874885,扩展,SMD5050-4P,库存 40.7 万)**

### 核心结论:VIH 是**文档修订级**属性,不是型号级属性
这是全场最反直觉的一条 —— 同叫「WS2812B」的几份 datasheet,VIH 定义**互不相同**:

| 型号 / 文档 | VIH min(原文) | @VDD=5.0V | 3.3V 能否驱动 |
|---|---|---|---|
| WS2812B 经典 | `0.7VDD` | **3.50V** | ❌ **差 0.20V,超规格** |
| WS2812B-V5(文档 V1.0) | `2.7V` 固定 | 2.70V | ✅ +0.60V |
| **WS2812B-V5/W(文档 V6.0 = LCSC 那颗)** | `0.63VDD` | **3.15V** | ✅ +0.15V(薄) |
| WS2812C-2020 | `2.7V` 固定 | 2.70V | ✅ +0.60V(**但引脚定义不同**) |
| SK6812 | `3.4V` **典型值,无 min**| 3.40V | ❌ 连典型值都不够 |

→ **经典 WS2812B 必须加电平转换;V5 变体可直驱,但 5.25V(USB 容差)时余量归零**。
→ 建议:选 V5/W + **电平转换器做成 DNP 位**(贴 74AHCT1G125 或短接 0Ω 二选一)。

**worst-case 诚实说明**:ESP32-S3 datasheet Table 5-4 保证 `VOH min 0.8×VDD = 2.64V`,
严格按两份 datasheet 对打,**连 V5 都不达标**。实际能用是因为 WS2812B 输入只吃 `II=±1µA`,
远不到 2.64V 标定的 40mA 条件下 —— 即**所有 3.3V 直驱设计都依赖 typical 而非 guaranteed**。

### 引脚定义陷阱(可重用块的大坑)
- WS2812B 与 WS2812B-V5:`1=VDD, 2=DOUT, 3=VSS, 4=DIN` ✅ 相同
- **SK6812**:`1=VSS, 2=DIN, 3=VDD, 4=DOUT` ❌ 完全不同
- **WS2812C-2020**:`1=DO, 2=GND, 3=DI, 4=VDD` ❌ 完全不同
→ **绝不可当 drop-in 替换**。

### 其余数值
- 去耦:经典 datasheet 每颗 **100nF**;**V5/W 明说不用**(『The peripheral circuit don't need
  to add filter capacitor』)—— 仍建议照贴(0402 近乎免费)
- DIN 串阻 **300~500Ω**(Adafruit,**非** Worldsemi;datasheet 里没有),且『**resistor should
  be at the end of the wire closest to the NeoPixel(s), not the microcontroller**』
- 体电容 **500~1000µF**;电流 **60mA/颗**(Adafruit 满白)vs Worldsemi V5/W 标 12mA×3=36mA
  → 按 **60mA** 做电源预算
- MSL **5a**(24h @≤30°C/60%RH),回流峰值 **245°C**

---

## 3. I2C EEPROM

**推荐:M24C64-RMN6TP(C79988,Preferred Extended,SOIC-8,1.8–5.5V,库存 11.8 万,$0.15)**
—— 比 AT24C02/M24C02 更便宜却大 32 倍,电压范围更宽。

### 诚实结论:ESP32-S3 上多数情况**不该加**
配置存储用 **NVS**(已在 flash 里,免费、带磨损均衡)。只有这些才值得外挂:
- **数据要能扛住重刷固件** —— `esptool erase_flash` 会抹掉 NVS,EEPROM 不受影响。
  这才是出厂序列号 / MAC / 标定值的真实理由
- 擦写寿命:EEPROM 4M 次 vs SPI-NOR flash ~10 万次
→ 「存配置」这半个理由**不成立**,「存序列号」这半个成立。

### ⚠️ ST 的脚名和别家不一样
M24C64 不叫 A0/A1/A2/WP,而是 **E0/E1/E2(Chip Enable)+ WC(Write Control)** —— 脚位号相同、
脚名不同。要字面 A0/A1/A2/WP 就选 AT24C02C(C6203)/AT24C256C(C6482)。

- **地址脚必须接死**:ST §2.3『These inputs **must be tied** to VCC or VSS… When not
  connected (left floating), these inputs are read as low』;Microchip 措辞更软但同样
  因『**capacitive coupling**』要求接固定电平。全接地 → 7 位地址 **0x50**
- **WP/WC**:接 GND = 允许写。ST §2.4 有个微妙行为:『When WC is driven high, device select
  and address bytes **are** acknowledged, data bytes are **not**』→ 别拿 ACK 反推写保护状态
- 去耦 **100nF**(仅 ST §2.6.1 明说 10–100nF;**Microchip datasheet 全文没有去耦建议**)

### I2C 上拉:4.7k 是错的(按 NXP UM10204 Rev.7.0 推)
公式(§7.1 Eq.1/Eq.2):`Rp(max) = tr / (0.8473 × Cb)`,`Rp(min) = (VDD − VOL) / IOL`

3.3V / 400kHz(tr ≤ **300ns**,IOL ≥ **3mA**):
- `Rp(min) = (3.3−0.4)/0.003 = **967Ω**`
- `Rp(max) @Cb=100pF = 300n/(0.8473×100p) = **3.54kΩ**`

| Rp | tr@50pF | tr@100pF | 判定 |
|---|---|---|---|
| **2.2k** | 93ns | **186ns** | ✅ 全区间过,灌流仅 1.32mA |
| 4.7k | 199ns | **398ns** | ❌ **100pF 时超 300ns 限值 33%** |
| 45k(ESP32 内部) | 1906ns | 3813ns | ❌ 连 100kHz 的 1000ns 都过不了 |

→ **取 2.2kΩ**。且 **ESP32-S3 内部上拉(Table 5-4:RPU 典型 45kΩ)对 I2C 不可用** ——
这不是风格偏好,是算术。Espressif 自己也写『**not strong enough**… A range of 2 kΩ to 5 kΩ
is recommended』,与 NXP 公式推出的 967Ω–3.54kΩ 窗口**两个独立来源交叉吻合**。

---

## 4. 蜂鸣器 / N-MOS 低边开关

**推荐:AO3400A(C20917,✅**基础库**)+ MLT-8530 无源磁蜂鸣器(C94599,扩展)+
1N4148W(C81598,✅基础库),3V3 供电,栅极 220Ω 串阻 + 10kΩ 下拉,挂非 strapping 脚。**

### 2N7002 陷阱:实锤
| 器件 | V_GS(th) max | R_DS(on) 标定条件 | 2.5/3.3V 有标定? | ID |
|---|---|---|---|---|
| **AO3400A** | 1.45V | 10V→18mΩ · 4.5V→19mΩ · **2.5V→24典型/48max mΩ** | ✅ **有** | 5.7A |
| 2N7002 | **2.5V** | **10V→2.8/5Ω** · 4.5V→**ID 仅 75mA** | ❌ **无 4.5V 以下任何行** | 115mA(CJ) |
| BSS138 | 1.5V | 10V→3.5Ω · 4.5V→**6.0Ω** | ❌ 到 4.5V 为止 | **0.22A** |

Nexperia Rev.7 自称『Suitable for logic level gate drive』,却**没有任何 4.5V 以下的
R_DS(on) 行**;3.3V 栅驱动下 worst-case 只有 0.8V 过驱。
**LCSC 参数页自己就复现了这个陷阱**:C8545(2N7002)写 `5Ω@10V / 115mA`,
C20917(AO3400A)写 `48mΩ@2.5V / 5.7A` —— **这个差别本身就是答案**。

且 MLT-8530 线圈 **16±3Ω**,最坏 13Ω 在 3.3V 下 **254mA** —— BSS138(0.22A)和
2N7002(115mA)**电流根本不够**。

### 续流二极管:找到原厂一手依据
MLT-8530 datasheet **p.3 §3 "Recommended Driving Circuit" 明确画了二极管**(标 `IN4148`),
NPN 低边 + 470Ω 基极串阻。CUI《Buzzer Basics》p.5 给出原理:
『**The diode is required to clamp the fly-back voltage created when the switch
(transistor) is shut off quickly.**』并在下一页反证:压电蜂鸣器**不需要**二极管
(『the inductance of a piezo transducer is small』)—— 磁蜂鸣器需要,因为**它就是个线圈**。
**方向**:阴极(带条端)→ 3V3/蜂鸣器「+」,阳极 → FET 漏极。**接反 = 直接短路 3V3**。

### 数值依据
- **栅极串阻 220Ω**:下限由 GPIO 驱动能力定 —— ESP32-S3 datasheet Table 2-1 note 5
  『All other pins: **20 mA**』→ Rg ≥ 3.3/0.02 = 165Ω → 取 220Ω(峰值 15mA)。
  上限由开关速度定:τ=220×630pF=139ns,2.7kHz 周期 370µs → 边沿占 0.08%,毫无压力
- **栅极下拉 10kΩ**:ESP32-S3 通用 GPIO 复位期 **高阻**(Table 2-1「At Reset」列为空),
  630pF 栅极浮空 + V_GS(th) 低至 0.65V → FET 半开 → 蜂鸣器尖叫 / 线性区烧管。
  10k 对 220Ω 分压只损 2%(3.23V),对 100nA 漏电只 1mV —— 两端都有巨大余量
- **必须 3V3 不能 5V**:MLT-8530 工作电压 **2.5~4.5 Vo-p**,5V **超它自己的上限**
  (⚠️ 该 datasheet 自相矛盾:额定电流/声压却标在「5Vo-p」条件下测)
  代价:声压约 −3.6dB(~76dB@10cm,估算值)

### ⚠️ strapping 脚:这是算术不是玄学
ESP32-S3 strapping = **GPIO0 / 3 / 45 / 46**。把本块的 10k 栅极下拉挂 GPIO0,
会与内部 **45kΩ** 弱上拉(Table 5-4 RPU)分压:
> **V_GPIO0 = 3.3 × 10/(10+45) = 0.60V** < `VIL max = 0.25×VDD = 0.825V`
→ GPIO0 被判为 **0** → 每次上电**静默进下载模式,应用永不运行**。GPIO45/46 同理。

**额外收获(Table 2-2 上电毛刺)**:GPIO1–14/17 是**低电平毛刺**(~60µs)→ 毛刺时 FET 关断=静音,**适合**;
GPIO18/19/20 是**高电平毛刺** → 每次上电蜂鸣器「咔」一声。
→ 推荐 **GPIO4–GPIO14**。

---

## 参考(全部为实际抓取的一手源)

**CAN**:[TJA1051 Rev.7](https://www.farnell.com/datasheets/1931504.pdf) ·
[NXP TR1014/AH1014 应用提示](https://www.farnell.com/datasheets/1815511.pdf) ·
[TI SLOS346O](https://www.ti.com/lit/ds/symlink/sn65hvd230.pdf) ·
[PSM712](https://protekdevices.com/wp-content/uploads/datasheets/psm712.pdf) ·
[TDK ACT1210](https://www.tdk-electronics.tdk.com/inf/30/ds/ACT1210.pdf)

**WS2812B**:[经典 datasheet](https://cdn-shop.adafruit.com/datasheets/WS2812B.pdf) ·
[V5/W V6.0](https://pihrt.com/attachments/article/471/tme%20WS2812B-V5W%20Datasheet_V6.0_EN.pdf) ·
[WS2812C-2020](https://cdn.sparkfun.com/assets/4/c/8/a/9/WS2812C-2020_Datasheet.pdf) ·
[SK6812](https://cdn-shop.adafruit.com/product-files/1138/SK6812+LED+datasheet+.pdf) ·
[Adafruit NeoPixel Überguide](https://learn.adafruit.com/adafruit-neopixel-uberguide/best-practices)

**EEPROM**:[NXP UM10204 Rev.7.0](https://www.pololu.com/file/0J435/UM10204.pdf)(nxp.com 已 404) ·
[ST M24C64](https://www.st.com/resource/en/datasheet/m24c64-r.pdf) ·
[Microchip AT24C01C/02C](https://ww1.microchip.com/downloads/en/DeviceDoc/AT24C01C-AT24C02C-I2C-Compatible-Two-Wire-Serial-EEPROM-1Kbit-2Kbit-20006111A.pdf)

**蜂鸣器**:[AO3400A](https://www.aosmd.com/res/data_sheets/AO3400A.pdf) ·
[2N7002 Nexperia Rev.7](https://assets.nexperia.com/documents/data-sheet/2N7002.pdf) ·
[MLT-8530](https://files.seeedstudio.com/products/107020109/document/MLT_8530_datasheet.pdf) ·
[CUI Buzzer Basics](https://media.digikey.com/pdf/Application%20Notes/CUI%20Application%20Notes/Buzzer_Basics.pdf)

**共用**:[ESP32-S3 Datasheet v2.2](https://www.espressif.com/sites/default/files/documentation/esp32-s3_datasheet_en.pdf) ·
[ESP32-S3 硬件设计指南](https://docs.espressif.com/projects/esp-hardware-design-guidelines/en/latest/esp32s3/schematic-checklist.html)
