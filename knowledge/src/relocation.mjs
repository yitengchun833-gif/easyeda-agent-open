import { readFileSync } from 'node:fs';
import path from 'node:path';

// Resolve storage access only. Never rewrite the source identity or frozen evidence.
export function resolveKnowledgeFile(root, file) {
  if (typeof file !== 'string') return file;
  let originalRoot;
  try { ({ originalRoot } = JSON.parse(readFileSync(path.join(root, 'relocation.json'), 'utf8'))); }
  catch (error) { if (error.code === 'ENOENT') return file; throw error; }
  if (typeof originalRoot !== 'string' || !path.isAbsolute(originalRoot)) throw new Error('Invalid knowledge relocation root');
  const relative = path.relative(originalRoot, file);
  if (relative === '..' || relative.startsWith('..' + path.sep) || path.isAbsolute(relative)) return file;
  return path.resolve(root, relative);
}
