# easyeda-agent MCP

Open stdio MCP adapter for the existing Go CLI/HTTP daemon and EasyEDA Connector.
The current server defines 30 tools: connection and runtime control; action
discovery, native JavaScript and batches; plans, snapshots and canvas images;
project transfer, circuit blocks and API lookup; PCB constraints and pad-to-net
reports; nine action domains; and six knowledge tools. The authoritative list and
schemas are in `src/server.mjs`, `src/core.mjs` and `src/knowledge-tools.mjs`.
Loaded client tools must be checked separately after an update.

```bash
npm ci --ignore-scripts
EASYEDA_BIN=/absolute/path/to/easyeda npm test
EASYEDA_BIN=/absolute/path/to/easyeda npm start
```

Codex registration:

```bash
codex mcp add easyeda-open \
  --env EASYEDA_OPEN_BIN=/absolute/path/to/easyeda \
  -- node /absolute/path/to/mcp/src/launch.mjs
```

This registration example is for a new installation; retain an existing verified
portable or plugin registration instead of adding another server. MCP calls use
the same Go daemon, Connector, audit and official `eda.*` API execution chain.
Bind the intended `window` from health and provide exact target identities when
needed. `project`/`doc` are optional legacy CLI navigation fields, not mandatory
fields for every Open mutation. Use `easyeda_actions(exact: ...)` when an action's
payload is unknown. Native JavaScript is available through `easyeda_execute`;
errors and unknown writes still require scoped readback before another write.

Reload the corresponding MCP client after an update to discover its tools. Use
the current Open Skill at `skills/easyeda-agent-open` relative to the plugin root.
Plans and `runtime finish/resume` support reviewed execution and acceptance;
they are selected for the task rather than required for every native operation.
Preserve target checks, request identity, in-flight exclusion, persistence and
real errors. Saved, reopened, DRC and visual-review evidence prove different facts.

MCP connection and `easyeda_health` are read-only with respect to service
lifecycle. A missing daemon is reported without creating runtime files or
starting a service. When the user requests connection/reconnection, call
`easyeda_connect({})`: it reuses the healthy configured daemon or explicitly
starts the companion daemon when absent. It never force-restarts a live daemon,
opens EasyEDA, replays old requests, or edits/saves a project. A response with
`connectionState:waiting_for_connector` means only that the daemon is ready;
there is no connected EDA window yet. Do not call it for status-only requests
or when service startup is forbidden. No caller-supplied path/port/restart
arguments are accepted. Reload the MCP client after updating to discover it.
Health uses the same configured endpoint as direct window-bound actions and
checks the service, exact component version, and PID from the configured runtime
directory. An identity mismatch remains an error and never replaces a process.
Connecting or closing an MCP client does not start or stop the daemon or knowledge
service.

## DeepSeek Harness (DSH) 集成

DSH 原生支持 skill 与 MCP client 两种形态，本仓库两者都已具备，接入是配置级
工作：详见 [`docs/dsh-integration.md`](../docs/dsh-integration.md)。要点：skill
软链到 `~/.dsh/skills/` 即被发现；MCP 在 profile 的 `cordis.patch.yml` 加一个
`@deepseek-ai/dsh-mcp-client` 实例（`serverName: easyeda`，指向本目录
`src/server.mjs`）即可，工具以 `mcp__easyeda__easyeda_*` 命名。注意 in-box
插件无需 pnpm 安装（fallback 从 dsh 安装目录解析），profile 里误装旧版会遮蔽
fallback。

## Creating a project from the home screen

Call `easyeda_health` and choose the intended connected window. Then call
`easyeda_project` with the following shape (replace the window ID):

```json
{
  "action": "project.create",
  "window": "<windowId from easyeda_health>",
  "payload": { "friendlyName": "New project", "open": true }
}
```

Do not supply `project` or `doc`: the new project is not an existing routing
target, and a home tab such as `tab_page1` is not a schematic/PCB document.
The adapter rejects contradictory routing instead of forwarding it to the
CLI document guard. The explicit window requirement also prevents implicit
selection between multiple editor windows.

Creation only makes the project container. Inspect the returned `created`,
`opened`, and `partial` fields and read back project state before continuing;
an open failure must not trigger blind duplicate creation. Document creation
is a separate operation. This exception does not relax routing requirements
for other mutations, including `schematic.page.create` and `board.create`.

The connected extension must implement `project.create`. If the call returns
`UNKNOWN_ACTION`, update the connector to a version providing that handler;
a compatible version reported by `easyeda_health` alone does not prove action
availability. Do not retry creation with a fabricated `doc` to work around it.


## Project transfer

Use `easyeda_project_transfer` for `operation: "open"` or `"export"`. Both require
an explicit `window` and `projectUuid`, without `project`/`doc` routing. Opening
requires `allowDiscardUnsaved: true` after saving all documents; optional `pageUuid`
waits for a schematic page and verifies its active identity. Export requires a new
`out` path ending in `.epro2`; it verifies the active project before/after capture,
validates ZIP integrity and reports SHA-256 without overwriting an existing file.
`restoreVerified: false` means an import round trip has not been verified.

These commands require the matching CLI build. Their fixed official-API adapters
validate project transfer without requiring caller-authored native JavaScript.

Project transfer requires connector handlers `project.open` and `project.export` and the matching CLI/daemon catalog. Both are catalogued; prefer `easyeda_project_transfer` for native export because the CLI validates and writes the archive. Old daemons/connectors reject unknown actions; no debug.exec_js fallback.
