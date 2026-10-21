// Adapted from easyeda/eext-knowledge-base@39acb6a (Apache-2.0): CJK bigrams
// are for recall only. Identity fields are always checked separately.
export function terms(value = '') {
  const text = String(value).toLowerCase();
  const out = new Set(text.match(/[a-z0-9][a-z0-9._+\-/]*/g) ?? []);
  for (const run of text.match(/[\u3400-\u9fff]+/g) ?? []) {
    for (let i = 0; i < run.length; i += 1) out.add(run.slice(i, i + 2));
  }
  return [...out].filter(Boolean);
}

export function keywordScore(query, text) {
  const q = terms(query); const hay = searchText(text).toLowerCase();
  if (!q.length) return 0;
  return q.reduce((score, term) => score + (hay.includes(term) ? 1 : 0), 0) / q.length;
}

// Layout/OCR can split symbols (R ON, V CC); tags are markup, not evidence.
export const searchText=value=>String(value).normalize('NFKC').replace(/\b([A-Za-z])\s+(?=[A-Z_]+\b)/g,'$1').replace(/<[^>]+>/g,' ');
export function isEvidence(text,metadata={},query='') {
  if(['section_header','caption','page_header','page_footer'].includes(metadata.label))return false;
  return terms(searchText(text)).length>1 || terms(query).length<=1;
}

// A part/model/pin must match a complete stored token, never a prefix.
export function strictIdentityMatches(query, metadata = {}) {
  const wanted = new Set((String(query).match(/[A-Za-z]+[A-Za-z0-9._+\-/]*/g) ?? []).map(x => x.toLowerCase()));
  const fields = ['models', 'pins', 'partNumber', 'partNumbers', 'package', 'aliases'];
  const available = new Set(fields.flatMap(key => Array.isArray(metadata[key]) ? metadata[key] : [metadata[key]])
    .filter(Boolean).map(x => String(x).toLowerCase()));
  return [...wanted].filter(value => available.has(value));
}
