import { readFile, open } from 'node:fs/promises';
import path from 'node:path';
import { planTool, planRunSummary } from './plan-tools.mjs';
import { compileRoutes } from './route-tools.mjs';
import { runEasyeda } from './core.mjs';
import { createHash } from 'node:crypto';

// File arguments avoid shell interpolation and preserve the same MCP guards and receipts.
const [inputFile, outputFile] = process.argv.slice(2);
if (!inputFile || !outputFile || !path.isAbsolute(inputFile) || !path.isAbsolute(outputFile)) {
  throw new Error('Usage: node plan-cli.mjs <absolute input.json> <absolute new result.json>');
}
const input = JSON.parse(await readFile(inputFile, 'utf8'));
const output = await open(outputFile, 'wx');
const started = performance.now();
let result;
try {
  if (input.operation === 'compile_routes') {
    result = await compileRoutes(input);
  } else if (['prepare', 'prepare_correction', 'summary'].includes(input.operation)) {
    result = await planTool(input, new Map());
  } else {
    const catalog = await runEasyeda(['actions']);
    if (!catalog.ok || !Array.isArray(catalog.result)) throw new Error('Action catalog unavailable');
    result = await planTool(input, new Map(catalog.result.map(action => [action.name, action])));
  }
} catch (error) {
  result = { ok: false, error: { message: error.message, ...(error.missing ? { missing: error.missing } : {}), ...(error.receipt ? { receipt: error.receipt } : {}) } };
}
const elapsedMs = Math.round(performance.now() - started);
try { await output.writeFile(JSON.stringify({ elapsedMs, result }, null, 2) + '\n'); }
finally { await output.close(); }
const summary = planRunSummary(result, outputFile, elapsedMs);
summary.resultSha256 = createHash('sha256').update(await readFile(outputFile)).digest('hex');
if (summary.ok && summary.review?.imageSha256) summary.finishInput = { operation: 'finish', runResultFile: outputFile,
  expectedRunResultHash: summary.resultSha256, window: summary.review.visualReview.window };
console.log(JSON.stringify(summary));
if (!summary.ok) process.exitCode = 1;
