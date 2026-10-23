# Runtime recovery storage

This change is a source candidate. Installing it and migrating the shared live
runtime require a separate adoption step. No EDA project is changed by migration.

## Responsibilities

| Responsibility | Owner | Lifetime |
| --- | --- | --- |
| Connection, pending transport calls, execution claims | Existing Go daemon/hub, fenced by connection epoch | Current connection/process |
| Objects, pins, geometry, checks and recent receipt projections | Runtime cache (8 document projections; no receipt-count admission limit) | Disposable only after a successful recovery commit |
| Intent, attempts, original request/result evidence, unresolved scopes | Local SQLite recovery store | Across eviction, disconnect and restart |

Remaining intent and an unconfirmed save no longer pin an idle cache entry.
Releasing a cache does not complete a task or authorize another execution.
Startup reads the historical index; document state is loaded only for relevant
window/document observations. Old observations and verification results do not
become fresh after restart. A confirmed paused prefix can resume after cache
eviction in the same connection, using the existing generation, task revision,
baseline and scope checks. A new connection/restart must not reuse those proofs.

The architecture remains MCP → one Go daemon → Connector → official API.
No knowledge service is involved in recovery. Existing JSON stage/delivery
receipts, their exclusive pending creation, and atomic replacement are unchanged.

## Durable contract

The audit directory contains `recovery-<port>.sqlite` (schema version 1) and
`recovery-<port>.sqlite.identity.json`. The latter identifies the database
generation; it contains no independent task state. Missing/replaced storage or
unsupported/corrupt records deny dangerous dispatch. Read-only recovery remains
available and does not grant write permission.

SQLite uses one connection, DELETE journal and EXTRA synchronous mode through
the pure-Go `modernc.org/sqlite` dependency. This supports the existing Windows
CGO-free build. Transactions protect local records only; they do not make native
EDA calls atomic.

`recovery_evidence` retains append-only raw records and SHA256 hashes.
`recovery_scopes` and `recovery_attempts` are current projections maintained by
the same storage interface and transaction. The original request and its scope
are immutable; revisions prevent stale writers. The request-ID index is durable
and independent of observation-cache retention. JSONL remains an audit mirror,
not the authority that decides whether a request can execute.

An attempt records project/document identity, window/connection epoch, task ID
and revision, request ID, recovery basis, effect scope and original outcome.
Legal transitions are constrained: pending → dispatched or not_dispatched;
dispatched → settled or unknown. Unknown/terminal records cannot become a new
pending dispatch. Additional evidence can be retained without granting replay.

Before forwarding a dangerous request, intent and required recovery basis must
commit. A persistence error prevents sending. After forwarding, execution and
durability are reported separately: `result.execution`,
`result.persistence.ok:false`, `result.retryable:false`, and
`RECOVERY_PERSISTENCE_FAILED` preserve the returned native result or its unknown
status. The caller must not infer that the operation did not execute.

An active conflicting project write is serialized. New direct requests do not
inherit old task, historical unknown, unsaved-owner or failed-check locks. This
does not settle or replay the old attempt: its original ID and evidence remain.
For a related unknown on a still-connected execution epoch, the existing native
queue must confirm that no call remains pending/in flight. A persisted pending or
dispatched call without such confirmation remains busy, rather than becoming a
historical completion merely because the daemon restarted. Explicit continuations
and verified finish retain their own proof checks; use a new direct request for
newly authorized work. Identity assertions, durable intent, duplicate-ID rejection
and persistence errors are unchanged. Unsaved history keeps its original owner;
no record is cleared to admit new work. Late frames cannot replace new state.

The former 256-receipt rejection and import/restore truncation are removed.
SQLite retains the recovery history; before admitting another request, disposable
settled projections are flushed and released without a count threshold. Active
proof, the latest relevant read and current verified-finish evidence stay live.
Without durable storage, receipts are not discarded to create room. Physical
memory/disk exhaustion can still fail explicitly; this is not unlimited storage.
The dashboard's bounded event feed is separate from durable operation receipts.

## Legacy import and compatibility

On first adoption, the daemon imports the existing same-port
`runtime-<port>.json` and same-directory JSONL evidence into SQLite. Audit-only
history is also indexed when the checkpoint is absent. Original files remain
unchanged. Import keeps original IDs, statuses, evidence bytes and SHA256 hashes;
source/record hashes make repeated import idempotent. New SQLite records take
precedence over repeated legacy projections. For a new attempt only, the importer
joins checkpoint and audit evidence by request ID before applying
`legacy-execution/v1`. Supported, complete and matching native terminal responses
can retain `settled` execution while `evidenceUnknown` remains true. This never
restores current checks, confirmed page persistence, an epoch, or resume authority.
Creation uses the returned project UUID; its before-context need not already
contain the newly created project. Batch results must match their requested
steps individually. Missing, conflicting, partial and unsupported execution
evidence remains conservative. Original rule failures and remaining task intent
are preserved independently of the execution outcome.

Each initial classification records its rule version, reason and original source
hashes in append-only `legacy-classification` evidence. Re-import preserves every
existing current attempt, including an earlier importer’s unknown record; it is
not an in-place repair of a previously migrated database. Validate a new database
from the immutable inputs instead. Malformed input or conflicting declared
targets that cannot be safely scoped abort the transaction and deny dangerous
dispatch. A response context is an observation taken after the action, not proof
of another write target. Unsupported raw scripts remain unknown under their
declared project scope when that observation differs; their original response
and the identity discrepancy remain in the evidence.

After adoption, the old checkpoint is frozen input. New evidence belongs to
SQLite plus the existing JSON receipts/audit mirror. These are not two
independently writable task state machines. The identity marker and database
must be preserved together; do not clear either to recover capacity.

The offline acceptance imports only an existing backup ZIP into a temporary
directory, verifies its original archive/checkpoint hashes, and exercises the
actual HTTP/WebSocket dispatch handlers. Migration interruption and native-call
interruption use disposable test subprocesses. They never terminate the shared
daemon or connect to a real EDA project.

## Explicit manual acceptance of current state

`easyeda_runtime(operation:"task_accept_current_baseline")` is an explicit
manual disposition in the existing runtime control and recovery store. It does
not settle an old attempt or infer native success from the current drawing.
The old unknown, original evidence, unsaved history and permanent request-ID
index remain intact. There is no automatic reconciliation or replay.

First send `window`, an exact project/document `target`, and
`acceptance:{acceptCurrentState:false,requestIds:[...]}`. Preview obtains native
project, schematic/page and Board reads and returns the stable structural
`baselineSha256`, current epoch/generation/task revision, immutable attempt
references and relevant historical owner references. It grants no permission.

After the user explicitly accepts that state and attests that the old execution
instance has ended, submit `acceptCurrentState:true`, `connectionEpoch`,
`expectedBaselineSha256`, a reason, the exact preview references, and a new task
with `expectedRevision` and `generation`. `nativeEndEvidence` must specify
`type:"manual_session_termination_attestation"`, `attestedBy:"user"`, the old
window and the actual user statement. Each terminal reference identifies the old
request plus same-store initial and terminal status audit requests and their
SHA256 hashes. Execution-window identity and status-collector identity remain
separate; the exact request, original client/revision and arrival are correlated.
Queue idle, elapsed time, disconnection or a caller-authored success flag alone
cannot supply this evidence. The attestation is recorded as a human decision,
not independent proof of a native effect or its final UUID.

The daemon re-reads the native structure and compares it with the reviewed hash,
checks the live target/epoch/task revision and queue, and atomically commits the
new task and append-only disposition in the existing SQLite evidence store.
Failure grants no execution permission. Acceptance leaves the queue paused;
resuming it is a separate, explicit action. No drawing or save occurs as part
of acceptance.

The disposition applies only to its precise new task revision, page, connection
and observed scene. It permits supported typed page edits/save, never raw JS,
project-wide operations, cross-page work or a different unresolved attempt.
Normal generation changes within that task remain observed; changing the task
revision, connection or scene invalidates the permission. Same-document saved
close/reopen verification is bounded by actual save and close receipts; it must
not discard unsaved work or authorize another page. New page edits still need
their own object, wiring and pin baseline. A structural acceptance is not an
electrical check, save/reopen proof or permission to resume an old unknown.

## Adoption and rollback boundary

Before formal adoption, coordinate a saved/stopped checkpoint for all shared
windows, back up the complete runtime evidence, then install the single candidate
build. Verify source/package/process identity before allowing a small personal
test-project write, save/readback and confirmed-prefix continuation. Re-check
old unknown scopes read-only. Do not mass replay historical requests.

Source rollback is a Git revert. Data rollback is different: pre-SQLite versions
cannot interpret the new ledger and must not write from the frozen legacy
checkpoint. Preserve the upgraded database, marker, JSON receipts, audit files
and artifacts; never replace them with an older runtime snapshot. If software
must be rolled back, keep writes stopped until a schema-compatible repair or an
explicitly reviewed evidence-preserving conversion is available. No reverse
migration or automatic unknown-result reconciliation is provided here.

Process-crash and local persistence-failure tests are not physical power-loss,
network-filesystem or real EDA acceptance. Storage may grow with retained evidence;
this change adds no evidence deletion or automatic cleanup policy.
