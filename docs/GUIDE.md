# Tomato Tunnel Guide

**English** · [Bahasa Indonesia](GUIDE.id.md)

Every Tomato Tunnel feature up to **version 1.7.1**: what it does, where it is, and how to use it.

## Contents

1. [What Tomato Tunnel is](#1-what-tomato-tunnel-is)
2. [Requirements](#2-requirements)
3. [Install](#3-install)
4. [Quick start](#4-quick-start)
5. [Account types](#5-account-types)
6. [Accounts tab](#6-accounts-tab)
7. [Dashboard tab](#7-dashboard-tab)
8. [Profile selection: Manual, Fallback, Auto, Balance](#8-profile-selection-manual-fallback-auto-balance)
9. [Balance: adding up several modems](#9-balance-adding-up-several-modems)
10. [Change IP and several modems](#10-change-ip-and-several-modems)
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

## 1. What Tomato Tunnel is

Tomato Tunnel is a tunnel client for OpenWrt and ImmortalWrt routers. You control it from its LuCI page (**Services › Tomato Tunnel**) or from a Telegram bot.

- **SSH** works like HTTP Custom: SSH is the base, and you add the layers you need on top (Payload, TLS/SNI, WebSocket, Enhanced, SlowDNS).
- **ZiVPN** works like the ZiVPN app: host, port and password only.
- **Xray** accounts (VLESS, VMess, Trojan) are added by pasting the link.

Every device on the router (phones, laptops, TVs) goes through the tunnel without any app on the device.

## 2. Requirements

- OpenWrt or ImmortalWrt **23.05 or newer**.
- Supported architectures: `aarch64_generic`, `aarch64_cortex-a53`, `aarch64_cortex-a72`, `arm_cortex-a7`, `arm_cortex-a7_neon-vfpv4`, `arm_cortex-a9`, `arm_cortex-a9_vfpv3-d16`, `arm_cortex-a15_neon-vfpv4`, `mipsel_24kc`, `mips_24kc`, `x86_64`.
- About **13 MB** of free flash and **64 MB** of free RAM.
- The Xray add-on (optional, only for Xray accounts) needs about **30 MB** more flash.
- `kmod-tun ip-full ca-bundle luci-base` are installed automatically from the official repo.

To check your router's architecture:

```sh
cat /etc/apk/arch        # OpenWrt 25.x and newer
opkg print-architecture  # OpenWrt 23.05 and 24.10
```

## 3. Install

1. Download the file for your router from the **Releases** page on GitHub and copy it to `/tmp` on the router.
2. Install it:

   ```sh
   # OpenWrt 25.x and newer (apk)
   apk add --allow-untrusted /tmp/luci-app-tomato-tunnel_1.7.1_aarch64_generic.apk

   # OpenWrt 23.05 and 24.10 (opkg)
   opkg install /tmp/luci-app-tomato-tunnel_1.7.1_aarch64_generic.ipk
   ```

3. Open LuCI and go to **Services › Tomato Tunnel**.

The **Xray add-on** is only needed for Xray accounts. The easiest way: **Settings › About & update › Xray add-on › Install Xray**, or the **Install Xray** button in an Xray account. It downloads the right package and checks its SHA256.

> If you also use OpenClash or PassWall, do not run both in TUN mode at the same time. Set Tomato Tunnel to **SOCKS5 only** and use `127.0.0.1:1080` as a proxy there.

## 4. Quick start

1. Open the **Accounts** tab and press **+ Add account**. Or use **Import** and paste a `vless://`, `vmess://`, `trojan://` or `tomatotunnel://` link, or the account message from your seller.
2. Fill in the account and press **Test first** to test it without touching the running connection.
3. Press **Save & connect**.
4. Open the **Dashboard** tab. When the status shows a green **Connected**, every device on the LAN goes through the tunnel.

To start the tunnel when the router boots, turn on **Settings › General › Start on boot**.

## 5. Account types

### SSH

| Type | What it does |
|---|---|
| SSH Direct | Plain SSH to the server |
| SSH + Payload | Sends an HTTP payload first (WebSocket, CONNECT, ...) |
| SSH + Payload + Enhanced | Sends the payload in parts at every `[split]` |
| SSH + TLS | TLS with a custom SNI, then SSH |
| SSH + Payload + TLS | TLS with SNI, then the payload, then SSH |
| SSH + Payload + TLS + Enhanced | Same, with the payload split |
| SSH + SlowDNS | SSH carried over DNS (dnstt) |
| SSH + Payload + SlowDNS | Payload sent through the SlowDNS stream |

Any other combination can be set with **Custom combination** in the account form.

Important fields in the SSH form:

- **Bug host**: used as the TLS SNI and for `[bug]` in the payload.
- **Payload text**: the payload. See the tags in [section 16](#16-payload-tags).
- **Server host**, **Bug host** and **Remote proxy** can hold `[rotate=a;b;c]` or `[random=a;b;c]` to change the value on every connect.
- **[split] delay (ms)**: the pause between payload parts (default 1000 ms).
- **Remote proxy**: connect through this `ip:port` instead of straight to the server.
- **User-Agent [ua]**: the value of the `[ua]` tag.
- **UDPGW port**: UDP for games and calls. Default `7300`, `0` turns it off.
- **Allow insecure**: do not check the TLS certificate. Usually needed for bug host / SNI tricks. On by default.
- For **SlowDNS**: Nameserver (NS), Public key, Record type (Automatic, TXT, NULL, CNAME, NS, MX, SRV) and DNS resolver.

**Old Dropbear servers.** In **Settings › Network › SSH compatibility**, **Auto** tries modern algorithms first and falls back to `diffie-hellman-group1/14-sha1`, `ssh-rsa`, `aes128-cbc` and `3des-cbc`. **Legacy** (old Dropbear first) and **Modern only** are also available.

Every HTTP reply from the front server is skipped until the SSH server answers. The server banner shows in the log during login.

### ZiVPN

The ZiVPN app protocol (Hysteria2 over QUIC). Host, UDP port and password only. Carries TCP and UDP.

### Xray

VLESS, VMess or Trojan over TCP, WebSocket, HTTPUpgrade, gRPC or XHTTP, with TLS or REALITY (Vision too). Carries TCP and UDP.

- Easiest: paste the account link in **Import from link** and every field is filled in.
- Needs the **Xray add-on** (see [Install](#3-install)).
- The bundled Xray build keeps `allowInsecure`, which upstream Xray removed.
- **Direct domains (Xray)** and **Block ads (Xray)** are in Settings › Network.

### Fields in every account type

- **Go out through**: the modem or interface this account always uses. **Auto** follows the router's own route (mwan3 too). See [section 9](#9-balance-adding-up-several-modems).
  - **One modem:** just pick **Auto**, whatever the interface is called (`wan`, `wwan`, `eth1`, `usb0`, ...).
  - **More than one modem:** pick the interface by its name in Network › Interfaces. The list shows the name and the real device, such as `wan (wwan0)` or `hp (usb0)`. The name does not matter, as long as the interface is the one on that modem.
- **Balance weight**: the share of connections in Balance mode, 1–5.
- **Expires on**: the account's end date. You get a reminder before it ends, and expired accounts are skipped by Auto, Fallback and Balance.
- **Show on Dashboard**: turn it off to keep the account as a spare. It is never used by Fallback, Auto or Balance and has no card on the Dashboard, but Save & connect still uses it; a plain **Save** is enough for the change.

## 6. Accounts tab

- **+ Add account**: a new account.
- **Import**: paste an account link or the account message from your seller. Imported accounts are saved at once *(1.7.0)*.
- **Share link**: share one account as a link.
- **Backup / restore**: **Export all accounts (.json)** and **Restore from file**.
- **Edit**, **Delete**, **Test**: per account.
- **Reorder** accounts by dragging them. Fallback uses this order.
- The line on the left of each account shows its type (same colours as the Dashboard) *(1.7.0)*.

## 7. Dashboard tab

The page has a new look in 1.7.0: every part is a row with its title on the left, and the colours, buttons and cards come from your LuCI theme, so it fits any theme.

- **Status**: Connected / Connecting / Stopped, the account, connection type, traffic mode and uptime, with **Start**, **Restart**, **Stop** and **Change IP** (when turned on).
- **Status numbers**: the live download speed (big) with upload, then **Downloaded, Uploaded, Ping, Today**, and one line with the public IP, the ISP and the reconnects of today.
- **Traffic**: 60-second chart, kept by the daemon so it is still there after a page reload. Drops show as gaps.
- **Accounts**: the mode buttons (Manual, Fallback, Auto, Balance) and the latency test (bolt icon) above the account cards. The cards sit in up to three columns. The line on the left of a card shows the account type: SSH teal, SlowDNS purple, ZiVPN orange, VLESS blue, VMess indigo, Trojan pink. Click a card to switch **without Stop / Start**: the current tunnel keeps working until the new account is connected; if it fails, nothing changes. An account with **Show on Dashboard** off has no card, also while it runs.
- **Ping on the cards** *(1.7.0)*: a real test. Each account logs in and opens the check URL through itself, so a dead, locked or expired account shows **failed** even when its server still answers. Accounts that are running show the ping of the live tunnel. With **Unified delay** (on by default, like OpenClash) the connection handshakes are left out and only the round trip counts, so the numbers are smaller and fair between SSH and Xray.
- **Modems** *(more than one modem)*: each modem's IP and a Change IP button per modem.
- **Balance** *(Balance mode only)*: its own row with the speed, share of traffic, ping and connections of each account.
- **Recent log**: the latest log lines.

### Connections tab *(1.7.0)*

Every connection that goes through the tunnel right now, fastest first, refreshed every 2 seconds: **Device** (name from DHCP), **Site** (name from the tunnel DNS), **Account** it went out on, **Speed**, **Total** and **Time**. Filter by device, site or account; the boxes on the left count the connections per account, and a click on one shows only that account *(1.7.1)*. On a phone every connection is one card. The list is only made while the tab is open, so it costs nothing otherwise. When an Xray account runs alone (not Balance), the TUN goes straight to Xray: devices and sites are still shown, speed and total are not.

## 8. Profile selection: Manual, Fallback, Auto, Balance

Set in **Dashboard › Profiles** or **Settings › General › Profile selection**.

| Mode | How it works |
|---|---|
| **Manual** | Stays on the chosen profile. |
| **Fallback** | Moves to the next profile after 3 failed reconnects. With *Fallback: go back to the chosen account* (on by default, 1.7.0) it goes back to the account you chose once a real login works again: checked every 5 minutes, at least 3 minutes after the move. |
| **Auto** | Moves to a profile at least 20 ms faster (checked every 5 minutes). |
| **Balance** | Several accounts run at the same time and share the traffic. See the next section. |

## 9. Balance: adding up several modems

Balance runs several accounts **at the same time**, each through its own modem, and spreads the connections of the LAN devices over them. The speed of several modems adds up (1 + 1 ≈ 2).

### Network setup (needed with more than one modem)

Each modem needs its own interface in **Network › Interfaces**, for example `wan` for the modem and `hp` for phone USB tethering. In **Edit › Advanced Settings** of each interface:

- **Use default gateway**: keep it on.
- **Use gateway metric**: a **different** number on each interface, for example `wan` = `10` and `hp` = `20`.

Check **Status › Routes**: there must be one `default` line per modem. With the same metric the router installs only one default route, and accounts set to the other modem fail with *network unreachable*.

### Steps

1. In each account, set **Go out through** to its modem, for example account A → `wan`, account B → `hp`.
2. (Optional) Set **Balance weight**. Give a faster modem a higher number, for example 4G+ modem = 2 and 4G phone = 1.
3. In **Dashboard › Profiles**, choose **Balance**. Click an account card to add it to Balance or take it out. Cards in Balance have a dashed border.
4. Press **Start**.

### How the traffic is shared

- New connections take turns by **weight** *(1.7.1)*: with weights 3 and 1 the order is A, A, B, A, and so on. Connections already open are not counted, because idle ones (chat, push, music apps) carry no traffic.
- **Only the weight decides the share** *(1.7.1)*, like OpenClash's load-balance. The speed an account carries is not used: it depends on how many connections it was given and on the other accounts on the same modem, so it used to push traffic onto the wrong account. Give a slower account (SlowDNS, say) a lower weight than the fast one.
- **UDP** (YouTube QUIC, games, calls) is shared per flow too. Each flow stays on one account until it ends. Accounts that carry UDP: Xray, ZiVPN and SSH with UDPGW.
- **The account with the best ping becomes the main one by itself.** Every 30 seconds Tomato Tunnel checks. When the link is quiet, an account with a ping 150 ms lower takes over; while it is busy nothing changes *(1.7.1)*. If the main account fails its checks it is replaced right away. Normal changes happen at most once every 3 minutes, and the running session is reused, so nothing reconnects.
- **When an account dies:** after one failed check it gets no new connections. After 3 failed checks it is dropped and reconnected every 10–60 seconds, and it shares the traffic again as soon as it is back. If the modem itself dies (its interface goes down), every account on it is closed within about 5 seconds *(1.7.1)*, so downloads that were running on it start again on the other modem instead of hanging for half a minute; when the interface is up again the accounts reconnect by themselves.

### Extra setting

**Settings › General › Balance: same account per site**

- **Off** (default, to add the speed up): even connections to the same site are spread over every account.
- **On**: one site stays on one account for 10 minutes. Good for banking apps or games that dislike a changing IP, but one site then gets the speed of one modem only.

### Good to know

- The speed adds up **per connection**. A multi-connection speed test, YouTube, a download manager or several devices at once get close to the total of all modems. One download over one connection still gets the speed of one account.
- One account used through two modems shows up from two IPs on the server. An account limited to one IP can get locked, so one account per modem is safer.
- Two accounts on one modem work. They add speed only when the server or the account is the limit, not the modem. They are also a good spare.
- SlowDNS can join, but it gets a small share because it is slow. It is better kept as a spare.

### One modem, several accounts

Balance also works with only one modem. Leave every account on **Go out through = Auto**; no metrics needed.

- **Faster** when the server or the account is the limit: a speed cap per account, a busy server at peak hours, or a server far away.
- **Not faster** when the modem itself is maxed out. More accounts only share the same capacity.
- **Still useful:** a spare with no gap (if one server goes down, traffic moves at once, without a reconnect), the account with the best ping as the main one, and the load spread over several servers.
- **The cost:** about 40 MB of RAM per Xray account, and a little more CPU.

To find out: run a multi-connection speed test in **Manual** mode with one account, then in **Balance** with every account, ideally at peak hours too. If Balance is clearly faster, it pays off for you.

**Example (1.7.1).** King3399, ImmortalWrt, TUN mode, every account out through the same WAN, two Xray Trojan WS TLS accounts on different servers (A and B), Speedtest by Ookla CLI from a LAN PC over Wi-Fi, same server (Jakarta) every run, quiet hours (05:00). Each number is one run.

| Mode | Download (Mbps) | Upload (Mbps) |
|---|---|---|
| Manual, account A | 145 · 152 · 143 | ~53 |
| Manual, account B | ~113 ¹ | – |
| Balance before 1.7.1 | 110 · 123 | ~53 |
| Balance 1.7.1, weight A 1 · B 1 | 152 · 164 · 161 | ~54 |
| Balance 1.7.1, weight A 3 · B 1 | 180 · 149 · 169 | ~51 |

¹ Measured with 8 parallel downloads from speed.cloudflare.com, not Ookla.

Before 1.7.1 Balance judged the accounts by the traffic they carried, so the slower account B became the main one and took most of the traffic. Since 1.7.1 only the weight decides. Your numbers depend on your modem, servers and time of day: test at peak hours too.

**Example with two modems (1.7.1).** Same router, account A on a USB modem, account B on a phone over USB tethering, Speedtest by Ookla (Jakarta) and 8 parallel downloads from a speed test server.

| Mode | Download | Upload (Ookla) |
|---|---|---|
| Manual, account A on the modem | ~95 Mbps (8 downloads) · 132–141 Mbps (Ookla) | 50–54 Mbps |
| Balance, weight A 1 · B 1 | 122 Mbps (8 downloads: modem 95 + phone 27) · 109–119 Mbps (Ookla) | 95–106 Mbps |

Upload doubled; the many-download test added the phone's speed; Ookla's download did not gain here. When the phone was unplugged during a download, its account was closed within a second and the downloads went on over the modem.

## 10. Change IP and several modems

**Settings › Change IP**. Change IP asks the operator for a new IP. The tunnel waits, then reconnects on the new IP. Progress is in the Log.

### Methods

| Method | How it works |
|---|---|
| **Restart interface** | `ifdown` / `ifup` the WAN interface. Works with most modems in RNDIS/QMI mode. |
| **Airplane mode (AT command)** | Sends AT commands to the modem serial port: radio off, wait, radio on. **Detect** finds the port. |
| **ModemManager** | Disables and enables the modem with `mmcli`. Needs `modemmanager`. |
| **HiLink / Orbit modem** | Turns mobile data off and on through the modem's web API (Huawei HiLink, Telkomsel Orbit). |
| **Android phone (ADB)** | Airplane mode on the tethered phone (USB debugging on). Needs `adb`. |

### Several modems

- **+ Add modem** adds a modem. Each modem has its own name, method, interface, schedule and IP ranges. Your old settings become Modem 1.
- **When to change automatically**:
  - **After failed connects**: change the IP when the tunnel fails to connect several times in a row.
  - **Which modem** (with more than one): **Auto** (the modem the tunnel uses), **All modems, one after another**, or one modem.
  - **Buttons on the Dashboard**: one Change IP button per modem.
- **Every (hours)** per modem: a scheduled change. `0` turns it off.
- The tunnel only reconnects when the modem that changed IP is the one it uses.

### IP ranges

With **Keep trying until the IP is in**, Change IP repeats until the IP is in one of the ranges (at most **Max tries** times). Accepted formats:

| Entry | Means |
|---|---|
| `10.0` – `10.19` | 10.0.0.0 to 10.19.255.255 (short form, the missing parts are filled in) |
| `10.130` – `10.159` | 10.130.0.0 to 10.159.255.255 |
| `114.12.0.0/14` | CIDR block: 114.12.0.0 to 114.15.255.255 |
| `114.12.` | Every IP that starts with 114.12. |

You can add several ranges. The **Means** column explains each line as you type. Both the **public IP** and the **address on the modem interface** are checked, because operators often hand out a private (CGNAT) address such as 10.x that only the modem shows.

Tip: end a prefix with a dot. `114.1` also matches 114.10.x to 114.19x.x, while `114.1.` only matches 114.1.x.

## 11. Usage tab

- Traffic **per day**, **per account** and **per device** (TUN mode).
- **Monthly** totals, with a period start you can set (**Period starts on day**, for example the day your package renews).
- **Warn at (GB per period)**: a quota warning on the Dashboard.
- **Reset usage**: clears the statistics.

## 12. Settings

### General

| Setting | What it does |
|---|---|
| Start on boot | Connect when the router starts |
| Profile selection | Manual / Fallback / Auto / Balance |
| Fallback: go back to the chosen account *(1.7.0)* | See [section 8](#8-profile-selection-manual-fallback-auto-balance). On by default |
| Block QUIC (UDP 443) *(1.7.0)* | Browsers and apps use TCP instead of QUIC/HTTP3. Can make video steadier when UDP goes through UDPGW or Xray over TCP. Games, calls and DNS are not touched. Off by default |
| Balance: same account per site | See [section 9](#9-balance-adding-up-several-modems) |
| Warn before expiry | A reminder 1, 3 or 7 days before an account ends |
| Connection check URL | URL used to check the connection (default `http://cp.cloudflare.com/generate_204`) |
| Unified delay *(1.7.0)* | Ping without the connection handshakes, like OpenClash. On by default |
| Check every (seconds) | Time between checks (default 5 s) |
| Failures before reconnect | Failed checks before a reconnect (default 3) |
| Parallel SSH sessions | 1–4 SSH sessions at once for one account |

### Change IP

See [section 10](#10-change-ip-and-several-modems).

### Schedule *(1.7.0)*

At a set time the tunnel uses another account or another mode, as if you clicked it. Each line: **Time**, **Days** (every day, Mon–Fri, Sat–Sun or one day of the week), **Action** (*Use account* or *Set mode*), the account or mode, and **On / Off**. Press **+ Add**, then **Save**; changes count without a restart. Example: 00:00 the night-quota account, 06:00 back to the main one. Schedules run while the tunnel is on, by the router clock.

**Tunnel on/off** *(1.7.1)*: choose the action **Tunnel on/off**, then **Off** or **On only** and an end time (**until**).
- **Off** keeps the tunnel off in that window, for example 01:00 until 06:00: at 01:00 it stops, at 06:00 it starts again.
- **On only** keeps the tunnel on just in that window and off outside it.
- A window may cross midnight (23:00 until 02:00); with chosen days it starts on those days.
- These windows run through the router's cron, so they also work while the tunnel is stopped. If the router starts inside an off window, the tunnel stays off until the window ends. **Start** on the Dashboard still works at any time.

**Examples**

| What you want | Time | Days | Action | Account / mode / tunnel |
|---|---|---|---|---|
| Night-quota account at night, main account by day | 00:00 | Every day | Use account | Night account |
| | 06:00 | Every day | Use account | Main account |
| Tunnel off while everyone sleeps (saves quota) | 01:00 | Every day | Tunnel on/off | Off until 06:00 |
| Tunnel only in office hours | 08:00 | Mon–Fri | Tunnel on/off | On only until 17:00 |
| Balance in the busy evening, Manual afterwards | 19:00 | Every day | Set mode | Balance |
| | 23:00 | Every day | Set mode | Manual |
| Another account at the weekend | 00:00 | Sat–Sun | Use account | Weekend account |
| | 00:00 | Mon–Fri | Use account | Main account |
| Off on Friday night across midnight | 23:00 | Fri | Tunnel on/off | Off until 02:00 |

Good to know:
- An account or mode change needs two lines: one to switch, one to switch back. A tunnel window is one line with its end time.
- While a tunnel window keeps the tunnel off, account and mode schedules in that window do nothing. The last account or mode chosen is used when the tunnel starts again.
- With an **On only** window the tunnel is off outside it, also after a reboot. Remove the line, or set it to **Off** in the Status column, to go back to always on.
- Check the router clock and time zone (**System › System**) first: every schedule follows it.


### Network

| Setting | What it does |
|---|---|
| Traffic mode | **TUN** (recommended, TCP + UDP), **TCP redirect**, or **SOCKS5 only** (for OpenClash / PassWall) |
| SOCKS5 port / listen | SOCKS5 port and address (default `127.0.0.1:1080`) |
| Outgoing interface | The way out for every account set to Go out through = Auto. Auto = normal routing. |
| DNS through the tunnel | DNS through the tunnel (DNS over TCP) or the ISP's DNS. Since 1.7.1 two connections to the DNS server stay open and carry every query, so a lookup takes one round trip (about 40–50 ms measured, before 1.7.1 about 220 ms because every query opened a new tunnel connection) |
| DNS server | DNS server at the far end of the tunnel (default `1.1.1.1`) |
| Manage dnsmasq | dnsmasq is pointed at the tunnel DNS while connected and restored on Stop |
| Indonesian IPs go direct | Local sites and games use your normal line instead of the tunnel (APNIC list for Indonesia). Works for every account type. |
| Direct IPs / devices | IPs, CIDRs or devices that skip the tunnel, one per line |
| Direct domains | Domains that leave straight through the modem, such as bank apps and games that block VPN IPs. One per line, subdomains included. Every account type *(1.7.0)*; SSH, SlowDNS and ZiVPN need DNS through the tunnel, and an app may need to be reopened after you add a domain |
| Block ads (Xray) | A small ad and tracker block list |
| SSH compatibility | Auto / Legacy / Modern only |

### Telegram

See [section 13](#13-telegram-bot).

### Appearance

- The banner is the header of the Tomato Tunnel page on every tab, with the logo, the protocols, the version and the author.
- Six built-in banners (Tomato, Night Sky, Forest, Ocean, Minimal, Dusk City), **Your own image**, or **No banner**. No banner gives a plain header in the theme's card colour, without a picture.
- Your own image: JPG, PNG or WebP, about 1200×200, at most 500 KB. Once uploaded, its card shows your image with **Change…** and **Remove**.

### About & update

Updates from GitHub, the Xray add-on and credits. See [section 15](#15-update).

## 13. Telegram bot

**Settings › Telegram**, off by default.

1. Create a bot with [@BotFather](https://t.me/BotFather) and paste the **Bot token**.
2. Turn on **Enable bot** and save.
3. Press **Pair new admin** and send the 6-digit code to the bot. Only paired admins can use the bot.

What the bot can do:

- Status, switch profile, Manual / Fallback / Auto / Balance, Change IP (with a choice of modem when there is more than one), start / stop.
- Usage, accounts (add from a link, expiry, delete), router and devices, log, update, backup and restore, reboot.

Alerts:

- The tunnel drops or comes back, a profile is switched, a login fails.
- An account is about to expire, the IP changes (with the modem's name), the router starts, a new device joins, an update is out.
- A **Daily report** with yesterday's usage.

Bot settings:

- **Language**: English or Bahasa Indonesia.
- **Connect via**: Auto (WAN first, then the tunnel), WAN only, or Tunnel only.
- **Alert after down for**: how long a drop lasts before it is reported (15 seconds to 5 minutes).
- **Allowed actions**: each group of actions can be turned off (start/stop, Change IP, accounts, update, restore, reboot).

The bot runs as its own service, so Start works while the tunnel is stopped.

## 14. Log tab

- **Level**: Info, Debug (payload parts and raw replies), or Errors only.
- **Auto-scroll**, **Download log**, **Clear**.
- Useful log tags: `connect`, `ping`, `switch`, `balance`, `changeip`, `xray`, `slowdns`, `tun`.

## 15. Update

- **Settings › About & update** checks GitHub for the latest release. Updates install from LuCI with a SHA256 check, and settings are kept.
- **Channel**: Stable, or Beta (pre-release).
- **Daily check**: shows a notice on the Dashboard; it never installs by itself.
- The Xray add-on is updated together with Tomato Tunnel.

## 16. Payload tags

Tags go in **Payload text** and are replaced with their value when connecting. Tag names are case-insensitive (`[CRLF]` is the same as `[crlf]`). Unknown tags are sent as written.

### Server

| Tag | Value |
|---|---|
| `[host]` `[server]` `[ssh_host]` | Server host |
| `[port]` `[ssh_port]` | Server port |
| `[host_port]` `[ssh]` | `host:port` |
| `[ip]` | Server IP (resolved) |
| `[ip_port]` | `ip:port` |

### Bug host / SNI and remote proxy

| Tag | Value |
|---|---|
| `[bug]` `[sni]` `[sni_host]` | Bug host (empty: the server host) |
| `[sni_port]` | Server port |
| `[proxy]` | Remote proxy `host:port` |
| `[proxy_host]` `[proxy_port]` | Host / port part of the remote proxy |

### Request

| Tag | Value |
|---|---|
| `[method]` | `CONNECT` |
| `[protocol]` | `HTTP/1.1` |
| `[ua]` | The User-Agent field |
| `[raw]` `[real_raw]` | `CONNECT host:port HTTP/1.0` followed by an empty line |
| `[netData]` `[realData]` | `CONNECT host:port HTTP/1.0` without the empty line |
| `[ws_key]` | A random `Sec-WebSocket-Key`, new on every connect |
| `[auth]` | Empty |
| `[crlf]` `[lfcr]` `[cr]` `[lf]` | Line breaks `\r\n`, `\n\r`, `\r`, `\n` |

**Repeat a tag** with `*N`, such as `[crlf*2]` or `[lf*3]`. A written `\r` or `\n` inside the payload also turns into a line break.

### Split and pause

A split cuts the payload into separate writes.

| Tag | Pause before the next part |
|---|---|
| `[split]` | The **[split] delay** field (default 1000 ms) |
| `[delay]` `[delay_split]` `[split_delay]` | 1.5 seconds |
| `[instant_split]` `[splitNoDelay]` | None |

### Rotate and random

| Tag | What it does |
|---|---|
| `[rotate]` | Separates several payloads. Each connect uses the next one in turn. |
| `[random]` | The same, but a payload is picked at random. |
| `[rotate=a;b;c]` | Becomes `a`, then `b`, then `c`, in turn on each connect. |
| `[random=a;b;c]` | Becomes one of the values at random. |

`[rotate=…]` and `[random=…]` also work in **Server host**, **Bug host** and **Remote proxy**. Bug host example: `[rotate=bug1.com;bug2.com;bug3.com]`.

### Examples

```
HEAD / HTTP/1.1[crlf]Host: [bug][crlf][crlf]PATCH / HTTP/1.1[crlf]Host: [host][crlf]Upgrade: websocket[crlf][crlf][split]HTTP/1.1[crlf][crlf]
```

WebSocket payload with a random key:

```
GET / HTTP/1.1[crlf]Host: [bug][crlf]Upgrade: websocket[crlf]Connection: Upgrade[crlf]Sec-WebSocket-Key: [ws_key][crlf]Sec-WebSocket-Version: 13[crlf][crlf]
```

Two payloads in turn:

```
GET / HTTP/1.1[crlf]Host: [bug][crlf]Upgrade: websocket[crlf][crlf][rotate]CONNECT [host_port] HTTP/1.1[crlf]Host: [bug][crlf][crlf]
```

## 17. CPU and RAM

Measured on a King3399 (RK3399, 6 cores, 2 GB) running ImmortalWrt in TUN mode, Tomato Tunnel 1.7.1, 25 November 2026. 100 % CPU means one core fully busy. Load: 8 downloads or uploads at once from a PC on the LAN; CPU and RAM of `tomato-tunnel`, `xray` and `hev-socks5-tunnel` together, read every second from `/proc`.

| Situation | CPU idle | RAM idle | CPU download | CPU upload |
|---|---|---|---|---|
| Manual, SSH + TLS | 0.7 % | 20 MB | 139 % at 117 Mbps | 41 % at 59 Mbps |
| Manual, Xray Trojan WS TLS | 1.3 % | 70 MB | 113 % at 90 Mbps | 37 % at 59 Mbps |
| Balance, 2 Xray accounts (modem + phone) | 1.8 % | 100 MB | 162 % at 122 Mbps | 90 % at 120 Mbps |

- In **Balance** mode all traffic goes through `tomato-tunnel` first so it can be shared, so its CPU use is higher than in the other modes.
- RAM: each **Xray** account in Balance adds about **40 MB**, because it runs its own Xray process. **SSH** and **ZiVPN** accounts add only a few MB.
- On slower devices (S905X STBs and similar), Balance at 80–100 Mbps can use 1.5–2 cores. On routers with 256 MB of RAM or less, keep Balance to 2–3 Xray accounts.

## 18. Common problems

**A Balance account on the second modem fails with "network unreachable".**
The interfaces have the same metric, or "Use default gateway" is off. See [Network setup](#network-setup-needed-with-more-than-one-modem).

**My own banner image does not show.**
Fixed in 1.6.0. After the update the image is copied again with the right permissions.

**The speed test does not show the added-up speed.**
Use a multi-connection speed test (speedtest.net in Multi mode, fast.com), and keep **Balance: same account per site** off.

**One account keeps failing to log in.**
Its card shows "Login failed": the account is locked, expired, or the password is wrong. It clears after a successful login.

**An old SSH server (Dropbear) keeps dropping.**
Set **SSH compatibility** to Auto (default) or Legacy.

**"Could not save: … uci/commit … Access denied" (often on OpenWrt 24.10).**
Another LuCI change (for example in Network) was applied and is still waiting for its confirmation, or your LuCI login has expired. Since 1.7.1 Save waits until that change is confirmed or rolled back and tells you when the login has expired. On older versions: confirm or revert the pending change (top right of LuCI), or log out and in again, then press Save.

**WhatsApp (or other) notifications arrive late.**
Before 1.7.1 the tunnel closed a TCP connection after 5 idle minutes (hev and Xray defaults). Push channels such as Google FCM send a heartbeat only every 15 to 28 minutes, so the phone noticed only at its next heartbeat. Since 1.7.1 idle TCP connections are kept for 1 hour, and Xray WebSocket accounts send a small ping every 30 seconds, because bug hosts and CDNs in front of the server close a quiet WebSocket after about 90 seconds (measured: before 1.7.1 an idle connection died between 75 and 100 s, with 1.7.1 it was still alive after 7 minutes). A switch of account, Balance on/off or a reconnect still closes every connection once; the phone reconnects at its next heartbeat.

**OpenClash or PassWall conflicts.**
Do not run both in TUN mode. Set Tomato Tunnel to **SOCKS5 only**.

## 19. Version history

| Version | Highlights |
|---|---|
| 1.0.0 | SSH client for OpenWrt like HTTP Custom: Payload, TLS/SNI, WebSocket, Enhanced, SlowDNS |
| 1.1.0 | Dashboard, live profile switch, Manual / Fallback / Auto, ZiVPN accounts |
| 1.2.0 | Usage tab, Change IP, account expiry, new layout, 11 architectures |
| 1.2.1 | New logo |
| 1.3.0 | Xray accounts and the Xray add-on, Show on Dashboard, Indonesian IPs direct |
| 1.3.1 | Only the Tomato Tunnel Xray build is used |
| 1.4.0 | Telegram bot, Dashboard chart kept after reload |
| 1.5.0 | Fix drops on old Dropbear, Xray TUN without the extra hop, benchmark |
| 1.6.0 | Several modems, IP ranges for Change IP, Go out through per account, Balance mode (weight, UDP per flow, fastest main account), new payload tags (`[ip]`, `[proxy]`, `[ws_key]`, rotate/random, `*N`), banner as the header on every tab, banner upload fix |
| 1.7.0 | New look on every tab that follows the LuCI theme, Connections tab, real ping with unified delay, Fallback goes back to the chosen account, Block QUIC, Direct domains for every account type, Schedule, account type colours, imported accounts saved at once |
| 1.7.1 | Schedule: turn the tunnel off or on in a time window (for example 01:00–06:00), also while it is stopped and after a reboot; DNS through the tunnel about 5× faster (kept connections, ~45 ms instead of ~220 ms); Balance: accounts on a modem that goes down are closed within ~5 s (downloads move to the other modem); idle TCP connections kept for 1 hour instead of 5 minutes and a WebSocket ping every 30 s for Xray WS (late WhatsApp notifications); Balance: new connections take turns by weight (idle connections no longer steer them), the share follows the weight only and the main account is no longer changed by the traffic it carries (it could make the slower account the main one); leftover Xray processes (used by no account) are stopped automatically; the banner no longer fills the screen while the page loads; fix "Could not save: Access denied" (OpenWrt 24.10): Save waits while another LuCI Apply is pending and says when the login has expired; a failed save no longer restarts or switches the tunnel; bot pairing no longer wipes unsaved edits; invalid Bypass lines are skipped; SlowDNS: choosing "Custom…" as DNS resolver no longer jumps back to 1.1.1.1; Connections: per-account filter boxes, one card per connection on phones; "Saved…" notices close by themselves after a few seconds (warnings and errors stay) |

---

Made by [@dudulrealnofek](https://t.me/dudulrealnofek). Group: [@tfnuklir](https://t.me/tfnuklir).
