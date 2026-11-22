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
	if (L.zivpn) return 'ZiVPN (UDP)';
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
var TABS = [ [ 'dashboard', 'Dashboard' ], [ 'accounts', 'Accounts' ], [ 'settings', 'Settings' ], [ 'log', 'Log' ], [ 'about', 'About' ] ];

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
		if (!TABS.some(function(t) { return t[0] === this.tab; }, this)) this.tab = 'dashboard';

		var self = this;
		this.tabsEl = E('ul', { 'class': 'cbi-tabmenu' });
		this.bodyEl = E('div', { 'class': 'tt-wrap' });
		var root = E('div', { 'class': 'cbi-map tt-wrap' }, [
			E('link', { 'rel': 'stylesheet', 'href': L.resource('tomato-tunnel/tt.css') + '?v=' + encodeURIComponent(this.version) + '-6' }),
			E('h2', { 'name': 'content' }, 'Tomato Tunnel'),
			E('div', { 'class': 'cbi-map-descr' }, [ 'SSH tunnel: Direct, Payload, SSL/SNI, WebSocket, SlowDNS and Enhanced payload. Made by ',
				E('a', { 'href': TG, 'target': '_blank', 'rel': 'noopener' }, '@dudulrealnofek'), '.' ]),
			this.bannerEl = E('div'),
			this.tabsEl,
			this.bodyEl,
			E('div', { 'class': 'tt-foot' }, [ 'Tomato Tunnel v' + (this.version || '?') + ' · GPL-3.0 · made by ',
				E('a', { 'href': TG, 'target': '_blank', 'rel': 'noopener' }, '@dudulrealnofek') ])
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
		case 'log': body = this.renderLog(); break;
		case 'about': body = this.renderAbout(); break;
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
			if (self.tab === 'log' || self.tab === 'dashboard') return self.refreshLog();
			if (self.tab === 'about' && self.updating) return self.refreshUpdateLog();
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
		var mode = this.get('banner', 'default');
		if (mode === 'none') return null;
		var art = mode === 'custom' ? E('img', { 'src': '/luci-static/tomato-tunnel/banner.img?' + Date.now(), 'alt': '' }) : raw(BANNER_SVG);
		return E('div', { 'class': 'tt-banner' }, [ art, E('div', { 'class': 'tt-bt' }, [
			E('span', { 'class': 'tt-name' }, 'Tomato Tunnel'),
			E('span', { 'class': 'tt-sub' }, 'SSH · Payload · SSL/SNI · WebSocket · SlowDNS · Enhanced'),
			E('a', { 'href': TG, 'target': '_blank', 'rel': 'noopener' }, 'by @dudulrealnofek')
		]) ]);
	},

	renderStatus: function() {
		var r = this.refs, self = this, out = [];
		if (this.upd && this.upd.available)
			out.push(E('div', { 'class': 'alert-message warning' }, [
				E('strong', {}, 'Update available: '), 'v' + this.upd.latest + ' is on GitHub (you have v' + this.version + '). ',
				E('a', { 'href': '#about', 'click': function(ev) { ev.preventDefault(); self.show('about'); } }, "See what's new")
			]));
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
		r.chart = E('div', { 'class': 'tt-chartbox' });
		r.sshEl = E('div', { 'class': 'tt-note tt-mono' });
		out.push(section('Status', [
			E('div', { 'class': 'tt-head' }, [ E('div', { 'class': 'tt-hl' }, [ r.stateEl, r.subEl ]), r.acts ]),
			r.swBox,
			E('div', { 'class': 'tt-stats' }, [ stat('Download', 'dl', 'tt-dl'), stat('Upload', 'ul', 'tt-ul'), stat('Ping', 'ping', 'tt-ok'), stat('Public IP', 'ip') ]),
			E('div', { 'class': 'tt-chart' }, [
				E('div', { 'class': 'tt-chart-h' }, [ E('span', {}, 'Traffic · last 60 s'),
					E('span', {}, [ E('span', { 'class': 'tt-dl' }, '■'), ' download  ', E('span', { 'class': 'tt-ul' }, '■'), ' upload' ]) ]),
				r.chart
			]),
			r.sshEl
		]));

		r.polEl = E('div', { 'class': 'tt-seg', 'role': 'group', 'aria-label': 'Profile selection' });
		r.polHint = E('div', { 'class': 'cbi-section-descr tt-polhint' });
		r.testBtn = E('button', { 'type': 'button', 'class': 'btn cbi-button cbi-button-neutral tt-ic', 'title': 'Test latency', 'aria-label': 'Test latency',
			'click': function(ev) { self.testLatency(ev.currentTarget); } }, raw(BOLT));
		r.grid = E('div', { 'class': 'tt-grid' });
		out.push(section('Profiles', [
			E('div', { 'class': 'tt-ptools' }, [ r.polEl, r.testBtn ]),
			r.polHint,
			r.grid
		], 'Click a profile to switch. The tunnel stays up while switching.'));

		r.log = E('div', { 'class': 'tt-logbox' });
		out.push(section('Recent log', [ r.log, E('div', { 'class': 'tt-actions' }, button('Open full log', 'action', function() { self.show('log'); })) ]));
		return out;
	},

	paintChart: function() {
		var r = this.refs, h = this.hist || [];
		if (!r.chart) return;
		var W = 600, H = 120, n = 30, max = 1;
		h.forEach(function(s) { max = Math.max(max, s[0], s[1]); });
		var pts = function(k) {
			return h.map(function(s, i) {
				var x = (W * (n - h.length + i) / (n - 1)).toFixed(1), y = (H - 4 - (H - 8) * s[k] / max).toFixed(1);
				return x + ',' + y;
			}).join(' ');
		};
		var grid = '<line x1="0" y1="30" x2="600" y2="30"/><line x1="0" y1="60" x2="600" y2="60"/><line x1="0" y1="90" x2="600" y2="90"/>';
		r.chart.innerHTML = '<svg viewBox="0 0 600 120" preserveAspectRatio="none" aria-hidden="true">' +
			'<g class="tt-cg">' + grid + '</g>' +
			(h.length > 1 ? '<polyline class="tt-cd" points="' + pts(0) + '"/><polyline class="tt-cu" points="' + pts(1) + '"/>' : '') +
			'</svg><div class="tt-cmax">' + fmtRate(max) + '</div>';
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
		var name = live && st.profile_name ? st.profile_name : (this.profiles().length ? ((uci.get(CONF, this.activeId()) || {}).name || '—') : '');
		r.stateEl.innerHTML = '';
		r.stateEl.appendChild(E('span', { 'class': cls }, txt));
		if (name) r.stateEl.appendChild(E('span', { 'class': 'tt-sname' }, name + (live ? '' : ' (selected)')));
		var l1 = [], l2 = [];
		if (live) l1.push((st.mode || '—') + ' · ' + (st.net_mode || this.get('net_mode', 'tun')).toUpperCase());
		if (on && st.since) l2.push('Uptime ' + fmtDur((st.updated || Date.now() / 1000) - st.since));
		if (on) { var rc = st.reconnects_today || 0; l2.push(rc + (rc === 1 ? ' reconnect' : ' reconnects') + ' today'); }
		if (st.message && !on) l2.push(st.message);
		if (s === 'stopped' && !this.profiles().length) l2.push('Add an account in the Accounts tab first.');
		r.subEl.innerHTML = '';
		if (l1.length) r.subEl.appendChild(E('span', {}, l1.join(' · ')));
		if (l2.length) r.subEl.appendChild(E('span', {}, l2.join(' · ')));

		r.dl.textContent = on ? fmtRate(st.rate_rx) : '—';
		r.dlSub.textContent = live ? 'total ' + fmtBytes(st.rx) : '';
		r.ul.textContent = on ? fmtRate(st.rate_tx) : '—';
		r.ulSub.textContent = live ? 'total ' + fmtBytes(st.tx) : '';
		var host = (this.get('check_url', 'http://cp.cloudflare.com/generate_204').match(/\/\/([^\/:]+)/) || [])[1] || '';
		r.ping.textContent = on && st.ping_ms ? st.ping_ms + ' ms' : '—';
		r.pingSub.textContent = on ? 'every ' + this.get('check_interval', '5') + ' s' : '';
		r.ping.title = host;
		r.ip.textContent = on && st.public_ip ? st.public_ip : '—';
		r.ip.title = r.ip.textContent;
		r.ipSub.textContent = on ? [ st.country, st.isp ].filter(function(x) { return x; }).join(' · ') : '';
		r.sshEl.textContent = on && st.banner ? 'SSH server: ' + st.banner + ' · ' + (st.kex || '') + (st.legacy ? ' (legacy)' : '') + ' · ' + (st.sessions || 1) + ' session(s)' : '';
		this.paintChart();

		r.swBox.innerHTML = '';
		if (live && st.switching)
			r.swBox.appendChild(E('div', { 'class': 'alert-message warning tt-swbox' }, [
				E('strong', {}, 'Switching to ' + st.switching + '… '),
				'Internet keeps working on ' + (st.profile_name || 'the current profile') + '. If the new profile fails, nothing changes. ',
				button('Cancel switch', 'neutral', function(ev) { ev.currentTarget.disabled = true; ctl([ 'cancel-switch' ]).then(function() { self.refresh(); }); })
			]));

		var key = live ? 'on' : 'off';
		if (r.actsKey !== key) {
			r.actsKey = key;
			r.acts.innerHTML = '';
			var big = function(b) { b.classList.add('tt-big'); return b; };
			if (!live) r.acts.appendChild(big(button('Start', 'positive', function(ev) { self.act(ev.currentTarget, 'start'); })));
			else {
				r.acts.appendChild(big(button('Restart', 'reload', function(ev) { self.act(ev.currentTarget, 'restart'); })));
				r.acts.appendChild(big(button('Stop', 'negative', function(ev) { self.act(ev.currentTarget, 'stop'); })));
			}
		}
		this.paintProfiles();
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
		var key = [ pol, cur, live, st.switching || '', JSON.stringify(lat), JSON.stringify(lf), ps.map(function(p) { return p['.name'] + p.name; }).join() ].join('|');
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
				E('span', { 'class': 'tt-tt' }, [ E('strong', {}, p.name || id), E('span', { 'class': bcls }, badge) ]),
				E('span', { 'class': 'tt-td' }, layerLabel(L)),
				E('span', { 'class': 'tt-td tt-mono' }, where),
				E('span', { 'class': 'tt-tg ' + tcls }, tag)
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
		this.refs.editor = E('div');
		this.paintList();
		this.paintEditor();
		return [
			section('Profiles', [
				this.refs.list,
				E('div', { 'class': 'tt-actions' }, [
					button('Add', 'add', function() { self.addProfile({}); }),
					button('Import', 'action', function() { self.importDialog(); })
				])
			], 'Pick the active profile on the Dashboard. List order = fallback order.'),
			this.refs.editor
		];
	},

	paintList: function() {
		var self = this, active = this.activeId(), ps = this.profiles();
		var list = this.refs.list;
		list.innerHTML = '';
		if (!ps.length) { list.appendChild(E('em', {}, 'No profiles yet.')); return; }
		list.appendChild(E('div', { 'class': 'tt-plist' }, ps.map(function(p, i) {
			var id = p['.name'];
			var L = layers(p);
			var where = L.dns ? 'NS ' + (p.dns_domain || '?') : (p.host || '?') + ':' + (p.port || defPort(L));
			var on = id === active;
			return E('div', { 'class': 'tt-pi' + (id === self.sel ? ' sel' : '') }, [
				E('div', { 'class': 'tt-pm' }, [
					E('div', { 'class': 'tt-pn' }, [ E('strong', {}, p.name || id) ]),
					E('div', { 'class': 'tt-pd' }, layerLabel(L)),
					E('div', { 'class': 'tt-pd tt-mono' }, where)
				]),
				E('div', { 'class': 'tt-pb' }, [
					button('Edit', 'edit', function() { self.sel = id; self.draft = null; self.paintList(); self.paintEditor(); self.refs.editor.scrollIntoView({ behavior: 'smooth' }); }),
					i > 0 ? button('↑', 'neutral', function() { self.move(id, -1); }) : '',
					i < ps.length - 1 ? button('↓', 'neutral', function() { self.move(id, 1); }) : ''
				])
			]);
		})));
	},

	addProfile: function(v) {
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
		Object.keys(defs).forEach(function(k) { uci.set(CONF, sid, k, v[k] != null ? v[k] : defs[k]); });
		Object.keys(v).forEach(function(k) { if (defs[k] == null) uci.set(CONF, sid, k, v[k]); });
		if (!this.get('profile', '')) this.set('profile', sid);
		this.sel = sid;
		this.draft = null;
		this.paintList();
		this.paintEditor();
		return sid;
	},

	importDialog: function() {
		var self = this;
		var ta = E('textarea', { 'class': 'cbi-input-textarea tt-log', 'rows': '8', 'placeholder': 'host.example.com:80@user:pass\nssh://user:pass@host:22\n\nor a Tomato account block:\nHost : sg1.example.com\nUsername : dudul\nPassword : secret\nPort WS : 80' });
		ui.showModal('Import account', [
			ta,
			E('div', { 'class': 'right' }, [
				E('button', { 'class': 'btn cbi-button', 'click': ui.hideModal }, 'Cancel'), ' ',
				E('button', { 'class': 'btn cbi-button cbi-button-positive', 'click': function() {
					var v = self.parseAccount(ta.value);
					if (!v) { ui.addNotification(null, E('p', 'Could not find host, username and password in that text.'), 'warning'); return; }
					ui.hideModal();
					self.addProfile(v);
					ui.addNotification(null, E('p', 'Imported "' + v.name + '". Check the mode and payload, then Save & Apply.'), 'info');
				} }, 'Import')
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
		var wss = get(/^\s*port\s*(?:ws\s*)?(?:ssl|tls)[^:=]*[:=]\s*(\d+)/i);
		var ws = get(/^\s*port\s*(?:ssh\s*)?(?:ws|websocket|http)[^:=]*[:=]\s*(\d+)/i);
		var any = get(/^\s*port[^:=]*[:=]\s*(\d+)/i);
		var mode = ws ? 'ws' : (wss ? 'wss' : 'direct');
		return { name: host.split('.')[0].toUpperCase(), host: host, user: user, pass: pass, mode: mode, port: ws || wss || any || (mode === 'wss' ? '443' : (mode === 'ws' ? '80' : '22')) };
	},

	paintEditor: function() {
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

		this.refs.testOut = E('div');
		ed.appendChild(section('Edit profile: ' + (d.name || id), rows.concat([
			E('div', { 'class': 'tt-actions' }, [
				button('Save & Apply', 'apply', function() { self.saveDraft('run'); }),
				button('Save', 'save', function() { self.saveDraft(false); }),
				button('Test connection', 'action', function(ev) { self.testProfile(ev.target); }),
				button('Delete profile', 'remove', function() { self.deleteProfile(id); })
			]),
			this.refs.testOut
		])));
	},

	saveDraft: function(apply) {
		var d = this.draft, id = this.sel, self = this;
		if (!d) return Promise.resolve();
		uci.unset(CONF, id, 'mode');
		[ 'name', 'type', 'use_payload', 'use_tls', 'use_slowdns', 'enhanced', 'insecure', 'host', 'port', 'user', 'pass', 'bug', 'ua', 'payload', 'proxy', 'udpgw', 'split_delay', 'dns_domain', 'dns_resolver', 'dns_pubkey', 'dns_type' ].forEach(function(k) {
			var v = d[k];
			if (v == null || v === '') uci.unset(CONF, id, k);
			else uci.set(CONF, id, k, (k === 'pass' || k === 'payload') ? v : String(v).trim());
		});
		this.draft = null;
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
		var ps = this.profiles(), i = ps.findIndex(function(p) { return p['.name'] === id; }), j = i + dir;
		if (i < 0 || j < 0 || j >= ps.length) return;
		var t = ps[i]; ps[i] = ps[j]; ps[j] = t;
		ps.forEach(function(p, n) { uci.set(CONF, p['.name'], 'order', String(n + 1)); });
		var self = this;
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

	renderSettings: function() {
		var self = this;
		var g = function(o, d) { return self.get(o, d); };
		var txt = function(o, d, attrs) { return textInput(g(o, d), function(v) { self.set(o, v); }, attrs); };
		var sel = function(o, d, opts) {
			var cur = g(o, d);
			if (!opts.some(function(x) { return x[0] === cur; })) opts = opts.concat([ [ cur, cur ] ]);
			return select(cur, opts, function(v) { self.set(o, v); });
		};
		var flag = function(o, d) { return checkbox(g(o, d) === '1', function(v) { self.set(o, v ? '1' : '0'); }); };
		var ifOpts = [ [ '', 'Auto (system route)' ] ].concat(this.ifaces.map(function(i) { return [ i, i ]; }));
		var bypass = E('textarea', { 'class': 'cbi-input-textarea tt-log', 'rows': '4', 'spellcheck': 'false', 'input': function(ev) {
			var lines = ev.target.value.split('\n').map(function(l) { return l.split('#')[0].trim(); }).filter(function(l) { return l; });
			uci.set(CONF, 'main', 'bypass', lines.length ? lines : null);
		} }, (uci.get(CONF, 'main', 'bypass') || []).join('\n'));
		var bannerSel = select(g('banner', 'default'), [ [ 'default', 'Default (Dusk City)' ], [ 'custom', 'Custom image…' ], [ 'none', 'No banner' ] ], function(v) {
			if (v === 'custom') return self.uploadBanner();
			self.set('banner', v);
		});

		return [
			section('How LAN traffic uses the tunnel', [
				row('Traffic mode', sel('net_mode', 'tun', [ [ 'tun', 'TUN (recommended) · TCP + UDP' ], [ 'redirect', 'TCP redirect (nftables / iptables)' ], [ 'socks', 'SOCKS5 only (for OpenClash / PassWall)' ] ]),
					'TUN: TCP + UDP. Redirect: TCP only. SOCKS5: no routing changes.'),
				row('SOCKS5 port', txt('socks_port', '1080', { 'inputmode': 'numeric' })),
				row('SOCKS5 listen', sel('socks_listen', '127.0.0.1', [ [ '127.0.0.1', '127.0.0.1 (router only)' ], [ '0.0.0.0', 'All interfaces (LAN)' ] ]))
			]),
			section('Connection & reconnect', [
				row('Outgoing interface', sel('wan_iface', '', ifOpts), 'e.g. the modem (wwan0). Auto = normal routing.'),
				row('Parallel SSH sessions', sel('sessions', '1', [ [ '1', '1 (normal)' ], [ '2', '2' ], [ '3', '3' ], [ '4', '4' ] ])),
				row('Connection check URL', txt('check_url', 'http://cp.cloudflare.com/generate_204', { 'spellcheck': 'false' })),
				row('Check every (seconds)', txt('check_interval', '5', { 'inputmode': 'numeric' })),
				row('Failures before reconnect', txt('check_fail', '3', { 'inputmode': 'numeric' })),
				row('Profile selection', sel('policy', self.policy(), [ [ 'manual', 'Manual · stay on the chosen profile' ], [ 'fallback', 'Fallback · next profile after 3 failed reconnects' ], [ 'auto', 'Auto · move to the fastest profile' ] ]),
					'Also on the Dashboard.'),
				row('Restart modem', flag('restart_modem', '0'), 'ifup the modem when every profile fails.'),
				row('Modem interface', txt('modem_iface', 'wwan'), 'Name in Network › Interfaces.'),
				row('Start on boot', flag('autostart', '0'))
			]),
			section('SSH compatibility', [
				row('Algorithms', sel('ssh_compat', 'auto', [ [ 'auto', 'Auto (recommended)' ], [ 'legacy', 'Legacy (old Dropbear first)' ], [ 'modern', 'Modern only' ] ]),
					'Auto tries modern algorithms first, then the ones old Dropbear needs.')
			]),
			section('DNS', [
				row('DNS through tunnel', sel('dns_mode', 'tunnel', [ [ 'tunnel', 'Yes · DNS over TCP through SSH' ], [ 'off', 'No (use ISP DNS)' ] ])),
				row('DNS server', txt('dns_server', '1.1.1.1'), 'Asked from the SSH server side.'),
				row('Manage dnsmasq', flag('manage_dnsmasq', '1'), 'Restored on Stop.')
			]),
			section('Bypass', [
				row('Outside the tunnel', bypass, 'IP, CIDR or device, one per line. Server and LAN are bypassed automatically.')
			]),
			section('Appearance', [
				row('Banner', bannerSel, 'JPG, PNG or WebP, about 1200×200, max 500 KB.')
			]),
			E('div', { 'class': 'cbi-page-actions tt-pgact' }, [
				button('Save & Apply', 'apply', function() { self.commit(false).then(function() { return self.runAfterSave(false); }); }), ' ',
				button('Reset', 'reset', function() { uci.unload(CONF); uci.load(CONF).then(function() { self.show('settings'); }); })
			])
		];
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
				E('div', { 'class': 'tt-box' }, [ 'Made by ', E('a', { 'href': TG, 'target': '_blank', 'rel': 'noopener' }, E('strong', {}, '@dudulrealnofek')), ' · Telegram · bug reports & ideas welcome' ]),
				E('div', { 'class': 'tt-box' }, [ 'Group ', E('a', { 'href': 'https://t.me/tfnuklir', 'target': '_blank', 'rel': 'noopener' }, E('strong', {}, '@tfnuklir')), ' · Telegram' ]),
				E('h4', {}, 'Thanks to'),
				E('div', { 'class': 'tt-thx' }, [
					[ 'wegare', 'https://github.com/wegare123/stl', 'stl — SSH injector for OpenWrt, the starting idea' ],
					[ 'heiher', 'https://github.com/heiher/hev-socks5-tunnel', 'hev-socks5-tunnel — TUN to SOCKS5' ],
					[ 'David Fifield', 'https://www.bamsoftware.com/software/dnstt/', 'dnstt — DNS tunnel behind SlowDNS' ],
					[ 'Hysteria (apernet)', 'https://github.com/apernet/hysteria', 'QUIC core behind ZiVPN (MIT)' ],
					[ 'Go team', 'https://pkg.go.dev/golang.org/x/crypto/ssh', 'golang.org/x/crypto/ssh' ],
					[ 'OpenWrt & ImmortalWrt', 'https://openwrt.org/', 'the firmware and LuCI' ],
					[ 'HTTP Custom', null, 'inspiration for the payload modes' ]
				].map(function(t) {
					return E('div', { 'class': 'tt-thr' }, [ t[1] ? E('a', { 'href': t[1], 'target': '_blank', 'rel': 'noopener' }, E('strong', {}, t[0])) : E('strong', {}, t[0]), E('span', {}, t[2]) ]);
				})),
				E('p', {}, 'License: GPL-3.0 · source on GitHub')
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
