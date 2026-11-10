package tsin

import (
	"net"
	"strings"
)

// A front server stands between users and the server that runs the countries:
// the direct configs point at the front, which hands every port on to that
// server, so its own address never appears in a user's config and cannot be
// filtered. The front only forwards: nothing runs on it, it cannot read the
// REALITY traffic it carries, and when its address is filtered it is replaced
// in minutes without touching the server behind it.
//
// It forwards with plain DNAT and masquerade. The server behind then sees the
// front's address instead of each user's, which costs per-user IP limits on
// these configs; carrying the real address (PROXY protocol) would make Xray
// refuse every connection without it, cutting users still on the old configs.

// FrontScript is what an admin runs on a new front server. dest is the
// countries' server; "" when it is not a plain IPv4 address.
func FrontScript(dest string) string {
	ip := net.ParseIP(strings.TrimSpace(dest))
	if ip == nil || ip.To4() == nil {
		return ""
	}
	script := strings.ReplaceAll(frontScriptTemplate, "@DEST@", ip.String())
	return "sudo tee /tmp/next-front.sh >/dev/null <<'FRONT_SCRIPT_END'\n" + script +
		"FRONT_SCRIPT_END\nsudo bash /tmp/next-front.sh; sudo rm -f /tmp/next-front.sh\n"
}

const frontScriptTemplate = `#!/bin/bash
# Makes this server a front for the countries' server @DEST@: every TCP and
# UDP port except SSH is handed on to it. Nothing else is installed.
# To undo it later:
#   systemctl disable --now next-front; iptables -t nat -F NEXT_FRONT
set -euo pipefail
DEST=@DEST@
if [ "$(id -u)" != 0 ]; then echo "Run this as root"; exit 1; fi
if ip -4 addr | grep -qw "$DEST"; then
  echo "This is the countries' server itself ($DEST). Run this on the NEW front server, not here."
  exit 1
fi
IFACE=$(ip -4 route show default | awk '{print $5; exit}')
if [ -z "$IFACE" ]; then echo "No default route found"; exit 1; fi
SSH_PORTS=$(ss -Htlnp 2>/dev/null | awk '/sshd/ {n=split($4,a,":"); print a[n]}' | sort -u | tr '\n' ' ')
SSH_PORTS=${SSH_PORTS:-22}
cat > /usr/local/sbin/next-front-apply <<APPLY_END
#!/bin/bash
set -e
sysctl -qw net.ipv4.ip_forward=1
iptables -t nat -N NEXT_FRONT 2>/dev/null || iptables -t nat -F NEXT_FRONT
for port in $SSH_PORTS; do iptables -t nat -A NEXT_FRONT -p tcp --dport \$port -j RETURN; done
iptables -t nat -A NEXT_FRONT -p tcp -j DNAT --to-destination $DEST
iptables -t nat -A NEXT_FRONT -p udp -j DNAT --to-destination $DEST
iptables -t nat -C PREROUTING -i $IFACE -j NEXT_FRONT 2>/dev/null || iptables -t nat -A PREROUTING -i $IFACE -j NEXT_FRONT
iptables -t nat -C POSTROUTING -d $DEST -j MASQUERADE 2>/dev/null || iptables -t nat -A POSTROUTING -d $DEST -j MASQUERADE
iptables -C FORWARD -d $DEST -j ACCEPT 2>/dev/null || iptables -I FORWARD 1 -d $DEST -j ACCEPT
iptables -C FORWARD -s $DEST -j ACCEPT 2>/dev/null || iptables -I FORWARD 1 -s $DEST -j ACCEPT
APPLY_END
chmod 700 /usr/local/sbin/next-front-apply
printf 'net.ipv4.ip_forward=1\n' > /etc/sysctl.d/99-next-front.conf
cat > /etc/systemd/system/next-front.service <<'UNIT_END'
[Unit]
Description=Hand this server's ports on to the countries' server
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/sbin/next-front-apply

[Install]
WantedBy=multi-user.target
UNIT_END
systemctl daemon-reload
systemctl enable --now next-front >/dev/null
systemctl restart next-front
echo "Front ready: every port except SSH ($SSH_PORTS) now goes to $DEST"
echo "Next: put this server's IP in the panel as the address of the direct configs and press Apply."
`
