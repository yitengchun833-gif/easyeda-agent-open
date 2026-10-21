# Settled review receipt retention

2026-10-16: fixed recurring `RUNTIME_CAPACITY` after normal script/batch work.

`needs_review` means the action settled but its checks were unknown or failed.
Previously every such receipt was protected indefinitely, even after a later
document generation made it unusable for finish or continuation. This filled
both the 16-batch continuation store and the 256-receipt store.

The live pool now retains these receipts while their document is unknown or
their generation is current/newer. An older settled review can be reclaimed.
Its original unknown/fail result remains unchanged in audit JSONL; eviction
does not certify checks, save the document, or replay an action.

Queued/running/resuming/saving/paused and `needs_readback` remain protected.
Parent/continuation/image dependencies still restore protection transitively.
The document's unsaved marker remains independent of receipt reclamation.

Regression coverage in `runtime_retention_review_test.go`:

- 320 script actions and 320 batches with unknown checks do not exhaust storage;
- current review, missing-document evidence and live dependencies stay protected;
- 256 unresolved readback receipts still reject admission;
- all original unknown-check audit rows survive reclamation.

An already-running daemon must load the rebuilt binary for this change to apply.
Restart only the identified backend after active tasks settle. It does not
require opening, closing or modifying editor documents. Preserve the checkpoint
and old binary for rollback. After restart, old proof is historical; verify a
fresh target-bound read before claiming the editor connection is usable.
