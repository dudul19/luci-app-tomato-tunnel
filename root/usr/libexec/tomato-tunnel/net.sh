#!/bin/sh

TABLE=1080
PREF_MAIN=1070
PREF_BYPASS=1075
PREF_TUN=1080
STATE=/etc/tomato-tunnel
IDLIST=/usr/share/tomato-tunnel/cidr-id.txt
IDMARK=0x7454
DNSBAK=$STATE/dnsmasq.bak

TUN=${TUN:-tt0}
LAN=${LAN:-br-lan}

have_nft() { command -v nft >/dev/null 2>&1 && nft list tables >/dev/null 2>&1; }
log() { echo "$*"; }

private4="0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 127.0.0.0/8 169.254.0.0/16 172.16.0.0/12 192.168.0.0/16 224.0.0.0/4 240.0.0.0/4"

is_v6() { case "$1" in *:*) return 0;; esac; return 1; }

rules_del() {
	for fam in -4 -6; do
		for p in $PREF_MAIN $PREF_BYPASS $PREF_TUN; do
			while ip $fam rule del pref $p 2>/dev/null; do :; done
		done
		ip $fam route flush table $TABLE 2>/dev/null
	done
}

tun_routes() {
	ip -4 route replace default dev "$TUN" table $TABLE || return 1
	ip -6 route replace default dev "$TUN" table $TABLE 2>/dev/null
	return 0
}

tun_up() {
	rules_del
	tun_routes || { log "tun: $TUN missing"; return 1; }
	for fam in -4 -6; do
		ip $fam rule add pref $PREF_MAIN lookup main suppress_prefixlength 0 2>/dev/null
		ip $fam rule add pref $PREF_TUN iif "$LAN" lookup $TABLE 2>/dev/null
	done
	for b in $BYPASS; do
		if is_v6 "$b"; then f=-6; else f=-4; fi
		ip $f rule add pref $PREF_BYPASS from "$b" lookup main 2>/dev/null
		ip $f rule add pref $PREF_BYPASS to "$b" lookup main 2>/dev/null
	done
	byp_id_up
	fw_allow
	acct_up
	if ! have_nft; then
		iptables -C FORWARD -i "$LAN" -o "$TUN" -j ACCEPT 2>/dev/null || iptables -I FORWARD -i "$LAN" -o "$TUN" -j ACCEPT
		iptables -C FORWARD -i "$TUN" -o "$LAN" -j ACCEPT 2>/dev/null || iptables -I FORWARD -i "$TUN" -o "$LAN" -j ACCEPT
	fi
	log "LAN ($LAN) routed through $TUN, table $TABLE"
}

fw_allow() {
	have_nft || return 0
	nft list chain inet fw4 forward >/dev/null 2>&1 || return 0
	nft list chain inet fw4 forward | grep -q tomato-tunnel-rt && return 0
	nft insert rule inet fw4 forward iifname "$TUN" counter accept comment "tomato-tunnel-rt"
	nft insert rule inet fw4 forward oifname "$TUN" counter accept comment "tomato-tunnel-rt"
}

acct_up() {
	have_nft || return 0
	nft delete table inet tomato_tunnel_acct 2>/dev/null
	nft -f - <<NFT 2>/dev/null || log "per-device usage not available (nftables)"
table inet tomato_tunnel_acct {
	set lan_up { type ipv4_addr; size 4096; flags dynamic; }
	set lan_dn { type ipv4_addr; size 4096; flags dynamic; }
	chain count {
		type filter hook forward priority filter - 5; policy accept;
		iifname "$LAN" oifname "$TUN" update @lan_up { ip saddr counter }
		iifname "$TUN" oifname "$LAN" update @lan_dn { ip daddr counter }
	}
}
NFT
}

id_elements() {
	tr '\n' ',' < "$IDLIST" | sed 's/,*$//; s/,/, /g'
}

byp_id_up() {
	have_nft && nft delete table inet tomato_tunnel_byp 2>/dev/null
	[ "$BYPASS_ID" = 1 ] || return 0
	[ -s "$IDLIST" ] || { log "Indonesian IP list missing"; return 0; }
	have_nft || { log "Indonesian IPs direct needs nftables"; return 0; }
	nft -f - <<NFT || { log "Indonesian IPs direct: nft failed"; return 0; }
table inet tomato_tunnel_byp {
	set id4 { type ipv4_addr; flags interval; auto-merge; elements = { $(id_elements) } }
	chain pre {
		type filter hook prerouting priority mangle - 5; policy accept;
		iifname "$LAN" ip daddr @id4 meta mark set $IDMARK
	}
}
NFT
	ip -4 rule add pref $PREF_BYPASS fwmark $IDMARK lookup main 2>/dev/null
	log "Indonesian IPs go direct ($(wc -l < "$IDLIST") ranges)"
}

byp_id_down() {
	have_nft && nft delete table inet tomato_tunnel_byp 2>/dev/null
}

acct_down() {
	have_nft && nft delete table inet tomato_tunnel_acct 2>/dev/null
}

fw_revoke() {
	have_nft || return 0
	nft -a list chain inet fw4 forward 2>/dev/null | sed -n 's/.*tomato-tunnel-rt.*# handle \([0-9]*\).*/\1/p' | while read -r h; do
		nft delete rule inet fw4 forward handle "$h"
	done
}

tun_down() {
	byp_id_down
	acct_down
	fw_revoke
	rules_del
	if command -v iptables >/dev/null 2>&1; then
		while iptables -D FORWARD -i "$LAN" -o "$TUN" -j ACCEPT 2>/dev/null; do :; done
		while iptables -D FORWARD -i "$TUN" -o "$LAN" -j ACCEPT 2>/dev/null; do :; done
	fi
}

redir_up() {
	redir_down
	local by="" from=""
	for b in $BYPASS; do
		is_v6 "$b" && continue
		by="$by, $b"
		from="$from, $b"
	done
	if have_nft; then
		local set4=$(echo $private4 | sed 's/ /, /g')
		if [ "$BYPASS_ID" = 1 ] && [ -s "$IDLIST" ]; then
			set4="$set4, $(id_elements)"
			log "Indonesian IPs go direct"
		fi
		nft -f - <<NFT || return 1
table ip tomato_tunnel {
	set skip_to { type ipv4_addr; flags interval; auto-merge; elements = { $set4$by } }
	set skip_from { type ipv4_addr; flags interval; auto-merge; elements = { 255.255.255.255$from } }
	chain pre {
		type nat hook prerouting priority dstnat - 5; policy accept;
		iifname "$LAN" ip daddr @skip_to return
		iifname "$LAN" ip saddr @skip_from return
		iifname "$LAN" meta l4proto tcp redirect to :$REDIR_PORT
	}
}
NFT
	else
		iptables -t nat -N TOMATO_TUNNEL 2>/dev/null
		for n in $private4 $(echo "$by" | tr ',' ' '); do
			iptables -t nat -A TOMATO_TUNNEL -d "$n" -j RETURN
		done
		for n in $(echo "$from" | tr ',' ' '); do
			iptables -t nat -A TOMATO_TUNNEL -s "$n" -j RETURN
		done
		iptables -t nat -A TOMATO_TUNNEL -p tcp -j REDIRECT --to-ports "$REDIR_PORT"
		iptables -t nat -I PREROUTING -i "$LAN" -p tcp -j TOMATO_TUNNEL
	fi
	log "LAN TCP redirected to port $REDIR_PORT"
}

redir_down() {
	have_nft && nft delete table ip tomato_tunnel 2>/dev/null
	if command -v iptables >/dev/null 2>&1; then
		while iptables -t nat -D PREROUTING -i "$LAN" -p tcp -j TOMATO_TUNNEL 2>/dev/null; do :; done
		iptables -t nat -F TOMATO_TUNNEL 2>/dev/null
		iptables -t nat -X TOMATO_TUNNEL 2>/dev/null
	fi
}

dns_up() {
	[ "$DNS_MODE" = tunnel ] && [ "$MANAGE_DNSMASQ" = 1 ] || return 0
	mkdir -p "$STATE"
	if [ ! -f "$DNSBAK" ]; then
		{
			echo "noresolv=$(uci -q get dhcp.@dnsmasq[0].noresolv)"
			echo "server=$(uci -q get dhcp.@dnsmasq[0].server)"
		} > "$DNSBAK"
	fi
	uci -q batch <<UCI
set dhcp.@dnsmasq[0].noresolv='1'
delete dhcp.@dnsmasq[0].server
add_list dhcp.@dnsmasq[0].server='127.0.0.1#$DNS_PORT'
commit dhcp
UCI
	/etc/init.d/dnsmasq restart >/dev/null 2>&1
	log "dnsmasq uses 127.0.0.1#$DNS_PORT"
}

dns_down() {
	[ -f "$DNSBAK" ] || return 0
	local nr srv s
	nr=$(sed -n 's/^noresolv=//p' "$DNSBAK")
	srv=$(sed -n 's/^server=//p' "$DNSBAK")
	uci -q delete dhcp.@dnsmasq[0].server
	for s in $srv; do uci -q add_list dhcp.@dnsmasq[0].server="$s"; done
	if [ -n "$nr" ]; then uci -q set dhcp.@dnsmasq[0].noresolv="$nr"; else uci -q delete dhcp.@dnsmasq[0].noresolv; fi
	uci -q commit dhcp
	rm -f "$DNSBAK"
	/etc/init.d/dnsmasq restart >/dev/null 2>&1
	log "dnsmasq restored"
}

case "$1" in
	up)
		case "$NET_MODE" in
			tun) tun_up || exit 1 ;;
			redirect) redir_up || exit 1 ;;
		esac
		dns_up
		;;
	routes)
		tun_routes
		;;
	down)
		for p in $(pidof hev-socks5-tunnel); do
			grep -q tomato-tunnel "/proc/$p/cmdline" 2>/dev/null && kill "$p"
		done
		tun_down
		redir_down
		dns_down
		;;
	*)
		echo "usage: $0 up|routes|down" >&2
		exit 2
		;;
esac
exit 0
