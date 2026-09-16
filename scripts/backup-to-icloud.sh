#!/usr/bin/env bash
# Dumps the denarix Postgres database to a timestamped, gzipped file inside
# iCloud Drive (or wherever $1 points), so Apple's sync picks it up and
# backs it up off-machine automatically.
#
# Deliberately takes a logical pg_dump rather than syncing the running
# db_data Docker volume directly: that volume is Postgres's live data
# directory (WAL included), and a sync client rewriting/evicting those
# files out from under a running database - even one that's just idle,
# since checkpoints and autovacuum still touch it - risks corrupting it.
# A pg_dump is one complete, static file the moment it's written, so it's
# safe to hand to iCloud right after.
#
# Usage: scripts/backup-to-icloud.sh [dest-dir] [keep-count]
# Run from the repo root (or set COMPOSE_FILE) so `docker compose exec`
# finds the right project. See docs/quickstart.md for a launchd example
# that runs this nightly.
set -euo pipefail

dest="${1:-$HOME/Library/Mobile Documents/com~apple~CloudDocs/denarix-backups}"
keep="${2:-30}"

mkdir -p "$dest"
stamp="$(date +%Y%m%d-%H%M%S)"
out="$dest/denarix-$stamp.sql.gz"
tmp="$out.partial"
trap 'rm -f -- "$tmp"' EXIT # clean up a failed run's partial file - the mv below outruns this on success

docker compose exec -T db pg_dump -U denarix -d denarix | gzip > "$tmp"
mv "$tmp" "$out" # atomic rename - iCloud only ever sees the finished file

# Prune old backups, keeping the most recent $keep.
ls -1t "$dest"/denarix-*.sql.gz 2>/dev/null | tail -n "+$((keep + 1))" | while IFS= read -r old; do
  rm -f -- "$old"
done

echo "backed up to $out"
