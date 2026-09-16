# Quick start

Runs the whole stack (Postgres, SeaweedFS object storage, and the `denarix` API server) in Docker
Compose, then drives it with `dxctl` from your host. For the day-to-day dev loop (running `denarix`
outside Docker, `make generate`, tests, etc.) see the main [README](../README.md) and
[CLAUDE.md](../CLAUDE.md) instead - this doc is for standing up a working instance with the least
ceremony.

## 1. Configure

```sh
cp .env.example .env
```

Edit `.env`:

- `DENARIX_JWT_SECRET` - required, the server won't start without it. Generate one with
  `openssl rand -base64 32`.
- `DENARIX_BOOTSTRAP_ADMIN_EMAIL` - set this to the email you'll sign in with. On first boot (only
  while no global admin exists yet) denarix grants that email global-admin, which is what lets you
  create the first business.

Leave `DENARIX_PUBLIC_BASE_URL` / `DENARIX_RP_ID` at their `localhost` defaults unless you're exposing this
beyond your own machine.

## 2. Start the stack

```sh
docker compose up -d --build
docker compose logs -f denarix   # watch it come up; Ctrl-C once you see "starting denarix"
```

This builds `denarix` from the repo's `Dockerfile`, starts Postgres and SeaweedFS, runs migrations
once (the `migrate` service), then starts the API server on `localhost:9090` (gRPC, what `dxctl`
talks to) and `localhost:9091` (plain HTTP, the passkey login pages).

## 3. Install dxctl

`dxctl` isn't shipped in the Docker image - it's the client you run locally against the server
above. Needs a Go toolchain:

```sh
make install     # builds with version info stamped in, installs to $GOBIN
```

## 4. Log in and create your first business

A context needs a business id up front, so point it at a placeholder (`0`) until one exists:

```sh
dxctl config set-context dev --server localhost:9090 --insecure --business 0
dxctl config use-context dev
dxctl login      # opens your browser to localhost:9091/auth/start
```

Click **Create a passkey** and register with the same email as `DENARIX_BOOTSTRAP_ADMIN_EMAIL` - since
that email was pre-seeded as an admin, no invite token is needed for this first account (any
account after this one does need an invite: `dxctl business invite create`).

```sh
dxctl whoami
dxctl business create --name "My Company"     # note the returned id
dxctl config set-context dev --server localhost:9090 --insecure --business <id>
dxctl config use-context dev
```

You're now set up for the "Common workflows" section in [CLAUDE.md](../CLAUDE.md) - chart of
accounts, contacts, invoices, payments, reports.

## 5. Set up a chart of accounts

A new business gets four system accounts automatically, at `business create` time: `3900 Retained
Earnings` / `3910 Income Summary` (close depends on these, see `docs/architecture.md#period-close`)
and `1100 Accounts Receivable` / `2000 Accounts Payable` (roll-up containers - `contact create
--customer`/`--vendor` auto-provisions each contact's own sub-account underneath, named after the
contact; there's no way to point a contact at a different account). Everything else (Cash, Revenue,
Expenses, tax liabilities, fixed assets) is deliberately left for you to set up, since it's
genuinely business-specific rather than something the server should assume.

The fastest way to get the rest of a working chart of accounts is to ask Claude Code to build it
for you with `dxctl ledger-account create` calls, one per account - it already knows this
codebase's conventions (account types, the category ids, the 1000s/2000s/3000s/4000s/5000s/6000s
numbering) from this file and `CLAUDE.md`. For example:

```
Using dxctl against my local context, create the rest of a standard QuickBooks-style chart of
accounts for this business (AR/AP already exist as system accounts): checking/savings, fixed
assets, a sales-tax-payable liability, opening balance equity, revenue, COGS, and the common
operating expense accounts. Follow the numbering convention already used by this schema.
```

That's exactly how the accounts in this guide's examples were created - live `dxctl` calls
against a running instance, not a migration or server change (see `CLAUDE.md`'s "Adding a
resource" section for the boundary between what's a server change and what's a script over the
API). Ask it to run `dxctl report trial-balance` afterward to confirm everything landed
with zero balances, and `dxctl ledger-account list --all` to review the result before you start
posting real transactions.

## Stopping / resetting

```sh
docker compose down        # stop everything, keep data
docker compose down -v     # stop everything AND delete the Postgres/SeaweedFS volumes
```

---

## Backing up your data to iCloud Drive

`docker-compose.yml` stores Postgres's data in the `db_data` Docker volume (on disk it's prefixed
with the Compose project name, which defaults to the checkout directory's name - e.g. `denarix_db_data`). **Don't point that
volume (or a sync client) at iCloud Drive directly** - it's a live database's data directory
(WAL files included), and having iCloud rewrite or evict files out from under it while Postgres is
running risks corrupting it, not backing it up. The safe version of "back this up to iCloud" is a
scheduled logical dump: one complete, static file that's safe to hand to iCloud the moment it's
written.

[`scripts/backup-to-icloud.sh`](../scripts/backup-to-icloud.sh) does that - `pg_dump` through
`docker compose exec`, gzipped, written atomically into
`~/Library/Mobile Documents/com~apple~CloudDocs/denarix-backups/`, pruning to the last 30 by default:

```sh
./scripts/backup-to-icloud.sh                 # default dest + keep-30
./scripts/backup-to-icloud.sh /some/other/dir 10   # custom dest, keep-10
```

### Automate it with launchd

Run it nightly with a `launchd` user agent (`cron` also works, but launchd is the macOS-native
way and survives sleep/reboot better). Save as `~/Library/LaunchAgents/com.denarix.backup.plist`,
substituting your actual repo path:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.denarix.backup</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>-lc</string>
    <string>cd /Users/you/code/denarix && ./scripts/backup-to-icloud.sh</string>
  </array>
  <key>StartCalendarInterval</key>
  <dict><key>Hour</key><integer>2</integer><key>Minute</key><integer>0</integer></dict>
  <key>StandardOutPath</key><string>/tmp/denarix-backup.log</string>
  <key>StandardErrorPath</key><string>/tmp/denarix-backup.log</string>
</dict>
</plist>
```

```sh
launchctl load ~/Library/LaunchAgents/com.denarix.backup.plist
```

The `docker compose` service needs to be up at 2am for this to work (Docker Desktop launching at
login is enough - the `db` container itself only needs to be running, not `denarix`).

### Restoring

Into a fresh stack (this wipes whatever's currently in `db`):

```sh
docker compose down -v && docker compose up -d db
# wait for it to report healthy, then:
gunzip -c "$HOME/Library/Mobile Documents/com~apple~CloudDocs/denarix-backups/denarix-<stamp>.sql.gz" \
  | docker compose exec -T db psql -U denarix -d denarix
docker compose up -d --build
```

### Attachments

File attachments (`context attach`) live in SeaweedFS's `seaweed_data` volume (`denarix_seaweed_data` on disk, same prefix rule), not Postgres -
`pg_dump` doesn't cover them. If you use attachments and want them backed up too, snapshot that
volume into a single tar file the same way (a completed tar file is just as safe to hand to iCloud
as a completed `pg_dump`):

```sh
docker run --rm -v denarix_seaweed_data:/data:ro \
  -v "$HOME/Library/Mobile Documents/com~apple~CloudDocs/denarix-backups":/backup \
  alpine tar czf "/backup/seaweed-$(date +%Y%m%d-%H%M%S).tar.gz" -C / data
```
