# Panduan Tomato Tunnel

[English](GUIDE.md) · **Bahasa Indonesia**

Panduan lengkap semua fitur Tomato Tunnel sampai **versi 1.6.0**: apa fungsinya, di mana tempatnya, dan cara memakainya.

Nama menu dan tombol ditulis dalam bahasa Inggris, sama seperti yang tampil di LuCI.

## Daftar isi

1. [Apa itu Tomato Tunnel](#1-apa-itu-tomato-tunnel)
2. [Kebutuhan](#2-kebutuhan)
3. [Instalasi](#3-instalasi)
4. [Mulai cepat](#4-mulai-cepat)
5. [Jenis akun](#5-jenis-akun)
6. [Tab Accounts](#6-tab-accounts)
7. [Tab Dashboard](#7-tab-dashboard)
8. [Pemilihan profil: Manual, Fallback, Auto, Balance](#8-pemilihan-profil-manual-fallback-auto-balance)
9. [Balance: menggabungkan speed beberapa modem](#9-balance-menggabungkan-speed-beberapa-modem)
10. [Change IP dan multi modem](#10-change-ip-dan-multi-modem)
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

## 1. Apa itu Tomato Tunnel

Tomato Tunnel adalah aplikasi tunnel untuk router OpenWrt dan ImmortalWrt. Diatur lewat halaman LuCI (**Services › Tomato Tunnel**) atau bot Telegram.

- **SSH** bekerja seperti HTTP Custom: SSH sebagai dasar, lalu tambahkan lapisan yang dibutuhkan (Payload, TLS/SNI, WebSocket, Enhanced, SlowDNS).
- **ZiVPN** bekerja seperti aplikasi ZiVPN: cukup host, port, dan password.
- **Xray** (VLESS, VMess, Trojan) cukup ditambahkan dengan menempel link akun.

Semua perangkat yang tersambung ke router (HP, laptop, TV) ikut lewat tunnel tanpa perlu aplikasi apa pun di perangkatnya.

## 2. Kebutuhan

- OpenWrt atau ImmortalWrt **23.05 atau lebih baru**.
- Arsitektur yang didukung: `aarch64_generic`, `aarch64_cortex-a53`, `aarch64_cortex-a72`, `arm_cortex-a7`, `arm_cortex-a7_neon-vfpv4`, `arm_cortex-a9`, `arm_cortex-a9_vfpv3-d16`, `arm_cortex-a15_neon-vfpv4`, `mipsel_24kc`, `mips_24kc`, `x86_64`.
- Sekitar **13 MB** flash kosong dan **64 MB** RAM kosong.
- Add-on Xray (opsional, hanya untuk akun Xray) butuh sekitar **30 MB** flash tambahan.
- Paket `kmod-tun ip-full ca-bundle luci-base` terpasang otomatis dari repo resmi.

Cara mengecek arsitektur router:

```sh
cat /etc/apk/arch        # OpenWrt 25.x dan lebih baru
opkg print-architecture  # OpenWrt 23.05 dan 24.10
```

## 3. Instalasi

1. Unduh file untuk router kamu dari halaman **Releases** di GitHub, lalu salin ke `/tmp` di router.
2. Pasang:

   ```sh
   # OpenWrt 25.x dan lebih baru (apk)
   apk add --allow-untrusted /tmp/luci-app-tomato-tunnel_1.6.0_aarch64_generic.apk

   # OpenWrt 23.05 dan 24.10 (opkg)
   opkg install /tmp/luci-app-tomato-tunnel_1.6.0_aarch64_generic.ipk
   ```

3. Buka LuCI, lalu **Services › Tomato Tunnel**.

**Add-on Xray** hanya perlu untuk akun Xray. Cara paling mudah: **Settings › About & update › Xray add-on › Install Xray**, atau tekan tombol **Install Xray** di form akun Xray. Paket yang cocok diunduh otomatis dan dicek SHA256-nya.

> Jika memakai OpenClash atau PassWall, jangan jalankan dua-duanya dalam mode TUN bersamaan. Set Tomato Tunnel ke **SOCKS5 only**, lalu pakai `127.0.0.1:1080` sebagai proxy di OpenClash atau PassWall.

## 4. Mulai cepat

1. Buka tab **Accounts**, lalu **+ Add account**. Bisa juga **Import** dengan menempel link `vless://`, `vmess://`, `trojan://`, `tomatotunnel://`, atau teks akun dari penjual.
2. Isi data akun, lalu tekan **Test first** untuk mengetes tanpa mengganggu koneksi yang sedang jalan.
3. Tekan **Save & connect**.
4. Buka tab **Dashboard**. Jika status **Connected** berwarna hijau, semua perangkat di LAN sudah lewat tunnel.

Supaya tunnel menyala sendiri setelah router restart, aktifkan **Settings › General › Start on boot**.

## 5. Jenis akun

### SSH

| Jenis | Fungsi |
|---|---|
| SSH Direct | SSH biasa ke server |
| SSH + Payload | Mengirim payload HTTP dulu (WebSocket, CONNECT, dll.) |
| SSH + Payload + Enhanced | Payload dikirim terpisah-pisah di setiap `[split]` |
| SSH + TLS | TLS dengan SNI pilihan, lalu SSH |
| SSH + Payload + TLS | TLS dengan SNI, lalu payload, lalu SSH |
| SSH + Payload + TLS + Enhanced | Sama, dengan payload terpisah |
| SSH + SlowDNS | SSH lewat DNS (dnstt) |
| SSH + Payload + SlowDNS | Payload dikirim lewat jalur SlowDNS |

Kombinasi lain bisa dibuat lewat pilihan **Custom combination** di form akun.

Kolom penting di form SSH:

- **Bug host**: dipakai sebagai SNI TLS dan sebagai `[bug]` di payload.
- **Payload text**: isi payload. Daftar tag ada di [bagian 16](#16-tag-payload).
- **Server host**, **Bug host**, dan **Remote proxy** bisa berisi `[rotate=a;b;c]` atau `[random=a;b;c]` untuk berganti nilai di setiap koneksi.
- **[split] delay (ms)**: jeda antar potongan payload (default 1000 ms).
- **Remote proxy**: konek lewat proxy `ip:port` ini, bukan langsung ke server.
- **User-Agent [ua]**: nilai untuk tag `[ua]`.
- **UDPGW port**: untuk UDP (game, call). Default `7300`. Isi `0` untuk mematikan.
- **Allow insecure**: tidak memeriksa sertifikat TLS. Biasanya perlu dinyalakan untuk trik bug host / SNI. Default menyala.
- Untuk **SlowDNS**: Nameserver (NS), Public key, Record type (Automatic, TXT, NULL, CNAME, NS, MX, SRV), dan DNS resolver.

**Kompatibilitas Dropbear lama.** Di **Settings › Network › SSH compatibility**, pilihan **Auto** mencoba algoritma modern dulu, lalu turun ke `diffie-hellman-group1/14-sha1`, `ssh-rsa`, `aes128-cbc`, dan `3des-cbc` untuk server Dropbear lama. Tersedia juga **Legacy** (Dropbear lama dulu) dan **Modern only**.

Balasan HTTP dari server depan dilewati sampai server SSH menjawab. Banner server tampil di log saat login.

### ZiVPN

Protokol aplikasi ZiVPN (Hysteria2 di atas QUIC). Cukup isi host, port UDP, dan password. Membawa TCP dan UDP.

### Xray

VLESS, VMess, atau Trojan lewat TCP, WebSocket, HTTPUpgrade, gRPC, atau XHTTP, dengan TLS atau REALITY (termasuk Vision). Membawa TCP dan UDP.

- Paling mudah: tempel link akun di **Import from link**, semua kolom terisi otomatis.
- Butuh **add-on Xray** (lihat [Instalasi](#3-instalasi)).
- Build Xray bawaan Tomato Tunnel tetap mendukung `allowInsecure`, yang sudah dihapus di Xray resmi.
- **Direct domains (Xray)** dan **Block ads (Xray)** ada di Settings › Network.

### Kolom yang sama di semua jenis akun

- **Go out through** *(baru di 1.6.0)*: modem atau interface yang selalu dipakai akun ini. **Auto** mengikuti route router (termasuk mwan3). Lihat [bagian 9](#9-balance-menggabungkan-speed-beberapa-modem).
  - **Satu modem:** cukup pilih **Auto**, apa pun nama interface-nya (`wan`, `wwan`, `eth1`, `usb0`, dan lain-lain).
  - **Lebih dari satu modem:** pilih interface sesuai namanya di Network › Interfaces. Daftar menampilkan nama dan device aslinya, misalnya `wan (wwan0)` atau `hp (usb0)`. Namanya bebas, yang penting interface itu memang terhubung ke modem yang dimaksud.
- **Balance weight** *(baru di 1.6.0)*: jatah koneksi di mode Balance, 1–5.
- **Expires on**: tanggal habis akun. Ada pengingat sebelum habis, dan akun yang sudah habis dilewati oleh Auto, Fallback, dan Balance.
- **Show on Dashboard**: kalau dimatikan, akun jadi cadangan. Akun ini tidak dipakai Fallback, Auto, atau Balance dan tidak punya kartu di Dashboard, tapi tetap bisa dipakai lewat Save & connect; cukup tekan **Save** untuk menyimpan perubahannya.

## 6. Tab Accounts

- **+ Add account**: membuat akun baru.
- **Import**: menempel link akun atau teks pesan akun dari penjual. Akun hasil import langsung tersimpan *(1.7.0)*.
- **Share link**: membagikan satu akun sebagai link.
- **Backup / restore**: **Export all accounts (.json)** dan **Restore from file**.
- **Edit**, **Delete**, **Test**: per akun.
- **Urutan akun** bisa diubah dengan cara diseret. Urutan ini dipakai oleh Fallback.
- Garis di kiri tiap akun menunjukkan jenisnya (warna sama seperti di Dashboard) *(1.7.0)*.

## 7. Tab Dashboard

Tampilan baru di 1.7.0: setiap bagian berupa baris dengan judul di kiri, dan warna, tombol, serta kartunya ikut tema LuCI, jadi cocok di tema apa pun.

- **Status**: Connected / Connecting / Stopped, nama akun, jenis koneksi, mode trafik, dan lama tersambung, dengan tombol **Start**, **Restart**, **Stop**, dan **Change IP** (jika diaktifkan).
- **Angka status**: speed download live (besar) beserta upload, lalu **Downloaded, Uploaded, Ping, Today**, dan satu baris berisi IP publik, ISP, serta jumlah reconnect hari ini.
- **Traffic**: grafik 60 detik, disimpan oleh daemon jadi tetap ada setelah halaman di-reload. Saat koneksi putus, grafik menampilkan celah.
- **Accounts**: tombol mode (Manual, Fallback, Auto, Balance) dan tes latency (ikon petir) di atas kartu akun. Kartu tampil sampai tiga kolom. Garis di kiri kartu menunjukkan jenis akun: SSH teal, SlowDNS ungu, ZiVPN oranye, VLESS biru, VMess indigo, Trojan pink. Klik kartu untuk pindah akun **tanpa Stop/Start**: tunnel lama tetap jalan sampai akun baru tersambung, dan kalau gagal tidak ada yang berubah. Akun dengan **Show on Dashboard** mati tidak punya kartu, termasuk saat sedang jalan.
- **Ping di kartu** *(1.7.0)*: tes sungguhan. Setiap akun login lalu membuka URL cek lewat dirinya sendiri, jadi akun yang mati, terkunci, atau expired tampil **failed** walaupun servernya masih menjawab. Akun yang sedang jalan memakai ping tunnel yang aktif. Dengan **Unified delay** (nyala secara default, seperti OpenClash), waktu handshake tidak dihitung, hanya waktu bolak-balik, jadi angkanya lebih kecil dan adil antara SSH dan Xray.
- **Modems** *(jika lebih dari satu modem)*: IP tiap modem dan tombol Change IP per modem.
- **Balance** *(hanya mode Balance)*: baris sendiri berisi speed, pembagian trafik, ping, dan jumlah koneksi tiap akun.
- **Recent log**: log terbaru.

### Tab Connections *(1.7.0)*

Semua koneksi yang sedang lewat tunnel, yang tercepat di atas, diperbarui setiap 2 detik: **Device** (nama dari DHCP), **Site** (nama dari DNS tunnel), **Account** yang dipakai, **Speed**, **Total**, dan **Time**. Bisa difilter berdasarkan perangkat, situs, atau akun; kolom kiri menghitung koneksi per akun. Daftar hanya dibuat selama tab ini dibuka, jadi tidak membebani router di luar itu. Kalau akun Xray jalan sendiri (bukan Balance), TUN langsung masuk ke Xray: perangkat dan situs tetap tampil, speed dan total tidak.

## 8. Pemilihan profil: Manual, Fallback, Auto, Balance

Dipilih di **Dashboard › Profiles** atau **Settings › General › Profile selection**.

| Mode | Cara kerja |
|---|---|
| **Manual** | Tetap di profil yang dipilih. |
| **Fallback** | Pindah ke profil berikutnya setelah 3 kali gagal reconnect. Dengan *Fallback: go back to the chosen account* (nyala secara default, 1.7.0), tunnel kembali ke akun pilihanmu begitu login sungguhan berhasil lagi: dicek setiap 5 menit, minimal 3 menit setelah pindah. |
| **Auto** | Pindah ke profil yang minimal 20 ms lebih cepat (dicek setiap 5 menit). |
| **Balance** *(1.6.0)* | Beberapa akun jalan bersamaan dan trafik dibagi. Lihat bagian berikutnya. |

## 9. Balance: menggabungkan speed beberapa modem

*Baru di 1.6.0.*

Balance menjalankan beberapa akun **bersamaan**, masing-masing lewat modemnya sendiri, lalu membagi koneksi dari perangkat LAN ke akun-akun itu. Hasilnya, speed beberapa modem bisa tergabung (1 + 1 ≈ 2).

### Persiapan network (wajib untuk lebih dari satu modem)

Setiap modem harus punya interface sendiri di **Network › Interfaces**, misalnya `wan` untuk modem dan `hp` untuk USB tethering HP. Di **Edit › Advanced Settings** tiap interface:

- **Use default gateway**: tetap dicentang.
- **Use gateway metric**: angka **berbeda** di tiap interface, misalnya `wan` = `10` dan `hp` = `20`.

Cek di **Status › Routes**: harus ada satu baris `default` untuk setiap modem. Kalau metric-nya sama, router hanya memasang satu default route, dan akun yang diarahkan ke modem lain akan gagal dengan error *network unreachable*.

### Langkah

1. Di setiap akun, isi **Go out through** dengan modemnya, misalnya akun A → `wan`, akun B → `hp`.
2. (Opsional) Isi **Balance weight**. Modem yang lebih cepat diberi angka lebih besar, misalnya modem 4G+ = 2 dan HP 4G = 1.
3. Di **Dashboard › Profiles**, pilih **Balance**. Klik kartu akun untuk memasukkan atau mengeluarkan akun dari Balance. Kartu yang ikut ditandai garis putus-putus.
4. Tekan **Start**.

### Cara pembagian

- Setiap koneksi baru diberikan ke akun dengan koneksi paling sedikit, dibanding jatahnya (**weight**).
- **Speed nyata tiap akun diukur.** Akun yang jauh lebih lambat (misalnya SlowDNS) otomatis mendapat lebih sedikit koneksi, tapi minimal 30% dari jatah weight-nya. Saat trafik sepi, pengukuran ini diabaikan.
- **UDP** (YouTube QUIC, game, call) juga dibagi per aliran. Setiap aliran tetap di satu akun sampai selesai. Akun yang bisa membawa UDP: Xray, ZiVPN, dan SSH yang punya UDPGW.
- **Akun utama otomatis yang tercepat.** Setiap 30 detik Tomato Tunnel mengecek. Saat ramai, akun dengan speed minimal 1,5× akun utama jadi utama. Saat sepi, akun dengan ping 150 ms lebih rendah jadi utama. Kalau akun utama gagal ping, langsung diganti. Perpindahan biasa paling cepat sekali per 3 menit, dan sesi yang sudah jalan dipakai ulang tanpa reconnect.
- **Kalau satu akun mati**: setelah satu kali gagal ping, akun itu berhenti menerima koneksi baru. Setelah 3 kali gagal, akun dilepas dan dicoba sambung ulang setiap 10–60 detik. Begitu hidup lagi, otomatis ikut dibagi lagi. Kalau modemnya yang mati, semua akun di modem itu ikut mati, dan trafik pindah ke modem lain.

### Pengaturan tambahan

**Settings › General › Balance: same account per site**

- **Mati** (default, untuk menggabungkan speed): koneksi ke situs yang sama pun dibagi ke semua akun.
- **Nyala**: satu situs tetap di satu akun selama 10 menit. Cocok untuk aplikasi bank atau game yang tidak suka IP berganti, tapi speed per situs jadi hanya satu modem.

### Yang perlu diketahui

- Speed digabung **per koneksi**. Speedtest multi-thread, YouTube, download manager, atau beberapa perangkat sekaligus bisa mendekati jumlah semua modem. Satu download yang hanya memakai satu koneksi tetap mendapat speed satu akun.
- Satu akun yang dipakai lewat dua modem terlihat dari dua IP di server. Akun dengan limit 1 IP bisa terkunci, jadi lebih aman memakai satu akun per modem.
- Dua akun di satu modem bisa dipakai. Ini menambah speed hanya kalau yang membatasi adalah server atau akunnya, bukan modemnya. Selain itu berguna sebagai cadangan.
- SlowDNS boleh ikut, tapi jatahnya akan kecil karena lambat. Lebih baik dipakai sebagai cadangan saja.

### Satu modem, banyak akun

Balance juga bisa dipakai dengan satu modem saja. Semua akun cukup diisi **Go out through = Auto**, tanpa perlu setting metric.

- **Speed bertambah** kalau yang membatasi adalah server atau akun: server membatasi speed per akun, sedang padat di jam ramai, atau lokasinya jauh.
- **Speed tidak bertambah** kalau modemnya sendiri sudah mentok. Akun tambahan hanya membagi kapasitas yang sama.
- **Yang tetap didapat:** cadangan tanpa putus (kalau satu server down, trafik langsung pindah tanpa jeda reconnect), akun utama otomatis yang tercepat, dan beban tersebar ke beberapa server.
- **Harganya:** sekitar 40 MB RAM per akun Xray, dan CPU sedikit lebih tinggi.

Cara mengetahui berguna atau tidak: jalankan speedtest multi-thread di mode **Manual** dengan satu akun, lalu di mode **Balance** dengan semua akun, sebaiknya juga di jam ramai. Kalau hasil Balance jelas lebih tinggi, Balance berguna di tempatmu.

## 10. Change IP dan multi modem

**Settings › Change IP**. Change IP meminta IP baru dari operator. Tunnel menunggu, lalu reconnect dengan IP baru. Prosesnya bisa dilihat di Log.

### Metode

| Metode | Cara kerja |
|---|---|
| **Restart interface** | `ifdown` / `ifup` interface WAN. Cocok untuk modem mode RNDIS/QMI. |
| **Airplane mode (AT command)** | Mengirim perintah AT ke port serial modem: radio mati, tunggu, radio nyala. Tombol **Detect** mencari port. |
| **ModemManager** | Mematikan dan menyalakan modem lewat `mmcli`. Butuh paket `modemmanager`. |
| **HiLink / Orbit modem** | Mematikan dan menyalakan data lewat API web modem (Huawei HiLink, Telkomsel Orbit). |
| **Android phone (ADB)** | Mode pesawat di HP yang di-tethering (USB debugging nyala). Butuh paket `adb`. |

### Multi modem *(1.6.0)*

- **+ Add modem** menambah modem. Setiap modem punya nama, metode, interface, jadwal, dan range IP sendiri. Setting lama otomatis menjadi Modem 1.
- **When to change automatically**:
  - **After failed connects**: ganti IP setelah tunnel gagal tersambung beberapa kali berturut-turut.
  - **Which modem** (jika lebih dari satu modem): **Auto** (modem yang dipakai tunnel), **All modems, one after another**, atau satu modem tertentu.
  - **Buttons on the Dashboard**: satu tombol Change IP per modem.
- **Every (hours)** per modem: ganti IP terjadwal. Isi `0` untuk mematikan.
- Tunnel hanya reconnect kalau modem yang diganti IP-nya adalah modem yang dipakai tunnel.

### Range IP *(1.6.0)*

Di **Keep trying until the IP is in**, Change IP diulang sampai IP masuk salah satu range (maksimal **Max tries** kali). Format yang diterima:

| Isi | Artinya |
|---|---|
| `10.0` – `10.19` | 10.0.0.0 sampai 10.19.255.255 (bentuk pendek, bagian yang kosong diisi otomatis) |
| `10.130` – `10.159` | 10.130.0.0 sampai 10.159.255.255 |
| `114.12.0.0/14` | Blok CIDR: 114.12.0.0 sampai 114.15.255.255 |
| `114.12.` | Semua IP yang diawali 114.12. |

Range bisa lebih dari satu. Kolom **Means** langsung menjelaskan arti tiap baris. Yang dicek adalah **IP publik** dan juga **IP di interface modem**, karena operator sering memberi IP privat (CGNAT) seperti 10.x yang hanya terlihat di modem.

Tips: tulis prefix dengan titik di akhir. `114.1` juga cocok dengan 114.10.x sampai 114.19x.x, sedangkan `114.1.` hanya cocok dengan 114.1.x.

## 11. Tab Usage

- Trafik **per hari**, **per akun**, dan **per perangkat** (mode TUN).
- Total **bulanan**, dengan tanggal awal periode bisa diatur (**Period starts on day**, misalnya tanggal perpanjangan paket).
- **Warn at (GB per period)**: peringatan kuota di Dashboard.
- **Reset usage**: menghapus statistik.

## 12. Settings

### General

| Pengaturan | Fungsi |
|---|---|
| Start on boot | Konek otomatis saat router menyala |
| Profile selection | Manual / Fallback / Auto / Balance |
| Fallback: go back to the chosen account *(1.7.0)* | Lihat [bagian 8](#8-pemilihan-profil-manual-fallback-auto-balance). Nyala secara default |
| Block QUIC (UDP 443) *(1.7.0)* | Browser dan aplikasi memakai TCP, bukan QUIC/HTTP3. Bisa membuat video lebih stabil kalau UDP lewat UDPGW atau Xray di atas TCP. Game, telepon, dan DNS tidak terpengaruh. Mati secara default |
| Balance: same account per site | Lihat [bagian 9](#9-balance-menggabungkan-speed-beberapa-modem) |
| Warn before expiry | Pengingat 1, 3, atau 7 hari sebelum akun habis |
| Connection check URL | URL untuk cek koneksi (default `http://cp.cloudflare.com/generate_204`) |
| Unified delay *(1.7.0)* | Ping tanpa waktu handshake, seperti OpenClash. Nyala secara default |
| Check every (seconds) | Jarak antar cek (default 5 detik) |
| Failures before reconnect | Berapa kali cek gagal sebelum reconnect (default 3) |
| Parallel SSH sessions | 1–4 sesi SSH sekaligus untuk satu akun |

### Change IP

Lihat [bagian 10](#10-change-ip-dan-multi-modem).

### Schedule *(1.7.0)*

Pada jam tertentu tunnel memakai akun lain atau mode lain, sama seperti kalau kamu klik sendiri. Setiap baris berisi **Time**, **Days** (setiap hari, Senin–Jumat, Sabtu–Minggu), **Action** (*Use account* atau *Set mode*), akun atau modenya, dan **On / Off**. Tekan **+ Add**, lalu **Save**; perubahan berlaku tanpa restart. Contoh: 00:00 pakai akun kuota malam, 06:00 kembali ke akun utama. Jadwal hanya jalan selama tunnel menyala, memakai jam router.

### Network

| Pengaturan | Fungsi |
|---|---|
| Traffic mode | **TUN** (disarankan, TCP + UDP), **TCP redirect**, atau **SOCKS5 only** (untuk OpenClash / PassWall) |
| SOCKS5 port / listen | Port dan alamat SOCKS5 (default `127.0.0.1:1080`) |
| Outgoing interface | Interface keluar untuk semua akun yang Go out through-nya Auto. Auto = route biasa. |
| DNS through the tunnel | DNS lewat tunnel (DNS over TCP) atau DNS ISP |
| DNS server | Server DNS di ujung tunnel (default `1.1.1.1`) |
| Manage dnsmasq | dnsmasq diarahkan ke DNS tunnel saat tersambung, dan dikembalikan saat Stop |
| Indonesian IPs go direct | Situs dan game lokal lewat jalur biasa, tidak lewat tunnel (daftar APNIC Indonesia). Berlaku untuk semua jenis akun. |
| Direct IPs / devices | IP, CIDR, atau perangkat yang tidak lewat tunnel, satu per baris |
| Direct domains | Domain yang keluar langsung lewat modem, misalnya aplikasi bank dan game yang memblokir IP VPN. Satu per baris, subdomain ikut. Berlaku untuk semua jenis akun *(1.7.0)*; SSH, SlowDNS, dan ZiVPN butuh DNS lewat tunnel, dan aplikasinya mungkin perlu dibuka ulang setelah domain ditambahkan |
| Block ads (Xray) | Daftar kecil pemblokir iklan dan tracker |
| SSH compatibility | Auto / Legacy / Modern only |

### Telegram

Lihat [bagian 13](#13-bot-telegram).

### Appearance

- Banner adalah header halaman Tomato Tunnel dan tampil di semua tab, berisi logo, daftar protokol, versi, dan pembuat *(1.6.0)*.
- Enam banner bawaan (Tomato, Night Sky, Forest, Ocean, Minimal, Dusk City), **Your own image**, atau **No banner**. No banner berarti header polos dengan warna kartu tema, tanpa gambar.
- Gambar sendiri: JPG, PNG, atau WebP, sekitar 1200×200, maksimal 500 KB. Setelah di-upload, kotaknya menampilkan gambar kamu dengan tombol **Change…** dan **Remove** *(1.6.0)*.

### About & update

Update dari GitHub, add-on Xray, dan kredit. Lihat [bagian 15](#15-update).

## 13. Bot Telegram

**Settings › Telegram**, mati secara default.

1. Buat bot di [@BotFather](https://t.me/BotFather), lalu tempel **Bot token**.
2. Nyalakan **Enable bot**, lalu Save.
3. Tekan **Pair new admin**. Kirim kode 6 digit yang muncul ke bot. Hanya admin yang sudah di-pair yang bisa memakai bot.

Yang bisa dilakukan lewat bot:

- Status, pindah profil, Manual / Fallback / Auto / Balance, Change IP (dengan pilihan modem jika ada lebih dari satu), start / stop.
- Usage, akun (tambah dari link, masa aktif, hapus), info router dan perangkat, log, update, backup dan restore, reboot.

Notifikasi otomatis:

- Tunnel putus atau tersambung lagi, profil berpindah, login gagal.
- Akun hampir habis, IP berganti (beserta nama modemnya), router menyala, perangkat baru tersambung, update tersedia.
- **Daily report** pemakaian kemarin.

Pengaturan bot:

- **Language**: English atau Bahasa Indonesia.
- **Connect via**: Auto (WAN dulu, lalu tunnel), WAN only, atau Tunnel only.
- **Alert after down for**: lama putus sebelum dilaporkan (15 detik sampai 5 menit).
- **Allowed actions**: setiap kelompok aksi bisa dimatikan (start/stop, Change IP, akun, update, restore, reboot).

Bot jalan sebagai service terpisah, jadi tetap bisa menerima perintah Start walaupun tunnel sedang mati.

## 14. Tab Log

- **Level**: Info, Debug (potongan payload dan balasan mentah), atau Errors only.
- **Auto-scroll**, **Download log**, **Clear**.
- Tag log yang berguna: `connect`, `ping`, `switch`, `balance`, `changeip`, `xray`, `slowdns`, `tun`.

## 15. Update

- **Settings › About & update** mengecek rilis terbaru di GitHub. Update dipasang dari LuCI dan dicek SHA256-nya. Setting tetap tersimpan.
- **Channel**: Stable, atau Beta (pre-release).
- **Daily check**: menampilkan pemberitahuan di Dashboard, tidak pernah memasang sendiri.
- Add-on Xray ikut diperbarui saat Tomato Tunnel diperbarui.

## 16. Tag payload

Tag ditulis di kolom **Payload text**, lalu diganti dengan nilainya saat konek. Nama tag tidak peka huruf besar/kecil (`[CRLF]` sama dengan `[crlf]`). Tag yang tidak dikenal dikirim apa adanya.

### Server

| Tag | Isi |
|---|---|
| `[host]` `[server]` `[ssh_host]` | Host server |
| `[port]` `[ssh_port]` | Port server |
| `[host_port]` `[ssh]` | `host:port` |
| `[ip]` | IP server (hasil resolve) |
| `[ip_port]` | `ip:port` |

### Bug host / SNI dan remote proxy

| Tag | Isi |
|---|---|
| `[bug]` `[sni]` `[sni_host]` | Bug host (kalau kosong: host server) |
| `[sni_port]` | Port server |
| `[proxy]` | Remote proxy `host:port` |
| `[proxy_host]` `[proxy_port]` | Bagian host / port dari remote proxy |

### Request

| Tag | Isi |
|---|---|
| `[method]` | `CONNECT` |
| `[protocol]` | `HTTP/1.1` |
| `[ua]` | User-Agent dari kolom User-Agent |
| `[raw]` `[real_raw]` | `CONNECT host:port HTTP/1.0` diikuti baris kosong |
| `[netData]` `[realData]` | `CONNECT host:port HTTP/1.0` tanpa baris kosong |
| `[ws_key]` | `Sec-WebSocket-Key` acak, baru di setiap koneksi |
| `[auth]` | Kosong |
| `[crlf]` `[lfcr]` `[cr]` `[lf]` | Baris baru `\r\n`, `\n\r`, `\r`, `\n` |

**Ulangi tag** dengan `*N`, misalnya `[crlf*2]` atau `[lf*3]`. Tulisan `\r` dan `\n` di dalam payload juga otomatis jadi baris baru.

### Split dan jeda

Split memotong payload menjadi beberapa kiriman terpisah.

| Tag | Jeda sebelum potongan berikutnya |
|---|---|
| `[split]` | Sesuai kolom **[split] delay** (default 1000 ms) |
| `[delay]` `[delay_split]` `[split_delay]` | 1,5 detik |
| `[instant_split]` `[splitNoDelay]` | Tanpa jeda |

### Rotate dan random

| Tag | Fungsi |
|---|---|
| `[rotate]` | Memisahkan beberapa payload. Setiap koneksi memakai payload berikutnya secara bergiliran. |
| `[random]` | Sama, tapi payload dipilih acak. |
| `[rotate=a;b;c]` | Diganti `a`, lalu `b`, lalu `c`, bergiliran di setiap koneksi. |
| `[random=a;b;c]` | Diganti salah satu nilai secara acak. |

`[rotate=…]` dan `[random=…]` juga bisa dipakai di kolom **Server host**, **Bug host**, dan **Remote proxy**. Contoh bug host: `[rotate=bug1.com;bug2.com;bug3.com]`.

### Contoh

```
HEAD / HTTP/1.1[crlf]Host: [bug][crlf][crlf]PATCH / HTTP/1.1[crlf]Host: [host][crlf]Upgrade: websocket[crlf][crlf][split]HTTP/1.1[crlf][crlf]
```

Payload WebSocket dengan key acak:

```
GET / HTTP/1.1[crlf]Host: [bug][crlf]Upgrade: websocket[crlf]Connection: Upgrade[crlf]Sec-WebSocket-Key: [ws_key][crlf]Sec-WebSocket-Version: 13[crlf][crlf]
```

Dua payload bergiliran:

```
GET / HTTP/1.1[crlf]Host: [bug][crlf]Upgrade: websocket[crlf][crlf][rotate]CONNECT [host_port] HTTP/1.1[crlf]Host: [bug][crlf][crlf]
```

## 17. CPU dan RAM

Diukur di King3399 (RK3399, 6 core, RAM 2 GB) dengan ImmortalWrt mode TUN. CPU 100% berarti satu core penuh.

| Kondisi | tomato-tunnel | xray (per akun Xray) |
|---|---|---|
| Diam | ~0%, ~36 MB | ~1%, ~38 MB |
| Satu akun Xray, ~60 Mbps (1.5.0) | ~0% (TUN langsung ke Xray) | — |
| Balance, ~80 Mbps (1.6.0) | ~30% | ~50% |

- Di mode **Balance**, semua trafik lewat `tomato-tunnel` dulu supaya bisa dibagi, jadi CPU-nya naik dibanding mode biasa.
- RAM: setiap akun **Xray** di Balance menambah sekitar **40 MB**, karena menjalankan proses Xray sendiri. Akun **SSH** dan **ZiVPN** hanya menambah beberapa MB.
- Di perangkat yang lebih lemah (STB S905X dan sejenisnya), Balance di 80–100 Mbps bisa memakai 1,5–2 core. Di router dengan RAM 256 MB atau kurang, batasi akun Xray di Balance maksimal 2–3.

## 18. Masalah umum

**Akun Balance di modem kedua gagal dengan "network unreachable".**
Metric interface sama, atau "Use default gateway" mati. Lihat [persiapan network](#persiapan-network-wajib-untuk-lebih-dari-satu-modem).

**Gambar banner sendiri tidak tampil.**
Sudah diperbaiki di 1.6.0. Setelah update, gambar otomatis disalin ulang dengan izin yang benar.

**Speedtest tidak menunjukkan speed gabungan.**
Pakai speedtest multi-thread (speedtest.net mode Multi, fast.com), dan pastikan **Balance: same account per site** mati.

**Login gagal terus di satu akun.**
Kartu akun menampilkan "Login failed": akun terkunci, habis, atau password salah. Status ini hilang setelah login berhasil.

**Server SSH lama (Dropbear) putus-putus.**
Set **SSH compatibility** ke Auto (default) atau Legacy.

**OpenClash atau PassWall bentrok.**
Jangan dua-duanya di mode TUN. Set Tomato Tunnel ke **SOCKS5 only**.

## 19. Riwayat versi

| Versi | Isi utama |
|---|---|
| 1.0.0 | Klien SSH untuk OpenWrt seperti HTTP Custom: Payload, TLS/SNI, WebSocket, Enhanced, SlowDNS |
| 1.1.0 | Dashboard, pindah profil tanpa putus, Manual / Fallback / Auto, akun ZiVPN |
| 1.2.0 | Tab Usage, Change IP, masa aktif akun, tampilan baru, 11 arsitektur |
| 1.2.1 | Logo baru |
| 1.3.0 | Akun Xray dan add-on Xray, Show on Dashboard, IP Indonesia direct |
| 1.3.1 | Hanya memakai build Xray milik Tomato Tunnel |
| 1.4.0 | Bot Telegram, grafik Dashboard tetap ada setelah reload |
| 1.5.0 | Perbaikan putus di Dropbear lama, TUN langsung ke Xray, benchmark |
| 1.6.0 | Multi modem, range IP Change IP, Go out through per akun, mode Balance (weight, UDP per aliran, akun utama tercepat), tag payload baru (`[ip]`, `[proxy]`, `[ws_key]`, rotate/random, `*N`), banner jadi header di semua tab, perbaikan upload banner |
| 1.7.0 | Tampilan baru di semua tab yang mengikuti tema LuCI, tab Connections, ping sungguhan dengan unified delay, Fallback kembali ke akun pilihan, Block QUIC, Direct domains untuk semua jenis akun, Schedule, warna per jenis akun, akun hasil import langsung tersimpan |

---

Dibuat oleh [@dudulrealnofek](https://t.me/dudulrealnofek). Grup: [@tfnuklir](https://t.me/tfnuklir).
