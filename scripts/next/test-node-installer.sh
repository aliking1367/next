#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

colorized_echo() { :; }

for SCRIPT in "$ROOT/next-node.sh" "$ROOT/next-node-binary.sh"; do
    eval "$(sed -n '/^select_node_version() {$/,/^}$/p' "$SCRIPT")"
    select_node_version dev
    [ "$SELECTED_NODE_VERSION" = "dev" ]
    select_node_version latest
    [ "$SELECTED_NODE_VERSION" = "latest" ]

    eval "$(sed -n '/^read_node_certificate_bundle() {$/,/^}$/p' "$SCRIPT")"
    CERT_FILE="$TMP/cert.pem"
    CERT_KEY_FILE="$TMP/cert.key"
    BUNDLE_FILE="$TMP/bundle.pem"
    rm -f "$CERT_FILE" "$CERT_KEY_FILE"

    printf '%s\r\n' \
        '-----BEGIN CERTIFICATE-----' \
        'certificate' \
        '-----END CERTIFICATE-----' \
        '-----BEGIN PRIVATE KEY-----' \
        'private-key' >"$BUNDLE_FILE"
    printf '%s\r' '-----END PRIVATE KEY-----' >>"$BUNDLE_FILE"
    read_node_certificate_bundle <"$BUNDLE_FILE"

    grep -qx -- '-----END CERTIFICATE-----' "$CERT_FILE"
    grep -qx -- '-----END PRIVATE KEY-----' "$CERT_KEY_FILE"
    if [[ "$(uname -s)" == Linux* ]]; then
        [ "$(stat -c '%a' "$CERT_KEY_FILE")" = "600" ]
    fi

    eval "$(sed -n '/^stop_next_node_for_uninstall() {$/,/^}$/p' "$SCRIPT")"
    detect_calls=0
    down_calls=0
    detect_compose() { detect_calls=$((detect_calls + 1)); }
    down_next_node() { down_calls=$((down_calls + 1)); }
    is_next_node_up() { return 1; }
    install_mode=docker
    stop_next_node_for_uninstall
    [ "$detect_calls" -eq 1 ]
    [ "$down_calls" -eq 1 ]

    down_calls=0
    install_mode=binary
    stop_next_node_for_uninstall
    [ "$down_calls" -eq 0 ]
    is_next_node_up() { return 0; }
    stop_next_node_for_uninstall
    [ "$down_calls" -eq 1 ]
done

# ---- release asset resolution (needs jq; CI runners have it) -----------------
if command -v jq >/dev/null 2>&1; then
    FIXTURE="$TMP/releases.json"
    # Newest release has no node binary yet; the one before it does.
    cat >"$FIXTURE" <<'JSON'
[
  {"tag_name": "v1.5.0", "draft": false, "prerelease": false,
   "assets": [{"name": "rebecca-telemt-linux-amd64", "browser_download_url": "https://x/telemt"}]},
  {"tag_name": "v1.5.0-rc1", "draft": false, "prerelease": true,
   "assets": [{"name": "rebecca-node-v1.5.0-rc1-linux-amd64", "browser_download_url": "https://x/rc"}]},
  {"tag_name": "v1.4.0", "draft": false, "prerelease": false,
   "assets": [{"name": "rebecca-node-v1.4.0-linux-amd64.sha256", "browser_download_url": "https://x/sum"},
              {"name": "rebecca-node-v1.4.0-linux-amd64", "browser_download_url": "https://x/node-amd64"},
              {"name": "rebecca-node-v1.4.0-linux-arm64", "browser_download_url": "https://x/node-arm64"}]}
]
JSON
    for SCRIPT in "$ROOT/next-node.sh" "$ROOT/next-node-binary.sh"; do
        eval "$(sed -n '/^get_node_binary_release_asset_metadata() {$/,/^}$/p' "$SCRIPT")"
        NEXT_NODE_RELEASE_REPO="rebeccapanel/Rebecca-node"
        NEXT_NODE_ASSET_PREFIX="rebecca-node"
        sleep() { :; }
        curl() {
            case "$*" in
                *"/releases?per_page=30"*) cat "$FIXTURE" ;;
                *"/releases/tags/v1.4.0"*) jq '.[2]' "$FIXTURE" ;;
                *"/releases/tags/v1.5.0"*) jq '.[0]' "$FIXTURE" ;;
                *) return 22 ;;
            esac
        }
        [ "$(get_node_binary_release_asset_metadata latest amd64)" = "v1.4.0|https://x/node-amd64" ]
        [ "$(get_node_binary_release_asset_metadata latest arm64)" = "v1.4.0|https://x/node-arm64" ]
        [ "$(get_node_binary_release_asset_metadata v1.4.0 amd64)" = "v1.4.0|https://x/node-amd64" ]
        if get_node_binary_release_asset_metadata v1.5.0 amd64 >/dev/null 2>&1; then
            echo "a release without the node binary must fail" >&2
            exit 1
        fi
        if get_node_binary_release_asset_metadata latest s390x >/dev/null 2>&1; then
            echo "a missing architecture must fail" >&2
            exit 1
        fi
        unset -f curl sleep
    done
    echo "node release asset resolution ok"
else
    echo "jq not installed; skipping release asset resolution checks"
fi

# ---- node name from the installed command (custom node names) ---------------
NAME_SNIPPET="$(sed -n '/^SCRIPT_DEFAULT_APP_NAME=/,/^fi$/p' "$ROOT/next-node-binary.sh"); printf '%s' \"\$SCRIPT_DEFAULT_APP_NAME\""
[ "$(bash -c "$NAME_SNIPPET" /usr/local/bin/mynode)" = "mynode" ]
[ "$(bash -c "$NAME_SNIPPET" /usr/local/bin/next-node)" = "next-node" ]
[ "$(bash -c "$NAME_SNIPPET" /tmp/tmp.Ab12Cd)" = "next-node" ]
[ "$(bash -c "$NAME_SNIPPET" bash)" = "next-node" ]
[ "$(NEXT_NODE_DEFAULT_APP_NAME=custom bash -c "$NAME_SNIPPET" /usr/local/bin/mynode)" = "custom" ]
echo "node name resolution ok"
