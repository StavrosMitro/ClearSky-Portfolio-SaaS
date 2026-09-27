#!/bin/sh
# Backups of the databases that are a source of truth (roadmap 3.1).
# grades_query is not backed up: it is rebuilt from grades_ingest by reconcile.
#
#   backup.sh          loop: one backup now, then every BACKUP_INTERVAL seconds
#   backup.sh once     one backup and exit
#
# Each run writes /backups/<db>/<UTC time>.dump (pg_dump custom format) and
# keeps the newest BACKUP_KEEP per database. Passwords come from
# <DB>_DB_PASSWORD (upper case), as in docker-compose.yml.
set -eu

DATABASES="identity institutions grades_ingest reviews"
INTERVAL="${BACKUP_INTERVAL:-86400}"
KEEP="${BACKUP_KEEP:-7}"

backup_all() {
	stamp=$(date -u +%Y%m%dT%H%M%SZ)
	status=0
	for db in $DATABASES; do
		dir="/backups/$db"
		mkdir -p "$dir"
		password=$(printenv "$(echo "$db" | tr a-z A-Z)_DB_PASSWORD")
		if PGPASSWORD="$password" pg_dump -h "${db}_db" -U "$db" -d "$db" -Fc -f "$dir/$stamp.dump.partial"; then
			mv "$dir/$stamp.dump.partial" "$dir/$stamp.dump"
			echo "{\"level\":\"INFO\",\"msg\":\"backup written\",\"service\":\"backup\",\"database\":\"$db\",\"file\":\"$db/$stamp.dump\"}"
		else
			rm -f "$dir/$stamp.dump.partial"
			echo "{\"level\":\"ERROR\",\"msg\":\"backup failed\",\"service\":\"backup\",\"database\":\"$db\"}"
			status=1
		fi
		# Keep the newest $KEEP dumps.
		ls -1 "$dir"/*.dump 2>/dev/null | sort -r | tail -n +"$((KEEP + 1))" | while read -r old; do rm -f "$old"; done
	done
	return $status
}

if [ "${1:-}" = "once" ]; then
	backup_all
	exit $?
fi
while true; do
	backup_all || true
	sleep "$INTERVAL"
done
