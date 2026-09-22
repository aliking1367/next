#!/usr/bin/env bash
# The binary installer must install the bundled templates; without them the
# subscription page silently fell back to the plain built-in page.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

for SCRIPT in next-binary.sh next.sh; do
    eval "$(sed -n '/^install_binary_templates() {$/,/^}$/p' "$ROOT/$SCRIPT")"
    grep -q 'install_binary_templates "$tmp_dir/templates"' "$ROOT/$SCRIPT"

    APP_DIR="$TMP/$SCRIPT/app"
    mkdir -p "$APP_DIR/templates/removed" "$TMP/$SCRIPT/release/templates/subscription"
    echo new >"$TMP/$SCRIPT/release/templates/subscription/index.html"
    echo stale >"$APP_DIR/templates/removed/file"

    install_binary_templates "$TMP/$SCRIPT/release/templates"
    [ "$(cat "$APP_DIR/templates/subscription/index.html")" = "new" ]
    [ ! -e "$APP_DIR/templates/removed" ]

    # A release without templates keeps what is installed.
    install_binary_templates "$TMP/$SCRIPT/none"
    [ -f "$APP_DIR/templates/subscription/index.html" ]
done
echo "template install tests passed"
