#!/usr/bin/env bash
# Tests for the Telegram backup schedule: a saved configuration that never
# runs is the failure this guards against.
set -uo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
pass=0
fail=0
check() {
    if [ "$2" = "$3" ]; then
        pass=$((pass + 1))
    else
        fail=$((fail + 1))
        printf 'FAIL: %s\n  expected: %s\n  actual:   %s\n' "$1" "$2" "$3"
    fi
}
contains() {
    if printf '%s' "$2" | grep -qF -- "$3"; then
        pass=$((pass + 1))
    else
        fail=$((fail + 1))
        printf 'FAIL: %s\n  missing: %s\n  in:      %s\n' "$1" "$3" "$2"
    fi
}

for SCRIPT in next.sh next-binary.sh; do
    (
        set -u
        messages=""
        colorized_echo() { messages="$messages|$2"; }
        detect_os() { :; }
        for fn in ensure_cron_daemon add_cron_job; do
            eval "$(sed -n "/^${fn}() {\$/,/^}\$/p" "$ROOT/$SCRIPT")"
        done

        # cron missing and not installable: the caller must be told, not left
        # with a schedule that silently never fires.
        installed=""
        install_package() { installed="$installed|$1"; return 1; }
        command() { if [ "${2:-}" = "crontab" ]; then return 1; fi; builtin command "$@"; }
        systemctl() { return 1; }
        pgrep() { return 1; }
        messages=""
        ensure_cron_daemon
        check "$SCRIPT reports missing cron" "1" "$?"
        contains "$SCRIPT tries to install cron" "$installed" "cron"
        contains "$SCRIPT says a backup cannot run" "$messages" "cannot run"

        # cron present but the daemon is dead: still a failure, because the
        # job would be stored and never executed.
        command() { builtin command "$@"; }
        crontab() { return 0; }
        systemctl() { return 1; }
        pgrep() { return 1; }
        messages=""
        ensure_cron_daemon
        check "$SCRIPT reports a stopped cron daemon" "1" "$?"
        contains "$SCRIPT says the daemon is not running" "$messages" "not running"

        # A running daemon found through pgrep is good enough.
        pgrep() { [ "${2:-}" = "cron" ]; }
        ensure_cron_daemon
        check "$SCRIPT accepts a running cron" "0" "$?"

        # add_cron_job refuses rather than pretending, and writes the job when
        # cron is healthy.
        ensure_cron_daemon() { return 1; }
        add_cron_job "0 */6 * * *" "/usr/local/bin/next backup" >/dev/null 2>&1
        check "$SCRIPT add_cron_job fails without cron" "1" "$?"

        written=""
        ensure_cron_daemon() { return 0; }
        crontab() {
            if [ "${1:-}" = "-l" ]; then
                return 1
            fi
            written="$(cat "$1")"
            return 0
        }
        messages=""
        add_cron_job "0 */6 * * *" "/usr/local/bin/next backup"
        check "$SCRIPT add_cron_job succeeds with cron" "0" "$?"
        contains "$SCRIPT writes the schedule" "$written" "0 */6 * * * /usr/local/bin/next backup"
        contains "$SCRIPT tags the job for removal" "$written" "# next-backup-service"

        printf '%s: %d passed, %d failed\n' "$SCRIPT" "$pass" "$fail"
        [ "$fail" -eq 0 ]
    ) || exit 1
done

echo "backup service tests passed"
