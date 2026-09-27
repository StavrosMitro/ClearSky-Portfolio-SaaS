#!/bin/sh
# Restore test (roadmap 3.1): restores the newest dump of every backed-up
# database into a throw-away PostgreSQL server inside this container and
# checks that the main tables came back. Touches no live database.
#
#   docker compose exec backup sh /scripts/restore-check.sh
set -eu

scratch=$(mktemp -d)
trap 'pg_ctl -D "$scratch/data" -m immediate stop >/dev/null 2>&1 || true; rm -rf "$scratch"' EXIT
chown postgres "$scratch"
su postgres -c "initdb -D '$scratch/data' -A trust >/dev/null && pg_ctl -D '$scratch/data' -o '-p 5499 -k $scratch -c listen_addresses=' -l '$scratch/log' -w start >/dev/null"

failed=0
check() { # database, table
	count=$(psql -h "$scratch" -p 5499 -U postgres -d "$1" -Atc "SELECT count(*) FROM $2")
	echo "  $1.$2: $count rows"
}
for db in identity institutions grades_ingest reviews; do
	latest=$(ls -1 "/backups/$db"/*.dump 2>/dev/null | sort -r | head -n 1 || true)
	if [ -z "$latest" ]; then
		echo "$db: no backup found"; failed=1; continue
	fi
	echo "$db: restoring $(basename "$latest")"
	createdb -h "$scratch" -p 5499 -U postgres "$db"
	if ! pg_restore -h "$scratch" -p 5499 -U postgres -d "$db" --no-owner "$latest"; then
		echo "$db: restore failed"; failed=1; continue
	fi
	case $db in
		identity) check identity users; check identity student_roster ;;
		institutions) check institutions institutions; check institutions credit_ledger ;;
		grades_ingest) check grades_ingest gradings; check grades_ingest grades ;;
		reviews) check reviews review_requests ;;
	esac
done
[ $failed -eq 0 ] && echo "restore check passed" || echo "restore check FAILED"
exit $failed
