/** One FIFO per window. Expiry starts on arrival; cancellation is cooperative.
 * Native EDA calls cannot be forcibly interrupted: unresolved calls stay visible,
 * and subsequent FIFO actions fail promptly until they settle. No automatic replay.
 */
import { armDeadline } from './deadlines';
import { ActionError } from './protocol';
export const ABANDON_FALLBACK_MS = 60_000;
export const ABANDON_GRACE_MS = 2_000;
export const MAX_QUEUE_DEPTH = 64;
export const ABANDONED_ID_RING = 32;
export type ExecutionSignal = Pick<AbortSignal, 'aborted' | 'reason' | 'throwIfAborted'>;
export interface Execution { signal: ExecutionSignal; progress: (value: Record<string, unknown>) => void; isPaused?: () => boolean }
// The extension sandbox can mask browser constructors. Cancellation must work
// before the first EDA call even when AbortController is completely unavailable.
function createCancellation() {
 const NativeController = globalThis.AbortController;
 if (typeof NativeController === 'function') {
  try { const controller = new NativeController(); if (typeof controller.signal.throwIfAborted === 'function') return controller; }
  catch { /* fall back to a host-independent cooperative token */ }
 }
 // ponytail: fallback supports cooperative checks only; native signals provide events when available.
 const signal = { aborted: false, reason: undefined as unknown,
  throwIfAborted() { if (this.aborted) throw this.reason; } };
 return { signal, abort(reason: unknown) { if (!signal.aborted) { signal.aborted = true; signal.reason = reason; } } };
}
interface Entry {
 id: string; clientId?: string; revision: number; controller: ReturnType<typeof createCancellation>;
 state: string; bypass?: boolean; started: boolean; settled: boolean; arrivedAt: number;
 progress?: Record<string, unknown>; finishedAt?: number; error?: string;
 cancel: (reason: string) => void;
}
export interface QueueStamp {
	/**
	 * 已完成的动作数:每个 FIFO handler settle(resolve 或 reject)之后 +1。
	 * 单调递增、永不重用、永不回退。响应上带的是「**本动作完成之后**」的值,
	 * 也就是「本动作是第 seq 个完成的」。
	 *
	 * 于是对一次读的响应,`seq - 1` = 这次读的 handler 开跑时已经完成的动作数
	 * —— 所有 seq 更小的动作都在它开跑**之前**就 settle 了。
	 */
	seq: number;
	/** 累计被放弃的动作数,单调递增。变化 = 那段时间的顺序证据作废。 */
	seqAbandoned: number;
	/**
	 * true = 这条响应走了旁路通道、不在 FIFO 里,因此它的 `seq` **不构成任何
	 * 顺序证据**。省略 = false。
	 */
	unordered?: boolean;
	/** 最近被放弃的 request id(最多 ABANDONED_ID_RING 条),供判定点名。 */
	abandonedIds?: string[];
}

/** 一次入队的结局。四种互斥,调用方据此构造 response frame。 */
export type QueueOutcome<T> =
	| { status: 'ok'; value: T; stamp: QueueStamp }
	| { status: 'error'; error: unknown; stamp: QueueStamp }
	| { status: 'abandoned'; waitedMs: number; stamp: QueueStamp }
	| { status: 'overflow'; depth: number; stamp: QueueStamp };

export interface QueueTask<T> {
	/** request id —— 被放弃时进 `abandonedIds`。 */
	id: string;
	/** 请求自带的往返预算(daemon 下发)。<=0 / 缺省 → ABANDON_FALLBACK_MS。 */
	timeoutMs?: number;
	/** true = 走旁路,不进 FIFO,不动 seq(见 transport.ts 的旁路名单)。 */
	bypass?: boolean;
	clientId?: string;
	revision?: number;
	run: (execution: Execution) => Promise<T>;
}

export interface ActionQueueOptions {
	maxDepth?: number;
	fallbackTimeoutMs?: number;
	graceMs?: number;
}

export class ActionQueue {
 private seq = 0;
 private abandoned = 0;
 private abandonedIds: string[] = [];
 private depth = 0;
 private tail: Promise<void> = Promise.resolve();
 private entries = new Map<string, Entry>();
 private revisions = new Map<string, number>();
 private paused = new Set<string>();
 private maxDepth: number;
 private fallbackTimeoutMs: number;
 private graceMs: number;
 constructor(options: ActionQueueOptions = {}) {
  this.maxDepth = options.maxDepth ?? MAX_QUEUE_DEPTH;
  this.fallbackTimeoutMs = options.fallbackTimeoutMs ?? ABANDON_FALLBACK_MS;
  this.graceMs = options.graceMs ?? ABANDON_GRACE_MS;
 }
 counters(): QueueStamp { return this.stamp(false); }
 pending(): number { return this.depth; }
 control(payload: Record<string, unknown>, currentRequestId?: string): Record<string, unknown> {
  const operation = payload.operation ?? 'status';
  const clientId = payload.clientId;
  const requestId = payload.requestId;
  if (!['status', 'cancel', 'supersede', 'pause', 'resume'].includes(String(operation)) ||
      (clientId !== undefined && (typeof clientId !== 'string' || !clientId)) ||
      (requestId !== undefined && (typeof requestId !== 'string' || !requestId)))
   throw new ActionError('INVALID_CONTROL', 'Invalid control operation, clientId or requestId.');
  if (operation === 'supersede') {
   if (typeof clientId !== 'string' || !Number.isSafeInteger(payload.revision) || Number(payload.revision) < 1)
    throw new ActionError('INVALID_CONTROL', 'Supersede requires clientId and a positive integer revision.');
   this.revisions.set(clientId, Math.max(this.revisions.get(clientId) ?? 0, Number(payload.revision)));
  }
  const selected = [...this.entries.values()].filter(e => e.id !== currentRequestId && (!clientId || e.clientId === clientId) && (!requestId || e.id === requestId));
  // A cancellation can overtake its HTTP request. Remember the ID before it arrives.
  if (operation === 'cancel' && typeof requestId === 'string' && !this.entries.has(requestId)) {
   this.entries.set(requestId, { id: requestId, clientId: typeof clientId === 'string' ? clientId : undefined, revision: 0,
    controller: createCancellation(), state: 'cancelled_before_arrival', started: false, settled: true,
    arrivedAt: Date.now(), finishedAt: Date.now(), cancel: () => {} });
  }
  if (operation === 'pause') this.paused.add(typeof clientId === 'string' ? clientId : '*');
  if (operation === 'resume') { if(typeof clientId==='string')this.paused.delete(clientId);else this.paused.clear(); }
  if (operation === 'cancel' || operation === 'supersede') for (const entry of selected) {
   if (!entry.settled && (operation === 'cancel' || entry.revision < (this.revisions.get(String(clientId)) ?? 0))) entry.cancel('cancelled');
  }
  return { revision: this.revisions.get(String(clientId)) ?? 0, pending: this.depth,
   paused: clientId===undefined?this.paused.size>0:this.paused.has('*') || this.paused.has(String(clientId)),
   inFlight: [...this.entries.values()].filter(e => e.id !== currentRequestId && e.started && !e.settled).map(e => e.id),
   requests: selected.map(({ controller, cancel, ...receipt }) => receipt),
   note: 'Cancellation stops queued work and remaining batch steps. A running native API may still finish; completed edits are not rolled back.' };
 }
 submit<T>(task: QueueTask<T>): Promise<QueueOutcome<T>> {
  const bypass = task.bypass === true;
  const reject = (code: string, message: string) => Promise.resolve<QueueOutcome<T>>({ status: 'error', error: new ActionError(code, message), stamp: this.stamp(bypass) });
  if ((task.clientId !== undefined && (typeof task.clientId !== 'string' || !task.clientId)) || (task.revision !== undefined && (!Number.isSafeInteger(task.revision) || task.revision < 0))) return reject('INVALID_EXECUTION', 'Invalid clientId or revision; no action executed.');
  if (this.entries.has(task.id)) return reject('DUPLICATE_REQUEST', 'Request already received; inspect its status rather than replay it.');
  if (task.clientId && (task.revision ?? 0) !== (this.revisions.get(task.clientId) ?? 0)) return reject('STALE_INSTRUCTION', 'Instruction revision is not current; no action executed. Use control supersede before sending new work.');
  if (!bypass && this.depth >= this.maxDepth) return Promise.resolve({ status: 'overflow', depth: this.depth, stamp: this.stamp(false) });
  // Bound receipts without evicting live operations; settled receipts are diagnostic, not persistent history.
  for (const [id, entry] of this.entries) if (this.entries.size >= 128 && entry.settled) this.entries.delete(id);
  const entry: Entry = { id: task.id, clientId: task.clientId, revision: task.revision ?? 0,
   controller: createCancellation(), bypass, state: 'queued', started: false, settled: false, arrivedAt: Date.now(), cancel: () => {} };
  this.entries.set(task.id, entry);
  if (!bypass) this.depth++;
  return new Promise(resolve => {
   let answered = false;
   let release!: () => void;
   const released = new Promise<void>(r => { release = r; });
   const answer = (outcome: QueueOutcome<T>) => { if (!answered) { answered = true; resolve(outcome); } };
   const finish = () => { entry.settled = true; entry.finishedAt = Date.now(); };
   const deadline = armDeadline((task.timeoutMs && task.timeoutMs > 0 ? task.timeoutMs : this.fallbackTimeoutMs) + this.graceMs, () => entry.cancel('expired'));
   entry.cancel = reason => {
    if (entry.settled || entry.controller.signal.aborted) return;
    entry.state = entry.started ? `${reason}_in_flight` : reason;
    entry.controller.abort(new ActionError(reason === 'expired' ? 'ACTION_EXPIRED' : 'ACTION_CANCELLED', reason));
    deadline.cancel();
    if (!entry.started) { if (!bypass) this.depth--; finish(); }
    else if (!bypass) { this.abandoned++; this.abandonedIds.push(task.id); this.abandonedIds = this.abandonedIds.slice(-ABANDONED_ID_RING); }
    if (reason === 'expired' && entry.started) answer({ status: 'abandoned', waitedMs: Date.now() - entry.arrivedAt, stamp: this.stamp(bypass) });
    else answer({ status: 'error', error: new ActionError(reason === 'expired' ? 'ACTION_EXPIRED' : 'ACTION_CANCELLED', entry.started ? 'Cancellation requested; native operation may still complete. Query status.' : 'Request stopped before execution; no action executed.'), stamp: this.stamp(bypass) });
    release();
   };
   const run = async () => {
    if (entry.settled) return;
    if (Date.now() - entry.arrivedAt >= (task.timeoutMs && task.timeoutMs > 0 ? task.timeoutMs : this.fallbackTimeoutMs) + this.graceMs) { entry.cancel('expired'); return; }
    if (!bypass) this.depth--;
    const unresolved = [...this.entries.values()].find(e => e !== entry && !e.bypass && e.started && !e.settled && e.controller.signal.aborted);
    if (!bypass && unresolved) {
     entry.state = 'not_executed'; finish(); deadline.cancel();
     answer({ status: 'error', error: new ActionError('ACTION_IN_FLIGHT', `Request ${unresolved.id} is still settling. This action was NOT executed; query debug.control status.`), stamp: this.stamp(false) }); return;
    }
    entry.started = true; entry.state = 'running';
    const isPaused = () => this.paused.has('*') || (!!task.clientId && this.paused.has(task.clientId));
    const running = Promise.resolve().then(() => {
     if (!bypass && isPaused()) throw new ActionError('ACTION_PAUSED', 'Action paused before execution; resume the queue first.');
     return task.run({ signal: entry.controller.signal, progress: value => { entry.progress = value; }, isPaused });
    }).then(value => {
     entry.state = entry.controller.signal.aborted ? 'settled_after_cancel' : 'completed'; finish();
     if (!answered && !bypass) this.seq++;
     answer({ status: 'ok', value, stamp: this.stamp(bypass) });
    }, error => {
     entry.state = entry.controller.signal.aborted ? 'settled_after_cancel' : 'failed'; entry.error = error instanceof Error ? error.message : String(error); finish();
     if (!answered && !bypass) this.seq++;
     answer({ status: 'error', error, stamp: this.stamp(bypass) });
    }).finally(() => deadline.cancel());
    await Promise.race([running, released]);
   };
   if (bypass) void run(); else this.tail = this.tail.then(run);
  });
 }
 private stamp(unordered: boolean): QueueStamp {
  return { seq: this.seq, seqAbandoned: this.abandoned, ...(unordered ? { unordered: true } : {}), ...(this.abandonedIds.length ? { abandonedIds: [...this.abandonedIds] } : {}) };
 }
}
// Both reads of connection identity and runtime control must work while FIFO is blocked.
export const BYPASS_ACTIONS: ReadonlySet<string> = new Set(['document.current', 'debug.control']);
export function isBypassAction(action: string): boolean { return BYPASS_ACTIONS.has(action); }
