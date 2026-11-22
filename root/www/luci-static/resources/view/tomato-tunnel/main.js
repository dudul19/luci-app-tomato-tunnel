'use strict';
'require view';
'require fs';
'require ui';
'require uci';
'require rpc';
'require poll';

var CTL = '/usr/libexec/tomato-tunnel/ctl';
var CONF = 'tomato-tunnel';
var TG = 'https://t.me/dudulrealnofek';

var callCommit = rpc.declare({ object: 'uci', method: 'commit', params: [ 'config' ] });

var PRESETS = [
	[ '', 'SSH Direct', 0, 0, 0, 0 ],
	[ 'p', 'SSH + Payload', 1, 0, 0, 0 ],
	[ 'pe', 'SSH + Payload + Enhanced', 1, 0, 1, 0 ],
	[ 't', 'SSH + TLS (SNI)', 0, 1, 0, 0 ],
	[ 'pt', 'SSH + Payload + TLS', 1, 1, 0, 0 ],
	[ 'pte', 'SSH + Payload + TLS + Enhanced', 1, 1, 1, 0 ],
	[ 'd', 'SSH + SlowDNS', 0, 0, 0, 1 ],
	[ 'pd', 'SSH + Payload + SlowDNS', 1, 0, 0, 1 ],
	[ 'ped', 'SSH + Payload + Enhanced + SlowDNS', 1, 0, 1, 1 ]
];
function layers(p) {
	var m = p.mode || '';
	var L = { payload: m === 'payload' || m === 'ws' || m === 'wss', tls: m === 'ssl' || m === 'wss', dns: m === 'dns' };
	if (p.use_payload != null) L.payload = p.use_payload === '1';
	if (p.use_tls != null) L.tls = p.use_tls === '1';
	if (p.use_slowdns != null) L.dns = p.use_slowdns === '1';
	L.enh = L.payload && p.enhanced === '1';
	L.zivpn = p.type === 'zivpn';
	if (L.zivpn) { L.payload = L.tls = L.dns = L.enh = false; }
	L.insecure = p.insecure !== '0';
	return L;
}
function layerLabel(L) {
	if (L.zivpn) return 'ZiVPN';
	if (!L.payload && !L.tls && !L.dns) return 'SSH Direct';
	return 'SSH' + (L.payload ? ' + Payload' : '') + (L.tls ? ' + TLS' : '') + (L.enh ? ' + Enhanced' : '') + (L.dns ? ' + SlowDNS' : '');
}
function defPort(L) { return L.zivpn ? '5667' : (L.tls ? '443' : (L.payload ? '80' : '22')); }
var TEMPLATES = {
	ws: 'GET / HTTP/1.1[crlf]Host: [bug][crlf]Connection: Upgrade[crlf]User-Agent: [ua][crlf]Upgrade: websocket[crlf][crlf]',
	wss: 'GET wss://[bug]/ HTTP/1.1[crlf]Host: [bug][crlf]Connection: Upgrade[crlf]User-Agent: [ua][crlf]Upgrade: websocket[crlf][crlf]',
	connect: 'CONNECT [host_port] HTTP/1.1[crlf]Host: [bug][crlf][crlf]',
	enhanced: 'HEAD / HTTP/1.1[crlf]Host: [bug][crlf][crlf]PATCH / HTTP/1.1[crlf]Host: [host][crlf]Upgrade: websocket[crlf][crlf][split]HTTP/1.1[crlf][crlf]'
};
var UAS = [
	[ 'Dalvik/2.1.0 (Linux; U; Android 13)', 'Android (Dalvik)' ],
	[ 'Mozilla/5.0 (Linux; Android 13; Mobile) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Mobile Safari/537.36', 'Chrome mobile' ]
];
var BOLT = '<svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true"><path d="M13 2 4 14h7l-1 8 9-12h-7z" fill="currentColor"/></svg>';
var TABS = [ [ 'dashboard', 'Dashboard' ], [ 'accounts', 'Accounts' ], [ 'usage', 'Usage' ], [ 'settings', 'Settings' ], [ 'log', 'Log' ] ];
function kindOf(d) {
	var L = layers(d);
	if (L.zivpn) return 'zv';
	if (L.dns && !L.payload && !L.tls) return 'dns';
	if (L.tls && !L.payload && !L.dns) return 'ssl';
	if (L.payload && !L.dns && !L.enh && !d.proxy && (!d.payload || d.payload === TEMPLATES.ws || d.payload === TEMPLATES.wss)) return 'ws';
	return 'custom';
}
function niceLabel(d) {
	var k = kindOf(d), L = layers(d);
	if (k === 'ws') return L.tls ? 'SSH WebSocket + TLS' : 'SSH WebSocket';
	if (k === 'ssl') return 'SSH SSL / SNI';
	if (k === 'dns') return 'SSH SlowDNS';
	if (k === 'zv') return 'ZiVPN (UDP)';
	return layerLabel(L);
}
var RESTART_KEYS = [ 'net_mode', 'socks_port', 'socks_listen', 'wan_iface', 'dns_mode', 'dns_server', 'manage_dnsmasq', 'bypass', 'ssh_compat', 'check_url', 'check_interval', 'check_fail', 'sessions', 'log_level' ];
var PROFILE_KEYS = [ 'name', 'type', 'use_payload', 'use_tls', 'use_slowdns', 'enhanced', 'insecure', 'host', 'port', 'user', 'pass', 'bug', 'ua', 'payload', 'proxy', 'udpgw', 'split_delay', 'dns_domain', 'dns_resolver', 'dns_pubkey', 'dns_type', 'expires' ];
var MON = [ 'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec' ];

function parseExp(v) {
	var m = String(v || '').match(/^(\d{4})-(\d\d)-(\d\d)(?:[ T](\d\d):(\d\d))?/);
	if (!m) return null;
	return m[4] != null ? new Date(+m[1], +m[2] - 1, +m[3], +m[4], +m[5]) : new Date(+m[1], +m[2] - 1, +m[3], 23, 59, 59);
}
function dm(d) { return d.getDate() + ' ' + MON[d.getMonth()]; }
function expInfo(p, warn) {
	var t = parseExp(p.expires);
	if (t && t < new Date()) return { text: 'Expired ' + dm(t), cls: 'tt-exp bad', expired: true };
	if (!t) return null;
	var days = Math.floor((t - new Date()) / 86400000);
	if (days <= warn) return { text: days < 1 ? 'Expires today' : (days === 1 ? '1 day left' : days + ' days left'), cls: 'tt-exp warn', soon: true, days: days, at: t };
	return { text: 'Expires ' + dm(t) + ' · ' + days + ' days', cls: 'tt-exp' };
}
function firstPort(s) { var m = String(s || '').match(/\d+/); return m ? m[0] : ''; }
function b64enc(s) { return btoa(unescape(encodeURIComponent(s))); }
function b64dec(s) { return decodeURIComponent(escape(atob(s.replace(/-/g, '+').replace(/_/g, '/')))); }
function smooth(pts) {
	if (pts.length < 2) return '';
	var d = 'M' + pts[0][0].toFixed(1) + ',' + pts[0][1].toFixed(1);
	for (var i = 0; i < pts.length - 1; i++) {
		var p0 = pts[i - 1] || pts[i], p1 = pts[i], p2 = pts[i + 1], p3 = pts[i + 2] || p2;
		var c1x = p1[0] + (p2[0] - p0[0]) / 6, c1y = p1[1] + (p2[1] - p0[1]) / 6;
		var c2x = p2[0] - (p3[0] - p1[0]) / 6, c2y = p2[1] - (p3[1] - p1[1]) / 6;
		d += ' C' + c1x.toFixed(1) + ',' + c1y.toFixed(1) + ' ' + c2x.toFixed(1) + ',' + c2y.toFixed(1) + ' ' + p2[0].toFixed(1) + ',' + p2[1].toFixed(1);
	}
	return d;
}

var BANNER_SVG = '<svg viewBox="0 0 1072 180" preserveAspectRatio="xMidYMid slice" aria-hidden="true"><defs>' +
	'<linearGradient id="ttsky" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#1e1b4b"/><stop offset=".5" stop-color="#7e2d8f"/><stop offset=".85" stop-color="#f0607e"/><stop offset="1" stop-color="#fb923c"/></linearGradient>' +
	'<radialGradient id="ttsun" cx=".5" cy=".5" r=".5"><stop offset="0" stop-color="#fff7d6"/><stop offset=".6" stop-color="#fde68a"/><stop offset="1" stop-color="#fbbf24" stop-opacity="0"/></radialGradient></defs>' +
	'<rect width="1072" height="180" fill="url(#ttsky)"/>' +
	'<circle cx="40" cy="20" r="1.4" fill="#fff" opacity=".8"/><circle cx="300" cy="14" r="1.3" fill="#fff" opacity=".8"/><circle cx="470" cy="30" r="1" fill="#fff" opacity=".6"/><circle cx="640" cy="12" r="1.5" fill="#fff" opacity=".8"/><circle cx="1010" cy="22" r="1.3" fill="#fff" opacity=".8"/>' +
	'<circle cx="780" cy="136" r="110" fill="url(#ttsun)" opacity=".55"/><circle cx="780" cy="136" r="58" fill="#fde68a"/>' +
	'<rect x="714" y="136" width="132" height="5" fill="#f0607e"/><rect x="714" y="148" width="132" height="7" fill="#f0607e"/><rect x="714" y="162" width="132" height="9" fill="#f0607e"/>' +
	'<ellipse cx="620" cy="70" rx="110" ry="8" fill="#fbcfe8" opacity=".35"/><ellipse cx="900" cy="54" rx="80" ry="6" fill="#fbcfe8" opacity=".3"/>' +
	'<path d="M0 144 L110 100 L210 136 L330 86 L440 132 L540 106 L650 136 L760 108 L880 138 L980 100 L1072 126 L1072 180 L0 180 Z" fill="#5b21b6" opacity=".55"/>' +
	'<path d="M470 180 V136 H496 V116 H518 V136 H534 V106 H562 V180 Z M576 180 V124 H608 V146 H630 V110 H654 V180 Z M880 180 V128 H906 V106 H934 V180 Z M944 180 V136 H970 V120 H992 V180 Z" fill="#2e1065"/>' +
	'<rect x="502" y="122" width="4" height="5" fill="#fde68a"/><rect x="544" y="114" width="4" height="5" fill="#fde68a"/><rect x="590" y="132" width="4" height="5" fill="#fde68a"/><rect x="640" y="120" width="4" height="5" fill="#fde68a"/><rect x="916" y="114" width="4" height="5" fill="#fde68a"/><rect x="976" y="128" width="4" height="5" fill="#fde68a"/>' +
	'<path d="M0 180 L0 160 Q260 136 540 158 T1072 152 L1072 180 Z" fill="#1a0b2e"/>' +
	'<path d="M1020 156 L1031 84 L1042 156 M1024 132 H1038 M1027 112 H1035" stroke="#1a0b2e" stroke-width="4" fill="none"/>' +
	'<path d="M1012 76 A24 24 0 0 1 1050 76 M1003 66 A36 36 0 0 1 1059 66" stroke="#fde68a" stroke-width="3" fill="none" stroke-linecap="round" opacity=".9"/>' +
	'<ellipse cx="400" cy="40" rx="5" ry="3" fill="#fbcfe8" transform="rotate(30 400 40)"/><ellipse cx="480" cy="70" rx="4" ry="2.5" fill="#fbcfe8" transform="rotate(-20 480 70)"/><ellipse cx="690" cy="30" rx="5" ry="3" fill="#fbcfe8" transform="rotate(45 690 30)"/><ellipse cx="870" cy="92" rx="4" ry="2.5" fill="#fbcfe8" transform="rotate(10 870 92)"/><ellipse cx="960" cy="46" rx="5" ry="3" fill="#fbcfe8" transform="rotate(-35 960 46)"/>' +
	'</svg>';
var BANNERS = [
	[ 'tomato', 'Tomato', '<svg viewBox="0 0 1072 180" preserveAspectRatio="xMidYMid slice" aria-hidden="true"><defs><linearGradient id="tt_tmb" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#3b0909"/><stop offset=".5" stop-color="#a51c1c"/><stop offset="1" stop-color="#f97316"/></linearGradient><radialGradient id="tt_tmf" cx=".36" cy=".32" r=".75"><stop offset="0" stop-color="#ff8a75"/><stop offset=".5" stop-color="#e11d2e"/><stop offset="1" stop-color="#7f0d17"/></radialGradient><pattern id="tt_tmd" width="22" height="22" patternUnits="userSpaceOnUse"><circle cx="2" cy="2" r="1.4" fill="#fff" opacity=".1"/></pattern></defs><rect width="1072" height="180" fill="url(#tt_tmb)"/><rect width="1072" height="180" fill="url(#tt_tmd)"/><circle cx="860" cy="112" r="140" fill="none" stroke="#fff" stroke-opacity=".07" stroke-width="18"/><circle cx="860" cy="112" r="190" fill="none" stroke="#fff" stroke-opacity=".05" stroke-width="14"/><circle cx="860" cy="112" r="86" fill="url(#tt_tmf)"/><ellipse cx="827" cy="81" rx="17.2" ry="29.2" fill="#fff" opacity=".3" transform="rotate(-25 827 81)"/><path d="M0 0 C9.6 -9.6 32.0 -8.0 48.0 0 C32.0 8.0 9.6 9.6 0 0Z" fill="#22c55e" transform="translate(860 36) rotate(-160)"/><path d="M0 0 C9.6 -9.6 32.0 -8.0 48.0 0 C32.0 8.0 9.6 9.6 0 0Z" fill="#16a34a" transform="translate(860 36) rotate(-20)"/><path d="M0 0 C9.6 -9.6 32.0 -8.0 48.0 0 C32.0 8.0 9.6 9.6 0 0Z" fill="#22c55e" transform="translate(860 36) rotate(35)"/><path d="M0 0 C9.6 -9.6 32.0 -8.0 48.0 0 C32.0 8.0 9.6 9.6 0 0Z" fill="#16a34a" transform="translate(860 36) rotate(92)"/><path d="M0 0 C9.6 -9.6 32.0 -8.0 48.0 0 C32.0 8.0 9.6 9.6 0 0Z" fill="#22c55e" transform="translate(860 36) rotate(145)"/><path d="M860 36 q3.2 -12.8 9.6 -22.4" stroke="#15803d" stroke-width="6.4" stroke-linecap="round" fill="none"/><circle cx="706" cy="150" r="26" fill="url(#tt_tmf)"/><ellipse cx="696" cy="140" rx="5.2" ry="8.8" fill="#fff" opacity=".3" transform="rotate(-25 696 140)"/><path d="M0 0 C3.3 -3.3 11.0 -2.8 16.5 0 C11.0 2.8 3.3 3.3 0 0Z" fill="#22c55e" transform="translate(706 127) rotate(-160)"/><path d="M0 0 C3.3 -3.3 11.0 -2.8 16.5 0 C11.0 2.8 3.3 3.3 0 0Z" fill="#16a34a" transform="translate(706 127) rotate(-20)"/><path d="M0 0 C3.3 -3.3 11.0 -2.8 16.5 0 C11.0 2.8 3.3 3.3 0 0Z" fill="#22c55e" transform="translate(706 127) rotate(35)"/><path d="M0 0 C3.3 -3.3 11.0 -2.8 16.5 0 C11.0 2.8 3.3 3.3 0 0Z" fill="#16a34a" transform="translate(706 127) rotate(92)"/><path d="M0 0 C3.3 -3.3 11.0 -2.8 16.5 0 C11.0 2.8 3.3 3.3 0 0Z" fill="#22c55e" transform="translate(706 127) rotate(145)"/><path d="M706 127 q1.1 -4.4 3.3 -7.7" stroke="#15803d" stroke-width="2.2" stroke-linecap="round" fill="none"/><circle cx="1004" cy="60" r="18" fill="url(#tt_tmf)"/><ellipse cx="997" cy="53" rx="3.6" ry="6.1" fill="#fff" opacity=".3" transform="rotate(-25 997 53)"/><path d="M0 0 C2.4 -2.4 8.0 -2.0 12.0 0 C8.0 2.0 2.4 2.4 0 0Z" fill="#22c55e" transform="translate(1004 44) rotate(-160)"/><path d="M0 0 C2.4 -2.4 8.0 -2.0 12.0 0 C8.0 2.0 2.4 2.4 0 0Z" fill="#16a34a" transform="translate(1004 44) rotate(-20)"/><path d="M0 0 C2.4 -2.4 8.0 -2.0 12.0 0 C8.0 2.0 2.4 2.4 0 0Z" fill="#22c55e" transform="translate(1004 44) rotate(35)"/><path d="M0 0 C2.4 -2.4 8.0 -2.0 12.0 0 C8.0 2.0 2.4 2.4 0 0Z" fill="#16a34a" transform="translate(1004 44) rotate(92)"/><path d="M0 0 C2.4 -2.4 8.0 -2.0 12.0 0 C8.0 2.0 2.4 2.4 0 0Z" fill="#22c55e" transform="translate(1004 44) rotate(145)"/><path d="M1004 44 q0.8 -3.2 2.4 -5.6" stroke="#15803d" stroke-width="1.6" stroke-linecap="round" fill="none"/><circle cx="612" cy="46" r="10" fill="url(#tt_tmf)"/><ellipse cx="608" cy="42" rx="2.0" ry="3.4" fill="#fff" opacity=".3" transform="rotate(-25 608 42)"/><path d="M0 0 C1.5 -1.5 5.0 -1.2 7.5 0 C5.0 1.2 1.5 1.5 0 0Z" fill="#22c55e" transform="translate(612 37) rotate(-160)"/><path d="M0 0 C1.5 -1.5 5.0 -1.2 7.5 0 C5.0 1.2 1.5 1.5 0 0Z" fill="#16a34a" transform="translate(612 37) rotate(-20)"/><path d="M0 0 C1.5 -1.5 5.0 -1.2 7.5 0 C5.0 1.2 1.5 1.5 0 0Z" fill="#22c55e" transform="translate(612 37) rotate(35)"/><path d="M0 0 C1.5 -1.5 5.0 -1.2 7.5 0 C5.0 1.2 1.5 1.5 0 0Z" fill="#16a34a" transform="translate(612 37) rotate(92)"/><path d="M0 0 C1.5 -1.5 5.0 -1.2 7.5 0 C5.0 1.2 1.5 1.5 0 0Z" fill="#22c55e" transform="translate(612 37) rotate(145)"/><path d="M612 37 q0.5 -2.0 1.5 -3.5" stroke="#15803d" stroke-width="1.0" stroke-linecap="round" fill="none"/></svg>' ],
	[ 'night', 'Night Sky', '<svg viewBox="0 0 1072 180" preserveAspectRatio="xMidYMid slice" aria-hidden="true"><defs><linearGradient id="tt_ng" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#020617"/><stop offset=".6" stop-color="#1e1b4b"/><stop offset="1" stop-color="#3b2f7a"/></linearGradient><mask id="tt_nm"><rect width="1072" height="180" fill="#fff"/><circle cx="898" cy="48" r="30" fill="#000"/></mask></defs><rect width="1072" height="180" fill="url(#tt_ng)"/><circle cx="60" cy="30" r="1.2" fill="#fff" opacity="0.80"/><circle cx="180" cy="60" r="1.0" fill="#fff" opacity="0.60"/><circle cx="300" cy="22" r="1.5" fill="#fff" opacity="0.90"/><circle cx="420" cy="70" r="1.0" fill="#fff" opacity="0.50"/><circle cx="520" cy="30" r="1.3" fill="#fff" opacity="0.80"/><circle cx="610" cy="90" r="1.0" fill="#fff" opacity="0.50"/><circle cx="700" cy="20" r="1.6" fill="#fff" opacity="0.90"/><circle cx="760" cy="64" r="1.0" fill="#fff" opacity="0.60"/><circle cx="980" cy="90" r="1.2" fill="#fff" opacity="0.70"/><circle cx="1040" cy="30" r="1.0" fill="#fff" opacity="0.60"/><circle cx="380" cy="110" r="1.0" fill="#fff" opacity="0.40"/><circle cx="150" cy="120" r="1.0" fill="#fff" opacity="0.40"/><circle cx="880" cy="58" r="34" fill="#fef3c7" mask="url(#tt_nm)"/><circle cx="880" cy="58" r="52" fill="#fef3c7" opacity=".06"/><path d="M0 180 L0 140 L120 96 L220 130 L330 84 L450 128 L560 100 L680 140 L800 104 L930 136 L1072 98 L1072 180 Z" fill="#141033"/><path d="M0 180 L0 160 L160 130 L300 158 L470 126 L620 160 L780 134 L940 162 L1072 140 L1072 180 Z" fill="#0b0920"/></svg>' ],
	[ 'forest', 'Forest', '<svg viewBox="0 0 1072 180" preserveAspectRatio="xMidYMid slice" aria-hidden="true"><defs><linearGradient id="tt_fg" x1="0" y1="0" x2="1" y2="0"><stop offset="0" stop-color="#052e16"/><stop offset=".6" stop-color="#166534"/><stop offset="1" stop-color="#65a30d"/></linearGradient></defs><rect width="1072" height="180" fill="url(#tt_fg)"/><circle cx="900" cy="70" r="44" fill="#fde68a" opacity=".9"/><path d="M0 180 L0 120 Q200 70 420 118 T820 104 T1072 96 L1072 180 Z" fill="#14532d" opacity=".9"/><path d="M0 180 L0 146 Q260 110 520 146 T1072 132 L1072 180 Z" fill="#052e16"/><path d="M560 150 l14 -40 l14 40 z" fill="#022c16"/><path d="M600 150 l14 -40 l14 40 z" fill="#022c16"/><path d="M640 150 l14 -40 l14 40 z" fill="#022c16"/><path d="M980 150 l14 -40 l14 40 z" fill="#022c16"/><path d="M1020 150 l14 -40 l14 40 z" fill="#022c16"/></svg>' ],
	[ 'ocean', 'Ocean', '<svg viewBox="0 0 1072 180" preserveAspectRatio="xMidYMid slice" aria-hidden="true"><defs><linearGradient id="tt_og" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#082f49"/><stop offset=".55" stop-color="#0369a1"/><stop offset="1" stop-color="#38bdf8"/></linearGradient></defs><rect width="1072" height="180" fill="url(#tt_og)"/><circle cx="880" cy="64" r="40" fill="#fef9c3" opacity=".95"/><path d="M0 120 Q90 100 180 120 T360 120 T540 120 T720 120 T900 120 T1080 120 L1072 180 L0 180 Z" fill="#fff" opacity=".12"/><path d="M0 140 Q90 122 180 140 T360 140 T540 140 T720 140 T900 140 T1080 140 L1072 180 L0 180 Z" fill="#fff" opacity=".18"/><path d="M0 160 Q90 146 180 160 T360 160 T540 160 T720 160 T900 160 T1080 160 L1072 180 L0 180 Z" fill="#082f49" opacity=".6"/></svg>' ],
	[ 'minimal', 'Minimal', '<svg viewBox="0 0 1072 180" preserveAspectRatio="xMidYMid slice" aria-hidden="true"><defs><linearGradient id="tt_mg" x1="0" y1="0" x2="1" y2="0"><stop offset="0" stop-color="#0f172a"/><stop offset="1" stop-color="#334155"/></linearGradient><pattern id="tt_mp" width="36" height="36" patternUnits="userSpaceOnUse"><path d="M36 0H0V36" fill="none" stroke="#fff" stroke-opacity=".06"/></pattern></defs><rect width="1072" height="180" fill="url(#tt_mg)"/><rect width="1072" height="180" fill="url(#tt_mp)"/><path d="M700 180 L860 0 L900 0 L740 180 Z" fill="#dc2626" opacity=".85"/><path d="M770 180 L930 0 L950 0 L790 180 Z" fill="#f97316" opacity=".7"/></svg>' ],
	[ 'dusk', 'Dusk City', BANNER_SVG ]
];
function bannerSvg(k) { var b = BANNERS.filter(function(x) { return x[0] === k; })[0] || BANNERS[0]; return b[2]; }

function raw(html) { var s = E('div', { 'style': 'display:contents' }); s.innerHTML = html; return s; }

function ctl(args) {
	return fs.exec(CTL, args).then(function(r) { return (r && r.stdout) || ''; }).catch(function() { return ''; });
}
function reloadConf() { uci.unload(CONF); return uci.load(CONF); }
function ctlJSON(args) {
	return ctl(args).then(function(s) { try { return JSON.parse(s); } catch (e) { return {}; } });
}

function fmtBytes(n) {
	n = +n || 0;
	if (n < 1024) return n + ' B';
	var u = [ 'KB', 'MB', 'GB', 'TB' ], i = -1;
	do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
	return n.toFixed(n < 10 ? 2 : 1) + ' ' + u[i];
}
function fmtRate(bps) {
	var b = (+bps || 0) * 8;
	if (b < 1000) return b + ' bps';
	if (b < 1e6) return (b / 1e3).toFixed(0) + ' Kbps';
	return (b / 1e6).toFixed(1) + ' Mbps';
}
function fmtDur(sec) {
	sec = Math.max(0, sec | 0);
	var h = Math.floor(sec / 3600), m = Math.floor(sec % 3600 / 60), s = sec % 60;
	return (h < 10 ? '0' : '') + h + ':' + (m < 10 ? '0' : '') + m + ':' + (s < 10 ? '0' : '') + s;
}
function hhmm(ts) { var d = new Date(ts * 1000); return ('0' + d.getHours()).slice(-2) + ':' + ('0' + d.getMinutes()).slice(-2); }

var uid = 0;
function section(title, kids, descr) {
	return E('div', { 'class': 'cbi-section' }, [
		title ? E('h3', {}, title) : '',
		descr ? E('div', { 'class': 'cbi-section-descr' }, descr) : '',
		E('div', { 'class': 'cbi-section-node' }, kids)
	]);
}
function row(title, widget, descr) {
	var sw = widget && widget.classList && widget.classList.contains('cbi-checkbox');
	return E('div', { 'class': 'cbi-value' + (sw ? ' tt-sw' : '') }, [
		E('label', { 'class': 'cbi-value-title' }, title),
		E('div', { 'class': 'cbi-value-field' }, [ widget, descr ? E('div', { 'class': 'cbi-value-description' }, descr) : '' ])
	]);
}
function textInput(value, oninput, attrs) {
	var a = { 'class': 'cbi-input-text', 'type': 'text', 'value': value == null ? '' : value, 'input': function(ev) { oninput(ev.target.value); } };
	Object.keys(attrs || {}).forEach(function(k) { a[k] = attrs[k]; });
	return E('input', a);
}
function select(value, options, onchange) {
	return E('select', { 'class': 'cbi-input-select', 'change': function(ev) { onchange(ev.target.value); } },
		options.map(function(o) { return E('option', { 'value': o[0], 'selected': o[0] === value ? '' : null }, o[1]); }));
}
function checkbox(checked, onchange) {
	var id = 'tt-cb-' + (++uid);
	return E('div', { 'class': 'cbi-checkbox' }, [
		E('input', { 'type': 'checkbox', 'id': id, 'checked': checked ? '' : null, 'change': function(ev) { onchange(ev.target.checked); } }),
		E('label', { 'for': id })
	]);
}
function button(label, kind, fn) {
	return E('button', { 'class': 'btn cbi-button cbi-button-' + kind + ' tt-cbtn', 'type': 'button', 'click': fn }, label);
}
function table(head, rows) {
	return E('table', { 'class': 'table tt-table' }, [
		head ? E('tr', { 'class': 'tr table-titles' }, head.map(function(h) { return E('th', { 'class': 'th left' }, h); })) : ''
	].concat(rows.map(function(r) {
		return E('tr', { 'class': 'tr' }, r.map(function(c, i) {
			return E('td', { 'class': 'td left', 'width': (!head && i === 0) ? '33%' : null, 'data-title': head ? head[i] : null }, c);
		}));
	})));
}
var LVL = { 'OK': 'ok', 'WRN': 'warn', 'ERR': 'err', 'DBG': 'dbg', 'MSG': 'msg' };

function paintLog(box, text, max) {
	var lines = (text || '').split('\n').filter(function(l) { return l.length; });
	if (max) lines = lines.slice(-max);
	box.innerHTML = '';
	if (!lines.length) { box.appendChild(E('div', { 'class': 'tt-l dbg' }, 'No log yet.')); return; }
	lines.forEach(function(l) {
		var m = l.match(/^(\d\d:\d\d:\d\d)\s+\S+\s+(?:(OK|WRN|ERR|DBG|MSG) )?(.*)$/);
		if (!m) { box.appendChild(E('div', { 'class': 'tt-l' }, l)); return; }
		box.appendChild(E('div', { 'class': 'tt-l ' + (LVL[m[2]] || 'info') }, [
			E('span', { 'class': 'tt-t' }, m[1]), E('span', { 'class': 'tt-msg' }, m[3])
		]));
	});
}

function logText(text, max) {
	var lines = (text || '').split('\n').filter(function(l) { return l.length; });
	if (max) lines = lines.slice(-max);
	return lines.length ? lines.map(function(l) { return l.replace(/ (ERR|DBG) /, function(m, x) { return x === 'ERR' ? ' ✗ ' : ' · '; }); }).join('\n') : 'No log yet.';
}

return view.extend({
	load: function() {
		return ctl([ 'normalize' ]).then(function() { return Promise.all([
			uci.load(CONF),
			ctlJSON([ 'status' ]),
			ctlJSON([ 'update-cached' ]),
			ctl([ 'version' ]),
			ctl([ 'ifaces' ]),
			ctlJSON([ 'latency-cached' ])
		]); });
	},

	get: function(o, def) { var v = uci.get(CONF, 'main', o); return (v == null || v === '') ? def : v; },
	set: function(o, v) { uci.set(CONF, 'main', o, v); },
	profiles: function() {
		var list = uci.sections(CONF, 'profile');
		list.forEach(function(s, i) { s._i = i; });
		return list.sort(function(a, b) { return ((+a.order || 1000 + a._i) - (+b.order || 1000 + b._i)); });
	},
	activeId: function() {
		var id = this.get('profile', ''), ps = this.profiles();
		if (id && ps.some(function(p) { return p['.name'] === id; })) return id;
		return ps.length ? ps[0]['.name'] : '';
	},
	commit: function(msg) {
		return uci.save().then(function() { return callCommit(CONF); }).then(function() {
			return uci.load(CONF);
		}).then(function() {
			if (ui.changes && ui.changes.init) ui.changes.init();
			if (msg !== false) ui.addNotification(null, E('p', msg || 'Saved and applied.'), 'info');
		}).catch(function(e) {
			ui.addNotification(null, E('p', 'Could not save: ' + (e.message || e)), 'danger');
		});
	},

	render: function(data) {
		this.st = data[1] || { state: 'stopped' };
		this.upd = data[2] || {};
		this.version = (data[3] || '').trim() || (this.st.version || '');
		this.ifaces = (data[4] || '').split('\n').filter(function(x) { return x; });
		this.lat = data[5] && data[5].results ? data[5] : null;
		this.hist = [];
		this.sel = this.activeId();
		this.tab = (location.hash || '').replace('#', '');
		if (this.tab === 'status') this.tab = 'dashboard';
		if (this.tab === 'about') { this.tab = 'settings'; this.sub = 'about'; }
		if (!TABS.some(function(t) { return t[0] === this.tab; }, this)) this.tab = 'dashboard';

		var self = this;
		this.tabsEl = E('ul', { 'class': 'cbi-tabmenu' });
		this.bodyEl = E('div', { 'class': 'tt-wrap' });
		var root = E('div', { 'class': 'cbi-map tt-wrap' }, [
			E('link', { 'rel': 'stylesheet', 'href': L.resource('tomato-tunnel/tt.css') + '?v=' + encodeURIComponent(this.version) + '-11' }),
			E('h2', { 'name': 'content' }, 'Tomato Tunnel'),
			E('div', { 'class': 'cbi-map-descr' }, [ 'SSH and ZiVPN tunnel · v' + this.version + ' · by ',
				E('a', { 'href': TG, 'target': '_blank', 'rel': 'noopener' }, '@dudulrealnofek') ]),
			this.bannerEl = E('div'),
			this.tabsEl,
			this.bodyEl
		]);
		this.show(this.tab);
		poll.add(function() { return self.refresh(); }, 2);
		return root;
	},

	show: function(tab) {
		var self = this;
		this.tab = tab;
		if (location.hash !== '#' + tab) history.replaceState(null, '', '#' + tab);
		this.tabsEl.innerHTML = '';
		TABS.forEach(function(t) {
			self.tabsEl.appendChild(E('li', { 'class': t[0] === tab ? 'cbi-tab' : 'cbi-tab-disabled' },
				E('a', { 'href': '#' + t[0], 'click': function(ev) { ev.preventDefault(); self.show(t[0]); } }, t[1])));
		});
		this.bannerEl.innerHTML = '';
		if (tab === 'dashboard') {
			var b = this.renderBanner();
			if (b) this.bannerEl.appendChild(b);
		}
		this.refs = {};
		var body;
		switch (tab) {
		case 'accounts': body = this.renderAccounts(); break;
		case 'settings': body = this.renderSettings(); break;
		case 'usage': body = this.renderUsage(); break;
		case 'log': body = this.renderLog(); break;
		default: body = this.renderStatus();
		}
		this.bodyEl.innerHTML = '';
		body.forEach(function(k) { if (k) self.bodyEl.appendChild(k); });
		this.paintStatus();
		if (tab === 'log' || tab === 'dashboard') this.refreshLog();
	},

	refresh: function() {
		var self = this;
		return ctlJSON([ 'status' ]).then(function(st) {
			self.st = st || { state: 'stopped' };
			self.sample();
			self.paintStatus();
			self.ticks = (self.ticks || 0) + 1;
			if (self.tab === 'dashboard' && self.ticks % 5 === 0 && self.policy() === 'auto')
				ctlJSON([ 'latency-cached' ]).then(function(l) { if (l && l.results && (!self.lat || l.at !== self.lat.at)) { self.lat = l; self.paintProfiles(); } });
			if (self.tab === 'usage' && self.ticks % 15 === 0) return self.loadUsage();
			if (self.tab === 'log' || self.tab === 'dashboard') return self.refreshLog();
			if (self.tab === 'settings' && self.sub === 'about' && self.updating) return self.refreshUpdateLog();
		});
	},

	refreshLog: function() {
		var self = this, r = this.refs;
		if (!r.log) return Promise.resolve();
		var short = this.tab === 'dashboard';
		return ctl([ 'log', short ? '12' : '1000' ]).then(function(txt) {
			if (!r.log || txt === r.lastLog) return;
			r.lastLog = txt;
			paintLog(r.log, txt, short ? 12 : 0);
			if (!short && self.autoScroll !== false) r.log.scrollTop = r.log.scrollHeight;
		});
	},

	renderBanner: function() {
		var mode = this.get('banner', 'tomato');
		if (mode === 'default') mode = 'tomato';
		if (mode === 'none') return null;
		var art = mode === 'custom' ? E('img', { 'src': '/luci-static/tomato-tunnel/banner.img?' + Date.now(), 'alt': '' }) : raw(bannerSvg(mode));
		return E('div', { 'class': 'tt-banner' }, [ art, E('div', { 'class': 'tt-bt' }, [
			E('span', { 'class': 'tt-name' }, 'Tomato Tunnel'),
			E('span', { 'class': 'tt-sub' }, 'SSH · Payload · SSL/SNI · WebSocket · SlowDNS · Enhanced · ZiVPN'),
			E('a', { 'href': TG, 'target': '_blank', 'rel': 'noopener' }, 'by @dudulrealnofek')
		]) ]);
	},

	renderStatus: function() {
		var r = this.refs, self = this, out = [];
		if (this.upd && this.upd.available)
			out.push(E('div', { 'class': 'alert-message warning' }, [
				E('strong', {}, 'Update available: '), 'v' + this.upd.latest + ' is on GitHub (you have v' + this.version + '). ',
				E('a', { 'href': '#settings', 'click': function(ev) { ev.preventDefault(); self.sub = 'about'; self.show('settings'); } }, "See what's new")
			]));
		if (!this.profiles().length) {
			var step = function(n, on, t, d) {
				return E('li', { 'class': 'tt-step' + (on ? ' on' : '') }, [ E('span', { 'class': 'tt-stepn' }, String(n)), E('span', {}, [ E('strong', {}, t), E('span', { 'class': 'tt-ss' }, d) ]) ]);
			};
			out.push(section(null, [
				E('div', { 'class': 'tt-state' }, [ E('span', { 'class': 'tt-muted' }, 'Not set up yet') ]),
				E('p', {}, 'Three steps and your whole network goes through the tunnel.'),
				E('ol', { 'class': 'tt-steps' }, [
					step(1, true, 'Add your account', 'SSH, SlowDNS or ZiVPN from your seller. Or paste the account message.'),
					step(2, false, 'Press Start', 'The log shows each step while it connects.'),
					step(3, false, 'Turn on Start on boot', 'Settings › General, so it connects by itself after a restart.')
				]),
				E('div', { 'class': 'tt-actions' }, [
					button('Add account', 'add', function() { self.show('accounts'); self.newProfile(); }),
					button('Paste account text', 'action', function() { self.show('accounts'); self.importDialog(); })
				])
			]));
			return out;
		}
		var stat = function(label, key, cls) {
			r[key] = E('div', { 'class': 'tt-sv ' + (cls || '') }, '—');
			r[key + 'Sub'] = E('div', { 'class': 'tt-ss' }, '');
			return E('div', { 'class': 'tt-stat' }, [ E('div', { 'class': 'tt-sl' }, label), r[key], r[key + 'Sub'] ]);
		};
		r.dash = true;
		r.stateEl = E('div', { 'class': 'tt-state' });
		r.subEl = E('div', { 'class': 'tt-sub2' });
		r.acts = E('div', { 'class': 'tt-actions tt-hacts' });
		r.swBox = E('div');
		r.noteBox = E('div');
		r.chart = E('div', { 'class': 'tt-chartbox' });
		r.det = E('div', { 'class': 'tt-det' });
		out.push(section(null, [
			E('div', { 'class': 'tt-head' }, [ E('div', { 'class': 'tt-hl' }, [ r.stateEl, r.subEl ]), r.acts ]),
			r.swBox,
			r.noteBox,
			E('div', { 'class': 'tt-stats' }, [ stat('Download', 'dl', 'tt-dl'), stat('Upload', 'ul', 'tt-ul'), stat('Ping', 'ping', 'tt-ok'), stat('Today', 'today') ]),
			E('div', { 'class': 'tt-dash2' }, [
				E('div', { 'class': 'tt-chart' }, [
					E('div', { 'class': 'tt-chart-h' }, [ E('span', {}, 'Traffic · last 60 s'),
						E('span', {}, [ E('span', { 'class': 'tt-dl' }, '━'), ' download  ', E('span', { 'class': 'tt-ul' }, '━'), ' upload' ]) ]),
					r.chart
				]),
				r.det
			])
		]));

		r.polEl = E('div', { 'class': 'tt-seg', 'role': 'group', 'aria-label': 'Profile selection' });
		r.polHint = E('div', { 'class': 'cbi-section-descr tt-polhint' });
		r.testBtn = E('button', { 'type': 'button', 'class': 'btn cbi-button cbi-button-neutral tt-ic', 'title': 'Test latency', 'aria-label': 'Test latency',
			'click': function(ev) { self.testLatency(ev.currentTarget); } }, raw(BOLT));
		r.grid = E('div', { 'class': 'tt-grid' });
		out.push(E('div', { 'class': 'cbi-section' }, [
			E('div', { 'class': 'tt-sechead' }, [ E('h3', {}, [ 'Profiles ', E('small', { 'class': 'tt-muted' }, '· click one to switch, the internet stays on') ]), r.polEl, r.testBtn ]),
			E('div', { 'class': 'cbi-section-node' }, [ r.polHint, r.grid ])
		]));

		r.log = E('div', { 'class': 'tt-logbox' });
		out.push(E('div', { 'class': 'cbi-section' }, [
			E('div', { 'class': 'tt-sechead' }, [ E('h3', {}, 'Recent log'), E('a', { 'href': '#log', 'click': function(ev) { ev.preventDefault(); self.show('log'); } }, 'Open full log') ]),
			E('div', { 'class': 'cbi-section-node' }, [ r.log ])
		]));
		return out;
	},

	paintChart: function() {
		var r = this.refs, h = this.hist || [];
		if (!r.chart) return;
		var W = 600, H = 120, n = 30, max = 1;
		var sm = h.map(function(s, i) {
			var a = h[i - 1] || s, b = h[i + 1] || s;
			return [ (a[0] + 2 * s[0] + b[0]) / 4, (a[1] + 2 * s[1] + b[1]) / 4 ];
		});
		sm.forEach(function(s) { max = Math.max(max, s[0], s[1]); });
		max *= 1.15;
		var pts = function(k) {
			return sm.map(function(s, i) { return [ W * (n - sm.length + i) / (n - 1), H - 3 - (H - 10) * s[k] / max ]; });
		};
		var line = function(k, cls) {
			var p = pts(k), d = smooth(p);
			if (!d) return '';
			return '<path class="' + cls + 'a" d="' + d + ' L' + p[p.length - 1][0].toFixed(1) + ',' + H + ' L' + p[0][0].toFixed(1) + ',' + H + ' Z"/>' +
				'<path class="' + cls + '" d="' + d + '"/>';
		};
		var grid = '<line x1="0" y1="30" x2="600" y2="30"/><line x1="0" y1="60" x2="600" y2="60"/><line x1="0" y1="90" x2="600" y2="90"/>';
		r.chart.innerHTML = '<svg viewBox="0 0 600 120" preserveAspectRatio="none" aria-hidden="true">' +
			'<g class="tt-cg">' + grid + '</g>' +
			(sm.length > 1 ? line(0, 'tt-cd') + line(1, 'tt-cu') : '') +
			'</svg>' + (sm.length > 1 ? '<div class="tt-cmax">' + fmtRate(max) + '</div>' : '');
	},

	paintStatus: function() {
		var r = this.refs, st = this.st || {}, self = this;
		if (!r || !r.dash) return;
		var s = st.state || 'stopped';
		var cls = 'tt-bad', txt = 'Stopped';
		if (s === 'connected') { cls = 'tt-ok'; txt = 'Connected'; }
		else if (s === 'connecting' || s === 'reconnecting' || s === 'starting') { cls = 'tt-busy'; txt = s === 'reconnecting' ? 'Reconnecting…' : 'Connecting…'; }
		else if (s === 'failed') { txt = 'Failed'; }
		var on = s === 'connected', live = s !== 'stopped';
		var actId = live && st.profile ? st.profile : this.activeId();
		var act = uci.get(CONF, actId) || {};
		var name = live && st.profile_name ? st.profile_name : (act.name || '');
		r.stateEl.innerHTML = '';
		r.stateEl.appendChild(E('span', { 'class': 'tt-dotst ' + cls }));
		r.stateEl.appendChild(E('span', { 'class': cls }, txt));
		if (name) r.stateEl.appendChild(E('span', { 'class': 'tt-sname' }, name + (live ? '' : ' (selected)')));
		var l = [];
		if (act['.name']) l.push(niceLabel(act));
		if (live) l.push({ tun: 'all LAN traffic', redirect: 'LAN TCP only', socks: 'SOCKS5 only' }[st.net_mode || this.get('net_mode', 'tun')] || '');
		if (on && st.since) l.push('up ' + fmtDur((st.updated || Date.now() / 1000) - st.since));
		if (st.changing_ip) l.push('changing IP…');
		else if (st.message && !on) l.push(st.message);
		r.subEl.innerHTML = '';
		r.subEl.appendChild(E('span', {}, l.filter(function(x) { return x; }).join(' · ')));

		r.dl.textContent = on ? fmtRate(st.rate_rx) : '—';
		r.dlSub.textContent = live ? fmtBytes(st.rx) + ' this session' : '';
		r.ul.textContent = on ? fmtRate(st.rate_tx) : '—';
		r.ulSub.textContent = live ? fmtBytes(st.tx) + ' this session' : '';
		r.ping.textContent = on && st.ping_ms ? st.ping_ms + ' ms' : '—';
		r.pingSub.textContent = on && st.ping_ms ? (st.ping_ms < 150 ? 'good' : (st.ping_ms < 400 ? 'ok' : 'slow')) : '';
		r.today.textContent = st.today_bytes != null && live ? fmtBytes(st.today_bytes) : '—';
		var q = +this.get('usage_quota_gb', '0');
		r.todaySub.textContent = q > 0 && st.period_bytes != null ? Math.round(st.period_bytes / (q * 1073741824) * 100) + '% of the ' + q + ' GB warning' : 'through the tunnel';
		this.paintChart();

		var L = layers(act), where = act['.name'] ? (L.dns ? 'NS ' + (act.dns_domain || '?') : (act.host || '?') + ':' + (act.port || defPort(L))) : '—';
		var ex = act['.name'] ? expInfo(act, +this.get('expiry_warn', '3')) : null;
		var drow = function(k, v, c, t) { return E('div', { 'class': 'tt-drow' }, [ E('span', { 'class': 'tt-muted' }, k), E('span', { 'class': c || '', 'title': t || null }, v) ]); };
		var dkey = [ on, st.public_ip, st.country, st.isp, where, ex && ex.text, st.reconnects_today, st.banner ].join('|');
		if (r.detKey !== dkey) {
			r.detKey = dkey;
			r.det.innerHTML = '';
			r.det.appendChild(drow('Public IP', on && st.public_ip ? st.public_ip : '—', 'tt-mono'));
			r.det.appendChild(drow('Network', on ? ([ st.country, st.isp ].filter(function(x) { return x; }).join(' · ') || '—') : '—'));
			r.det.appendChild(drow('Server', where, 'tt-mono', on && st.banner ? st.banner + ' · ' + (st.kex || '') : null));
			r.det.appendChild(drow('Account', ex ? ex.text : 'no expiry set', ex && (ex.soon || ex.expired) ? (ex.expired ? 'tt-bad' : 'tt-busy') : ''));
			r.det.appendChild(drow('Reconnects today', on ? String(st.reconnects_today || 0) : '—'));
		}

		r.swBox.innerHTML = '';
		if (live && st.switching)
			r.swBox.appendChild(E('div', { 'class': 'alert-message warning tt-swbox' }, [
				E('strong', {}, 'Switching to ' + st.switching + '… '),
				'Internet keeps working on ' + (st.profile_name || 'the current profile') + '. If the new profile fails, nothing changes. ',
				button('Cancel switch', 'neutral', function(ev) { ev.currentTarget.disabled = true; ctl([ 'cancel-switch' ]).then(function() { self.refresh(); }); })
			]));

		this.paintNotes();
		var ipBtn = this.get('ip_method', this.get('restart_modem', '0') === '1' ? 'iface' : 'none') !== 'none' && this.get('ip_button', '1') !== '0';
		var key = (live ? 'on' : 'off') + (ipBtn ? (st.changing_ip ? 'C' : 'c') : '');
		if (r.actsKey !== key) {
			r.actsKey = key;
			r.acts.innerHTML = '';
			if (ipBtn) {
				var cb = button(st.changing_ip ? 'Changing IP…' : 'Change IP', 'neutral', function(ev) { self.changeIP(ev.currentTarget); });
				if (st.changing_ip) cb.disabled = true;
				r.acts.appendChild(cb);
			}
			if (!live) r.acts.appendChild(button('Start', 'positive', function(ev) { self.act(ev.currentTarget, 'start'); }));
			else {
				r.acts.appendChild(button('Restart', 'reload', function(ev) { self.act(ev.currentTarget, 'restart'); }));
				r.acts.appendChild(button('Stop', 'negative', function(ev) { self.act(ev.currentTarget, 'stop'); }));
			}
		}
		this.paintProfiles();
	},

	changeIP: function(btn) {
		var self = this;
		if (!confirm('Change the IP now? The tunnel reconnects after the new IP is up.')) return;
		btn.disabled = true;
		return ctl([ 'changeip' ]).then(function() {
			ui.addNotification(null, E('p', 'Changing IP… progress is in the log.'), 'info');
			setTimeout(function() { self.refresh(); }, 800);
		});
	},

	paintNotes: function() {
		var r = this.refs, st = this.st || {}, self = this;
		if (!r.noteBox) return;
		var notes = [];
		var q = +this.get('usage_quota_gb', '0'), used = +st.period_bytes || 0;
		if (q > 0 && used >= q * 0.8 * 1073741824) {
			var pct = Math.round(used / (q * 1073741824) * 100);
			notes.push([ pct >= 100 ? 'error' : 'warning', 'Quota warning: ' + fmtBytes(used) + ' of ' + q + ' GB used this period (' + pct + '%).' ]);
		}
		var warn = +this.get('expiry_warn', '3');
		this.profiles().forEach(function(p) {
			var x = expInfo(p, warn);
			if (x && x.soon) notes.push([ 'warning', (p.name || p['.name']) + ' expires ' + (x.days < 1 ? 'today' : 'in ' + x.text.replace(' left', '')) + ' (' + dm(x.at) + '). Renew it on the server' + '.' ]);
		});
		var key = JSON.stringify(notes);
		if (r.noteKey === key) return;
		r.noteKey = key;
		r.noteBox.innerHTML = '';
		notes.forEach(function(n) { r.noteBox.appendChild(E('div', { 'class': 'alert-message ' + n[0] }, n[1])); });
	},

	policy: function() {
		var p = this.get('policy', '');
		if (p === 'manual' || p === 'fallback' || p === 'auto') return p;
		return this.get('failover', '1') === '0' ? 'manual' : 'fallback';
	},

	paintProfiles: function() {
		var r = this.refs, st = this.st || {}, self = this;
		if (!r.grid) return;
		var live = (st.state || 'stopped') !== 'stopped';
		var pol = this.policy(), lat = (this.lat && this.lat.results) || {};
		var ps = this.profiles();
		var cur = live && st.profile ? st.profile : this.activeId();
		var lf = st.login_failed || {};
		var warn = +this.get('expiry_warn', '3');
		var key = [ pol, cur, live, st.switching || '', JSON.stringify(lat), JSON.stringify(lf), ps.map(function(p) { return p['.name'] + p.name + (p.expires || '') + (p.sub_status || ''); }).join(), new Date().getDate() ].join('|');
		if (r.gridKey === key) return;
		r.gridKey = key;

		var POL = [ [ 'manual', 'Manual' ], [ 'fallback', 'Fallback' ], [ 'auto', 'Auto' ] ];
		var HINT = {
			manual: 'Manual: stays on the chosen profile.',
			fallback: 'Fallback: next profile when the current one fails.',
			auto: 'Auto: moves to a faster profile (checked every 5 min).'
		};
		r.polEl.innerHTML = '';
		POL.forEach(function(o) {
			r.polEl.appendChild(E('button', { 'type': 'button', 'class': 'btn cbi-button ' + (o[0] === pol ? 'cbi-button-action tt-on' : 'cbi-button-neutral'),
				'aria-pressed': o[0] === pol ? 'true' : 'false',
				'click': function(ev) {
					if (o[0] === pol) return;
					ev.currentTarget.disabled = true;
					ctl([ 'policy', o[0] ]).then(reloadConf).then(function() { r.gridKey = null; self.paintProfiles(); });
				} }, o[1]));
		});
		r.polHint.textContent = HINT[pol];

		r.grid.innerHTML = '';
		if (!ps.length) { r.grid.appendChild(E('em', {}, 'No profiles yet. Add one in the Accounts tab.')); return; }
		ps.forEach(function(p) {
			var id = p['.name'], L = layers(p);
			var where = L.dns ? 'NS ' + (p.dns_domain || '?') : (p.host || '?') + ':' + (p.port || defPort(L));
			var isCur = id === cur, isSw = live && st.switching && st.switching === (p.name || id);
			var t = lat[id], badge = '—', bcls = 'tt-lat';
			if (t) {
				if (t.ms >= 0) { badge = t.ms + ' ms'; bcls += t.ms < 200 ? ' good' : (t.ms < 500 ? ' mid' : ' slow'); }
				else { badge = 'failed'; bcls += ' bad'; }
			}
			var failAt = lf[id];
			if (failAt) { badge = 'login failed'; bcls = 'tt-lat bad'; }
			var tag, tcls;
			if (failAt && !isCur) { tag = 'Login failed at ' + hhmm(failAt) + ' · locked or expired?'; tcls = 'tt-bad'; }
			else if (isCur) { tag = live ? '● Active' : '● Selected'; tcls = 'tt-ok'; }
			else if (isSw) { tag = 'Connecting…'; tcls = 'tt-busy'; }
			else if (t && t.ms < 0) { tag = 'Failed the last test'; tcls = 'tt-bad'; }
			else { tag = live ? 'Click to switch' : 'Click to select'; tcls = 'tt-muted'; }
			var ex = expInfo(p, warn);
			if (ex && ex.expired && !isCur && pol !== 'manual') { tag = 'Skipped by Auto/Fallback'; tcls = 'tt-bad'; }
			var dis = !!(live && st.switching && !isCur);
			var go = function() { if (!isCur && !dis) self.switchTo(id, p.name || id, live); };
			r.grid.appendChild(E('div', {
				'role': 'button',
				'tabindex': (isCur || dis) ? '-1' : '0',
				'aria-disabled': (isCur || dis) ? 'true' : 'false',
				'aria-current': isCur ? 'true' : null,
				'keydown': function(ev) { if (ev.key === 'Enter' || ev.key === ' ') { ev.preventDefault(); go(); } },
				'class': 'tt-tile' + (isCur ? ' cur' : '') + (isSw ? ' sw' : '') + (dis ? ' dis' : ''),
				'title': failAt ? 'The server refused the login (account locked, expired or wrong password). Cleared after a successful login.' : (t ? (t.error || ('TCP ping ' + (t.target || ''))) : null),
				'click': go
			}, [
				E('span', { 'class': 'tt-tt' }, [ E('span', { 'class': 'tt-radio' }), E('strong', {}, p.name || id), E('span', { 'class': bcls }, badge) ]),
				E('span', { 'class': 'tt-td' }, niceLabel(p)),
				E('span', { 'class': 'tt-td tt-mono' }, where),
				E('span', { 'class': 'tt-tf' }, [ E('span', { 'class': 'tt-tg ' + tcls }, tag), ex ? E('span', { 'class': ex.cls }, ex.text) : '' ])
			]));
		});
	},

	switchTo: function(id, name, live) {
		var self = this;
		return ctl([ 'switch', id ]).then(reloadConf).then(function() {
			if (live) ui.addNotification(null, E('p', 'Switching to ' + name + '. The current tunnel stays up until it is ready.'), 'info');
			self.refs.gridKey = null;
			setTimeout(function() { self.refresh(); }, 600);
			if (self.refs.list) self.paintList();
			self.paintProfiles();
		});
	},

	testLatency: function(btn) {
		var self = this;
		btn.disabled = true;
		btn.classList.add('spin');
		return ctlJSON([ 'latency' ]).then(function(r) {
			if (r && r.results) self.lat = r;
			self.refs.gridKey = null;
			self.paintProfiles();
		}).finally(function() { btn.disabled = false; btn.classList.remove('spin'); });
	},

	sample: function() {
		var st = this.st || {};
		if (st.state !== 'connected') { if (st.state === 'stopped') this.hist = []; return; }
		this.hist.push([ +st.rate_rx || 0, +st.rate_tx || 0 ]);
		if (this.hist.length > 30) this.hist.shift();
	},

	act: function(btn, what) {
		var self = this;
		if (what === 'start' && !this.profiles().length) {
			ui.addNotification(null, E('p', 'Add an account in the Accounts tab first.'), 'warning');
			return this.show('accounts');
		}
		btn.disabled = true;
		return ctl([ what ]).then(function() { setTimeout(function() { self.refresh(); }, 800); })
			.finally(function() { btn.disabled = false; });
	},

	renderAccounts: function() {
		var self = this;
		this.refs.list = E('div');
		this.refs.pick = E('div');
		this.refs.editor = E('div');
		this.paintList();
		this.paintEditor();
		return [
			E('div', { 'class': 'cbi-section' }, [
				E('div', { 'class': 'tt-sechead' }, [
					E('h3', {}, [ 'Your accounts ', E('small', { 'class': 'tt-muted' }, '· order = fallback order') ]),
					button('+ Add account', 'add', function() { self.cancelNew(true); self.newProfile(); }),
					button('Import', 'action', function() { self.importDialog(); }),
					button('Backup / restore', 'neutral', function() { self.backupDialog(); })
				]),
				E('div', { 'class': 'cbi-section-node' }, [ this.refs.list ])
			]),
			this.refs.pick,
			this.refs.editor
		];
	},

	newProfile: function() {
		var sid = this.addProfile({}, true);
		this.newId = sid;
		this.paintList();
		this.paintEditor();
		var ed = this.refs.editor;
		if (ed) ed.scrollIntoView({ behavior: 'smooth', block: 'start' });
	},

	cancelNew: function(quiet) {
		var id = this.newId;
		if (!id) return;
		this.newId = null;
		if (uci.get(CONF, id)) uci.remove(CONF, id);
		if (this.get('profile', '') === id) this.set('profile', '');
		if (this.sel === id) { this.sel = null; this.draft = null; }
		if (!quiet) { this.paintList(); this.paintEditor(); }
	},

	backupDialog: function() {
		var self = this;
		ui.showModal('Backup and restore', [
			E('p', {}, 'A backup file contains every account with its password. Keep it private.'),
			E('div', { 'class': 'tt-actions' }, [
				button('Export all accounts (.json)', 'action', function() { self.exportProfiles(); }),
				button('Restore from file', 'action', function() { ui.hideModal(); self.restoreProfiles(); })
			]),
			E('div', { 'class': 'right' }, E('button', { 'class': 'btn cbi-button', 'click': ui.hideModal }, 'Close'))
		]);
	},

	paintList: function() {
		var self = this, ps = this.profiles(), list = this.refs.list, warn = +this.get('expiry_warn', '3');
		if (!list) return;
		list.innerHTML = '';
		var saved = ps.filter(function(p) { return p['.name'] !== self.newId; });
		if (!saved.length) { list.appendChild(E('em', { 'class': 'tt-muted' }, 'No accounts yet. Press + Add account.')); return; }
		var drag = null;
		var box = E('div', { 'class': 'tt-plist' });
		saved.forEach(function(p, i) {
			var id = p['.name'], L = layers(p);
			var where = L.dns ? 'NS ' + (p.dns_domain || '?') : (p.host || '?') + ':' + (p.port || defPort(L));
			var ex = expInfo(p, warn);
			var nm = p.name || id;
			var rowEl = E('div', { 'class': 'tt-pi' + (id === self.sel ? ' sel' : ''), 'draggable': 'true',
				'dragstart': function(ev) { drag = i; rowEl.classList.add('drag'); try { ev.dataTransfer.effectAllowed = 'move'; ev.dataTransfer.setData('text/plain', id); } catch (e) {} },
				'dragend': function() { rowEl.classList.remove('drag'); box.querySelectorAll('.tt-pi.over').forEach(function(x) { x.classList.remove('over'); }); },
				'dragover': function(ev) { if (drag == null || drag === i) return; ev.preventDefault(); rowEl.classList.add('over'); },
				'dragleave': function() { rowEl.classList.remove('over'); },
				'drop': function(ev) { ev.preventDefault(); rowEl.classList.remove('over'); if (drag != null && drag !== i) self.moveTo(saved[drag]['.name'], i); drag = null; }
			}, [
				E('span', { 'class': 'tt-grip', 'title': 'Drag to change the order', 'aria-hidden': 'true' }, '⠿'),
				E('span', { 'class': 'tt-pnum' }, String(i + 1)),
				E('div', { 'class': 'tt-pm' }, [
					E('div', { 'class': 'tt-pn' }, [ E('strong', {}, nm), ex ? E('span', { 'class': ex.cls }, ex.text) : '' ]),
					E('div', { 'class': 'tt-pd' }, [ niceLabel(p), ' · ', E('span', { 'class': 'tt-mono' }, where) ])
				]),
				E('div', { 'class': 'tt-pb' }, [
					E('button', { 'type': 'button', 'class': 'btn cbi-button cbi-button-neutral tt-icb', 'title': 'Move up', 'aria-label': 'Move ' + nm + ' up', 'disabled': i === 0 ? '' : null, 'click': function() { self.moveTo(id, i - 1); } }, '↑'),
					E('button', { 'type': 'button', 'class': 'btn cbi-button cbi-button-neutral tt-icb', 'title': 'Move down', 'aria-label': 'Move ' + nm + ' down', 'disabled': i === saved.length - 1 ? '' : null, 'click': function() { self.moveTo(id, i + 1); } }, '↓'),
					button('Edit', 'edit', function() { self.cancelNew(true); self.sel = id; self.draft = null; self.paintList(); self.paintEditor(); self.refs.editor.scrollIntoView({ behavior: 'smooth' }); }),
					button('Delete', 'remove', function() { self.deleteProfile(id); })
				])
			]);
			box.appendChild(rowEl);
		});
		list.appendChild(box);
		list.appendChild(E('div', { 'class': 'cbi-section-descr tt-dragh' }, 'Drag ⠿ or use ↑ ↓ to change the order. The first account is tried first by Fallback.'));
	},

	addProfile: function(v, batch) {
		if (v.mode) {
			v.use_payload = (v.mode === 'ws' || v.mode === 'wss') ? '1' : '0';
			v.use_tls = v.mode === 'wss' ? '1' : '0';
			delete v.mode;
		}
		var n = 1, max = 0;
		while (uci.get(CONF, 'p' + n)) n++;
		var sid = uci.add(CONF, 'profile', 'p' + n);
		this.profiles().forEach(function(p) { max = Math.max(max, +p.order || 0); });
		var defs = { name: 'New profile', use_payload: '1', use_tls: '0', use_slowdns: '0', enhanced: '0', insecure: '1', port: '80', udpgw: '7300', order: String(max + 1) };
		if (v.type === 'zivpn') { defs.use_payload = '0'; defs.port = '5667'; delete defs.udpgw; }
		Object.keys(defs).forEach(function(k) { uci.set(CONF, sid, k, v[k] != null && v[k] !== '' ? v[k] : defs[k]); });
		Object.keys(v).forEach(function(k) { if (defs[k] == null && k !== 'order' && PROFILE_KEYS.indexOf(k) >= 0 && v[k] !== '') uci.set(CONF, sid, k, String(v[k])); });
		if (!this.get('profile', '')) this.set('profile', sid);
		this.sel = sid;
		this.draft = null;
		if (!batch) { this.paintList(); this.paintEditor(); }
		return sid;
	},

	importDialog: function() {
		var body = E('div', { 'class': 'tt-imp' });
		ui.showModal('Paste account text', [ body ]);
		this.importText(body);
	},

	importText: function(body) {
		var self = this;
		var ta = E('textarea', { 'class': 'cbi-input-textarea tt-log', 'rows': '8', 'placeholder': 'tomatotunnel://…\nhost.example.com:80@user:pass\nssh://user:pass@host:22\n\nor an account message:\nHost : sg1.example.com\nUsername : dudul\nPassword : secret\nNS : ns.example.com\nPub key : 4f1a…\nExpired : 2026-12-20' });
		body.appendChild(E('div', { 'class': 'cbi-section-descr' }, 'Paste the account message from the bot or CLI, or a share link from another router. Lines it does not understand are ignored.'));
		body.appendChild(ta);
		body.appendChild(E('div', { 'class': 'right' }, [
			E('button', { 'class': 'btn cbi-button', 'click': ui.hideModal }, 'Cancel'), ' ',
			E('button', { 'class': 'btn cbi-button cbi-button-positive', 'click': function() {
				var t = ta.value.trim(), list = [];
				t.split(/\s+/).forEach(function(w) {
					if (/^tomatotunnel:\/\//i.test(w)) {
						try { list.push(JSON.parse(b64dec(w.replace(/^tomatotunnel:\/\//i, '')))); } catch (e) {}
					}
				});
				if (!list.length) { var v = self.parseAccount(t); if (v) list.push(v); }
				if (!list.length) { ui.addNotification(null, E('p', 'Could not find host, username and password in that text.'), 'warning'); return; }
				ui.hideModal();
				self.cancelNew(true);
				list.forEach(function(v) { self.addProfile(v, true); });
				self.paintList(); self.paintEditor();
				ui.addNotification(null, E('p', 'Imported ' + list.length + ' profile' + (list.length > 1 ? 's' : '') + '. Check them, then Save & Apply.'), 'info');
			} }, 'Read')
		]));
	},

	exportProfiles: function() {
		var list = this.profiles().map(function(p) {
			var o = {};
			PROFILE_KEYS.forEach(function(k) { if (p[k] != null && p[k] !== '') o[k] = p[k]; });
			return o;
		});
		var blob = new Blob([ JSON.stringify({ app: 'tomato-tunnel', version: this.version, exported: new Date().toISOString(), profiles: list }, null, 2) ], { type: 'application/json' });
		var a = E('a', { 'href': URL.createObjectURL(blob), 'download': 'tomato-tunnel-profiles.json' });
		document.body.appendChild(a); a.click(); a.remove();
	},

	restoreProfiles: function() {
		var self = this;
		var f = E('input', { 'type': 'file', 'accept': '.json,application/json', 'style': 'display:none', 'change': function(ev) {
			var file = ev.target.files[0];
			if (!file) return;
			var rd = new FileReader();
			rd.onload = function() {
				var j;
				try { j = JSON.parse(rd.result); } catch (e) { ui.addNotification(null, E('p', 'That is not a Tomato Tunnel backup.'), 'warning'); return; }
				var list = (j && j.profiles) || (Array.isArray(j) ? j : null);
				if (!list || !list.length) { ui.addNotification(null, E('p', 'No profiles in that file.'), 'warning'); return; }
				var have = {};
				self.profiles().forEach(function(p) { have[(p.name || '') + '|' + (p.host || '') + '|' + (p.user || '')] = 1; });
				var n = 0;
				list.forEach(function(p) {
					if (!p || typeof p !== 'object') return;
					if (have[(p.name || '') + '|' + (p.host || '') + '|' + (p.user || '')]) return;
					self.addProfile(p, true); n++;
				});
				self.paintList(); self.paintEditor();
				self.commit(n ? 'Restored ' + n + ' profile' + (n > 1 ? 's' : '') + '.' : 'Every profile in the file is already here.');
			};
			rd.readAsText(file);
			f.remove();
		} });
		document.body.appendChild(f);
		f.click();
	},

	shareLink: function(id) {
		var p = uci.get(CONF, id) || {}, o = {};
		PROFILE_KEYS.forEach(function(k) { if (k !== 'sub_status' && p[k] != null && p[k] !== '') o[k] = p[k]; });
		var link = 'tomatotunnel://' + b64enc(JSON.stringify(o));
		var ta = E('textarea', { 'class': 'cbi-input-textarea tt-log', 'rows': '5', 'readonly': '', 'style': 'white-space:pre-wrap;word-break:break-all' }, link);
		ui.showModal('Share link: ' + (p.name || id), [
			E('div', { 'class': 'cbi-section-descr' }, 'Import it on another router with Accounts › Import › Text / share link. It contains the password.'),
			ta,
			E('div', { 'class': 'right' }, [
				E('button', { 'class': 'btn cbi-button', 'click': ui.hideModal }, 'Close'), ' ',
				E('button', { 'class': 'btn cbi-button cbi-button-action', 'click': function(ev) {
					ta.select();
					var done = function() { ev.target.textContent = 'Copied'; };
					if (navigator.clipboard && window.isSecureContext) navigator.clipboard.writeText(link).then(done, function() { document.execCommand('copy'); done(); });
					else { document.execCommand('copy'); done(); }
				} }, 'Copy')
			])
		]);
	},

	parseAccount: function(t) {
		t = (t || '').trim();
		var m = t.match(/^ssh:\/\/([^:@\s]+):([^@\s]+)@([^:\s\/]+)(?::(\d+))?/i);
		if (m) return { name: m[3], user: m[1], pass: m[2], host: m[3], port: m[4] || '22', mode: 'direct' };
		m = t.match(/^([^\s:@]+):(\d+)@([^:\s]+):(\S+)$/);
		if (m) return { name: m[1], host: m[1], port: m[2], user: m[3], pass: m[4], mode: +m[2] === 443 ? 'wss' : 'ws' };
		m = t.match(/^([^\s:@]+):([^\s@]+)@([^\s:]+):(\d+)$/);
		if (m) return { name: m[3], host: m[3], port: m[4], user: m[1], pass: m[2], mode: +m[4] === 443 ? 'wss' : 'ws' };
		var get = function(re) {
			var lines = t.split(/\r?\n/);
			for (var i = 0; i < lines.length; i++) {
				var l = lines[i].replace(/[•➤►▶❖◇◆■□●○✦✧⭐️*`|]/g, ' ');
				var mm = l.match(re);
				if (mm) return mm[1].trim();
			}
			return '';
		};
		var host = get(/^\s*(?:host|domain|hostname|server|ip)\s*[:=]\s*([^\s,]+)/i);
		var user = get(/^\s*(?:username|user)\s*[:=]\s*(\S+)/i);
		var pass = get(/^\s*(?:password|pass)\s*[:=]\s*(\S+)/i);
		if (!host || !user || !pass) return null;
		var ns = get(/^\s*(?:ns|ns host|nameserver|ns domain)\s*[:=]\s*(\S+)/i);
		var pk = get(/^\s*(?:pub ?key|public key|key)\s*[:=]\s*([0-9a-f]{64})/i);
		var ex = get(/^\s*(?:expired|expires|exp|expiry|aktif sampai|masa aktif)\s*(?:on)?\s*[:=]\s*(.+)$/i);
		var exd = '';
		var m1 = ex.match(/(\d{4})-(\d\d)-(\d\d)/), m2 = ex.match(/(\d{1,2})[ -]([A-Za-z]{3})[a-z]*[ -,]*(\d{4})/);
		if (m1) exd = m1[0];
		else if (m2 && MON.indexOf(m2[2].charAt(0).toUpperCase() + m2[2].slice(1, 3).toLowerCase()) >= 0)
			exd = m2[3] + '-' + ('0' + (MON.indexOf(m2[2].charAt(0).toUpperCase() + m2[2].slice(1, 3).toLowerCase()) + 1)).slice(-2) + '-' + ('0' + m2[1]).slice(-2);
		var wss = get(/^\s*port\s*(?:ws\s*)?(?:ssl|tls)[^:=]*[:=]\s*(\d+)/i);
		var ws = get(/^\s*port\s*(?:ssh\s*)?(?:ws|websocket|http)[^:=]*[:=]\s*(\d+)/i);
		var any = get(/^\s*port[^:=]*[:=]\s*(\d+)/i);
		var mode = ws ? 'ws' : (wss ? 'wss' : 'direct');
		var o = { name: host.split('.')[0].toUpperCase(), host: host, user: user, pass: pass, mode: mode, port: ws || wss || any || (mode === 'wss' ? '443' : (mode === 'ws' ? '80' : '22')) };
		if (exd) o.expires = exd;
		if (ns && pk && !ws && !wss) { delete o.mode; o.use_payload = '0'; o.use_tls = '0'; o.use_slowdns = '1'; o.dns_domain = ns; o.dns_pubkey = pk; delete o.port; }
		return o;
	},

	paintEditor: function() {
		var ed = this.refs.editor, id = this.sel;
		if (!ed) return;
		ed.innerHTML = '';
		if (!id || !uci.get(CONF, id)) return;
		this.initDraft(id);
		this.paintAdvanced();
	},

	initDraft: function(id) {
		if (this.draft && this.draft['.name'] === id) return;
		var s = uci.get(CONF, id), dd = {};
		Object.keys(s).forEach(function(k) { dd[k] = s[k]; });
		var L0 = layers(dd);
		dd.use_payload = L0.payload ? '1' : '0';
		dd.use_tls = L0.tls ? '1' : '0';
		dd.use_slowdns = L0.dns ? '1' : '0';
		dd.enhanced = L0.enh ? '1' : '0';
		dd.insecure = L0.insecure ? '1' : '0';
		delete dd.mode;
		this.draft = dd;
	},

	expiryRow: function(d) {
		var ex = expInfo(d, +this.get('expiry_warn', '3'));
		var inp = E('input', { 'class': 'cbi-input-text', 'type': 'date', 'value': (d.expires || '').slice(0, 10), 'input': function(ev) { d.expires = ev.target.value; }, 'change': function(ev) { d.expires = ev.target.value; } });
		return row('Expires on', inp, (ex ? ex.text + '. ' : '') + 'Optional. Shows a reminder before it ends; expired accounts are skipped by Auto and Fallback.');
	},

	editorSection: function(rows, d, id, kind) {
		var self = this, isNew = id === this.newId;
		this.refs.testOut = E('div');
		var acts = isNew ? [
			button('Save & connect', 'apply', function() { self.saveDraft('run'); }),
			button('Test first', 'action', function(ev) { self.testProfile(ev.target); }),
			button('Cancel', 'neutral', function() { self.cancelNew(); })
		] : [
			button('Save & connect', 'apply', function() { self.saveDraft('run'); }),
			button('Save', 'save', function() { self.saveDraft(true); }),
			button('Test', 'action', function(ev) { self.testProfile(ev.target); }),
			button('Share link', 'neutral', function() { self.saveDraft(true).then(function() { self.shareLink(id); }); }),
			button('Delete', 'remove', function() { self.deleteProfile(id); })
		];
		var head = E('div', { 'class': 'tt-sechead' }, [ E('h3', {}, isNew ? 'New account' : 'Edit: ' + (d.name || id)) ]);
		this.refs.editor.appendChild(E('div', { 'class': 'cbi-section' }, [ head, E('div', { 'class': 'cbi-section-node' }, rows.concat([ E('div', { 'class': 'tt-actions' }, acts), this.refs.testOut ])) ]));
	},

	paintAdvanced: function() {
		var self = this, ed = this.refs.editor, id = this.sel;
		ed.innerHTML = '';
		if (!id || !uci.get(CONF, id)) return;
		if (!this.draft || this.draft['.name'] !== id) {
			var s = uci.get(CONF, id), dd = {};
			Object.keys(s).forEach(function(k) { dd[k] = s[k]; });
			var L0 = layers(dd);
			dd.use_payload = L0.payload ? '1' : '0';
			dd.use_tls = L0.tls ? '1' : '0';
			dd.use_slowdns = L0.dns ? '1' : '0';
			dd.enhanced = L0.enh ? '1' : '0';
			dd.insecure = L0.insecure ? '1' : '0';
			delete dd.mode;
			this.draft = dd;
		}
		var d = this.draft;
		var L = layers(d);
		var bind = function(k, attrs) { return textInput(d[k], function(v) { d[k] = v; }, attrs); };
		var isDns = L.dns, canEnh = L.payload;
		var rows = [];
		var flag = function(k, after) {
			return checkbox(d[k] === '1', function(v) {
				var before = defPort(layers(d));
				d[k] = v ? '1' : '0';
				if (k === 'use_payload' && !v) d.enhanced = '0';
				if (!d.port || d.port === before) d.port = defPort(layers(d));
				if (after !== false) self.paintEditor();
			});
		};
		var presetKey = (L.payload ? 'p' : '') + (L.tls ? 't' : '') + (L.enh ? 'e' : '') + (L.dns ? 'd' : '');
		var known = PRESETS.some(function(x) { return x[0] === presetKey; });

		var custom = !L.zivpn && (!known || this.customLayers === id);
		var payloadText = d.payload || '';
		var hint = [];
		if (L.dns) hint.push('carried over DNS (SlowDNS)');
		if (L.tls) hint.push('TLS with SNI');
		if (L.payload) hint.push(L.enh ? 'payload sent in parts at [split], all HTTP replies skipped (Enhanced)' : 'HTTP payload before SSH');

		rows.push(row('Profile name', bind('name')));
		rows.push(row('Connection', select(L.zivpn ? 'z' : (custom ? '*' : presetKey), PRESETS.map(function(x) { return [ x[0], x[1] ]; }).concat([ [ '*', custom && !known ? layerLabel(L) + ' (custom)' : 'Custom combination…' ], [ 'z', 'ZiVPN (UDP · Hysteria2)' ] ]), function(v) {
			var before = defPort(layers(d));
			if (v === 'z') {
				d.type = 'zivpn'; d.use_payload = d.use_tls = d.use_slowdns = d.enhanced = '0';
				if (!d.port || d.port === before) d.port = '5667';
				self.customLayers = null;
				return self.paintEditor();
			}
			if (d.type === 'zivpn') { d.type = ''; }
			if (v === '*') { self.customLayers = id; return self.paintEditor(); }
			self.customLayers = null;
			var x = PRESETS.filter(function(y) { return y[0] === v; })[0];
			d.use_payload = x[2] ? '1' : '0'; d.use_tls = x[3] ? '1' : '0'; d.enhanced = x[4] ? '1' : '0'; d.use_slowdns = x[5] ? '1' : '0';
			if (!d.port || d.port === before) d.port = defPort(layers(d));
			self.paintEditor();
		}), L.zivpn ? 'UDP tunnel of the ZiVPN app. Carries TCP and UDP, no UDPGW needed.' : (hint.length ? 'SSH ' + hint.join(' · ') + '.' : 'Plain SSH to the server.')));
		if (L.zivpn) {
			rows.push(row('Server host', bind('host', { 'placeholder': 'sg1.example.com', 'autocapitalize': 'off', 'spellcheck': 'false' })));
			rows.push(row('UDP port', bind('port', { 'inputmode': 'numeric', 'placeholder': '5667' }), '5667, or any port from 6000–19999.'));
			var zpw = bind('pass', { 'type': 'password', 'autocomplete': 'new-password', 'style': 'flex:1 1 auto;min-width:0;width:auto' });
			var zeye = E('button', { 'class': 'btn cbi-button cbi-button-neutral', 'type': 'button', 'style': 'flex:0 0 auto;margin:0', 'click': function() {
				var show = zpw.type === 'password';
				zpw.type = show ? 'text' : 'password';
				zeye.textContent = show ? 'Hide' : 'Show';
			} }, 'Show');
			rows.push(row('Password', E('div', { 'class': 'tt-inline' }, [ zpw, zeye ]), 'ZiVPN password. No username needed.'));
		}
		if (!L.zivpn) {
		if (custom) {
			rows.push(row('Payload', flag('use_payload')));
			if (L.payload) rows.push(row('Enhanced', flag('enhanced'), 'Send the payload in parts at every [split] and skip every HTTP reply until the SSH server answers.'));
			rows.push(row('TLS / SSL', flag('use_tls')));
			rows.push(row('SlowDNS', flag('use_slowdns')));
		}

		if (!isDns) {
			rows.push(row('Server host', bind('host', { 'placeholder': 'sg1.example.com', 'autocapitalize': 'off', 'spellcheck': 'false' })));
			rows.push(row('Port', bind('port', { 'inputmode': 'numeric', 'placeholder': defPort(L) })));
		} else if (L.payload && /\[(host|port|host_port|ssh)\]/.test(payloadText)) {
			rows.push(row('Server host', bind('host', { 'placeholder': 'sg1.example.com', 'autocapitalize': 'off' }), 'Used for [host] in the payload.'));
		}

		if (isDns) {
			rows.push(row('Nameserver (NS)', bind('dns_domain', { 'placeholder': 'ns.example.com', 'autocapitalize': 'off' })));
			rows.push(row('Public key', bind('dns_pubkey', { 'class': 'cbi-input-text tt-mono', 'placeholder': '64 hex characters', 'spellcheck': 'false' })));
			rows.push(row('Record type', select(d.dns_type || 'auto', [ [ 'auto', 'Automatic' ], [ 'txt', 'TXT' ], [ 'null', 'NULL' ], [ 'cname', 'CNAME' ], [ 'ns', 'NS' ], [ 'mx', 'MX' ], [ 'srv', 'SRV' ] ], function(v) { d.dns_type = v; }),
				'Automatic tries TXT, NULL and CNAME. Some resolvers only pass certain types (for example NULL).'));
			var res = d.dns_resolver || '1.1.1.1';
			var presets = [ [ '1.1.1.1', '1.1.1.1 (UDP)' ], [ '8.8.8.8', '8.8.8.8 (UDP)' ], [ 'https://cloudflare-dns.com/dns-query', 'DoH Cloudflare' ], [ 'dot:dns.google:853', 'DoT Google' ] ];
			var isPreset = presets.some(function(p) { return p[0] === res; });
			rows.push(row('DNS resolver', select(isPreset ? res : '*', presets.concat([ [ '*', 'Custom…' ] ]), function(v) {
				d.dns_resolver = v === '*' ? '' : v; self.paintEditor();
			})));
			if (!isPreset) rows.push(row('Custom resolver', bind('dns_resolver', { 'placeholder': '1.2.3.4 · https://… · dot:host:853' })));
		}

		rows.push(row('Username', bind('user', { 'autocapitalize': 'off', 'spellcheck': 'false' })));
		var pw = bind('pass', { 'type': 'password', 'autocomplete': 'new-password', 'style': 'flex:1 1 auto;min-width:0;width:auto' });
		var eye = E('button', { 'class': 'btn cbi-button cbi-button-neutral', 'type': 'button', 'style': 'flex:0 0 auto;margin:0', 'click': function() {
			var show = pw.type === 'password';
			pw.type = show ? 'text' : 'password';
			eye.textContent = show ? 'Hide' : 'Show';
		} }, 'Show');
		rows.push(row('Password', E('div', { 'class': 'tt-inline' }, [ pw, eye ])));

		if (L.tls) {
			rows.push(row(L.payload ? 'Bug host / SNI' : 'SNI', bind('bug', { 'placeholder': 'bug.example.com', 'autocapitalize': 'off', 'spellcheck': 'false' }), L.payload ? 'Used as TLS SNI and for [bug] in the payload.' : 'Server name sent in the TLS handshake.'));
			rows.push(row('Allow insecure', flag('insecure'), 'Do not verify the TLS certificate (needed for most bug hosts / SNI tricks).'));
		} else if (L.payload) {
			rows.push(row('Bug host', bind('bug', { 'placeholder': 'bug.example.com', 'autocapitalize': 'off', 'spellcheck': 'false' }), 'Used for [bug] in the payload.'));
		}

		if (L.payload) {
			var ta = E('textarea', { 'class': 'cbi-input-textarea tt-log', 'rows': '5', 'wrap': 'soft', 'style': 'white-space:pre-wrap;word-break:break-all', 'spellcheck': 'false',
				'placeholder': L.tls ? TEMPLATES.wss : TEMPLATES.ws,
				'input': function(ev) {
					var had = /\[split\]/.test(d.payload || ''), hasNow = /\[split\]/.test(ev.target.value);
					d.payload = ev.target.value;
					if (had !== hasNow) { self.paintEditor(); var t2 = document.getElementById('tt-payload'); if (t2) { t2.focus(); t2.selectionStart = t2.selectionEnd = t2.value.length; } }
				} }, payloadText);
			ta.id = 'tt-payload';
			rows.push(row('Payload text', E('div', {}, [ ta,
				select('', [ [ '', 'Insert template…' ], [ 'ws', 'WebSocket (ws)' ], [ 'wss', 'WebSocket TLS (wss)' ], [ 'connect', 'CONNECT' ], [ 'enhanced', 'Enhanced example' ] ], function(v) {
					if (TEMPLATES[v]) { d.payload = TEMPLATES[v]; }
					self.paintEditor();
				}) ]),
				'Empty = default WebSocket template. Tags: [bug] [host] [port] [host_port] [ua] [crlf] [cr] [lf] [split] [delay_split] [instant_split]'));
			if (/\[split\]/.test(payloadText))
				rows.push(row('[split] delay (ms)', bind('split_delay', { 'inputmode': 'numeric', 'placeholder': '1000' })));
		}

		if (!isDns) rows.push(row('Remote proxy', bind('proxy', { 'placeholder': 'ip:port (optional)' }), 'Connect to this proxy instead of the server host.'));
		if (L.payload) {
			var knownUA = UAS.some(function(u) { return u[0] === d.ua; });
			var cur = !d.ua ? UAS[0][0] : (knownUA ? d.ua : '*');
			rows.push(row('User-Agent [ua]', select(cur, UAS.concat([ [ '*', 'Custom…' ] ]), function(v) {
				d.ua = v === '*' ? (knownUA || !d.ua ? 'Custom' : d.ua) : v;
				self.paintEditor();
			})));
			if (d.ua && !knownUA) rows.push(row('Custom User-Agent', bind('ua')));
		}
		rows.push(row('UDPGW port', bind('udpgw', { 'inputmode': 'numeric', 'placeholder': '7300' }), 'UDP for games and calls; 0 = off.'));

		}

		rows.push(this.expiryRow(d));
		this.editorSection(rows, d, id, 'custom');
	},

	saveDraft: function(apply) {
		var d = this.draft, id = this.sel, self = this;
		if (!d) return Promise.resolve();
		uci.unset(CONF, id, 'mode');
		PROFILE_KEYS.forEach(function(k) {
			var v = d[k];
			if (v == null || v === '') uci.unset(CONF, id, k);
			else uci.set(CONF, id, k, (k === 'pass' || k === 'payload') ? v : String(v).trim());
		});
		this.draft = null;
		if (this.newId === id) this.newId = null;
		var p;
		if (apply === 'run') {
			this.set('profile', id);
			p = this.commit(false).then(function() { return self.runAfterSave(false); });
		} else if (apply) {
			p = this.commit(false);
		} else {
			p = uci.save().then(function() {
				ui.addNotification(null, E('p', 'Saved (not applied yet). Use Save & Apply to connect with it.'), 'info');
			});
		}
		return p.then(function() { self.paintList(); self.paintEditor(); });
	},

	runAfterSave: function(onlyIfRunning) {
		var self = this;
		if (!this.profiles().length) return Promise.resolve();
		return ctlJSON([ 'status' ]).then(function(st) {
			var live = st && st.state && st.state !== 'stopped';
			if (!live && onlyIfRunning) { ui.addNotification(null, E('p', 'Saved.'), 'info'); return; }
			ui.addNotification(null, E('p', live ? 'Saved — restarting the tunnel…' : 'Saved — starting the tunnel…'), 'info');
			return ctl([ live ? 'restart' : 'start' ]).then(function() { setTimeout(function() { self.refresh(); }, 800); });
		});
	},

	move: function(id, dir) {
		var ps = this.profiles(), i = ps.findIndex(function(p) { return p['.name'] === id; });
		return this.moveTo(id, i + dir);
	},

	moveTo: function(id, to) {
		var self = this, ps = this.profiles().filter(function(p) { return p['.name'] !== self.newId; });
		var i = ps.findIndex(function(p) { return p['.name'] === id; });
		if (i < 0 || to < 0 || to >= ps.length || to === i) return Promise.resolve();
		var it = ps.splice(i, 1)[0];
		ps.splice(to, 0, it);
		ps.forEach(function(p, n) { uci.set(CONF, p['.name'], 'order', String(n + 1)); });
		this.paintList();
		return this.commit(false).then(function() { self.paintList(); });
	},

	deleteProfile: function(id) {
		var self = this, s = uci.get(CONF, id);
		if (!confirm('Delete profile "' + ((s && s.name) || id) + '"?')) return;
		uci.remove(CONF, id);
		if (this.get('profile', '') === id) this.set('profile', '');
		this.draft = null;
		this.sel = null;
		return this.commit('Profile deleted.').then(function() { self.sel = self.activeId(); self.paintList(); self.paintEditor(); });
	},

	testProfile: function(btn) {
		var id = this.sel, out = this.refs.testOut;
		btn.disabled = true;
		out.innerHTML = '';
		out.appendChild(E('p', {}, E('em', { 'class': 'spinning' }, 'Saving and testing… (up to 60 s)')));
		return this.saveDraft(true).then(function() { return ctlJSON([ 'test', id ]); }).then(function(r) {
			out = this.refs.testOut;
			out.innerHTML = '';
			var ok = r && r.ok;
			var head = ok
				? (r.banner || '') + ' · ' + (r.kex || '') + (r.legacy ? ' (legacy)' : '') + ' · ' + r.ms + ' ms' + (r.check_ms ? ' · check URL ' + r.check_ms + ' ms' : '')
				: ((r && r.error) || 'no answer');
			out.appendChild(E('div', { 'class': 'alert-message ' + (ok && !r.error ? 'success' : (ok ? 'warning' : 'error')) }, [
				E('strong', {}, ok ? 'Connected: ' : 'Failed: '), head, ok && r.error ? ' · ' + r.error : ''
			]));
			if (r && r.log && r.log.length) {
				var box = E('div', { 'class': 'tt-logbox' });
				paintLog(box, r.log.join('\n'));
				out.appendChild(box);
			}
		}.bind(this)).finally(function() { btn.disabled = false; });
	},

	renderUsage: function() {
		var self = this, r = this.refs;
		r.uTiles = E('div', { 'class': 'tt-stats' });
		r.uRange = E('div', { 'class': 'tt-seg', 'role': 'group', 'aria-label': 'Range' });
		r.uBars = E('div', { 'class': 'tt-bars' });
		r.uAxis = E('div', { 'class': 'tt-baxis' });
		r.uProf = E('div', { 'class': 'tt-ubox' });
		r.uDev = E('div', { 'class': 'tt-ubox' });
		this.uRange = this.uRange || 'd30';
		var g = function(o, d) { return self.get(o, d); };
		this.loadUsage();
		return [
			section('Usage', [
				r.uTiles,
				E('div', { 'class': 'tt-chart' }, [
					E('div', { 'class': 'tt-chart-h' }, [ E('span', {}, [ E('strong', {}, 'Daily traffic'), ' · click a bar for details' ]), r.uRange ]),
					r.uBars, r.uAxis
				]),
				E('div', { 'class': 'tt-ugrid' }, [ r.uProf, r.uDev ])
			], 'Traffic that went through the tunnel. Totals are saved every 10 minutes and on Stop.'),
			section('Usage settings', [
				row('Period starts on day', textInput(g('usage_reset_day', '1'), function(v) { self.set('usage_reset_day', v.trim()); }, { 'inputmode': 'numeric' }), 'Day of the month your package renews (1–28).'),
				row('Warn at (GB per period)', textInput(g('usage_quota_gb', '0'), function(v) { self.set('usage_quota_gb', v.trim()); }, { 'inputmode': 'numeric' }), '0 = off. Shows a notice on the Dashboard at 80% and 100%.'),
				E('div', { 'class': 'tt-actions' }, [
					button('Save', 'save', function() { self.commit('Usage settings saved.').then(function() { self.paintUsage(); }); }),
					button('Reset usage', 'remove', function() {
						if (!confirm('Delete all usage statistics?')) return;
						ctl([ 'usage-reset' ]).then(function() { return self.loadUsage(); });
					})
				])
			], 'Kept for 90 days (months for 2 years) in /etc/tomato-tunnel/usage.json. Per-device numbers need TUN mode.')
		];
	},

	loadUsage: function() {
		var self = this;
		return ctlJSON([ 'usage' ]).then(function(u) { self.usage = u || {}; self.paintUsage(); });
	},

	paintUsage: function() {
		var self = this, r = this.refs, u = this.usage || {}, days = u.days || {}, months = u.months || {};
		if (!r.uBars) return;
		var key = function(d) { return d.getFullYear() + '-' + ('0' + (d.getMonth() + 1)).slice(-2) + '-' + ('0' + d.getDate()).slice(-2); };
		var now = new Date(), today = days[key(now)] || { rx: 0, tx: 0 };
		var rd = Math.min(28, Math.max(1, +this.get('usage_reset_day', '1') || 1));
		var ps = new Date(now.getFullYear(), now.getMonth(), rd);
		if (now < ps) ps = new Date(now.getFullYear(), now.getMonth() - 1, rd);
		var pe = new Date(ps.getFullYear(), ps.getMonth() + 1, rd);
		var per = 0, nd = 0;
		Object.keys(days).forEach(function(k) { if (k >= key(ps)) per += days[k].rx + days[k].tx; });
		nd = Math.max(1, Math.ceil((now - ps) / 86400000));
		var avg = per / nd, proj = avg * Math.round((pe - ps) / 86400000);
		var q = +this.get('usage_quota_gb', '0');
		var tile = function(l, v, sub, cls, bar) {
			return E('div', { 'class': 'tt-stat' }, [ E('div', { 'class': 'tt-sl' }, l), E('div', { 'class': 'tt-sv ' + (cls || '') }, v), bar || '', E('div', { 'class': 'tt-ss' }, sub) ]);
		};
		r.uTiles.innerHTML = '';
		r.uTiles.appendChild(tile('Today', fmtBytes(today.rx + today.tx), [ E('span', { 'class': 'tt-dl' }, '↓ ' + fmtBytes(today.rx)), ' · ', E('span', { 'class': 'tt-ul' }, '↑ ' + fmtBytes(today.tx)) ]));
		r.uTiles.appendChild(tile('This period (since ' + dm(ps) + ')', fmtBytes(per), 'resets on day ' + rd));
		if (q > 0) {
			var pct = Math.round(per / (q * 1073741824) * 100);
			r.uTiles.appendChild(tile('Quota warning', pct + '%', 'of ' + q + ' GB', pct >= 100 ? 'tt-bad' : (pct >= 80 ? 'tt-busy' : ''),
				E('div', { 'class': 'tt-qbar' }, E('i', { 'style': 'width:' + Math.min(100, pct) + '%' }))));
		} else r.uTiles.appendChild(tile('Quota warning', 'off', 'set it below'));
		r.uTiles.appendChild(tile('Daily average', fmtBytes(avg), 'at this rate: ' + fmtBytes(proj) + ' by ' + dm(new Date(pe - 86400000))));

		r.uRange.innerHTML = '';
		[ [ 'd7', '7 days' ], [ 'd30', '30 days' ], [ 'm', 'Months' ] ].forEach(function(o) {
			r.uRange.appendChild(E('button', { 'type': 'button', 'class': 'btn cbi-button ' + (self.uRange === o[0] ? 'cbi-button-action tt-on' : 'cbi-button-neutral'),
				'click': function() { self.uRange = o[0]; self.uSel = null; self.paintUsage(); } }, o[1]));
		});
		var list = [];
		if (this.uRange === 'm') {
			Object.keys(months).sort().slice(-12).forEach(function(k) {
				var m = k.split('-');
				list.push({ k: k, label: MON[+m[1] - 1] + ' ' + m[0].slice(2), v: months[k].rx + months[k].tx, month: k });
			});
			if (!list.length) list.push({ k: key(now).slice(0, 7), label: MON[now.getMonth()], v: 0, month: key(now).slice(0, 7) });
		} else {
			var n = this.uRange === 'd7' ? 7 : 30;
			for (var i = n - 1; i >= 0; i--) {
				var d = new Date(now.getFullYear(), now.getMonth(), now.getDate() - i), k2 = key(d), x = days[k2];
				list.push({ k: k2, label: dm(d), v: x ? x.rx + x.tx : 0 });
			}
		}
		if (!this.uSel || !list.some(function(x) { return x.k === self.uSel; })) this.uSel = list[list.length - 1].k;
		var max = 1;
		list.forEach(function(x) { max = Math.max(max, x.v); });
		r.uBars.innerHTML = '';
		list.forEach(function(x) {
			r.uBars.appendChild(E('button', { 'type': 'button', 'class': 'tt-bar' + (x.k === self.uSel ? ' sel' : ''), 'title': x.label + ' · ' + fmtBytes(x.v), 'aria-label': x.label + ' ' + fmtBytes(x.v),
				'click': function() { self.uSel = x.k; self.paintUsage(); } }, E('i', { 'style': 'height:' + Math.max(x.v ? 3 : 1, Math.round(x.v / max * 100)) + '%' })));
		});
		r.uAxis.innerHTML = '';
		r.uAxis.appendChild(E('span', {}, list[0].label));
		r.uAxis.appendChild(E('span', {}, 'max ' + fmtBytes(max)));
		r.uAxis.appendChild(E('span', {}, list[list.length - 1].label));

		var sel = list.filter(function(x) { return x.k === self.uSel; })[0];
		var agg = { profiles: {}, devices: {} };
		Object.keys(days).forEach(function(k) {
			if (sel.month ? k.indexOf(sel.month) !== 0 : k !== sel.k) return;
			[ 'profiles', 'devices' ].forEach(function(t) {
				var src = days[k][t] || {};
				Object.keys(src).forEach(function(id) {
					var a = agg[t][id] = agg[t][id] || { name: src[id].name, mac: src[id].mac, rx: 0, tx: 0 };
					a.rx += src[id].rx; a.tx += src[id].tx;
					if (src[id].name) a.name = src[id].name;
				});
			});
		});
		var title = sel.label + ' · ' + fmtBytes(sel.v);
		var listBox = function(box, head, obj, isDev) {
			box.innerHTML = '';
			box.appendChild(E('h4', {}, title + ' · ' + head));
			var ids = Object.keys(obj).sort(function(a, b) { return (obj[b].rx + obj[b].tx) - (obj[a].rx + obj[a].tx); });
			if (!ids.length) { box.appendChild(E('em', { 'class': 'tt-muted' }, isDev ? (self.get('net_mode', 'tun') === 'tun' ? 'No device traffic recorded.' : 'Needs TUN mode.') : 'No traffic.')); return; }
			var tot = 0;
			ids.forEach(function(id) { tot += obj[id].rx + obj[id].tx; });
			ids.slice(0, 12).forEach(function(id) {
				var o = obj[id], v = o.rx + o.tx;
				var nm = isDev ? (o.name || id) : (o.name || (uci.get(CONF, id) || {}).name || id);
				box.appendChild(E('div', { 'class': 'tt-urow' }, [
					E('div', { 'class': 'tt-urt' }, [ E('span', {}, [ E('strong', {}, nm), isDev && o.name ? E('span', { 'class': 'tt-mono tt-muted' }, ' ' + id) : '' ]), E('span', { 'class': 'tt-mono' }, fmtBytes(v)) ]),
					E('div', { 'class': 'tt-qbar' }, E('i', { 'style': 'width:' + Math.round(v / tot * 100) + '%' })),
					E('div', { 'class': 'tt-ss' }, '↓ ' + fmtBytes(o.rx) + ' · ↑ ' + fmtBytes(o.tx))
				]));
			});
		};
		listBox(r.uProf, 'by profile', agg.profiles, false);
		listBox(r.uDev, 'by device', agg.devices, true);
	},

	renderSettings: function() {
		var self = this;
		var SUBS = [
			[ 'general', 'General', 'Start, reconnect, reminders' ],
			[ 'changeip', 'Change IP', 'Modem or phone airplane mode' ],
			[ 'network', 'Network', 'TUN, SOCKS, DNS, bypass' ],
			[ 'look', 'Appearance', 'Banner' ],
			[ 'about', 'About & update', 'Version, update, credits' ]
		];
		if (!SUBS.some(function(x) { return x[0] === self.sub; })) this.sub = 'general';
		this.snap = JSON.stringify(uci.get(CONF, 'main') || {});
		var nav = E('div', { 'class': 'tt-subnav', 'role': 'tablist', 'aria-label': 'Settings sections' });
		var body = E('div', { 'class': 'tt-setbody' });
		var paint = function() {
			nav.innerHTML = '';
			SUBS.forEach(function(x) {
				nav.appendChild(E('button', { 'type': 'button', 'role': 'tab', 'aria-selected': x[0] === self.sub ? 'true' : 'false', 'class': 'tt-subtab' + (x[0] === self.sub ? ' on' : ''),
					'click': function() { self.sub = x[0]; paint(); } }, [ E('strong', {}, x[1]), E('span', { 'class': 'tt-ss' }, x[2]) ]));
			});
			body.innerHTML = '';
			self.settingsBody(self.sub).forEach(function(k) { if (k) body.appendChild(k); });
		};
		paint();
		return [ E('div', { 'class': 'tt-set' }, [ nav, body ]) ];
	},

	applySettings: function(btn) {
		var self = this, before = {}, after = uci.get(CONF, 'main') || {};
		try { before = JSON.parse(this.snap || '{}'); } catch (e) {}
		var changed = RESTART_KEYS.filter(function(k) { return JSON.stringify(before[k]) !== JSON.stringify(after[k]); });
		if (btn) btn.disabled = true;
		return this.commit(false).then(function() {
			self.snap = JSON.stringify(uci.get(CONF, 'main') || {});
			return ctlJSON([ 'status' ]);
		}).then(function(st) {
			var live = st && st.state && st.state !== 'stopped';
			if (live && changed.length) {
				ui.addNotification(null, E('p', 'Saved. Restarting the tunnel so the new network settings take effect…'), 'info');
				return ctl([ 'restart' ]).then(function() { setTimeout(function() { self.refresh(); }, 800); });
			}
			if (live) ctl([ 'reload' ]);
			ui.addNotification(null, E('p', 'Saved and applied.'), 'info');
		}).finally(function() { if (btn) btn.disabled = false; });
	},

	settingsBody: function(sub) {
		var self = this;
		var g = function(o, d) { return self.get(o, d); };
		var txt = function(o, d, attrs) { return textInput(g(o, d), function(v) { self.set(o, v); }, attrs); };
		var sel = function(o, d, opts) {
			var cur = g(o, d);
			if (!opts.some(function(x) { return x[0] === cur; })) opts = opts.concat([ [ cur, cur ] ]);
			return select(cur, opts, function(v) { self.set(o, v); });
		};
		var flag = function(o, d) { return checkbox(g(o, d) === '1', function(v) { self.set(o, v ? '1' : '0'); }); };
		var foot = E('div', { 'class': 'cbi-page-actions tt-pgact' }, [
			button('Save & Apply', 'apply', function(ev) { self.applySettings(ev.currentTarget); }), ' ',
			button('Reset', 'reset', function() { uci.unload(CONF); uci.load(CONF).then(function() { self.show('settings'); }); })
		]);
		if (sub === 'about') return this.renderAbout();
		if (sub === 'changeip') return [ this.renderChangeIP(), foot ];
		if (sub === 'look') {
			var cur = g('banner', 'tomato');
			if (cur === 'default') cur = 'tomato';
			var grid = E('div', { 'class': 'tt-bgrid', 'role': 'radiogroup', 'aria-label': 'Banner' });
			var prev = E('div');
			var paintB = function() {
				grid.innerHTML = '';
				BANNERS.map(function(b) { return [ b[0], b[1], raw(b[2]) ]; }).concat([
					[ 'custom', 'Your own image', E('div', { 'class': 'tt-bph' }, 'Upload…') ],
					[ 'none', 'No banner', E('div', { 'class': 'tt-bph' }, '—') ]
				]).forEach(function(b) {
					grid.appendChild(E('button', { 'type': 'button', 'role': 'radio', 'aria-checked': b[0] === cur ? 'true' : 'false', 'class': 'tt-bopt' + (b[0] === cur ? ' on' : ''),
						'click': function() {
							if (b[0] === 'custom') { return self.uploadBanner().then(function() { cur = self.get('banner', cur); paintB(); }); }
							cur = b[0]; self.set('banner', cur); paintB();
						} }, [ E('div', { 'class': 'tt-bthumb' }, b[2]), E('span', {}, [ b[0] === cur ? '● ' : '', b[1], b[0] === 'tomato' ? ' (default)' : '' ]) ]));
				});
				prev.innerHTML = '';
				var bb = self.renderBanner();
				if (bb) prev.appendChild(bb);
			};
			paintB();
			return [ section('Appearance', [
				E('div', { 'class': 'cbi-value-description', 'style': 'margin:0 0 .8em' }, 'Banner at the top of the Dashboard. Your own image: JPG, PNG or WebP, about 1200×200, max 500 KB.'),
				grid,
				E('h4', {}, 'Preview'),
				prev
			], 'Only changes the look of this page.'), foot ];
		}
		if (sub === 'network') {
			var ifOpts = [ [ '', 'Auto (system route)' ] ].concat(this.ifaces.map(function(i) { return [ i, i ]; }));
			var bypass = E('textarea', { 'class': 'cbi-input-textarea tt-log', 'rows': '4', 'spellcheck': 'false', 'input': function(ev) {
				var lines = ev.target.value.split('\n').map(function(l) { return l.split('#')[0].trim(); }).filter(function(l) { return l; });
				uci.set(CONF, 'main', 'bypass', lines.length ? lines : null);
			} }, (uci.get(CONF, 'main', 'bypass') || []).join('\n'));
			return [ section('Network', [
				row('Traffic mode', sel('net_mode', 'tun', [ [ 'tun', 'TUN (recommended) · TCP + UDP' ], [ 'redirect', 'TCP redirect (nftables / iptables)' ], [ 'socks', 'SOCKS5 only (for OpenClash / PassWall)' ] ]),
					'TUN: the whole LAN, TCP + UDP. SOCKS5 only: no routing changes.'),
				row('SOCKS5 port', txt('socks_port', '1080', { 'inputmode': 'numeric' })),
				row('SOCKS5 listen', sel('socks_listen', '127.0.0.1', [ [ '127.0.0.1', '127.0.0.1 (router only)' ], [ '0.0.0.0', 'All interfaces (LAN)' ] ])),
				row('Outgoing interface', sel('wan_iface', '', ifOpts), 'e.g. the modem (wwan0). Auto = normal routing.'),
				row('DNS through the tunnel', sel('dns_mode', 'tunnel', [ [ 'tunnel', 'Yes · DNS over TCP through the tunnel' ], [ 'off', 'No (use ISP DNS)' ] ])),
				row('DNS server', txt('dns_server', '1.1.1.1'), 'Asked from the server side.'),
				row('Manage dnsmasq', flag('manage_dnsmasq', '1'), 'Restored on Stop.'),
				row('Bypass', bypass, 'IP, CIDR or device that skips the tunnel, one per line. Server and LAN are bypassed automatically.'),
				row('SSH compatibility', sel('ssh_compat', 'auto', [ [ 'auto', 'Auto (recommended)' ], [ 'legacy', 'Legacy (old Dropbear first)' ], [ 'modern', 'Modern only' ] ]),
					'Old Dropbear servers are handled automatically.')
			], 'How the LAN uses the tunnel. Leave it as it is if unsure.'), foot ];
		}
		return [ section('General', [
			row('Start on boot', flag('autostart', '0'), 'Connect automatically when the router starts.'),
			row('Profile selection', sel('policy', self.policy(), [ [ 'manual', 'Manual · stay on the chosen profile' ], [ 'fallback', 'Fallback · next profile when one fails' ], [ 'auto', 'Auto · move to the fastest profile' ] ]), 'Also on the Dashboard.'),
			row('Warn before expiry', sel('expiry_warn', '3', [ [ '1', '1 day' ], [ '3', '3 days' ], [ '7', '7 days' ] ]), 'Expired profiles are skipped by Auto and Fallback.'),
			row('Connection check URL', txt('check_url', 'http://cp.cloudflare.com/generate_204', { 'spellcheck': 'false' })),
			row('Check every (seconds)', txt('check_interval', '5', { 'inputmode': 'numeric' })),
			row('Failures before reconnect', txt('check_fail', '3', { 'inputmode': 'numeric' })),
			row('Parallel SSH sessions', sel('sessions', '1', [ [ '1', '1 (normal)' ], [ '2', '2' ], [ '3', '3' ], [ '4', '4' ] ]))
		], 'The basics. Most people never change anything else.'), foot ];
	},

	renderChangeIP: function() {
		var self = this, box = E('div');
		var paint = function() {
			box.innerHTML = '';
			var g = function(o, d) { return self.get(o, d); };
			var method = g('ip_method', g('restart_modem', '0') === '1' ? 'iface' : 'none');
			var txt = function(o, d, attrs) { return textInput(g(o, d), function(v) { self.set(o, v.trim()); }, attrs); };
			var flag = function(o, d) { return checkbox(g(o, d) === '1', function(v) { self.set(o, v ? '1' : '0'); }); };
			var M = [ [ 'none', 'Off' ], [ 'iface', 'Restart interface' ], [ 'at', 'Airplane mode (AT command)' ], [ 'mm', 'ModemManager' ], [ 'hilink', 'HiLink / Orbit modem' ], [ 'adb', 'Android phone (ADB)' ] ];
			var HINT = {
				none: 'No automatic IP change.',
				iface: 'ifdown/ifup the WAN interface. Works with most modems in RNDIS/QMI mode.',
				at: 'Sends AT commands to the modem serial port: radio off, wait, radio on.',
				mm: 'Uses mmcli to disable and enable the modem. Needs modemmanager.',
				hilink: 'Turns mobile data off and on through the web API of the modem.',
				adb: 'Airplane mode on the tethered phone (USB debugging on). Needs the adb package.'
			};
			var rows = [ row('Method', select(method, M, function(v) {
				self.set('ip_method', v);
				if (self.get('restart_modem', '0') === '1') { self.set('restart_modem', '0'); if (v !== 'none') self.set('ip_after_fails', '1'); }
				paint();
			}), HINT[method]) ];
			var ifOpts = [ [ '', '—' ] ];
			(uci.sections('network', 'interface') || []).forEach(function(i) { if (i['.name'] !== 'loopback' && i['.name'] !== 'lan') ifOpts.push([ i['.name'], i['.name'] + (i.device ? ' (' + i.device + ')' : '') ]); });
			var curIf = g('ip_iface', g('modem_iface', 'wwan'));
			if (!ifOpts.some(function(o) { return o[0] === curIf; })) ifOpts.push([ curIf, curIf ]);
			var ifSel = select(curIf, ifOpts, function(v) { self.set('ip_iface', v); });
			var det = self.ipDetect || {};
			if (method === 'iface') rows.push(row('Interface', ifSel, 'Name in Network › Interfaces.'));
			if (method === 'at') {
				var ports = (det.at || []).map(function(p) { return [ p.dev, p.dev + (p.ok ? ' · OK' + (p.model ? ' (' + p.model + ')' : '') : ' · no answer') ]; });
				var curP = g('at_port', '/dev/ttyUSB2');
				if (!ports.some(function(o) { return o[0] === curP; })) ports.unshift([ curP, curP ]);
				rows.push(row('AT port', E('div', { 'class': 'tt-inline' }, [ select(curP, ports, function(v) { self.set('at_port', v); }),
					button('Detect', 'neutral', function(ev) { self.detectIP(ev.currentTarget, paint); }) ]), det.at && !det.at.length ? 'No serial ports found (/dev/ttyUSB*, /dev/ttyACM*).' : null));
				rows.push(row('Radio off command', txt('at_off', 'AT+CFUN=4', { 'class': 'cbi-input-text tt-mono' })));
				rows.push(row('Radio on command', txt('at_on', 'AT+CFUN=1', { 'class': 'cbi-input-text tt-mono' })));
				rows.push(row('Bring up interface after', ifSel, 'Optional: ifup this interface once the radio is back (QMI/MBIM modems).'));
			}
			if (method === 'mm') {
				var mms = (det.mm || []).map(function(m) { return [ m.index, m.index + ' · ' + m.name ]; });
				var curM = g('mm_modem', '0');
				if (!mms.some(function(o) { return o[0] === curM; })) mms.unshift([ curM, curM ]);
				rows.push(row('Modem', E('div', { 'class': 'tt-inline' }, [ select(curM, mms, function(v) { self.set('mm_modem', v); }),
					button('Detect', 'neutral', function(ev) { self.detectIP(ev.currentTarget, paint); }) ]), det.mm_error || null));
				rows.push(row('Bring up interface after', ifSel));
			}
			if (method === 'hilink') {
				rows.push(row('Modem address', txt('hilink_addr', '192.168.8.1')));
				rows.push(row('Modem username', txt('hilink_user', 'admin')));
				var pw = textInput(g('hilink_pass', ''), function(v) { self.set('hilink_pass', v); }, { 'type': 'password', 'autocomplete': 'new-password' });
				rows.push(row('Modem password', pw, 'Huawei HiLink and Telkomsel Orbit. Leave empty if the modem page has no login.'));
			}
			if (method === 'adb') {
				var ads = (det.adb || []).map(function(a) { return [ a.serial, a.serial + ' · ' + (a.model || '') + ' (' + a.state + ')' ]; });
				var curA = g('adb_serial', '');
				ads.unshift([ '', 'First phone found' ]);
				if (!ads.some(function(o) { return o[0] === curA; })) ads.push([ curA, curA ]);
				rows.push(row('Phone', E('div', { 'class': 'tt-inline' }, [ select(curA, ads, function(v) { self.set('adb_serial', v); }),
					button('Detect', 'neutral', function(ev) { self.detectIP(ev.currentTarget, paint); }) ]), det.adb_error || null));
			}
			if (method !== 'none') {
				rows.push(row('Wait in between (seconds)', txt('ip_wait', '5', { 'inputmode': 'numeric' }), 'Off time, then up to 60 s for the new IP.'));
				rows.push(row('After failed connects', flag('ip_after_fails', g('restart_modem', '0') === '1' ? '1' : '0'), 'Change the IP when the tunnel cannot connect several times in a row.'));
				rows.push(row('Failed connects', txt('ip_after_n', '3', { 'inputmode': 'numeric' })));
				rows.push(row('Every (hours)', txt('ip_every_h', '0', { 'inputmode': 'numeric' }), '0 = off. Changes the IP on a schedule while connected.'));
				rows.push(row('Button on the Dashboard', flag('ip_button', '1')));
				rows.push(row('Keep trying until IP starts with', txt('ip_prefix', '', { 'class': 'cbi-input-text tt-mono', 'placeholder': 'e.g. 114.12., 114.13. (optional)' }), 'Empty = any new IP is fine. Checks the WAN IP, not the tunnel IP.'));
				rows.push(row('Max tries', txt('ip_max_tries', '5', { 'inputmode': 'numeric' })));
				rows.push(E('div', { 'class': 'tt-actions' }, [ button('Save & Change IP now', 'action', function(ev) {
					var b = ev.currentTarget;
					self.commit(false).then(function() { return self.changeIP(b); }).then(function() { b.disabled = false; });
				}) ]));
			}
			box.appendChild(section('Change IP', rows, 'Gets a new IP from the operator. The tunnel waits, then reconnects on the new IP. Progress is in the Log.'));
		};
		uci.load('network').then(paint, paint);
		return box;
	},

	detectIP: function(btn, after) {
		var self = this;
		btn.disabled = true;
		return ctlJSON([ 'changeip-detect' ]).then(function(r) { self.ipDetect = r || {}; after(); });
	},

	uploadBanner: function() {
		var self = this;
		return ui.uploadFile('/tmp/tt-banner.upload').then(function() {
			return ctl([ 'banner-set' ]);
		}).then(function(out) {
			if ((out || '').trim() !== 'ok') throw new Error((out || 'upload failed').trim());
			self.set('banner', 'custom');
			return self.commit('Banner updated.');
		}).catch(function(e) {
			if (e && e.message) ui.addNotification(null, E('p', 'Banner: ' + e.message), 'warning');
		});
	},

	renderLog: function() {
		var self = this;
		this.refs.log = E('div', { 'class': 'tt-logbox tall' });
		return [ section('Log', [
			row('Level', select(this.get('log_level', 'info'), [ [ 'info', 'Info' ], [ 'debug', 'Debug (payload parts, raw replies)' ], [ 'error', 'Errors only' ] ], function(v) {
				self.set('log_level', v);
				self.commit('Log level saved (applies on the next restart).');
			})),
			row('Auto-scroll', checkbox(this.autoScroll !== false, function(v) { self.autoScroll = v; })),
			this.refs.log,
			E('div', { 'class': 'tt-actions' }, [
				button('Download log', 'action', function() {
					ctl([ 'log', '1000' ]).then(function(t) {
						var a = E('a', { 'href': URL.createObjectURL(new Blob([ t ], { type: 'text/plain' })), 'download': 'tomato-tunnel.log' });
						document.body.appendChild(a); a.click(); a.remove();
					});
				}),
				button('Clear', 'remove', function() { ctl([ 'clear-log' ]).then(function() { self.refs.lastLog = null; self.refreshLog(); }); })
			])
		]) ];
	},

	renderAbout: function() {
		var self = this;
		this.refs.updLog = E('textarea', { 'class': 'cbi-input-textarea tt-log', 'readonly': '', 'rows': '6', 'wrap': 'off', 'style': 'display:none' });
		this.refs.updInfo = E('div');
		this.paintUpdate();
		var g = function(o, d) { return self.get(o, d); };
		return [
			section('Update', [
				this.refs.updInfo,
				this.refs.updLog,
				row('GitHub repository', textInput(g('update_repo', 'dudul19/tomato-tunnel'), function(v) { self.set('update_repo', v.trim()); }, { 'spellcheck': 'false' })),
				row('Channel', select(g('update_channel', 'stable'), [ [ 'stable', 'Stable' ], [ 'beta', 'Beta (pre-release)' ] ], function(v) { self.set('update_channel', v); })),
				row('Daily check', checkbox(g('update_check', '1') === '1', function(v) { self.set('update_check', v ? '1' : '0'); }), 'Shows a notice on the Dashboard; never installs by itself.'),
				E('div', { 'class': 'tt-actions' }, button('Save', 'save', function() { self.commit(); }))
			], 'Installs the new release from GitHub (SHA256 checked). Settings are kept.'),
			section('Credits', [
				E('div', { 'class': 'tt-author' }, [
					raw('<svg class="tt-logo" viewBox="0 0 48 48" aria-hidden="true"><circle cx="24" cy="27" r="16" fill="#d9363e"/><path d="M24 11c-2-4-6-5-9-4 3 1 5 3 6 6-4-1-7 0-9 2 4 0 7 1 9 3 1-2 2-3 3-3s2 1 3 3c2-2 5-3 9-3-2-2-5-3-9-2 1-3 3-5 6-6-3-1-7 0-9 4z" fill="#2e9d48"/><ellipse cx="18" cy="24" rx="3" ry="5" fill="#fff" opacity=".25"/></svg>'),
					E('div', { 'class': 'tt-authi' }, [
						E('strong', {}, 'Tomato Tunnel'),
						E('span', { 'class': 'tt-ss' }, [ 'Made by ', E('a', { 'href': TG, 'target': '_blank', 'rel': 'noopener' }, '@dudulrealnofek'), ' · v' + this.version + ' · GPL-3.0' ]),
						E('span', { 'class': 'tt-ss' }, 'Bug reports and ideas are welcome on Telegram.')
					]),
					E('div', { 'class': 'tt-authb' }, [
						E('a', { 'class': 'btn cbi-button cbi-button-action', 'href': TG, 'target': '_blank', 'rel': 'noopener' }, 'Telegram'),
						E('a', { 'class': 'btn cbi-button cbi-button-neutral', 'href': 'https://t.me/tfnuklir', 'target': '_blank', 'rel': 'noopener' }, 'Group @tfnuklir'),
						E('a', { 'class': 'btn cbi-button cbi-button-neutral', 'href': 'https://github.com/' + (g('update_repo', 'dudul19/tomato-tunnel')), 'target': '_blank', 'rel': 'noopener' }, 'Source on GitHub')
					])
				]),
				E('h4', { 'class': 'tt-thxh' }, 'Thanks to'),
				E('div', { 'class': 'tt-thxg' }, [
					[ 'wegare', 'https://github.com/wegare123/stl', 'stl', 'SSH injector for OpenWrt, the starting idea' ],
					[ 'heiher', 'https://github.com/heiher/hev-socks5-tunnel', 'hev-socks5-tunnel', 'TUN to SOCKS5 (MIT)' ],
					[ 'David Fifield', 'https://www.bamsoftware.com/software/dnstt/', 'dnstt', 'the DNS tunnel behind SlowDNS' ],
					[ 'apernet', 'https://github.com/apernet/hysteria', 'Hysteria', 'QUIC core behind ZiVPN (MIT)' ],
					[ 'Go team', 'https://pkg.go.dev/golang.org/x/crypto/ssh', 'x/crypto/ssh', 'the SSH client' ],
					[ 'OpenWrt & ImmortalWrt', 'https://openwrt.org/', 'OpenWrt', 'the firmware and LuCI' ],
					[ 'HTTP Custom', null, 'HTTP Custom', 'inspiration for the payload modes' ]
				].map(function(t) {
					return E(t[1] ? 'a' : 'div', { 'class': 'tt-thxc', 'href': t[1], 'target': t[1] ? '_blank' : null, 'rel': t[1] ? 'noopener' : null }, [
						E('span', { 'class': 'tt-thxt' }, [ E('strong', {}, t[0]), E('span', { 'class': 'tt-thxp tt-mono' }, t[2]) ]),
						E('span', { 'class': 'tt-ss' }, t[3])
					]);
				}))
			])
		];
	},

	paintUpdate: function() {
		var self = this, u = this.upd || {}, box = this.refs.updInfo;
		if (!box) return;
		box.innerHTML = '';
		box.appendChild(table(null, [
			[ E('strong', {}, 'Installed'), 'v' + (this.version || '?') ],
			[ E('strong', {}, 'Latest on GitHub'), u.latest ? E('span', { 'class': u.available ? 'tt-busy' : '' }, 'v' + u.latest + (u.prerelease ? ' (beta)' : '')) : '—' ],
			[ E('strong', {}, 'Checked'), u.error ? E('span', { 'class': 'tt-bad' }, u.error) : (u.checked ? new Date(u.checked * 1000).toLocaleString() : 'not yet') ]
		]));
		if (u.available && u.notes) {
			box.appendChild(E('h4', {}, "What's new in v" + u.latest));
			box.appendChild(E('textarea', { 'class': 'cbi-input-textarea tt-log', 'readonly': '', 'rows': '6', 'style': 'white-space:pre-wrap' }, u.notes));
		}
		var acts = E('div', { 'class': 'tt-actions' });
		if (u.available) acts.appendChild(button('Update to v' + u.latest, 'apply', function(ev) { self.doUpdate(ev.target); }));
		acts.appendChild(button('Check now', 'action', function(ev) {
			ev.target.disabled = true;
			ctlJSON([ 'update-check' ]).then(function(res) { self.upd = res || {}; self.paintUpdate(); }).finally(function() { ev.target.disabled = false; });
		}));
		if (u.page) acts.appendChild(E('a', { 'class': 'btn cbi-button cbi-button-neutral', 'href': u.page, 'target': '_blank', 'rel': 'noopener' }, 'Release page'));
		box.appendChild(acts);
	},

	doUpdate: function(btn) {
		var self = this;
		if (!confirm('Install v' + this.upd.latest + ' now? The tunnel restarts during the update.')) return;
		btn.disabled = true;
		this.updating = true;
		this.refs.updLog.style.display = '';
		this.refs.updLog.value = 'Starting…';
		return ctl([ 'update-install' ]).then(function() { return self.refreshUpdateLog(); });
	},

	refreshUpdateLog: function() {
		var self = this;
		return ctl([ 'update-log' ]).then(function(t) {
			if (!self.refs.updLog) return;
			self.refs.updLog.value = t || 'Working…';
			if (/installed v|error:|up to date/.test(t || '')) {
				self.updating = false;
				if (/installed v/.test(t)) {
					self.refs.updLog.value += '\nReloading the page…';
					setTimeout(function() { location.reload(); }, 4000);
				}
			}
		});
	},

	handleSaveApply: null,
	handleSave: null,
	handleReset: null
});
