import { createHash } from 'node:crypto';
import { createWriteStream } from 'node:fs';
import { mkdir, readFile, rename, stat, writeFile } from 'node:fs/promises';
import { Readable } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import path from 'node:path';

const root = path.resolve(process.argv[2] || 'knowledge');
const model = 'Xenova/multilingual-e5-small';
const revision = '761b726dd34fb83930e26aab4e9ac3899aa1fa78';
const files = ['config.json', 'tokenizer.json', 'tokenizer_config.json', 'special_tokens_map.json', 'onnx/model_quantized.onnx'];
const response = await fetch(`https://huggingface.co/api/models/${model}/revision/${revision}?blobs=true`);
if (!response.ok) throw new Error(`Model metadata HTTP ${response.status}`);
const metadata = await response.json();
const manifest = { model, revision, baseModel: 'intfloat/multilingual-e5-small', dtype: 'q8', pooling: 'mean', normalize: true, queryPrefix: 'query: ', documentPrefix: 'passage: ', files: [] };
for (const file of files) {
  const expected = metadata.siblings.find(item => item.rfilename === file);
  if (!expected) throw new Error(`Missing upstream file ${file}`);
  const target = path.join(root, 'models', model, file);
  await mkdir(path.dirname(target), { recursive: true });
  let valid = false;
  try {
    const info = await stat(target);
    valid = info.size === expected.size;
    if (valid && expected.lfs?.sha256) valid = createHash('sha256').update(await readFile(target)).digest('hex') === expected.lfs.sha256;
  } catch (error) { if (error.code !== 'ENOENT') throw error; }
  if (!valid) {
    const download = await fetch(`https://huggingface.co/${model}/resolve/${revision}/${file}`, { signal: AbortSignal.timeout(300000) });
    if (!download.ok) throw new Error(`Download ${file} HTTP ${download.status}`);
    const partial = `${target}.part`;
    await pipeline(Readable.fromWeb(download.body), createWriteStream(partial));
    const content = await readFile(partial);
    if (content.length !== expected.size) throw new Error(`Size mismatch ${file}`);
    if (expected.lfs?.sha256 && createHash('sha256').update(content).digest('hex') !== expected.lfs.sha256) throw new Error(`Hash mismatch ${file}`);
    await rename(partial, target);
  }
  const hash = createHash('sha256').update(await readFile(target)).digest('hex');
  manifest.files.push({ file, size: expected.size, sha256: hash });
  process.stdout.write(`${file}: verified (${expected.size} bytes)\n`);
}
await writeFile(path.join(root, 'models', model, 'MODEL-MANIFEST.json'), JSON.stringify(manifest, null, 2));
