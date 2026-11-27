# Tomato Tunnel Guide

**English** · [Bahasa Indonesia](GUIDE.id.md)

Every feature up to **version 1.7.3**, in short.

## Contents

1. [What it is](#1-what-it-is)
2. [Requirements](#2-requirements)
3. [Install](#3-install)
4. [Quick start](#4-quick-start)
5. [Account types](#5-account-types)
6. [Accounts tab](#6-accounts-tab)
7. [Dashboard tab](#7-dashboard-tab)
8. [Modes: Manual, Fallback, Auto, Balance](#8-modes-manual-fallback-auto-balance)
9. [Balance](#9-balance)
10. [Change IP](#10-change-ip)
11. [Usage tab](#11-usage-tab)
12. [Settings](#12-settings)
13. [Telegram bot](#13-telegram-bot)
14. [Log tab](#14-log-tab)
15. [Update](#15-update)
16. [Payload tags](#16-payload-tags)
17. [CPU and RAM](#17-cpu-and-ram)
18. [Common problems](#18-common-problems)
19. [Version history](#19-version-history)

---

## 1. What it is

A tunnel client for OpenWrt and ImmortalWrt, controlled from LuCI (**Services › Tomato Tunnel**) or a Telegram bot. Every device on the router goes through the tunnel, no app needed.

- **SSH**: like HTTP Custom, with Payload, TLS/SNI, WebSocket, Enhanced and SlowDNS.
- **ZiVPN**: host, port and password.
- **Xray**: VLESS, VMess, Trojan, added by pasting the link.

## 2. Requirements

- OpenWrt or ImmortalWrt **23.05 or newer**.
- Architectures: `aarch64_generic`, `aarch64_cortex-a53`, `aarch64_cortex-a72`, `arm_cortex-a7`, `arm_cortex-a7_neon-vfpv4`, `arm_cortex-a9`, `arm_cortex-a9_vfpv3-d16`, `arm_cortex-a15_neon-vfpv4`, `mipsel_24kc`, `mips_24kc`, `x86_64`.
- About **13 MB** flash and **64 MB** RAM free. The Xray add-on needs about **30 MB** more flash.

Check your architecture:

```sh
cat /etc/apk/arch        # OpenWrt 25.x and newer
opkg print-architecture  # OpenWrt 23.05 and 24.10
```

## 3. Install

1. Download the file for your router from **Releases** and copy it to `/tmp` on the router.
2. Install:

   ```sh
   apk add --allow-untrusted /tmp/luci-app-tomato-tunnel_1.7.3_aarch64_generic.apk   # OpenWrt 25.x
   opkg install /tmp/luci-app-tomato-tunnel_1.7.3_aarch64_generic.ipk                # OpenWrt 23.05 / 24.10
   ```

3. Open **Services › Tomato Tunnel**.

Xray accounts need the **Xray add-on**: **Settings › About & update › Install Xray**, or the button in an Xray account.

> With OpenClash or PassWall, never run both in TUN mode. Set Tomato Tunnel to **SOCKS5 only** and use `127.0.0.1:1080` there.

## 4. Quick start

1. **Accounts › + Add account**, or **Import** and paste a link or your seller's account message.
2. **Test first**, then **Save & connect**.
3. **Dashboard** shows **Connected** when the LAN goes through the tunnel.

To connect at boot: **Settings › General › Start on boot**.

## 5. Account types

### SSH

| Type | What it does |
|---|---|
| SSH Direct | Plain SSH |
| SSH + Payload | HTTP payload first |
| SSH + Payload + Enhanced | Payload sent in parts at `[split]` |
| SSH + TLS | TLS with SNI, then SSH |
| SSH + Payload + TLS (+ Enhanced) | TLS, then payload, then SSH |
| SSH + SlowDNS | SSH over DNS (dnstt) |
| SSH + Payload + SlowDNS | Payload through SlowDNS |

Other combinations: **Custom combination**.

Fields:

- **Bug host**: TLS SNI and `[bug]` in the payload.
- **Payload text**: see [section 16](#16-payload-tags).
- **Server host**, **Bug host**, **Remote proxy** accept `[rotate=a;b;c]` or `[random=a;b;c]`.
- **[split] delay**: pause between payload parts (default 1000 ms).
- **Remote proxy**: connect through `ip:port`.
- **UDPGW port**: UDP for games and calls (default `7300`, `0` = off).
- **Allow insecure**: skip the certificate check (on by default, usual for bug hosts).
- **SlowDNS**: NS, public key, record type, resolver, EDNS0 size and KCP window.
  - **Record type**: Automatic tries each type. TXT, NULL, CNAME, HTTPS, SVCB, DNSKEY, A, AAAA, NS, MX and SRV. Some carrier DNS only passes some types; HTTPS carries the most data per answer, A and AAAA the least.
  - **Resolver**: **Auto** = the DNS the operator gave the modem or phone this account goes out through (for operators that block public DNS, such as XL OPOK), Cloudflare DoH, or a custom one (`1.2.3.4`, `1.2.3.4:53`, `https://…`, `dot:host:853`).
  - **EDNS0 size** (512–4096, default 1400) and **KCP window** (default 512): leave them unless the server's seller says otherwise.

Old Dropbear servers: **Settings › Network › SSH compatibility** (Auto handles them).

### ZiVPN

Host, UDP port, password. TCP and UDP.

### Xray

VLESS, VMess or Trojan over TCP, WebSocket, HTTPUpgrade, gRPC or XHTTP, with TLS or REALITY. TCP and UDP. Paste the link in **Import from link**. Needs the Xray add-on.

- **Over SlowDNS**: Xray through the DNS tunnel (Tomato 3.8+ servers), with WebSocket, HTTPUpgrade or XHTTP. Same SlowDNS fields as SSH. Slow, like SSH SlowDNS.
- **Payload**: your own HTTP request goes first, then Xray's own WebSocket / HTTPUpgrade request. TCP, WebSocket or HTTPUpgrade with Security None or TLS (not REALITY, gRPC, XHTTP or SlowDNS).
  - **Enhanced**: payload sent in parts at `[split]`, every reply to the payload is dropped until Xray gets its answer (`101`). Without it, the first reply must already be Xray's.
  - **Send payload** (TLS only): **Inside TLS** (after the handshake, usual) or **Before TLS** (plain text, then Xray's TLS).
  - **Bug host**, **Payload text** (same tags as SSH, empty = a simple GET), **[split] delay**, **Remote proxy**, **User-Agent**.
  - Put the real server in **Address** and the bug host in **Bug host** / **SNI**. The server needs nothing special.

### Fields in every account

- **Go out through**: the modem this account uses. **Auto** with one modem; with more, pick the interface (e.g. `wan (wwan0)`, `hp (usb0)`).
- **Balance weight**: share in Balance, 1–5.
- **Expires on**: reminder before the end; expired accounts are skipped.
- **Show on Dashboard**: off = spare account, never used by Fallback, Auto or Balance.

## 6. Accounts tab

**+ Add account**, **Import**, **Share link**, **Export / Restore** (.json), and per account **Edit**, **Delete**, **Test**. Drag to reorder; Fallback follows this order.

## 7. Dashboard tab

- **Status**: state, account, uptime, **Start / Restart / Stop / Change IP**.
- **Numbers**: live speed, downloaded, uploaded, ping, today, public IP.
- **Traffic**: last 60 seconds.
- **Accounts**: mode buttons, bolt icon = test every account with a real login. Click a card to switch; the current tunnel stays up until the new one connects. Card colours: SSH teal, SlowDNS purple, ZiVPN orange, VLESS blue, VMess indigo, Trojan pink.
- **Modems**: IP and Change IP per modem (with more than one).
- **Balance**: speed, share, ping and connections per account (Balance mode).
- **Recent log**.

### Connections tab

Live connections: device, site, account, speed, total, time. Filter by text, or click an account box. Only built while the tab is open.

## 8. Modes: Manual, Fallback, Auto, Balance

| Mode | What it does |
|---|---|
| **Manual** | Stays on one account. |
| **Fallback** | Next account after 3 failed reconnects. Goes back to the chosen one when it works again (checked every 5 min). |
| **Auto** | Moves to an account at least 20 ms faster (every 5 min). |
| **Balance** | Several accounts at once share the traffic. |

## 9. Balance

Several accounts run at once, each on its own modem if you like, and the LAN connections are spread over them. Speeds add up per connection.

### Several modems

Each modem needs its own interface (**Network › Interfaces**) with **Use default gateway** on and a **different gateway metric** (e.g. `wan` 10, `hp` 20). **Status › Routes** must show one `default` per modem.

### Steps

1. Set **Go out through** per account (A → `wan`, B → `hp`).
2. Optional: **Balance weight**, higher for the faster modem.
3. **Dashboard**: choose **Balance**, click cards to add or remove them (dashed border = in Balance).
4. **Start**.

### How it works

- New connections take turns by weight (weights 3 and 1: A, A, B, A…).
- UDP is shared per flow. UDP accounts: Xray, ZiVPN, SSH with UDPGW.
- YouTube stays on one account, so videos do not break.
- The account with the best ping becomes the main one, but only after winning 3 checks in a row, and at most every 10 minutes.
- An account that fails 2 checks gets no new connections until it answers again. A modem that goes down closes its accounts within ~5 s.
- All Xray accounts share **one Xray process** (saves ~40 MB RAM per extra account).

**Balance: same account per site** (Settings › General): keeps each site on one account for 10 minutes. For banks and games; off by default.

### Good to know

- One download over one connection gets one account's speed. Speed tests, YouTube, download managers and several devices get the sum.
- One account on two modems shows two IPs to the server; an account limited to one IP may get locked.
- Several accounts on one modem help only when the server or the account is the limit, not the modem.
- SlowDNS gets a small share; better kept as a spare.

## 10. Change IP

**Settings › Change IP** asks the operator for a new IP, then the tunnel reconnects.

| Method | How |
|---|---|
| Restart interface | `ifdown` / `ifup` (most RNDIS/QMI modems) |
| Airplane mode (AT) | AT commands on the modem port (**Detect** finds it) |
| ModemManager | `mmcli` off / on |
| HiLink / Orbit | Data off / on through the modem web API |
| Android phone (ADB) | Airplane mode on the tethered phone |

- **+ Add modem**: each modem has its own method, interface, schedule and IP ranges.
- **After failed connects**: change IP automatically after several failed connects.
- **Every (hours)**: scheduled change, `0` = off.
- **Keep trying until the IP is in**: repeat until the IP is in a range.

| Range | Means |
|---|---|
| `10.0` – `10.19` | 10.0.0.0 to 10.19.255.255 |
| `114.12.0.0/14` | 114.12.0.0 to 114.15.255.255 |
| `114.12.` | Every IP starting with 114.12. |

Both the public IP and the modem's own address are checked. End a prefix with a dot: `114.1.` matches only 114.1.x.

## 11. Usage tab

Traffic per day, month, account and device (device needs TUN). **Period starts on day** = your renewal day. **Warn at (GB)** shows a quota notice. With many devices only the top 8 show; **Show all** opens the full list.

## 12. Settings

### General

| Setting | What it does |
|---|---|
| Start on boot | Connect at boot |
| Profile selection | Manual / Fallback / Auto / Balance |
| Fallback: go back | Return to the chosen account when it works again |
| Block QUIC (UDP 443) | **Auto** (default): on, except for ZiVPN. Steadier YouTube; games and calls untouched |
| Balance: same account per site | See [section 9](#9-balance) |
| Warn before expiry | 1, 3 or 7 days |
| Connection check URL | Default `http://cp.cloudflare.com/generate_204` |
| Unified delay | Ping without handshakes, like OpenClash |
| Check every / Failures before reconnect | Default 5 s / 3 |
| Parallel SSH sessions | 1–4 |

### Schedule

Each line: **time**, **days**, **action**, value, **until**.

- **Use account** / **Set mode**: switch at the time, back to the previous one at **until**. Empty **until** = the change stays. A choice you make by hand meanwhile is kept.
- **Tunnel on/off**: **Off** keeps the tunnel off in the window; **On only** keeps it on only in the window. Works while the tunnel is stopped and after a reboot.
- Windows may cross midnight. Everything follows the router clock (**System › System**).

| Example | Time | Days | Action | Value |
|---|---|---|---|---|
| Night account | 00:00 | Every day | Use account | Night account until 06:00 |
| Off while sleeping | 01:00 | Every day | Tunnel on/off | Off until 06:00 |
| Office hours only | 08:00 | Mon–Fri | Tunnel on/off | On only until 17:00 |
| Balance in the evening | 19:00 | Every day | Set mode | Balance until 23:00 |

### Network

| Setting | What it does |
|---|---|
| Traffic mode | **TUN** (recommended), TCP redirect, or SOCKS5 only |
| SOCKS5 port / listen | Default `127.0.0.1:1080` |
| Outgoing interface | Way out for accounts on Auto |
| DNS through the tunnel / DNS server | DNS over the tunnel, default `1.1.1.1` |
| Manage dnsmasq | Point dnsmasq at the tunnel DNS, restored on Stop |
| Indonesian IPs go direct | Local sites and games skip the tunnel |
| Direct IPs / devices | Skip the tunnel, one per line |
| Direct domains | Leave through the modem (banks, games), subdomains included |
| Tailscale | See below |
| Block ads (Xray) | Small ad list |
| SSH compatibility | Auto / Legacy / Modern |

**Tailscale** (when installed on the router, TUN mode):

- Its traffic goes through the tunnel, so no regular quota is needed.
- LAN devices reach 100.x addresses.
- Your Tailscale devices open LuCI at the router's 100.x address from anywhere.
- The router can be an exit node; that traffic goes through the tunnel too.
- Start it with `tailscale up --accept-dns=false`. Do not use an exit node on the router itself.

### Telegram, Appearance, About & update

- **Telegram**: [section 13](#13-telegram-bot).
- **Appearance**: six banners, your own image (JPG/PNG/WebP, ~1200×200, max 500 KB), or none.
- **About & update**: [section 15](#15-update).

## 13. Telegram bot

1. Create a bot with [@BotFather](https://t.me/BotFather), paste the token.
2. **Enable bot**, save.
3. **Pair new admin**, send the code to the bot.

The bot can show status, switch account or mode, Change IP, start / stop, show usage, manage accounts, update, backup, reboot. Alerts: drop and return, switch, failed login, expiry, IP change, new device, update, daily report. Each action group can be turned off. It works while the tunnel is stopped.

## 14. Log tab

Level (Info, Debug, Errors), auto-scroll, download, clear.

## 15. Update

**Settings › About & update** installs new releases from GitHub (SHA256 checked, settings kept). Channel: Stable or Beta. The daily check only shows a notice. The Xray add-on updates with Tomato Tunnel.

## 16. Payload tags

For SSH and Xray payloads. Case-insensitive. Unknown tags are sent as written. With Xray, its own request is added after the payload by itself: leave the `Upgrade` header out unless you know the server answers it.

| Tag | Value |
|---|---|
| `[host]` `[port]` `[host_port]` | Server host, port, `host:port` |
| `[ip]` `[ip_port]` | Server IP, `ip:port` |
| `[bug]` `[sni]` | Bug host (empty: server host) |
| `[proxy]` `[proxy_host]` `[proxy_port]` | Remote proxy |
| `[method]` `[protocol]` `[ua]` | `CONNECT`, `HTTP/1.1`, User-Agent |
| `[raw]` | `CONNECT host:port HTTP/1.0` + empty line |
| `[netData]` | Same, without the empty line |
| `[ws_key]` | Random `Sec-WebSocket-Key` |
| `[crlf]` `[lf]` `[cr]` `[lfcr]` | Line breaks; repeat with `[crlf*2]` |
| `[split]` | Send in parts, pause = **[split] delay** |
| `[delay]` / `[instant_split]` | Split with 1.5 s / no pause |
| `[rotate]` / `[random]` | Several payloads, in turn / at random |
| `[rotate=a;b;c]` / `[random=a;b;c]` | One value per connect (also in host, bug host, proxy) |

Examples:

```
GET / HTTP/1.1[crlf]Host: [bug][crlf]Upgrade: websocket[crlf]Connection: Upgrade[crlf]Sec-WebSocket-Key: [ws_key][crlf]Sec-WebSocket-Version: 13[crlf][crlf]
```

```
HEAD / HTTP/1.1[crlf]Host: [bug][crlf][crlf]PATCH / HTTP/1.1[crlf]Host: [host][crlf]Upgrade: websocket[crlf][crlf][split]HTTP/1.1[crlf][crlf]
```

## 17. CPU and RAM

King3399 (RK3399, 2 GB), ImmortalWrt, TUN mode. 100 % = one core.

| Situation | RAM idle | CPU at full speed |
|---|---|---|
| Manual, SSH + TLS | ~20 MB | 139 % at 117 Mbps |
| Manual, Xray Trojan WS TLS | ~70 MB | 113 % at 90 Mbps |
| Balance, 2 Xray accounts | ~57 MB (1.7.1: ~105 MB) | 162 % at 122 Mbps |

- Since 1.7.2 all Xray accounts in Balance share one Xray process. SSH and ZiVPN accounts add only a few MB.
- Tailscale itself uses about 35–50 MB.
- On slow boxes (S905X and similar), Balance at 80–100 Mbps can use 1.5–2 cores.

## 18. Common problems

**Second modem fails with "network unreachable"**: same metric on both interfaces, or "Use default gateway" off. See [section 9](#9-balance).

**Speed test does not add up**: use a multi-connection test, keep "same account per site" off.

**"Login failed" on a card**: account locked, expired or wrong password.

**Old Dropbear keeps dropping**: SSH compatibility on Auto or Legacy.

**"Another LuCI change is still being applied"**: another LuCI page is waiting for its Apply confirmation. Wait about 90 s, or confirm / revert it (top right of LuCI). If the login expired, log in again.

**YouTube "Something went wrong"**: keep Block QUIC on Auto.

**OpenClash or PassWall conflict**: not both in TUN mode; set Tomato Tunnel to SOCKS5 only.

**Page looks old after an update**: refresh with Ctrl+Shift+R, or clear the browser cache on a phone.

**SlowDNS connects but there is no internet, or it is very slow**: try another resolver (Custom) and the HTTPS record type. A and AAAA carry little data per answer, so they are the slowest.

**Xray with payload: "server answered … not 101"**: turn on Enhanced, or check Bug host, Path and Host header.

## 19. Version history

| Version | Highlights |
|---|---|
| 1.0.0 | SSH client like HTTP Custom: Payload, TLS/SNI, WebSocket, Enhanced, SlowDNS |
| 1.1.0 | Dashboard, live switch, Manual / Fallback / Auto, ZiVPN |
| 1.2.0 | Usage, Change IP, account expiry, 11 architectures |
| 1.2.1 | New logo |
| 1.3.0 | Xray accounts and add-on, Show on Dashboard, Indonesian IPs direct |
| 1.3.1 | Only the Tomato Tunnel Xray build is used |
| 1.4.0 | Telegram bot |
| 1.5.0 | Old Dropbear fix, faster Xray TUN |
| 1.6.0 | Several modems, IP ranges, Go out through, Balance, new payload tags, banner header |
| 1.7.0 | New look, Connections tab, real ping, Fallback goes back, Block QUIC, Direct domains, Schedule |
| 1.7.1 | Tunnel on/off schedule, faster DNS, no more late notifications, Balance by weight, save fix on OpenWrt 24.10 |
| 1.7.2 | One Xray process for Balance (about half the RAM), YouTube fix (Block QUIC Auto, YouTube on one account), steadier Balance main account, Schedule "until", Tailscale through the tunnel + LuCI over Tailscale, Usage "Show all", phone layout fixes, shorter texts, faster page load |
| 1.7.3 | Xray over SlowDNS, payload for Xray (Enhanced, inside / before TLS), new SlowDNS record types HTTPS, SVCB, DNSKEY, A and AAAA (DNS64 networks too), resolver Auto (operator DNS), EDNS0 and KCP window settings, steadier A/AAAA, Stop no longer hangs on a stuck SlowDNS, phone button layout fix |

---

Made by [@dudulrealnofek](https://t.me/dudulrealnofek). Group: [@tfnuklir](https://t.me/tfnuklir).
