#!/usr/bin/env bash
# Tests for the install-time panel login (default admin/admin).
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

for SCRIPT in next-binary.sh next.sh; do
    (
        set -u
        colorized_echo() { :; }
        ui_section() { :; }
        ui_spinner_run() { shift; "$@"; }
        for fn in prompt_initial_admin_password prompt_initial_admin create_initial_admin_if_requested; do
            eval "$(sed -n "/^${fn}() {\$/,/^}\$/p" "$ROOT/$SCRIPT")"
        done
        eval "$(grep -m1 '^INITIAL_ADMIN_DEFAULT_USERNAME=' "$ROOT/$SCRIPT")"
        eval "$(grep -m1 '^INITIAL_ADMIN_DEFAULT_PASSWORD=' "$ROOT/$SCRIPT")"
        read_secret() { local line; IFS= read -r line; printf '%s' "$line"; }

        calls=""
        next_cli() { calls="$calls|$*"; [ -z "${CLI_FAIL:-}" ] || [ "$1" != admin ]; }

        # No terminal (piped install): admin/admin is created automatically.
        unset NEXT_ADMIN_USERNAME NEXT_ADMIN_PASSWORD
        prompt_initial_admin </dev/null
        check "$SCRIPT non-tty creates" "1" "$INITIAL_ADMIN_CREATE"
        check "$SCRIPT non-tty username" "admin" "$INITIAL_ADMIN_USERNAME"
        check "$SCRIPT non-tty password" "admin" "$INITIAL_ADMIN_PASSWORD"

        # Unattended override through the environment.
        NEXT_ADMIN_USERNAME=boss NEXT_ADMIN_PASSWORD='s3cret!' prompt_initial_admin </dev/null
        check "$SCRIPT env username" "boss" "$INITIAL_ADMIN_USERNAME"
        check "$SCRIPT env password" "s3cret!" "$INITIAL_ADMIN_PASSWORD"

        # Password prompt: Enter keeps the default, typed value needs confirmation.
        check "$SCRIPT enter = default" "admin" "$(printf '\n' | prompt_initial_admin_password)"
        check "$SCRIPT typed + confirmed" "MyPass1" "$(printf 'MyPass1\nMyPass1\n' | prompt_initial_admin_password)"
        check "$SCRIPT mismatch retries" "Good2" "$(printf 'a\nb\nGood2\nGood2\n' | prompt_initial_admin_password 2>/dev/null)"

        # Creation calls the CLI with a full-access role and the chosen password.
        INITIAL_ADMIN_CREATE=1 INITIAL_ADMIN_USERNAME=admin INITIAL_ADMIN_PASSWORD=admin
        calls=""
        create_initial_admin_if_requested
        check "$SCRIPT migrates first" "|migrate up|admin create admin --role full_access --password admin" "$calls"

        # An existing admin must not abort the install (set -e would exit here).
        CLI_FAIL=1
        set -e
        create_initial_admin_if_requested
        check "$SCRIPT survives existing admin" "reached" "reached"
        set +e
        unset CLI_FAIL

        # Declined / not requested: nothing runs.
        INITIAL_ADMIN_CREATE=0
        calls=""
        create_initial_admin_if_requested
        check "$SCRIPT no-op when not requested" "" "$calls"

        echo "$SCRIPT: $pass passed, $fail failed"
        [ "$fail" -eq 0 ]
    ) || fail=$((fail + 1))
done

[ "$fail" -eq 0 ]
