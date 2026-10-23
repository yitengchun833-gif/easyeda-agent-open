import { fileURLToPath } from 'node:url';
import path from 'node:path';


process.env.EASYEDA_BIN = process.env.EASYEDA_OPEN_BIN || fileURLToPath(new URL('../../bin/easyeda.exe', import.meta.url));
process.env.EASYEDA_NODE_BIN ||= process.execPath;
process.env.EASYEDA_RUNTIME_DIR ||= fileURLToPath(new URL('../../.runtime', import.meta.url));
process.env.EASYEDA_AUDIT_DIR ||= path.join(process.env.EASYEDA_RUNTIME_DIR, 'audit');
// Keep imported knowledge and models beside the user project, never under the versioned agent package.
const agentRoot=fileURLToPath(new URL('../../',import.meta.url));
process.env.EASYEDA_KNOWLEDGE_ROOT ||= path.resolve(agentRoot,path.basename(path.resolve(agentRoot))==='source'?'../../knowledge':'../knowledge');
await import('./server.mjs');
