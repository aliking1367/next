#!/usr/bin/env bash
# Tests for the `--dev` channel: it must never depend on a branch that does not
# exist, and a missing dev build must fall back to the stable release instead of
# failing with an empty download URL.
set -uo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
fail=0
check() {
    if [ "$2" = "$3" ]; then
        return
    fi
    fail=$((fail + 1))
    printf 'FAIL: %s\n  expected: %s\n  actual:   %s\n' "$1" "$2" "$3"
}

for SCRIPT in next-binary.sh next.sh; do
    (
        set -u
        colorized_echo() { :; }
        NEXT_REPO="aliking1367/next"
        NEXT_SCRIPT_BASE_URL_EXPLICIT=0
        eval "$(grep -m1 '^NEXT_BINARY_DEV_BRANCH=' "$ROOT/$SCRIPT")"
        for fn in resolve_binary_dev_metadata set_next_source_ref set_next_source_for_version; do
            eval "$(sed -n "/^${fn}() {\$/,/^}\$/p" "$ROOT/$SCRIPT")"
        done

        # The dev channel points at a branch that exists in the repository.
        check "$SCRIPT dev branch is master" "master" "$NEXT_BINARY_DEV_BRANCH"
        set_next_source_for_version dev
        check "$SCRIPT dev script source" "https://raw.githubusercontent.com/aliking1367/next/master/scripts/next" "$NEXT_SCRIPT_BASE_URL"
        set_next_source_for_version dev-abc1234
        check "$SCRIPT dev-<sha> script source" "https://raw.githubusercontent.com/aliking1367/next/master/scripts/next" "$NEXT_SCRIPT_BASE_URL"

        # A published dev build is used as-is.
        get_binary_dev_artifact_metadata() { printf 'dev-abc1234|https://example.test/next.tar.gz|next.tar.gz\n'; }
        out=$(resolve_binary_dev_metadata amd64 dev); st=$?
        check "$SCRIPT found: status" "0" "$st"
        check "$SCRIPT found: metadata" "dev-abc1234|https://example.test/next.tar.gz|next.tar.gz" "$out"

        # No dev build yet: `--dev` falls back to stable (status 2, no output).
        get_binary_dev_artifact_metadata() { return 1; }
        out=$(resolve_binary_dev_metadata amd64 dev 2>/dev/null); st=$?
        check "$SCRIPT missing dev: status" "2" "$st"
        check "$SCRIPT missing dev: no metadata" "" "$out"

        # An empty answer counts as missing, never as a usable build.
        get_binary_dev_artifact_metadata() { return 0; }
        resolve_binary_dev_metadata amd64 dev >/dev/null 2>&1; st=$?
        check "$SCRIPT empty answer: status" "2" "$st"

        # A specific dev-<sha> that does not exist is an error, not a silent stable install.
        get_binary_dev_artifact_metadata() { return 1; }
        resolve_binary_dev_metadata amd64 dev-abc1234 >/dev/null 2>&1; st=$?
        check "$SCRIPT missing dev-sha: status" "1" "$st"

        # The resolver may not run inside a process substitution again.
        if grep -q '< <(get_binary_dev_artifact_metadata' "$ROOT/$SCRIPT"; then
            check "$SCRIPT installer avoids process-substitution exit" "none" "found"
        fi

        [ "$fail" -eq 0 ]
    ) || fail=$((fail + 1))
done

if [ "$fail" -eq 0 ]; then
    echo "dev channel tests passed"
fi
[ "$fail" -eq 0 ]
