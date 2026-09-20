#!/usr/bin/env bash
# Tests the Finglish guided menu in next-binary.sh: what each choice runs, that
# input is validated, that risky actions need confirmation, and that a failing
# command returns to the menu instead of closing it.
set -o pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

export NEXT_SOURCE_ONLY=1
export NEXT_MENU_ALLOW_NON_ROOT=1
# shellcheck disable=SC1091
source "$ROOT/next-binary.sh"
set +e

APP_DIR="$TMP/app"
DATA_DIR="$TMP/data"
ENV_FILE="$APP_DIR/.env"
INSTALL_MODE_FILE="$APP_DIR/.install-mode"
CHANNEL_FILE="$APP_DIR/.channel"
BINARY_METADATA_FILE="$APP_DIR/.binary-release.json"
NEXT_SCRIPT_BASE_URL="https://example.test/scripts/next"
RUN_LOG="$TMP/run.log"
OUT="$TMP/out.txt"
PASS=0
FAIL=0

# ---- stubs: nothing here touches the machine ---------------------------------
ui_clear() { :; }
sleep() { :; }
detect_public_ip() { printf '203.0.113.9'; }
SVC_ACTIVE=1
systemctl() { [ "$SVC_ACTIVE" = "1" ]; }
STUB_FAIL=""
fg_run() {
    printf '%s\n' "$*" >>"$RUN_LOG"
    if [ -n "$STUB_FAIL" ] && [ "$1" = "$STUB_FAIL" ]; then
        return 3
    fi
    return 0
}
curl() {
    local out=""
    while [ $# -gt 0 ]; do
        [ "$1" = "-o" ] && out="$2"
        shift
    done
    printf '#!/usr/bin/env bash\necho "NODE-INSTALL-RAN $*"\n' >"$out"
}

ok() { PASS=$((PASS + 1)); }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL: %s\n' "$1"; }

# run_menu 'input' : feeds the menu; output in $OUT, commands run in $RUN_LOG
run_menu() {
    : >"$RUN_LOG"
    printf '%b' "$1" | finglish_menu >"$OUT" 2>&1
}
out_has() { grep -qF -- "$1" "$OUT" && ok || bad "$2 (missing: $1)"; }
out_lacks() { grep -qF -- "$1" "$OUT" && bad "$2 (unexpected: $1)" || ok; }
ran() { grep -qxF -- "$1" "$RUN_LOG" && ok || bad "$2 (expected command: $1; ran: $(tr '\n' ';' <"$RUN_LOG"))"; }
never_ran() { grep -qF -- "$1" "$RUN_LOG" && bad "$2 (should not run: $1)" || ok; }

mark_installed() {
    mkdir -p "$APP_DIR"
    printf 'binary\n' >"$INSTALL_MODE_FILE"
    printf '{"tag": "v1.4.0"}\n' >"$BINARY_METADATA_FILE"
    : >"$ENV_FILE"
}
mark_not_installed() { rm -rf "$APP_DIR"; }

# ---- 1. main menu, not installed ----------------------------------------------
mark_not_installed
run_menu '0\n'
out_has "Menu-ye Modiriyat" "menu shows its title"
out_has "Nasb nashode" "menu reports that the panel is not installed"
out_has "Az gozine 1 shoroo konid" "menu points a new server at option 1"
out_has "Khodahafez!" "0 exits politely"
run_menu '5\n\n0\n'
out_has "Aval gozine 1 (Nasb-e panel)" "actions that need a panel say to install first"
never_ran "status" "status is not run without a panel"
run_menu 'abc\n\n0\n'
out_has "Gozine-ye namotabar" "garbage input is rejected"
run_menu ''
ok

# ---- 2. non-root is turned away with the exact command to use ------------------
if [ "$(id -u)" != "0" ]; then
    NEXT_MENU_ALLOW_NON_ROOT=0 run_menu '0\n'
    out_has "sudo next" "non-root users are told to use sudo"
    out_lacks "Adad-e gozine" "the menu does not open for non-root users"
fi

# ---- 3. installed: version and state ------------------------------------------
mark_installed
SVC_ACTIVE=1
run_menu '0\n'
out_has "v1.4.0" "menu shows the installed version"
out_has "Roshan (kar mikonad)" "menu shows a running service"
SVC_ACTIVE=0
run_menu '0\n'
out_has "Khamoosh (kar nemikonad)" "menu shows a stopped service"
out_has "Gozine 6 ra bezanid" "a stopped panel points at option 6"
SVC_ACTIVE=1

# ---- 4. install ---------------------------------------------------------------
mark_not_installed
run_menu '1\n1\n1\nb\n\n0\n'
ran "install --binary --database sqlite --version latest" "install: SQLite + latest"
out_has "Dashboard port" "install shows the cheat-sheet for the installer's English prompts"
run_menu '1\n2\n2\nv1.4.0\nb\n\n0\n'
ran "install --binary --database mysql --version v1.4.0" "install: MySQL + pinned version"
out_has "ramz-e ghavi baraye database" "MySQL warns about the database password"
run_menu '1\n3\n1\nb\n\n0\n'
ran "install --binary --database mariadb --version latest" "install: MariaDB"
run_menu '1\n1\n2\nv1.4\n\n0\n'
never_ran "install" "a malformed version is refused"
out_has "Format-e nesekhe dorost nist" "malformed version explains itself"
run_menu '1\n1\n1\nn\n\n0\n'
never_ran "install" "declining the confirmation does not install"
run_menu '1\n0\n\n0\n'
never_ran "install" "0 goes back"
mark_installed
run_menu '1\n\n0\n'
never_ran "install" "an installed panel is never reinstalled from the menu"
out_has "ghablan roye in server nasb shode" "reinstall attempt points to update/uninstall"

# ---- 5. update ----------------------------------------------------------------
mark_installed
run_menu '2\n1\nn\nb\n\n0\n'
ran "update --version latest" "update to the latest release"
never_ran "backup" "no backup when declined"
run_menu '2\n2\nv1.5.0\nb\nb\n\n0\n'
ran "backup" "pinned update takes the backup first"
ran "update --version v1.5.0" "update to a pinned version"
STUB_FAIL=backup
run_menu '2\n1\nb\nn\n\n0\n'
never_ran "update" "a failed backup can stop the update"
run_menu '2\n1\nb\nb\nb\n\n0\n'
ran "update --version latest" "a failed backup can be overridden explicitly"
STUB_FAIL=""
run_menu '2\n0\n\n0\n'
never_ran "update" "0 goes back"
mark_not_installed
run_menu '2\n\n0\n'
never_ran "update" "no update without a panel"

# ---- 6. uninstall needs the word HAZF -----------------------------------------
mark_installed
run_menu '4\nhazf\n\n0\n'
never_ran "uninstall" "lower-case hazf is not enough"
run_menu '4\nyes\n\n0\n'
never_ran "uninstall" "'yes' is not enough"
run_menu '4\nHAZF\n\n0\n'
ran "uninstall" "HAZF uninstalls"
out_has "Do you really want to uninstall" "uninstall shows the installer's English prompts up front"

# ---- 7. runtime -------------------------------------------------------------
mark_installed
run_menu '6\n\n0\n'; ran "up" "6 starts the panel"
run_menu '7\nn\n\n0\n'; never_ran "down" "stopping needs confirmation"
run_menu '7\nb\n\n0\n'; ran "down" "confirmed stop runs down"
run_menu '8\nb\n\n0\n'; ran "restart" "8 restarts"
run_menu '9\n\n0\n'; ran "logs" "9 shows logs"
out_has "Ctrl+C" "logs explains how to leave"
run_menu '5\n\n0\n'; ran "status" "5 shows the status"
out_has "http://203.0.113.9:8000/dashboard/" "status reports the dashboard address"
STUB_FAIL=restart
run_menu '8\nb\n\n0\n'
out_has "kod-e khata: 3" "a failing command reports its exit code"
out_has "Khodahafez!" "and the menu is still alive afterwards"
STUB_FAIL=""

# ---- 8. admins ----------------------------------------------------------------
run_menu '10\nab\nreza_1\n2\n\n0\n'
out_has "Nam-e karbari motabar nist" "a too-short username is rejected"
ran "cli admin create reza_1 --role sudo" "admin create passes username and role"
run_menu '10\nreza_1\n1\n\n0\n'
ran "cli admin create reza_1 --role full_access" "option 1 is full access"
run_menu '10\nreza_1\n0\n\n0\n'
never_ran "admin create" "0 at the role prompt cancels"
run_menu '10\nbad name!\n\nq\n'
never_ran "admin create" "unsafe usernames never reach the CLI"
run_menu '11\nreza_1\n\n0\n'
ran "cli admin list" "password reset lists admins first"
ran "cli admin set-password reza_1" "password reset runs set-password"
run_menu '12\n\n0\n'; ran "cli admin list" "12 lists admins"

# ---- 9. nodes -----------------------------------------------------------------
run_menu '13\n\n0\n'
out_has "62050" "the node guide gives the service port"
out_has "62051" "the node guide gives the API port"
out_has "na 127.0.0.1" "the node guide warns against 127.0.0.1"
out_has "next-node-binary.sh" "the node guide gives the installer command for other servers"
out_has "Auto-Configure Best Protocols" "the node guide continues to auto-configure"
out_has "http://203.0.113.9:8000/dashboard/node-settings" "the node guide links the node settings page"
run_menu '14\nn\n\n0\n'
out_lacks "NODE-INSTALL-RAN" "no node install without a bundle"
run_menu '14\nb\nb\n\n0\n'
out_has "NODE-INSTALL-RAN install" "node install downloads and runs the node installer"
out_has "SERVICE_PORT" "node install shows the installer prompts in advance"

# ---- 10. backup, ssl, env, info ---------------------------------------------
run_menu '15\n\n0\n'; ran "backup" "15 backs up"
run_menu '16\nb\n\n0\n'; ran "backup-service" "16 sets up Telegram backups"
run_menu '17\n1\nme@example.com\npanel.example.com\n\n0\n'
ran "ssl issue --email me@example.com --domains panel.example.com" "SSL by domain"
run_menu '17\n2\nme@example.com\n\n\n0\n'
ran "ssl issue --email me@example.com --ip-address 203.0.113.9" "SSL by IP defaults to the detected address"
run_menu '17\n1\nnot-an-email\n\n0\n'
never_ran "ssl" "an invalid email is rejected"
run_menu '17\n1\nme@example.com\nnot a domain\n\n0\n'
never_ran "ssl" "an invalid domain is rejected"
run_menu '17\n3\n\n0\n'; ran "ssl renew" "SSL renew"
run_menu '18\nb\nb\n\n0\n'
ran "edit-env" "18 opens the env file"
ran "restart" "and offers a restart afterwards"
run_menu '19\n\n0\n'
out_has "ssh -L 8000:localhost:8000 root@203.0.113.9" "info explains the SSH tunnel when SSL is off"
printf 'UVICORN_PORT=8443\nUVICORN_SSL_CERTFILE=/var/lib/next/certs/panel.example.com/fullchain.pem\nSQLALCHEMY_DATABASE_URL=mysql+pymysql://next:secret@127.0.0.1:3306/next\n' >"$ENV_FILE"
run_menu '19\n\n0\n'
out_has "https://panel.example.com:8443/dashboard/" "info builds the https address from the env file"
out_lacks "ssh -L" "no SSH tunnel advice once SSL is on"
out_lacks "secret" "the database password is never printed"
out_has "mysql+pymysql" "only the database kind is shown"
: >"$ENV_FILE"

# ---- 11. confirmation prompt ------------------------------------------------
for yes in b B y bale Bale yes; do
    printf '%s\n' "$yes" | fg_confirm "test?" n >/dev/null && ok || bad "fg_confirm should accept '$yes'"
done
for no in n N kheyr no; do
    printf '%s\n' "$no" | fg_confirm "test?" y >/dev/null && bad "fg_confirm should refuse '$no'" || ok
done
printf '\n' | fg_confirm "test?" y >/dev/null && ok || bad "fg_confirm default yes"
printf '\n' | fg_confirm "test?" n >/dev/null && bad "fg_confirm default no" || ok
printf 'maybe\nb\n' | fg_confirm "test?" n >"$OUT" 2>&1 && ok || bad "fg_confirm should re-ask after garbage"
out_has "Faghat 'b'" "fg_confirm explains the accepted answers"

# ---- 12. presentation: the menu fits an 80-column terminal and is Finglish ----
mark_not_installed
run_menu '0\n'
too_wide=0
while IFS= read -r line; do
    if [ "${#line}" -gt 80 ]; then
        too_wide=1
        printf 'line too wide (%d chars): %s
' "${#line}" "$line"
    fi
done < <(LC_ALL=C.UTF-8 cat "$OUT")
[ "$too_wide" -eq 0 ] && ok || bad "menu lines must fit 80 columns"
if LC_ALL=C.UTF-8 grep -qP '[\x{0600}-\x{06FF}]' "$OUT" 2>/dev/null; then bad "menu must not use Persian script (breaks in SSH terminals)"; else ok; fi
for n in 1 5 10 13 19; do
    grep -qE "^ +$n\) " "$OUT" && ok || bad "menu item $n is missing"
done

# ---- 13. self re-run: a failing command must not close the menu ----------------
unset -f fg_run
unset NEXT_SOURCE_ONLY # the child must run for real, not just define functions
fg_run() { bash "$(fg_self)" "$@"; }
export NEXT_SELF="$ROOT/next-binary.sh"
export APP_NAME="nexttest-$$"
mark_installed
printf '5\n\n0\n' | finglish_menu >"$OUT" 2>&1
out_has "Not Installed" "the real script ran as a child process"
out_has "Panel roshan nist" "its failure was reported in Finglish"
out_has "Khodahafez!" "and the menu survived the child's exit 1"
help_out=$(bash "$NEXT_SELF" help 2>&1)
case "$help_out" in
    *"menu "*"Menu-ye rahnama"*) ok ;;
    *) bad "help must document the menu command" ;;
esac
unset NEXT_SELF
[ "$(fg_self)" = "$NEXT_SCRIPT_INSTALL_PATH" ] && ok || bad "fg_self falls back to the installed script path"

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
