# 从资料、工程档案到落图

工程师确认设计，AI 决定表达方式，现有执行层精确落图。这里复用同一个 MCP、队列、runtime 和任务记录；不增加第二个调度器。

## 文件分工

- **snapshot.json**：真实读取的原生工程档案，保留库/封装对象、属性、逐脚网络、导线与页面图元；同名 Markdown 是供人查阅的摘要和逐脚表。不能用摘要代替完整 JSON。
- **plan.json**：目标工程/页面、器件和逐脚目标、来源文件与哈希、布局意图、具体 typed steps。它表示要做什么，不表示现场已经完成。同名 Markdown 供工程师审阅；修改 JSON 后调用 `easyeda_plan(render,planFile)` 自动刷新当前修订的阅览文件，执行绑定 JSON 的准确 SHA256。
- **原始资料与图片**：保留在用户项目里，通过 sources 的 path/sha256/anchor 引用，不复制进插件。图片负责图形理解及最终排版审阅；JSON 负责身份与连接。
- **局部 patch**：引用固定 baseline 的路径和哈希；缺席对象不等于删除，几何读取的 null 网络不覆盖原来已知网络。当前档案保留 base+patch，不伪装成新的全页快照；现场缓存仍由原 runtime 更新。

## 现有原理图读取、重画与返工

1. `easyeda_snapshot(domain:"schematic",window,target,outputFile:"绝对路径/snapshot.json")`：一次完整读取直接落盘，返回路径、计数、缺项，避免全部图元进入模型上下文。默认完整属性与页面图元；部分读取明确标局部。默认不覆盖已有文件，cacheOnly 不能生成新鲜基线。
2. `easyeda_plan(operation:"prepare",snapshotFile,outputFile:"绝对路径/plan.json",goal,sources)`：从完整基线生成目标与 Markdown。保留器件属性和全部引脚，steps 初始为空，confirmation 初始未确认，不替用户签字。
3. AI 结合已确认设计及当前图片编写/修订 steps。布局坐标与线形属于表达，不从网表自动推定最佳画法。新建器件的 primitiveId 由实际返回建立映射，不能重用旧实例 ID；有依赖的新建操作分少量阶段完成。任意脚本可调用 `helpers.call` 复用 typed 适配，raw 脚本整体仍不能自动认定规则通过。
4. `easyeda_plan(operation:"inspect",planFile)` 校验文件结构与所有来源哈希，返回本版 SHA256；旧 `apply(planFile,expectedHash,window)` 提交已写好的完整 steps，并在最后追加一次 `schematic.target.check`。可选 `stages` 按下文指定阶段执行，禁止含糊执行全部阶段。不会自动生成步骤、自动重试或主动保存。既有 batch/execute 入口继续可用，阶段执行完也可用 `check(planFile,window)` 单独核对最终目标。
5. `target.check` 重新读完整页面，核对少件/多件、少脚/多脚、身份、属性及逐脚网络，输出 pass/fail/unknown 和当前实例映射。它不承担图面、电路设计合理性、实物封装或持久化验收。整页重建才用全页检查；局部返工继续现有 edit 范围核对，避免每次全页重读。

最后看当前图面、执行任务要求的 DRC，再按工作规则保存。中途 partial/unknown 只回读受影响部分并补剩余操作，不整批重放。

## 按实际回执编译下一阶段

`prepare` 仍生成 `steps:[]`，不预编造实例 ID。需要分阶段时保留顶层空 `steps`，在同一 plan 加入 1～32 个 `stages`；每阶段只有 `id`、静态 typed `steps`、`check`，可加局部器件 `primitiveIds`。阶段 ID 唯一，`check` 为 `geometry` 或 `final`；只有最后一个阶段可为 `final`。尚未编译的阶段可用空 steps，空步骤仅 `final` 可以执行。

```json
{
  "steps": [],
  "stages": [
    {"id":"place","check":"geometry","steps":[
      {"action":"schematic.component.place","payload":{"libraryUuid":"已核实库UUID","uuid":"已核实设备UUID","x":100,"y":100}}
    ]},
    {"id":"connect","check":"geometry","primitiveIds":["实际新器件ID"],"steps":[]},
    {"id":"final","check":"final","steps":[]}
  ]
}
```

这只是现有完整 plan 的字段片段；示例 UUID/ID 必须替换为实际值。调用 `apply` 必须传 `stage:"place"` 和当前 `expectedHash`。中间阶段执行一个原有 batch 后，从 place/modify 的实际回执及已指定的 primitiveIds 得到器件范围，只发一次 `schematic.components.list` 几何读回：含引脚坐标，不编译网表、不补全库身份、不扫描导线。接线/属性等阶段明确填写相关**器件** ID。缺 ID 不退回全页读取，也不把阶段标为完成。

返回的 `execution` 保留原批次回执，`readback` 保留局部读取回执；`stage.completed` 仅表示本阶段动作和所声明读回完成，`wholePageComplete` 始终为 false。partial、未知或取消保留已成功动作；能定位实际 ID 时读回已知范围，不重试写入。dryRun 不读工程、不标完成。中间临时断线阶段按任务传 `edit:{mode:"design",complete:false}` 延后原有完整单元电气检查；这不构成电气通过。

AI 根据 `execution` 和 `readback` 的真实 ID、引脚坐标编写下一阶段静态 steps，修订 JSON 后重新 inspect/render，使用新 hash。计划不支持 `$ref`、表达式或自动串接后续阶段；不同请求之间的顺序由调用者控制，没有第二套状态机或自动重放保护。最终阶段执行成功后独立调用一次 `schematic.target.check`；最后阶段 steps 为空时直接核对。失败不进入最终核对。目标、来源哈希与逐步骤 `_target` 绑定始终保留，计划禁止切页、嵌套 batch 或任意 debug 脚本。

仅测试或使用本地新 MCP 源码时，可从包根目录直接 import `planTool`，复用现有 daemon，无需重启当前 MCP。先将本机已核对 CLI 路径设置为 `EASYEDA_BIN`（以及需要时的 `EASYEDA_DAEMON_URL`），将调用参数保存为绝对路径 JSON，再运行以下 Node 模块代码；`process.argv[1]` 为输入文件，`process.argv[2]` 为结果文件。入口不启动或重启服务，完整回执落在指定结果文件。

```js
import { readFile, writeFile } from 'node:fs/promises';
import { planTool } from './mcp/src/plan-tools.mjs';
import { runEasyeda } from './mcp/src/core.mjs';
const catalog = await runEasyeda(['actions']);
if (!catalog.ok || !Array.isArray(catalog.result)) throw new Error('Action catalog unavailable');
const input = JSON.parse(await readFile(process.argv[1], 'utf8'));
const result = await planTool(input, new Map(catalog.result.map(action => [action.name, action])));
await writeFile(process.argv[2], JSON.stringify(result, null, 2));
```

调用参数例：`{"operation":"apply","planFile":"绝对路径/plan.json","expectedHash":"本版64位SHA256","window":"实际窗口ID","stage":"place","edit":{"mode":"design","complete":false},"timeoutMs":300000}`。上面的代码以 `node --input-type=module -e "模块代码" 输入文件 结果文件` 执行；PowerShell 可用单引号 here-string 保存模块代码，避免引号转义与插值。

## 固定位置布线和落线前核对

整页重建或大量接线时，先由 AI 结合功能关系和图面确定摆件，再读取真实器件 bbox、引脚坐标和方向。将独立目标中的逐脚网络映射到这些实际引脚，不能把尚未接线的 `net:null` 当成 NC，也不能用旧页面坐标冒充当前测量。

已有 Go 布线器支持保持位置的路径生成；不引入第二套执行服务：

```text
easyeda sch layout-plan --route-only --from measured.json --out routes.json --report routes-report.json
easyeda sch layout-plan --route-only --from measured.json --validate-wires planned-wires.json --out checked.json --report checked-report.json
```

`measured.json` 使用现有 schemaVersion:1 / components[].measurement / netPolicies。measurement 包含实测 designator、x/y、rotation、mirror、bbox、pins[{number,net,x,y,rotation}]，真实文字框可放 textBboxes；没有文字测量就保留最终图面审查，不用推测文字框阻塞人工布局。每网声明 direct、module_port、local_power 或 local_ground。route-only 只连接 direct；其他网仍保留引脚障碍，由调用方编排电源/地或跨模块短引线。程序不移动器件，不自动生成网名和模块框。

`planned-wires.json` 严格使用 `{ "routes": [{ "net": "目标网", "points": [[x,y],[x,y]] }] }`。可先审查自动生成的路径，再合并电源/地和已规划的导线，统一验证：非零、正交、引脚出线方向、本体/真实文字障碍、异网接触及 direct 网络连通。失败不输出可当作完成的部分线路；按精确位置修改受影响器件或路径后重新核对该方案。通过是计划几何证明，不能替代落图后的原生逐脚网络检查。

接线后按实际 wire/attribute ID 一次设置需要显示的网名；局部已直接连接且无跨区域用途的网名隐藏，真实 Name/网络保持。标签意图来自表达方案，不能默认每段线显示一个网名。文字与线的相对放置遵守 working-rules 第7条：器件位号/参数就近成组且绝不重叠，网名基线/锚点贴到所属导线，不另加悬空偏移；先用本次已有的实际边界/图片选择相邻空位，不套案例固定偏移表。最后用当前整图核对文字、折返和可读性，再保存。局部小改继续使用已知对象与引脚，不为了形式重复整个布线流程。

## 手册、图片、文本与表格入口

复用已安装 document-intake：PDF/DOCX 等先 Docling，已有解析可用就复用；失败或格式不适用才 MarkItDown。表格单元格/公式使用 spreadsheet-intake；关键图形与数值回看原件。不把“提取出 Markdown”当成资料完全理解，也不把所有转换器都运行一遍。

Docling 的 `OUTPUT.md.assets/{document.json,manifest.json,index.json,images/}` 可直接作为 plan.sources，格式为 `{id,path,sha256?,anchor?}`；prepare 自动计算/核对哈希，inspect/check/apply 再核对来源是否变化。anchor 使用现有 library find/show 得到的位置，不编造页码。转换器不进入实时 EDA 执行链。

从零设计时，由 AI 将工程师已确认的资料结论写成同一 `easyeda.plan/1` 结构；prepare 是已有原理图的便捷入口，不是手册到电路的自动设计器。未知引脚/封装仍保留 unknown，只暂停依赖该缺项的决定。
