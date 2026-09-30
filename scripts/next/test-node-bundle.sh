#!/usr/bin/env bash
# Tests for supplying a node install bundle from a file instead of pasting it.
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

SCRIPT=next-node-binary.sh
(
    set -u
    messages=""
    colorized_echo() { messages="$messages|$2"; }
    for fn in write_node_certificate_from_bundle read_node_certificate_bundle load_node_certificate_bundle; do
        eval "$(sed -n "/^${fn}() {\$/,/^}\$/p" "$ROOT/$SCRIPT")"
    done

    work=$(mktemp -d)
    CERT_FILE="$work/cert.pem"
    CERT_KEY_FILE="$work/cert.key"
    bundle="$work/bundle.txt"

    cat > "$bundle" <<'BUNDLE'
Node install bundle
-----BEGIN CERTIFICATE-----
MIIByDCCAW6gAwIBAgIUEXAMPLEEXAMPLEEXAMPLEEXAMPLEwCgYIKoZIzj0EAwIw
-----END CERTIFICATE-----
-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgEXAMPLEEXAMPLEEX
-----END PRIVATE KEY-----
BUNDLE

    # A bundle read from a file needs no paste at all.
    NODE_BUNDLE_FILE="$bundle"
    ( load_node_certificate_bundle ) >/dev/null 2>&1
    check "$SCRIPT accepts a bundle file" "0" "$?"
    check "$SCRIPT writes the certificate" "1" "$(grep -c -- '-----END CERTIFICATE-----' "$CERT_FILE" 2>/dev/null || true)"
    check "$SCRIPT writes the private key" "1" "$(grep -c -- '-----END PRIVATE KEY-----' "$CERT_KEY_FILE" 2>/dev/null || true)"
    # The certificate and the key must not leak into each other's file.
    check "$SCRIPT keeps the key out of the cert" "0" "$(grep -c -- 'PRIVATE KEY' "$CERT_FILE" 2>/dev/null || true)"
    check "$SCRIPT keeps the cert out of the key" "0" "$(grep -c -- 'BEGIN CERTIFICATE' "$CERT_KEY_FILE" 2>/dev/null || true)"

    # A missing file is reported instead of silently falling back to a prompt,
    # which on a non-interactive install would hang forever.
    rm -f "$CERT_FILE" "$CERT_KEY_FILE"
    NODE_BUNDLE_FILE="$work/not-here.txt"
    messages=""
    ( load_node_certificate_bundle ) >/dev/null 2>&1
    check "$SCRIPT fails on a missing bundle file" "1" "$?"

    # Half a bundle must never be installed: a node with a certificate but no
    # key starts and then fails every handshake.
    printf -- '-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----\n' > "$work/half.txt"
    NODE_BUNDLE_FILE="$work/half.txt"
    ( load_node_certificate_bundle ) >/dev/null 2>&1
    check "$SCRIPT rejects a bundle without a key" "1" "$?"
    check "$SCRIPT leaves no partial cert behind" "0" "$([ -f "$CERT_FILE" ] && echo 1 || echo 0)"

    # Without a bundle file the interactive paste still works.
    NODE_BUNDLE_FILE=""
    ( load_node_certificate_bundle < "$bundle" ) >/dev/null 2>&1
    check "$SCRIPT still accepts a pasted bundle" "0" "$?"
    check "$SCRIPT writes the pasted certificate" "1" "$(grep -c -- '-----END CERTIFICATE-----' "$CERT_FILE" 2>/dev/null || true)"

    rm -rf "$work"
    printf '%s: %d passed, %d failed\n' "$SCRIPT" "$pass" "$fail"
    [ "$fail" -eq 0 ]
) || exit 1

echo "node bundle tests passed"
