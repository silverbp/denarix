# Denarix Architecture Notes

Design notes for application-layer work that isn't visible in the schema itself.

## Period close

The schema enforces correctness (balanced entries, the hard lock) but has no opinion on process.
Six pieces of application logic are needed to drive it.

### 1. System account provisioning

Every business needs its own `Income Summary` and `Retained Earnings` `ledger_account` rows
(`account_type_id = 3` EQUITY, `is_system = true`) before it can ever be closed —
`period_close.income_summary_account_id` / `retained_earnings_account_id` are `NOT NULL`, so
there's no lazy-create path. `internal/periodclose.ProvisionSystemAccounts` runs at business
creation and again before every close; fixed `code` values (`3910`, `3900` - the QuickBooks-style
numbering convention, not descriptive strings) let it find the accounts without a naming
convention on `name`, and a found row is only adopted if it's actually a postable, active EQUITY
account at that code (an unusable match at code 3910/3900 - the wrong type, a container, an
inactive account - is a `FailedPrecondition`, not something silently adopted).

The resolved ids are then persisted onto `business.income_summary_account_id` /
`retained_earnings_account_id` (and, the same way, `business.ar_account_id` / `ap_account_id`
for the Accounts Receivable/Payable containers customer/vendor sub-accounts hang off - see
`internal/server/system_accounts.go`) via a guarded `UPDATE ... WHERE column IS NULL`. Once set,
every later resolution is a direct read of that column - no code lookup - which is what makes
calling `ProvisionSystemAccounts` on every close cheap rather than a lookup each time; only a
business's very first resolution (or a pre-existing business self-healing on its first touch
after this was added) does any writing.

### 2. The close service

The core transaction script, run inside a single DB transaction (required — see the lock-ordering
note in `schema.md`):

1. Resolve `period_start` = the day after the business's last unreversed `period_close.period_end`
   (or business inception if none), `period_end` = the requested close date.
2. For every `REVENUE`/`EXPENSE` `ledger_account` on the business, sum `ledger_entry` activity in
   `[period_start, period_end]`. Entries before `period_start` are already covered by a prior
   close and are unreachable anyway once locked.
3. For each account with nonzero net movement, insert one `ledger_transaction` dated
   `period_end` with two `ledger_entry` rows: one zeroing the account (opposite side of its
   normal balance), one hitting Income Summary.
4. Insert one more `ledger_transaction` sweeping Income Summary's resulting balance into
   Retained Earnings.
5. Insert the `period_close` row, then one `period_close_entry` per transaction from steps 3–4
   (`source_account_id` = the account each transaction zeroed — including Income Summary itself
   for the step-4 transaction).

Steps 3–4 must run *before* step 5 — the lock trigger only sees already-committed `period_close`
rows, so a close's own postings land while still unlocked.

### 3. Reversal / reopen

To undo a close: set `period_close.reversed_at`, which immediately drops that `period_end` from
the `MAX(period_end)` the lock trigger checks. Post genuine reversing entries for every
transaction in that close's `period_close_entry` rows rather than editing or deleting them —
consistent with the schema's existing soft-delete-over-mutation convention elsewhere. A
subsequent close can then re-cover the same (or an extended) range.

Closes stack and there is no cascade. Because the lock is a single high-water mark
(`MAX(period_end)` over unreversed closes), reversing an older close while a later one stands
would unlock nothing and its reversing entries could never post — so `ReverseClose` refuses
anything but the latest unreversed close with `FAILED_PRECONDITION`, naming the close to reverse
first. Each close's own Income Summary sweep is undone by that close's reversal and nothing else;
to reach a period several closes deep, reverse newest-first down to it, correct, then re-close
forward (each re-close generates fresh zeroing entries and a fresh sweep).

### 4. Guard rails the schema doesn't enforce

- **Contiguity**: nothing stops `period_start` from skipping or overlapping a prior close's
  range. The close service should reject a gap/overlap rather than rely on the DB.
- **Idempotency**: the partial unique index on `(business_id, period_end) WHERE reversed_at IS
  NULL` will reject a second concurrent close on the same date, but the service should check for
  an existing unreversed close first and fail with a clear error rather than surfacing a raw
  constraint violation.
- **Balance sanity**: `ledger_entry`'s debit/credit CHECK already guarantees each generated
  transaction balances; the service should still assert the sum of all step-3 zeroing entries
  equals the step-4 Income Summary sweep, as a defense-in-depth check on its own arithmetic.

### 5. API / MCP surface

Not designed yet. At minimum: trigger a close for a business + date, reverse a given
`period_close`, and list a business's close history. Given `entity_context` already anticipates an
MCP server as a consumer of this schema, period close is a natural candidate for an MCP tool
(e.g. "close the books through Dec 31") rather than (or in addition to) a REST endpoint.

### 6. Reporting integration

- A balance sheet as of any date *after* the latest close reads `Retained Earnings` directly.
- A balance sheet as of a date *within* the still-open current period needs an "current period
  earnings" line computed live (sum of REVENUE/EXPENSE activity since the last close) — the
  standard accounting treatment, and pure query logic, no schema change needed.
- P&L reports are unaffected either way; they already read `ledger_entry` over an arbitrary date
  range regardless of close state.

### Deferred / not required to ship this

- **Scheduling**: nothing here auto-triggers a close on a fiscal year end. `business` has no
  fiscal-year-end column today; adding one plus a scheduled job is a reasonable follow-up but not
  needed for the close service to work when invoked explicitly.
- **Automated tests**: the trigger logic was verified manually against a local Postgres
  container (full close doesn't self-lock; boundary-dated inserts rejected; post-close dates
  accepted; edits to locked entries rejected) but there's no test suite yet, since there's no
  application code to test against.

## Corrections model

`ledger_transaction`/`ledger_entry` have no built-in versioning or void flag — the schema enforces
that each transaction balances, but has no opinion on how a mistake gets fixed. Two different
correction patterns exist in the code today, and which one applies depends on where the mistake is
being fixed from:

1. **Editing a document's own lines, in an open period, supersedes its ledger entries in place.**
   `UpdateInvoiceLineItems`/`UpdateEstimateLineItems` on an already-posted document call
   `repostInvoiceLedger`: the existing `ledger_entry` rows under that document's
   `ledger_transaction_id` are soft-deleted (`SoftDeleteLedgerEntriesByTransaction`, an `UPDATE ...
   SET deleted_at`, never a hard `DELETE`) and replaced — the transaction id itself never changes.
   This is the schema's own soft-delete-over-mutation convention (every mutable table gets
   `deleted_at` instead of a real delete), applied to a ledger-entry edit rather than a document
   edit. The superseded rows are retained, just filtered out of every read query
   (`AND deleted_at IS NULL`) — there's currently no API surface to read them back.
2. **Once a period is closed, pattern 1 is rejected outright.** `enforce_period_lock`'s trigger on
   `ledger_entry` fires on `UPDATE OF debit_amount, credit_amount, account_id, deleted_at` — so the
   soft-delete step of a repost is itself a locked write, and the whole edit fails with
   `FAILED_PRECONDITION` once its transaction's date falls at or before the latest unreversed
   `period_close.period_end`. The only correction left at that point is a genuine reversing
   transaction (pattern 3) dated in the still-open period.
3. **Everything else — a raw `ledger-transaction post`, a cancelled invoice, a voided payment, an
   undone period close — is corrected with a new, mirrored transaction, never by editing or
   deleting the original.** `internal/ledgerpost.ReverseTransaction` is the one shared primitive for
   this: it reads the original transaction's entries and posts a new transaction with every debit
   and credit swapped, leaving the original completely untouched, and stamps the new row's
   `reverses_ledger_transaction_id` with the original's id. The partial unique index on that
   column is what makes a reversal once-only at the database level - a retried or concurrent
   second reversal of the same transaction fails on INSERT instead of doubling the correction.
   Three callers share it:
   - `ledger-transaction reverse` (`LedgerTransactionService.ReverseLedgerTransaction`) for a raw
     posting mistake. It refuses a transaction linked from an invoice or payment
     (`CountDocumentsForLedgerTransaction`, which deliberately counts voided payments too) —
     correct those through the document instead (next bullet), since a bare ledger reversal would
     fix the GL while leaving `paid_amount`/`balance_due`/`payment_application` stale. It also
     refuses a transaction that already has a reversal, and a transaction that *is* a reversal
     (reinstating a wrongly-reversed posting means posting it again, not reversing the reversal).
   - `invoice cancel` (`UpdateInvoiceStatus` transitioning to `CANCELLED`) and `payment void`
     (`VoidPayment`) — both reverse their own linked transaction *and* update the document-side
     state that lives outside the ledger (`balance_due`→0 for a cancelled invoice;
     `payment_application` rows removed and the invoice's `paid_amount`/`balance_due`/`status`
     restored for a voided payment, where `PAID` goes back to `SENT` and any other status is left
     alone). `invoice cancel` itself refuses while any payment is still applied — void those
     first. A cancelled invoice is then frozen: `UpdateInvoice`/`UpdateInvoiceLineItems` refuse it,
     since re-posting under the same `ledger_transaction_id` would leave the reversal mismatched.
     Payments can only be applied to a `SENT`/`OVERDUE` invoice (never `DRAFT`), so the
     restore-to-`SENT` on void is always right.

   All three take an optional reversal date (`--date` on the CLI; `reversal_date` on the RPC),
   defaulting to the original's own date and never allowed earlier than it. That's how a mistake
   in a *closed* period gets corrected: `enforce_period_lock` rejects any new transaction dated at
   or before the latest close, so the reversal is posted in the still-open period instead. Only
   `close reverse` always reuses the original date, since it has just unlocked that period itself.
   - `close reverse` (`internal/periodclose.Reverse`), the original motivating case: marks the
     `period_close` row `reversed_at` (unlocking the period), then reverses every transaction the
     close generated. Re-closing later generates fresh closing transactions, so the once-only
     index never gets in its way. Only the latest unreversed close can be reversed — there is
     no cascade through stacked closes (see "Reversal / reopen" above).

In short: an open-period document edit supersedes in place; anything closed, raw, or
document-linked gets a new reversing transaction instead. If this ever chafes — e.g. a future
requirement that *all* corrections leave the original untouched even in an open period — change
pattern 1 to call `ledgerpost.ReverseTransaction` too rather than `repostInvoiceLedger`'s
soft-delete-and-replace, but note that turns every document-edit id into a document → many
transactions relationship, which today's `invoice.ledger_transaction_id` (a single nullable FK)
doesn't model.
