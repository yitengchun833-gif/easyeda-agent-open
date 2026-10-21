import { ensureKnowledgeService, knowledgeRequest } from './client.mjs';

// Private daemon pipe only: never print this result to UI, logs or MCP output.
try {
  const service=await ensureKnowledgeService();
  await knowledgeRequest('/api/status');
  process.stdout.write(JSON.stringify({baseUrl:service.baseUrl,token:service.token}));
} catch {
  process.stderr.write('Knowledge startup failed; inspect knowledge-stderr.log');
  process.exitCode=1;
}
