/** Explicit negative evidence, not absence of positive verification. */
export function failedActionResult(value: unknown): boolean {
 if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
 const result = value as Record<string, unknown>;
 const nonempty = (item: unknown): boolean => Array.isArray(item) ? item.length > 0
  : item && typeof item === 'object' ? Object.values(item).some(nonempty) : !!item;
 return result.ok === false || result.partial === true || result.verified === false
  || (result.error != null && result.ok !== true)
  || (result.deleted === false && !(result.notFound === true && result.verified === true)) || result.disconnected === false
  || typeof result.survivedTotal === 'number' && result.survivedTotal > 0
  || ['notApplied', 'survived', 'survivedIds', 'failures'].some(key => nonempty(result[key]))
  || failedActionResult(result.result);
}
