# denarix

A double-entry accounting system you host yourself, so you keep complete control over your own
data. It ships as a Postgres-backed ledger schema, a gRPC API server (`cmd/denarix`), and a CLI
client (`cmd/dxctl`). Covers core ledger accounting, parties, trading documents,
banking/reconciliation, period close, tax, and reporting, with passkey (WebAuthn)-based auth.

## Quick start

Want to run this rather than develop it? See [docs/quickstart.md](docs/quickstart.md) - stands up
the whole stack with `docker compose`, connects `dxctl`, and covers backing your data up to
iCloud Drive.

## Layout

- `migrations/` — the Postgres schema (single up migration, no down migrations by design)
- `proto/denarix/v1/` — gRPC service and message definitions (`buf generate` → `gen/denarix/v1/`)
- `sql/queries/` — sqlc query definitions (`sqlc generate` → `internal/db/sqlcgen/`)
- `internal/server/` — gRPC service implementations
- `internal/ledgerpost/` — shared ledger-posting primitives (e.g. reversing transactions)
- `internal/dxctl/`, `cmd/dxctl/` — CLI client
- `cmd/denarix/` — API server entrypoint
- `docs/` — architecture notes and schema reference

## Development

```sh
make db-up        # start Postgres (+ SeaweedFS) via docker compose
make migrate-up    # apply the schema
make generate      # regenerate proto + sqlc code after touching proto/ or sql/queries/
make build         # go build ./...
make test          # go test ./...
make run           # go run ./cmd/denarix
```

## License

MIT — see [LICENSE](LICENSE). All source files carry an SPDX `MIT` identifier.
