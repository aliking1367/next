#!/usr/bin/env bash
# Tests for RAM-sized MySQL/MariaDB settings and `next tune-database`.
set -uo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
fail=0
check() { if [ "$2" != "$3" ]; then fail=$((fail + 1)); printf 'FAIL: %s\n  expected: %s\n  actual:   %s\n' "$1" "$2" "$3"; fi; }
meminfo() { printf 'MemTotal:       %s kB\nMemFree:          1000 kB\n' "$1" >"$TMP/meminfo"; }

for SCRIPT in next-binary.sh next.sh; do
    (
        for fn in host_database_memory_settings write_host_database_config; do
            eval "$(sed -n "/^${fn}() {\$/,/^}\$/p" "$ROOT/$SCRIPT")"
        done
        export NEXT_MEMINFO_FILE="$TMP/meminfo"

        meminfo 978000   # ~955 MB, like a 1 GB VPS
        write_host_database_config "$TMP/$SCRIPT/next.cnf"
        conf="$TMP/$SCRIPT/next.cnf"
        check "$SCRIPT 1GB buffer pool" "innodb_buffer_pool_size=64M" "$(grep '^innodb_buffer_pool_size' "$conf")"
        check "$SCRIPT 1GB perf schema" "performance_schema=OFF" "$(grep '^performance_schema' "$conf")"
        check "$SCRIPT 1GB connections" "max_connections=100" "$(grep '^max_connections' "$conf")"
        check "$SCRIPT keeps bind address" "bind-address=127.0.0.1" "$(grep '^bind-address' "$conf")"
        check "$SCRIPT section header first" "[mysqld]" "$(head -n1 "$conf")"
        check "$SCRIPT one max_connections" "1" "$(grep -c '^max_connections' "$conf")"

        meminfo 3000000
        check "$SCRIPT 3GB buffer pool" "innodb_buffer_pool_size=256M" "$(host_database_memory_settings | grep '^innodb_buffer_pool_size')"
        meminfo 8000000
        check "$SCRIPT 8GB keeps defaults" "max_connections=200" "$(host_database_memory_settings)"
        NEXT_MEMINFO_FILE="$TMP/missing" 
        check "$SCRIPT unreadable meminfo keeps defaults" "max_connections=200" "$(NEXT_MEMINFO_FILE="$TMP/missing" host_database_memory_settings)"
        [ "$fail" -eq 0 ]
    ) || fail=$((fail + 1))
done

# tune-database: rewrite, restart, and restore on failure (next-binary.sh only)
(
    for fn in host_database_memory_settings write_host_database_config tune_database_command; do
        eval "$(sed -n "/^${fn}() {\$/,/^}\$/p" "$ROOT/next-binary.sh")"
    done
    colorized_echo() { :; }
    check_running_as_root() { :; }
    export NEXT_MEMINFO_FILE="$TMP/meminfo"
    meminfo 978000
    NEXT_MYSQL_CONFIG_ROOT="$TMP/etc-mysql"
    mkdir -p "$NEXT_MYSQL_CONFIG_ROOT/mysql.conf.d"
    printf '[mysqld]\nmax_connections=200\n' >"$NEXT_MYSQL_CONFIG_ROOT/mysql.conf.d/next.cnf"
    get_configured_database_type() { echo mysql; }
    restarts=""
    systemctl() { restarts="$restarts|$*"; [ -z "${RESTART_FAILS:-}" ]; }

    tune_database_command >/dev/null; rc=$?
    check "tune succeeds" "0" "$rc"
    check "tune restarts mysql" "|restart mysql" "$restarts"
    check "tune writes low-memory profile" "innodb_buffer_pool_size=64M" "$(grep '^innodb_buffer_pool_size' "$NEXT_MYSQL_CONFIG_ROOT/mysql.conf.d/next.cnf")"
    check "no backup left behind" "no" "$([ -e "$NEXT_MYSQL_CONFIG_ROOT/mysql.conf.d/next.cnf.bak" ] && echo yes || echo no)"

    printf '[mysqld]\nmax_connections=200\n' >"$NEXT_MYSQL_CONFIG_ROOT/mysql.conf.d/next.cnf"
    RESTART_FAILS=1 tune_database_command >/dev/null; rc=$?
    check "failed restart reports failure" "1" "$rc"
    check "failed restart restores old config" "$(printf '[mysqld]\nmax_connections=200')" "$(cat "$NEXT_MYSQL_CONFIG_ROOT/mysql.conf.d/next.cnf")"

    get_configured_database_type() { echo sqlite; }
    restarts=""
    tune_database_command >/dev/null; rc=$?
    check "sqlite is a no-op" "0|" "$rc|$restarts"
    [ "$fail" -eq 0 ]
) || fail=$((fail + 1))

if [ "$fail" -eq 0 ]; then echo "database tuning tests passed"; fi
[ "$fail" -eq 0 ]
