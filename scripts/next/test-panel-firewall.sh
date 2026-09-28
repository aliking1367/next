#!/usr/bin/env bash
# Tests for "next secure-panel": restricting the panel port to Cloudflare.
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
lacks() {
    if printf '%s' "$2" | grep -qF -- "$3"; then
        fail=$((fail + 1))
        printf 'FAIL: %s\n  unexpected: %s\n  in:         %s\n' "$1" "$3" "$2"
    else
        pass=$((pass + 1))
    fi
}

for SCRIPT in next.sh next-binary.sh; do
    (
        set -u
        colorized_echo() { :; }
        check_running_as_root() { :; }
        detect_os() { :; }
        install_package() { :; }
        for fn in valid_cidr_list cloudflare_ip_ranges panel_firewall_port panel_firewall_ssh_port \
                  listening_tcp_ports panel_firewall_require_ufw panel_firewall_clear_rules \
                  panel_firewall_status panel_firewall_enable panel_firewall_disable secure_panel_command; do
            eval "$(sed -n "/^${fn}() {\$/,/^}\$/p" "$ROOT/$SCRIPT")"
        done
        for constant in PANEL_FIREWALL_COMMENT CLOUDFLARE_IPV4_URL CLOUDFLARE_IPV6_URL; do
            eval "$(grep -m1 "^${constant}=" "$ROOT/$SCRIPT")"
        done

        # Only well-formed CIDRs survive: a half-downloaded list must never
        # become firewall rules.
        cidrs=$(printf '%s\n' "173.245.48.0/20" "not-an-ip" "2400:cb00::/32" "" "1.2.3.4" "# comment" "  198.41.128.0/17  " | valid_cidr_list | tr '\n' ' ')
        check "$SCRIPT keeps valid IPv4" "0" "$(printf '%s' "$cidrs" | grep -cF '173.245.48.0/20' >/dev/null && echo 0 || echo 1)"
        contains "$SCRIPT keeps IPv6" "$cidrs" "2400:cb00::/32"
        contains "$SCRIPT trims whitespace" "$cidrs" "198.41.128.0/17"
        lacks "$SCRIPT drops junk" "$cidrs" "not-an-ip"
        lacks "$SCRIPT drops bare address" "$cidrs" "1.2.3.4 "

        # A short or failed download is refused rather than applied.
        curl() { printf '1.1.1.1/32\n'; }
        cloudflare_ip_ranges >/dev/null 2>&1
        check "$SCRIPT refuses a short range list" "1" "$?"
        curl() { return 1; }
        cloudflare_ip_ranges >/dev/null 2>&1
        check "$SCRIPT refuses a failed download" "1" "$?"

        # The panel port comes from the environment, with a safe fallback.
        get_env_value() { printf '2053'; }
        check "$SCRIPT reads the panel port" "2053" "$(panel_firewall_port)"
        get_env_value() { printf 'not-a-port'; }
        check "$SCRIPT falls back to 8000" "8000" "$(panel_firewall_port)"
        get_env_value() { printf '2053'; }

        # Nothing is touched when the ranges cannot be fetched.
        ufw_calls=""
        ufw() { ufw_calls="$ufw_calls|$*"; }
        curl() { return 1; }
        panel_firewall_enable --yes >/dev/null 2>&1
        check "$SCRIPT applies no rules without ranges" "" "$ufw_calls"

        # A full run: SSH stays open, every range is allowed and everything
        # else is denied on the panel port.
        curl() {
            case "${3:-}" in
                *) printf '173.245.48.0/20\n103.21.244.0/22\n103.22.200.0/22\n141.101.64.0/18\n108.162.192.0/18\n190.93.240.0/20\n188.114.96.0/20\n197.234.240.0/22\n198.41.128.0/17\n162.158.0.0/15\n104.16.0.0/13\n' ;;
            esac
        }
        listening_tcp_ports() { printf '22\n2053\n62050\n'; }
        ufw_calls=""
        ufw() { ufw_calls="$ufw_calls|$*"; }
        PANEL_FIREWALL_SSH_OVERRIDE=2222 panel_firewall_enable --yes --keep-port 8443 >/dev/null 2>&1
        contains "$SCRIPT keeps SSH open" "$ufw_calls" "allow 2222/tcp"
        contains "$SCRIPT keeps other services open" "$ufw_calls" "allow 62050/tcp"
        contains "$SCRIPT honours --keep-port" "$ufw_calls" "allow 8443/tcp"
        lacks "$SCRIPT never blanket-allows the panel port" "$ufw_calls" "allow 2053/tcp"
        contains "$SCRIPT allows a Cloudflare range" "$ufw_calls" "allow from 104.16.0.0/13 to any port 2053 proto tcp comment $PANEL_FIREWALL_COMMENT"
        contains "$SCRIPT allows a Cloudflare IPv6-style range" "$ufw_calls" "allow from 162.158.0.0/15 to any port 2053"
        contains "$SCRIPT denies everyone else" "$ufw_calls" "deny 2053/tcp comment $PANEL_FIREWALL_COMMENT"
        contains "$SCRIPT enables the firewall" "$ufw_calls" "--force enable"

        # Cancelling at the prompt changes nothing.
        ufw_calls=""
        panel_firewall_enable </dev/null >/dev/null 2>&1
        check "$SCRIPT cancels without --yes" "" "$ufw_calls"

        # Undo re-opens the port.
        ufw_calls=""
        ufw() { ufw_calls="$ufw_calls|$*"; }
        panel_firewall_disable >/dev/null 2>&1
        contains "$SCRIPT undo reopens the port" "$ufw_calls" "allow 2053/tcp"

        printf '%s: %d passed, %d failed\n' "$SCRIPT" "$pass" "$fail"
        [ "$fail" -eq 0 ]
    ) || exit 1
done

echo "panel firewall tests passed"
