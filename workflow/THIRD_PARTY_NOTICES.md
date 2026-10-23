# Third-party notices

- The base evidence-to-delivery workflow is retained under the root LICENSE. Its bundled notices remain under licenses/.
- Planning with Files: upstream https://github.com/OthmanAdi/planning-with-files, bundled source version 3.18.1, source commit recorded by the original package as 7c11cb75702a0d82f1ee8b6b1b632272cd84bb85. MIT license: licenses/planning-with-files-LICENSE. Existing local hook adaptations are retained.
- EasyEDA Agent: https://github.com/zhoushoujianwork/easyeda-agent. Current local Skill and MCP source imported from the user's modified working tree; source commit and per-file hashes in sources.lock.json. MIT licenses retained at licenses/easyeda-agent-LICENSE and components/easyeda-mcp/LICENSE. No CLI/Connector executable or legacy compiled beautify code is included.
- PCB Routing Knowledge: https://github.com/dh118713-oss/pcb-routing-knowledge, commit c2c86eb58c23d1141ff4d14f248275cde97fb364. MIT license and author notice retained at skills/pcb-routing-knowledge/LICENSE. Referenced third-party papers, standards, datasheets and external images retain their own rights; the code license does not grant redistribution of those originals.

Local integration changes: updated component routing, rule ownership, hardware workflow handoffs and repository checks. No upstream endorsement is implied.
