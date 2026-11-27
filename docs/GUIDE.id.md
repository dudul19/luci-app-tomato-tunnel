# Panduan Tomato Tunnel

[English](GUIDE.md) · **Bahasa Indonesia**

Semua fitur sampai **versi 1.7.2**, singkat saja. Nama menu ditulis dalam bahasa Inggris seperti di LuCI.

## Daftar isi

1. [Apa itu](#1-apa-itu)
2. [Kebutuhan](#2-kebutuhan)
3. [Instalasi](#3-instalasi)
4. [Mulai cepat](#4-mulai-cepat)
5. [Jenis akun](#5-jenis-akun)
6. [Tab Accounts](#6-tab-accounts)
7. [Tab Dashboard](#7-tab-dashboard)
8. [Mode: Manual, Fallback, Auto, Balance](#8-mode-manual-fallback-auto-balance)
9. [Balance](#9-balance)
10. [Change IP](#10-change-ip)
11. [Tab Usage](#11-tab-usage)
12. [Settings](#12-settings)
13. [Bot Telegram](#13-bot-telegram)
14. [Tab Log](#14-tab-log)
15. [Update](#15-update)
16. [Tag payload](#16-tag-payload)
17. [CPU dan RAM](#17-cpu-dan-ram)
18. [Masalah umum](#18-masalah-umum)
19. [Riwayat versi](#19-riwayat-versi)

---

## 1. Apa itu

Aplikasi tunnel untuk OpenWrt dan ImmortalWrt, diatur lewat LuCI (**Services › Tomato Tunnel**) atau bot Telegram. Semua perangkat di router ikut lewat tunnel tanpa aplikasi tambahan.

- **SSH**: seperti HTTP Custom, dengan Payload, TLS/SNI, WebSocket, Enhanced, dan SlowDNS.
- **ZiVPN**: host, port, dan password.
- **Xray**: VLESS, VMess, Trojan, cukup tempel link.

## 2. Kebutuhan

- OpenWrt atau ImmortalWrt **23.05 atau lebih baru**.
- Arsitektur: `aarch64_generic`, `aarch64_cortex-a53`, `aarch64_cortex-a72`, `arm_cortex-a7`, `arm_cortex-a7_neon-vfpv4`, `arm_cortex-a9`, `arm_cortex-a9_vfpv3-d16`, `arm_cortex-a15_neon-vfpv4`, `mipsel_24kc`, `mips_24kc`, `x86_64`.
- Sekitar **13 MB** flash dan **64 MB** RAM kosong. Add-on Xray butuh sekitar **30 MB** flash lagi.

Cek arsitektur:

```sh
cat /etc/apk/arch        # OpenWrt 25.x dan lebih baru
opkg print-architecture  # OpenWrt 23.05 dan 24.10
```

## 3. Instalasi

1. Unduh file untuk router kamu dari **Releases**, salin ke `/tmp` di router.
2. Pasang:

   ```sh
   apk add --allow-untrusted /tmp/luci-app-tomato-tunnel_1.7.2_aarch64_generic.apk   # OpenWrt 25.x
   opkg install /tmp/luci-app-tomato-tunnel_1.7.2_aarch64_generic.ipk                # OpenWrt 23.05 / 24.10
   ```

3. Buka **Services › Tomato Tunnel**.

Akun Xray butuh **add-on Xray**: **Settings › About & update › Install Xray**, atau tombol di akun Xray.

> Kalau memakai OpenClash atau PassWall, jangan sama-sama mode TUN. Set Tomato Tunnel ke **SOCKS5 only** dan pakai `127.0.0.1:1080` di sana.

## 4. Mulai cepat

1. **Accounts › + Add account**, atau **Import** lalu tempel link atau pesan akun dari penjual.
2. **Test first**, lalu **Save & connect**.
3. **Dashboard** menampilkan **Connected** kalau LAN sudah lewat tunnel.

Supaya tersambung saat router menyala: **Settings › General › Start on boot**.

## 5. Jenis akun

### SSH

| Jenis | Fungsi |
|---|---|
| SSH Direct | SSH biasa |
| SSH + Payload | Kirim payload HTTP dulu |
| SSH + Payload + Enhanced | Payload dikirim per bagian di `[split]` |
| SSH + TLS | TLS dengan SNI, lalu SSH |
| SSH + Payload + TLS (+ Enhanced) | TLS, payload, lalu SSH |
| SSH + SlowDNS | SSH lewat DNS (dnstt) |
| SSH + Payload + SlowDNS | Payload lewat SlowDNS |

Kombinasi lain: **Custom combination**.

Kolom:

- **Bug host**: SNI TLS dan `[bug]` di payload.
- **Payload text**: lihat [bagian 16](#16-tag-payload).
- **Server host**, **Bug host**, **Remote proxy** bisa diisi `[rotate=a;b;c]` atau `[random=a;b;c]`.
- **[split] delay**: jeda antar bagian payload (default 1000 ms).
- **Remote proxy**: sambung lewat `ip:port`.
- **UDPGW port**: UDP untuk game dan telepon (default `7300`, `0` = mati).
- **Allow insecure**: lewati cek sertifikat (default nyala, biasa untuk bug host).
- **SlowDNS**: NS, public key, jenis record, resolver, ukuran EDNS0, dan KCP window.
  - **Record type**: Automatic mencoba tiap jenis. TXT, NULL, CNAME, HTTPS, SVCB, DNSKEY, A, AAAA, NS, MX, dan SRV. Sebagian DNS operator hanya meloloskan jenis tertentu; HTTPS membawa data paling banyak per jawaban, A dan AAAA paling sedikit.
  - **Resolver**: **Auto** = DNS yang diberikan operator ke modem atau HP yang dipakai akun ini (untuk operator yang memblokir DNS publik, seperti XL OPOK), Cloudflare DoH, atau isi sendiri (`1.2.3.4`, `1.2.3.4:53`, `https://…`, `dot:host:853`).
  - **EDNS0 size** (512–4096, bawaan 1400) dan **KCP window** (bawaan 512): biarkan, kecuali penjual server bilang lain.

Server Dropbear lama: **Settings › Network › SSH compatibility** (Auto sudah menanganinya).

### ZiVPN

Host, port UDP, password. TCP dan UDP.

### Xray

VLESS, VMess, atau Trojan lewat TCP, WebSocket, HTTPUpgrade, gRPC, atau XHTTP, dengan TLS atau REALITY. TCP dan UDP. Tempel link di **Import from link**. Butuh add-on Xray.

- **Over SlowDNS**: Xray lewat terowongan DNS (server Tomato 3.8+), dengan WebSocket, HTTPUpgrade, atau XHTTP. Kolom SlowDNS sama seperti SSH. Lambat, seperti SSH SlowDNS.
- **Payload**: request HTTP buatanmu dikirim dulu, lalu request WebSocket / HTTPUpgrade milik Xray sendiri. Untuk TCP, WebSocket, atau HTTPUpgrade dengan Security None atau TLS (bukan REALITY, gRPC, XHTTP, atau SlowDNS).
  - **Enhanced**: payload dikirim terpisah di `[split]`, semua balasan untuk payload dibuang sampai Xray dapat jawabannya (`101`). Tanpa Enhanced, balasan pertama harus sudah milik Xray.
  - **Send payload** (khusus TLS): **Inside TLS** (setelah handshake, biasanya) atau **Before TLS** (teks biasa, lalu TLS milik Xray).
  - **Bug host**, **Payload text** (tag sama seperti SSH, kosong = GET sederhana), **[split] delay**, **Remote proxy**, **User-Agent**.
  - Isi server asli di **Address** dan bug host di **Bug host** / **SNI**. Server tidak perlu pengaturan khusus.

### Kolom di semua akun

- **Go out through**: modem yang dipakai akun ini. **Auto** kalau satu modem; kalau lebih, pilih interface-nya (mis. `wan (wwan0)`, `hp (usb0)`).
- **Balance weight**: porsi di Balance, 1–5.
- **Expires on**: pengingat sebelum habis; akun kedaluwarsa dilewati.
- **Show on Dashboard**: mati = akun cadangan, tidak dipakai Fallback, Auto, atau Balance.

## 6. Tab Accounts

**+ Add account**, **Import**, **Share link**, **Export / Restore** (.json), dan per akun **Edit**, **Delete**, **Test**. Geser untuk mengurutkan; Fallback mengikuti urutan ini.

## 7. Tab Dashboard

- **Status**: kondisi, akun, lama tersambung, **Start / Restart / Stop / Change IP**.
- **Angka**: speed live, download, upload, ping, hari ini, IP publik.
- **Traffic**: 60 detik terakhir.
- **Accounts**: tombol mode, ikon petir = tes semua akun dengan login sungguhan. Klik kartu untuk pindah; tunnel lama tetap jalan sampai yang baru tersambung. Warna kartu: SSH hijau toska, SlowDNS ungu, ZiVPN oranye, VLESS biru, VMess nila, Trojan merah muda.
- **Modems**: IP dan Change IP per modem (kalau lebih dari satu).
- **Balance**: speed, porsi, ping, dan koneksi per akun (mode Balance).
- **Recent log**.

### Tab Connections

Koneksi yang sedang jalan: perangkat, situs, akun, speed, total, waktu. Saring dengan teks, atau klik kotak akun. Hanya dibuat selama tab dibuka.

## 8. Mode: Manual, Fallback, Auto, Balance

| Mode | Fungsi |
|---|---|
| **Manual** | Tetap di satu akun. |
| **Fallback** | Pindah ke akun berikutnya setelah 3 kali gagal. Kembali ke akun pilihan kalau sudah jalan lagi (dicek tiap 5 menit). |
| **Auto** | Pindah ke akun yang minimal 20 ms lebih cepat (tiap 5 menit). |
| **Balance** | Beberapa akun sekaligus berbagi traffic. |

## 9. Balance

Beberapa akun jalan bersamaan, bisa masing-masing di modem sendiri, dan koneksi LAN dibagi ke semuanya. Speed bertambah per koneksi.

### Beberapa modem

Setiap modem butuh interface sendiri (**Network › Interfaces**) dengan **Use default gateway** nyala dan **gateway metric berbeda** (mis. `wan` 10, `hp` 20). **Status › Routes** harus menampilkan satu `default` per modem.

### Langkah

1. Atur **Go out through** per akun (A → `wan`, B → `hp`).
2. Opsional: **Balance weight**, lebih tinggi untuk modem yang lebih cepat.
3. **Dashboard**: pilih **Balance**, klik kartu untuk menambah atau mengeluarkan akun (garis putus-putus = ikut Balance).
4. **Start**.

### Cara kerja

- Koneksi baru bergantian sesuai weight (weight 3 dan 1: A, A, B, A…).
- UDP dibagi per aliran. Akun yang membawa UDP: Xray, ZiVPN, SSH dengan UDPGW.
- YouTube tetap di satu akun, jadi video tidak error.
- Akun dengan ping terbaik jadi akun utama, tapi harus menang 3 kali berturut-turut, dan paling sering tiap 10 menit.
- Akun yang gagal 2 kali cek tidak diberi koneksi baru sampai menjawab lagi. Modem yang mati menutup akunnya dalam ~5 detik.
- Semua akun Xray memakai **satu proses Xray** (hemat ~40 MB RAM per akun tambahan).

**Balance: same account per site** (Settings › General): satu situs tetap di satu akun selama 10 menit. Untuk bank dan game; default mati.

### Perlu diketahui

- Satu download lewat satu koneksi hanya dapat speed satu akun. Speed test, YouTube, download manager, dan banyak perangkat dapat jumlahnya.
- Satu akun di dua modem terlihat dari dua IP oleh server; akun yang dibatasi satu IP bisa terkunci.
- Beberapa akun di satu modem hanya membantu kalau yang membatasi server atau akunnya, bukan modem.
- SlowDNS dapat porsi kecil; lebih cocok jadi cadangan.

## 10. Change IP

**Settings › Change IP** meminta IP baru ke operator, lalu tunnel tersambung ulang.

| Metode | Cara |
|---|---|
| Restart interface | `ifdown` / `ifup` (kebanyakan modem RNDIS/QMI) |
| Airplane mode (AT) | Perintah AT ke port modem (**Detect** mencarinya) |
| ModemManager | `mmcli` mati / nyala |
| HiLink / Orbit | Data mati / nyala lewat web API modem |
| HP Android (ADB) | Mode pesawat di HP yang di-tethering |

- **+ Add modem**: tiap modem punya metode, interface, jadwal, dan rentang IP sendiri.
- **After failed connects**: ganti IP otomatis setelah beberapa kali gagal tersambung.
- **Every (hours)**: ganti terjadwal, `0` = mati.
- **Keep trying until the IP is in**: ulangi sampai IP masuk rentang.

| Rentang | Artinya |
|---|---|
| `10.0` – `10.19` | 10.0.0.0 sampai 10.19.255.255 |
| `114.12.0.0/14` | 114.12.0.0 sampai 114.15.255.255 |
| `114.12.` | Semua IP berawalan 114.12. |

IP publik dan alamat di modem sama-sama dicek. Akhiri awalan dengan titik: `114.1.` hanya cocok untuk 114.1.x.

## 11. Tab Usage

Traffic per hari, bulan, akun, dan perangkat (perangkat butuh TUN). **Period starts on day** = tanggal paket diperpanjang. **Warn at (GB)** memberi peringatan kuota. Kalau perangkatnya banyak, hanya 8 teratas yang tampil; **Show all** membuka semuanya.

## 12. Settings

### General

| Pengaturan | Fungsi |
|---|---|
| Start on boot | Tersambung saat router menyala |
| Profile selection | Manual / Fallback / Auto / Balance |
| Fallback: go back | Kembali ke akun pilihan kalau sudah jalan lagi |
| Block QUIC (UDP 443) | **Auto** (default): nyala, kecuali ZiVPN. YouTube lebih stabil; game dan telepon tidak terpengaruh |
| Balance: same account per site | Lihat [bagian 9](#9-balance) |
| Warn before expiry | 1, 3, atau 7 hari |
| Connection check URL | Default `http://cp.cloudflare.com/generate_204` |
| Unified delay | Ping tanpa handshake, seperti OpenClash |
| Check every / Failures before reconnect | Default 5 detik / 3 |
| Parallel SSH sessions | 1–4 |

### Schedule

Setiap baris: **jam**, **hari**, **aksi**, nilai, **until**.

- **Use account** / **Set mode**: pindah di jam itu, kembali ke sebelumnya saat **until**. **Until** kosong = perubahannya tetap. Pilihan yang kamu ubah sendiri di tengah rentang tetap dipakai.
- **Tunnel on/off**: **Off** mematikan tunnel di rentang itu; **On only** menyalakan tunnel hanya di rentang itu. Jalan walau tunnel mati dan setelah reboot.
- Rentang boleh melewati tengah malam. Semuanya mengikuti jam router (**System › System**).

| Contoh | Time | Days | Action | Nilai |
|---|---|---|---|---|
| Akun malam | 00:00 | Every day | Use account | Akun malam until 06:00 |
| Mati saat tidur | 01:00 | Every day | Tunnel on/off | Off until 06:00 |
| Hanya jam kantor | 08:00 | Mon–Fri | Tunnel on/off | On only until 17:00 |
| Balance di malam hari | 19:00 | Every day | Set mode | Balance until 23:00 |

### Network

| Pengaturan | Fungsi |
|---|---|
| Traffic mode | **TUN** (disarankan), TCP redirect, atau SOCKS5 only |
| SOCKS5 port / listen | Default `127.0.0.1:1080` |
| Outgoing interface | Jalan keluar untuk akun yang Auto |
| DNS through the tunnel / DNS server | DNS lewat tunnel, default `1.1.1.1` |
| Manage dnsmasq | Arahkan dnsmasq ke DNS tunnel, dikembalikan saat Stop |
| Indonesian IPs go direct | Situs dan game lokal tidak lewat tunnel |
| Direct IPs / devices | Tidak lewat tunnel, satu per baris |
| Direct domains | Keluar langsung lewat modem (bank, game), subdomain ikut |
| Tailscale | Lihat di bawah |
| Block ads (Xray) | Daftar iklan kecil |
| SSH compatibility | Auto / Legacy / Modern |

**Tailscale** (kalau terpasang di router, mode TUN):

- Traffic-nya lewat tunnel, jadi tidak perlu kuota reguler.
- Perangkat LAN bisa membuka alamat 100.x.
- Perangkat Tailscale-mu bisa membuka LuCI lewat alamat 100.x router dari mana saja.
- Router bisa jadi exit node; traffic-nya juga lewat tunnel.
- Jalankan dengan `tailscale up --accept-dns=false`. Jangan pakai exit node di router itu sendiri.

### Telegram, Appearance, About & update

- **Telegram**: [bagian 13](#13-bot-telegram).
- **Appearance**: enam banner, gambar sendiri (JPG/PNG/WebP, ~1200×200, maks 500 KB), atau tanpa banner.
- **About & update**: [bagian 15](#15-update).

## 13. Bot Telegram

1. Buat bot di [@BotFather](https://t.me/BotFather), tempel token-nya.
2. **Enable bot**, simpan.
3. **Pair new admin**, kirim kodenya ke bot.

Bot bisa menampilkan status, pindah akun atau mode, Change IP, start / stop, usage, kelola akun, update, backup, reboot. Notifikasi: putus dan tersambung lagi, pindah akun, login gagal, akun hampir habis, IP berubah, perangkat baru, update, laporan harian. Setiap kelompok aksi bisa dimatikan. Tetap jalan walau tunnel mati.

## 14. Tab Log

Level (Info, Debug, Errors), auto-scroll, unduh, hapus.

## 15. Update

**Settings › About & update** memasang rilis baru dari GitHub (dicek SHA256, pengaturan tetap). Channel: Stable atau Beta. Cek harian hanya memberi pemberitahuan. Add-on Xray ikut diperbarui.

## 16. Tag payload

Untuk payload SSH dan Xray. Huruf besar/kecil sama saja. Tag yang tidak dikenal dikirim apa adanya. Di Xray, request milik Xray otomatis ditambahkan setelah payload: jangan pakai header `Upgrade` kecuali yakin server menjawabnya.

| Tag | Nilai |
|---|---|
| `[host]` `[port]` `[host_port]` | Host, port, `host:port` server |
| `[ip]` `[ip_port]` | IP server, `ip:port` |
| `[bug]` `[sni]` | Bug host (kosong: host server) |
| `[proxy]` `[proxy_host]` `[proxy_port]` | Remote proxy |
| `[method]` `[protocol]` `[ua]` | `CONNECT`, `HTTP/1.1`, User-Agent |
| `[raw]` | `CONNECT host:port HTTP/1.0` + baris kosong |
| `[netData]` | Sama, tanpa baris kosong |
| `[ws_key]` | `Sec-WebSocket-Key` acak |
| `[crlf]` `[lf]` `[cr]` `[lfcr]` | Ganti baris; ulangi dengan `[crlf*2]` |
| `[split]` | Kirim per bagian, jeda = **[split] delay** |
| `[delay]` / `[instant_split]` | Split dengan jeda 1,5 detik / tanpa jeda |
| `[rotate]` / `[random]` | Beberapa payload, bergiliran / acak |
| `[rotate=a;b;c]` / `[random=a;b;c]` | Satu nilai per sambungan (juga di host, bug host, proxy) |

Contoh:

```
GET / HTTP/1.1[crlf]Host: [bug][crlf]Upgrade: websocket[crlf]Connection: Upgrade[crlf]Sec-WebSocket-Key: [ws_key][crlf]Sec-WebSocket-Version: 13[crlf][crlf]
```

```
HEAD / HTTP/1.1[crlf]Host: [bug][crlf][crlf]PATCH / HTTP/1.1[crlf]Host: [host][crlf]Upgrade: websocket[crlf][crlf][split]HTTP/1.1[crlf][crlf]
```

## 17. CPU dan RAM

King3399 (RK3399, 2 GB), ImmortalWrt, mode TUN. 100 % = satu core.

| Kondisi | RAM diam | CPU speed penuh |
|---|---|---|
| Manual, SSH + TLS | ~20 MB | 139 % di 117 Mbps |
| Manual, Xray Trojan WS TLS | ~70 MB | 113 % di 90 Mbps |
| Balance, 2 akun Xray | ~57 MB (1.7.1: ~105 MB) | 162 % di 122 Mbps |

- Sejak 1.7.2 semua akun Xray di Balance memakai satu proses Xray. Akun SSH dan ZiVPN hanya menambah beberapa MB.
- Tailscale sendiri memakai sekitar 35–50 MB.
- Di perangkat lambat (S905X dan sejenisnya), Balance di 80–100 Mbps bisa memakai 1,5–2 core.

## 18. Masalah umum

**Modem kedua gagal "network unreachable"**: metric kedua interface sama, atau "Use default gateway" mati. Lihat [bagian 9](#9-balance).

**Speed test tidak bertambah**: pakai tes multi-koneksi, matikan "same account per site".

**"Login failed" di kartu**: akun terkunci, habis, atau password salah.

**Dropbear lama sering putus**: SSH compatibility ke Auto atau Legacy.

**"Another LuCI change is still being applied"**: halaman LuCI lain masih menunggu konfirmasi Apply. Tunggu sekitar 90 detik, atau konfirmasi / batalkan (pojok kanan atas LuCI). Kalau login habis, login ulang.

**YouTube "Terjadi masalah"**: biarkan Block QUIC di Auto.

**Bentrok dengan OpenClash atau PassWall**: jangan sama-sama mode TUN; set Tomato Tunnel ke SOCKS5 only.

**Tampilan masih lama setelah update**: refresh dengan Ctrl+Shift+R, atau hapus cache browser di HP.

**SlowDNS tersambung tapi tidak ada internet, atau sangat lambat**: coba resolver lain (Custom) dan jenis record HTTPS. A dan AAAA membawa sedikit data per jawaban, jadi paling lambat.

**Xray dengan payload: "server answered … not 101"**: nyalakan Enhanced, atau cek Bug host, Path, dan Host header.

## 19. Riwayat versi

| Versi | Isi utama |
|---|---|
| 1.0.0 | Klien SSH seperti HTTP Custom: Payload, TLS/SNI, WebSocket, Enhanced, SlowDNS |
| 1.1.0 | Dashboard, pindah akun langsung, Manual / Fallback / Auto, ZiVPN |
| 1.2.0 | Usage, Change IP, masa aktif akun, 11 arsitektur |
| 1.2.1 | Logo baru |
| 1.3.0 | Akun dan add-on Xray, Show on Dashboard, IP Indonesia langsung |
| 1.3.1 | Hanya Xray buatan Tomato Tunnel yang dipakai |
| 1.4.0 | Bot Telegram |
| 1.5.0 | Perbaikan Dropbear lama, Xray TUN lebih cepat |
| 1.6.0 | Multi modem, rentang IP, Go out through, Balance, tag payload baru, banner di semua tab |
| 1.7.0 | Tampilan baru, tab Connections, ping sungguhan, Fallback kembali, Block QUIC, Direct domains, Schedule |
| 1.7.1 | Jadwal tunnel on/off, DNS lebih cepat, notifikasi tidak telat lagi, Balance sesuai weight, perbaikan simpan di OpenWrt 24.10 |
| 1.7.3 | Xray lewat SlowDNS, payload untuk Xray (Enhanced, di dalam / sebelum TLS), jenis record SlowDNS baru HTTPS, SVCB, DNSKEY, A, dan AAAA (juga jaringan DNS64), resolver Auto (DNS operator), pengaturan EDNS0 dan KCP window, A/AAAA lebih stabil, Stop tidak macet lagi saat SlowDNS tersendat, perbaikan tata letak tombol di HP |
| 1.7.2 | Satu proses Xray untuk Balance (RAM sekitar separuh), perbaikan YouTube (Block QUIC Auto, YouTube di satu akun), akun utama Balance lebih stabil, Schedule "until", Tailscale lewat tunnel + LuCI lewat Tailscale, Usage "Show all", perbaikan tampilan HP, teks lebih singkat, halaman lebih cepat terbuka |

---

Dibuat oleh [@dudulrealnofek](https://t.me/dudulrealnofek). Grup: [@tfnuklir](https://t.me/tfnuklir).
