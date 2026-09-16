# denarix

A double-entry accounting system: a Postgres-backed ledger schema, a gRPC API server (`cmd/denarix`),
and a CLI client (`cmd/dxctl`). Covers core ledger accounting, parties, trading documents,
banking/reconciliation, period close, tax, and reporting, with passkey (WebAuthn)-based auth.

## Layout

- `migrations/` — the Postgres schema (single up migration, no down migrations by design)
- `proto/denarix/v1/` — gRPC service and message definitions (`buf generate` → `gen/denarix/v1/`)
- `sql/queries/` — sqlc query definitions (`sqlc generate` → `internal/db/sqlcgen/`)
- `internal/server/` — gRPC service implementations
- `internal/dxctl/`, `cmd/dxctl/` — CLI client
- `cmd/denarix/` — API server entrypoint
- `docs/` — architecture notes (`architecture.md`), schema reference (`schema.md`), and a Docker
  Compose quick start (`quickstart.md`)

## Development

```sh
make db-up        # start Postgres (+ SeaweedFS) via docker compose
make migrate-up    # apply the schema
make generate      # regenerate proto + sqlc code after touching proto/ or sql/queries/
make build         # go build ./...
make test          # go test ./...
make run           # go run ./cmd/denarix
make dxctl        # build ./bin/dxctl with version info stamped in
make install       # go install dxctl (with version stamped) to $GOBIN — prefer this over a bare `go install ./cmd/dxctl`
```

`VERSION` = `<VERSION file>.<total commit count>` — advances automatically as commits land, never
hand-bump the patch number.

## Adding a resource

Every resource is the same five pieces; each one is a table entry or a one-line builder call, so
copy the closest existing resource (contact for a plain CRUD noun, invoice for a document) and
follow this list rather than reverse-engineering a sibling file.

1. **Schema + queries.** Table in `migrations/00001_initial.up.sql` (edited in place, see
   docs/schema.md); `Create/Get/List/Update/Deactivate` queries in `sql/queries/<area>.sql`, with
   `resource_version` preconditions on the writes (copy `UpdateContact`). Children (line items,
   applications) also get a `List<Child>By<Parent>IDs` batch query. `make sqlc`.
2. **Proto.** One `message X` plus `Get/List/Create/Update/Deactivate` request/response pairs in
   `proto/denarix/v1/<area>.proto`, `resource_version` on every mutating request. `make proto`.
3. **Server** — one file, `internal/server/<x>_service.go`, registered in `server.go`:
   - Add `xRes = resourceDef[sqlcgen.X]{"x", (*sqlcgen.Queries).GetX, businessOf}` to
     `resources.go`. Every handler that takes an id then opens with
     `row, err := xRes.load(ctx, q, id, "MEMBER")` (404 + role check in one call); an id that
     arrives inside a request body is vetted with `xRes.requireInBusiness(ctx, q, businessID, id)`.
     If the resource can carry notes/attachments, add it to `entityRefChecks` in `entity_ref.go`.
   - `xToProto(row) *denarixv1.X` is a plain conversion (`moneypb.ToProto`, `datepb.ToProto`,
     `timestampProto` never fail). A resource with children gets `xsToProto(ctx, q, rows)` built on
     `withChildren` (children.go) so list handlers never query per row, and `xToProto` becomes
     `one(xsToProto(ctx, q, []sqlcgen.X{row}))`.
   - Errors: `translatePgError` after a single query, `translateUpdateError(err, xRes.kind, id,
     version)` after an optimistic-concurrency UPDATE, `txErrorStatus` after `store.ExecTx`. Never
     build a status by hand for those three cases (`pgerror.go`).
   - Document lines go through `buildDocumentLines` / `insertXLines` (`document_lines.go`); ledger
     entries through `internal/ledgerpost`.
4. **CLI** — one file, `internal/dxctl/cmd/<x>.go`, added to `root.go`:
   - `var xNoun = resource.Noun{...}` with columns as `resource.Int("ID", (*denarixv1.X).GetId)`,
     `resource.Str`, `resource.Money`, `resource.Bool`, `resource.Date`, `resource.OptInt`.
   - Verbs are builder calls: `newListCmd`, `newGetCmd` (+ optional pdf func), `newCreateCmd` /
     `newNoArgCmd`, `newMutateCmd`, `newVersionedMutateCmd` (adds `--resource-version`). A closure
     gets a `run` (`r.ctx`, `r.conn`, `r.businessID`, `r.cmd`) and returns `resp.GetX(), err`; lists
     return `toMessages(resp.GetXs()), err`. Optional request fields are `r.optString("flag", &v)`
     (and `optInt32/optInt64/optBool/optDecimal/optDate`) — nil unless the flag was passed.
   - Never parse an id or dial in a noun file; the builders do both.
   - Add the row to the resource reference table below.
5. **Tests.** Add a `crudCase` to `internal/server/crud_integration_test.go` (create / get /
   update / deactivate against the real handlers) and, for anything with ledger impact, extend
   `TestDocumentFlow`.

---

## dxctl

`dxctl` is the CLI client for the `denarix` gRPC API. Binary lives at `internal/dxctl/cmd` (built via
`make dxctl` / `make install`); source for each resource is one file under
`internal/dxctl/cmd/<resource>.go`.

Full command tree at any time: `dxctl commands` (flat list, good for grepping). Any command's
flags: `dxctl <command> --help`.

### Config & contexts

State lives in `~/.dxctl/config` (kubeconfig-style: clusters/users/contexts), never edit it by
hand — use `dxctl config`. A context bundles a server address with a business id.

```sh
dxctl config set-context dev --server localhost:9090 --insecure --business 1
dxctl config set-context prod --server denarix.example.com:443 --business 1
dxctl config use-context dev
dxctl config get-contexts     # list contexts
dxctl config view             # print resolved config
```

Global flags on every command override the active context for one call, they don't persist:

- `--server <addr>` / `--insecure` (skip TLS — only for a local dev server with no cert)
- `--business <id>`
- `-o, --output table|json|yaml|pdf` (default `table`; `pdf` only works on `report *`, `invoice get`, `estimate get`)

### Auth

```sh
dxctl login     # passkey (WebAuthn) ceremony via browser against the current context's server; stores the session in ~/.dxctl/config
dxctl whoami     # show the signed-in user
```

Requires a context to already exist (`config set-context` first). `~/.dxctl/config` ends up
holding a live access/refresh token per user — treat it like any other credentials file, never
commit or paste its contents.

### Conventions that hold across (almost) every resource

- **Aliases**: most resource nouns accept a plural and/or short alias, e.g. `business`/`businesses`/`biz`,
  `contact`/`contacts`, `invoice`/`invoices`/`inv`, `ledger-account`/`ledger-accounts`/`la`,
  `ledger-transaction`/`ledger-transactions`/`lt`, `bank-statement`/`bank-statements`/`bs`,
  `payment`/`payments`/`pay`, `estimate`/`estimates`/`est`, `item`/`items`, `tax-rate`/`tax-rates`.
- **CRUD shape**: `create`, `get <id>`, `list`, `update <id>` (only passed flags change — omitted
  flags leave the field alone), `deactivate <id>` (soft delete; the only hard deletes through the
  CLI are pure link rows — `bank-statement unreconcile` lines and a voided payment's applications).
- **Corrections never edit the ledger**: `ledger-transaction reverse`, `invoice cancel`, and
  `payment void` all post a *new* mirrored transaction and leave the original untouched. Each takes
  `--date` to post the reversal in the open period when the original sits in a closed one (default:
  the original's date; never earlier). A transaction can be reversed once, and a reversal can't be
  reversed. See `docs/architecture.md`, "Corrections model".
- **Optimistic concurrency**: mutating commands (`update`, `deactivate`, status transitions like
  `invoice send`) accept `--resource-version <n>` — the `VERSION` column from a prior `get`/`list`.
  Pass it to make the write fail if someone else changed the resource first; omit it to write
  unconditionally.
- **Money and dates**: amounts are decimal strings (`"150.00"`), dates are `YYYY-MM-DD`.
- **`list` visibility**: `list` defaults to active/open items only. Flags widen it per-resource:
  `--inactive` (contact, item), `--all` (ledger-account: include sub-accounts; invoice: include
  paid/cancelled; estimate: include accepted/declined/expired).

### Resource reference

| Resource | Subcommands | Notes |
|---|---|---|
| `business` | create, get, list, update, deactivate, invite create/list/revoke | `create`/`invite create` require global-admin (or OWNER/ADMIN for invites). `invite create` prints a one-time token to hand the invitee yourself — denarix never emails it, and it's never shown again. |
| `contact` | create, get, list, update, deactivate | A contact can be `--customer` and/or `--vendor`; each side auto-provisions its own AR/AP sub-ledger account (named after the contact, under the business's system Accounts Receivable/Payable container) - there's no flag to point it at an existing account instead. |
| `item` | create, get, list, update, deactivate | Catalog entry. `--type SERVICE\|NON_INVENTORY\|INVENTORY`. `--default-ledger-account-id` is **required** — it's the account every invoice line for this item posts to (not overridable per line). `--default-tax-rate-id` / `--price` / `--taxable` / `--name` are the defaults that `estimate`/`invoice` `--line item=<id>` pulls from. Deactivated items can't go on new lines. |
| `ledger-account` | create, get, list, update, deactivate | Chart of accounts. `--account-type` is required: `1=ASSETS 2=LIABILITIES 3=EQUITY 4=REVENUE 5=EXPENSES 6=TAX_LIABILITY`. `--container` marks a non-postable roll-up node (e.g. "Fixed Assets") with real accounts hung off it via `--parent` - don't hand-create an "Accounts Receivable"/"Accounts Payable" container yourself, those are auto-provisioned system accounts at codes `1100`/`2000` (see the `contact` row above). `--reconcilable` makes it eligible for `bank-statement`. |
| `ledger-transaction` | get, list, post, reverse | `list` is newest-first and takes AND-combined filters: `--account <id>`, `--start`/`--end` (YYYY-MM-DD), `--description-contains <text>` (case-insensitive, transaction description only); `--limit` defaults to 50, `--limit 0` pages through everything. `post` takes ≥2 `--entry account=<id>,debit=<amt>` / `,credit=<amt>` flags; posting is atomic and validated balanced. There is no edit or delete — `reverse <id> [--date]` posts a new mirrored transaction (the `REVERSES` column shows the link). `reverse` refuses a transaction linked from an invoice or payment: use `invoice cancel` / `payment void` for those. |
| `estimate` | create, get, list, update, update-lines, send, accept, decline, expire | `update` edits only notes/terms/expiration date (`--notes`, `--terms`, `--expires`; customer, estimate date, and number are recreate). Lines: repeat `--line "item=<id>[,desc=...][,qty=][,price=][,taxable][,tax-rate=<id>]"`. `item=` is **required** on every line (no free-text lines); desc/price/taxable/tax-rate default from the item's catalog entry when omitted. Unknown keys are rejected. `update-lines` replaces the *entire* line set. |
| `invoice` | create, get, list, update, update-lines, send, cancel, mark-overdue | `--type SALES\|PURCHASE`. Same `--line` syntax as `estimate`: `item=` is **required** and the line always posts to that item's `default_ledger_account_id` — there is no `account=` key. The contact needs a matching customer/vendor ledger account. **Creating an invoice posts it to the ledger atomically.** `--estimate <id>` with no `--line` flags converts an estimate's lines over instead of specifying lines by hand. `update` edits only notes/terms/due date (contact, invoice date, and number are cancel-and-recreate). `update-lines` on an already-posted invoice regenerates its linked transaction's entries in place (and needs `item=` on every line, including on pre-catalog invoices); both refuse a CANCELLED invoice, and `update-lines` refuses a PAID one (void the payment first). `cancel [--date]` reverses the invoice's posting and zeroes its balance; it refuses while a payment is still applied. `get -o pdf > file.pdf` renders the invoice as PDF. There's no `discount_amount` field — a discount is a negative line against its own catalog item (e.g. an item pointed at a "Sales Discounts" account), posted as a contra-entry: `--line "item=<discount-id>,price=-100.00"`. A document's total may not go negative. (If you instead give the discount item itself a negative default `--price`, pass it as `--price=-100.00` — `--price -100.00` fails, since pflag reads the leading `-` as another flag.) |
| `payment` | create, get, list, void | `--apply invoice_id:amount` (repeatable) applies the payment across one or more invoices; each must be SENT or OVERDUE (send a DRAFT first) and the amount can't exceed that invoice's remaining balance. Add `--account <id>` (cash/bank account) to post the payment to the ledger atomically in the same call. `void <id> [--date]` removes the applications (a PAID invoice goes back to SENT), reverses the posting if there was one, and soft-deletes the payment — its payment number is not reusable. |
| `bank-statement` | create, get, list, update, deactivate, reconcile, unreconcile, unreconciled | `create` needs `--account` (a `--reconcilable` ledger account); its opening balance must equal the prior statement's closing balance on that account unless `--allow-opening-mismatch` (first statement, or a mid-history import). `update` re-checks that only when the opening balance or date changes. `unreconciled --account --through <date>` lists candidate ledger transactions; `reconcile <id> --transaction <id> [...]` links them to the statement and `unreconcile` unlinks them. `deactivate` refuses while any line is still reconciled. |
| `close` | trigger, reverse, list | `trigger --through <date>` closes the books through that date; `reverse <period-close-id>` undoes one — only the latest unreversed close, there's no cascade through stacked closes (reverse newest-first, then re-close forward); `list` shows close history. |
| `tax-rate` | create, get, list, update, deactivate | `--rate` is a decimal fraction (`0.0825` = 8.25%), posted into `--liability-account` (a `TAX_LIABILITY` ledger account). |
| `report` | balance-sheet, trial-balance, income-statement, general-ledger, customer-statement | All take date range/as-of flags and default to today; `general-ledger` needs `--account`, `customer-statement` needs `--contact`. Support `-o pdf`. |
| `context` | attach, download, get, get-attachment, list, note, remove-note, remove-attachment | Generic AI/user annotations + file attachments on *any* entity (`--entity-type invoice --entity-id 42`, etc). `note` records a `summary`/`categorization_hint`/`anomaly`/`user_note` (`--context-type`), optionally `--supersedes` an older note; `remove-note` soft-deletes one and un-hides whatever it superseded. `attach`/`download` stream file bytes through denarix's own object storage — there's no direct storage URL. |
| `admin` | grant, revoke | Global-admin-only. There is exactly one global admin at a time; `grant --user <id>` transfers it, `revoke --user <id>` leaves zero until someone is granted it again. |
| `accept-invite` | — | `dxctl accept-invite <token>` — redeem a `business invite create` token; you must already be logged in as the invited email. |

### Common workflows

```sh
# Stand up a chart of accounts entry, then post an opening balance
dxctl ledger-account create --code 1000 --name Cash --account-type 1 --reconcilable
dxctl ledger-account create --code 3000 --name "Opening Balance Equity" --account-type 3
dxctl ledger-transaction post --date 2026-01-01 \
  --entry account=1,debit=10000.00 --entry account=2,credit=10000.00

# Customer + catalog item + invoice + payment
dxctl contact create --contact-number C-1 --name "Acme Co"
dxctl item create --code CONSULT --name Consulting --price 150.00 --default-ledger-account-id 40
dxctl invoice create --contact 5 --type SALES --date 2026-01-01 --due 2026-01-31 \
  --line "item=71,qty=10"
dxctl payment create --contact 5 --type RECEIVED --number PAY-1 --date 2026-01-15 \
  --amount 1500.00 --method CASH --apply 42:1500.00 --account 1

# Estimate → invoice conversion
dxctl estimate create --customer 5 --date 2026-01-01 --expires 2026-02-01 \
  --line "item=71,qty=10"
dxctl estimate send 12
dxctl estimate accept 12
dxctl invoice create --contact 5 --type SALES --date 2026-01-05 --due 2026-02-05 --estimate 12

# Find an account's history without going through the reporting layer
dxctl ledger-transaction list --account 81 --start 2024-01-01 --description-contains "sales tax" --limit 0

# Reports
dxctl report trial-balance --as-of 2026-01-31
dxctl report income-statement --start 2026-01-01 --end 2026-01-31
dxctl report balance-sheet --as-of 2026-01-31 -o pdf > balance-sheet.pdf

# Bank reconciliation
dxctl bank-statement create --account 1 --name "Jan 2026" --date 2026-01-31 --opening 10000.00 --closing 11500.00
dxctl bank-statement unreconciled --account 1 --through 2026-01-31
dxctl bank-statement reconcile 3 --transaction 12 --transaction 13
```
