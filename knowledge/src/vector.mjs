import { Embeddings } from '@langchain/core/embeddings';
import { readFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import path from 'node:path';

export class LocalEmbeddings extends Embeddings {
  constructor(config) { super({}); this.config = config; }
  async prepare() {
    if (!this.loading) this.loading = (async () => {
      const { pipeline, env } = await import('@huggingface/transformers');
      env.allowRemoteModels = false;
      env.localModelPath = this.config.localModelPath;
      env.cacheDir = this.config.localModelPath;
      this.pipe = await pipeline('feature-extraction', this.config.model, { dtype: 'q8', device: 'cpu' });
      return this.pipe;
    })();
    return this.loading;
  }
  async encode(text, query) {
    const pipe = await this.prepare();
    const result = await pipe((query ? 'query: ' : 'passage: ') + text, { pooling: 'mean', normalize: true, truncation: true, max_length: 512 });
    return Array.from(result.data);
  }
  async embedQuery(text) { return this.encode(text, true); }
  async embedDocuments(texts) {
    const vectors = [];
    for (const text of texts) vectors.push(await this.encode(text, false));
    return vectors;
  }
}

export async function loadVectorRuntime(config) {
  const enabled = config.enableVector ?? process.env.EASYEDA_KNOWLEDGE_ENABLE_VECTOR === '1';
  if (!enabled) return { ready: false, reason: 'disabled' };
  const model = config.model || process.env.EASYEDA_KNOWLEDGE_MODEL || 'Xenova/multilingual-e5-small';
  const localModelPath = config.localModelPath || process.env.EASYEDA_KNOWLEDGE_LOCAL_MODEL_PATH || config.models;
  const manifest = JSON.parse(await readFile(path.join(localModelPath, model, 'MODEL-MANIFEST.json'), 'utf8'));
  if (manifest.model !== model || manifest.pooling !== 'mean' || manifest.dtype !== 'q8') throw new Error('Unsupported or mismatched model manifest');
  const encoding = { model, revision: manifest.revision, dtype: 'q8', pooling: 'mean', normalize: true, queryPrefix: 'query: ', documentPrefix: 'passage: ', maxLength: 512, chunkVersion: 2 };
  const signature = createHash('sha256').update(JSON.stringify(encoding)).digest('hex');
  const embeddings = new LocalEmbeddings({ model, localModelPath });
  const lance = await import('@lancedb/lancedb');
  const connection = await lance.connect(path.join(config.root || path.dirname(localModelPath), 'index', 'lancedb'));
  const tableName = `vectors_${signature.slice(0, 16)}`;
  let table = (await connection.tableNames()).includes(tableName) ? await connection.openTable(tableName) : null;
  let operations = Promise.resolve();
  const serial = work => { const result = operations.then(work); operations = result.catch(() => {}); return result; };
  return {
    ready: true, model, revision: manifest.revision, signature, encoding,
    embed: (text, query = false) => query ? embeddings.embedQuery(text) : embeddings.encode(text, false),
    publish: (source, chunks) => serial(async () => {
      if (!chunks.length) return;
      const vectors = await embeddings.embedDocuments(chunks.map(chunk => chunk.text));
      const rows = chunks.map((chunk, i) => ({ chunkId: chunk.chunk_id, sourceId: source.sourceId, revision: source.revision, generation: source.generation, vector: vectors[i] }));
      if (!table) table = await connection.createTable(tableName, rows);
      else { await table.delete(`sourceId = '${source.sourceId.replaceAll("'", "''")}'`); await table.add(rows); }
    }),
    remove: sourceId => serial(async () => { if (table) await table.delete(`sourceId = '${sourceId.replaceAll("'", "''")}'`); }),
    async search(query, sourceIds, limit = 20) {
      if (!table || !sourceIds.length) return [];
      const vector = await embeddings.embedQuery(query);
      const allowed = sourceIds.map(id => `'${id.replaceAll("'", "''")}'`).join(',');
      return table.vectorSearch(vector).where(`sourceId IN (${allowed})`).limit(limit).toArray();
    },
  };
}
