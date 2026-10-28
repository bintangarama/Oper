# Oper ⚡ — Instant Room-Based File & Text Transfer

Aplikasi web transfer file dan teks kilat antar perangkat (**HP ⇄ PC, PC ⇄ HP, PC ⇄ PC, HP ⇄ HP**) tanpa registrasi/login, tanpa database, dan tanpa dependensi eksternal. Dirancang khusus untuk menggantikan kebiasaan login WhatsApp Web di komputer kantor/publik hanya untuk transfer dokumen/foto/link yang ingin dibuka atau dicetak (*print*), dengan efisiensi performa ekstrem (**konsumsi RAM < 50 MB**).

---

## Daftar Isi
- [Fitur Utama](#fitur-utama)
- [Arsitektur & Keunggulan Teknis](#arsitektur--keunggulan-teknis)
- [Struktur Direktori](#struktur-direktori)
- [Menjalankan di Lokal (Pengujian Cepat)](#menjalankan-di-lokal-pengujian-cepat)
- [Kompilasi & Build Biner](#kompilasi--build-biner)
- [Panduan Deployment di VPS Linux (oper.bintangarama.cloud)](#panduan-deployment-di-vps-linux-operbintangaramacloud)
- [Konfigurasi Environment Variables](#konfigurasi-environment-variables)
- [Spesifikasi API & Real-Time SSE](#spesifikasi-api--real-time-sse)
- [Pengujian Unit & Observabilitas](#pengujian-unit--observabilitas)

---

## Fitur Utama

- 🔄 **Device-Agnostic & Dua Arah:** Bebas memilih peran **Kirim File** atau **Terima File** di perangkat mana saja (HP, PC kantor, laptop, atau tablet).
- ⚡ **Zero-Login & Instant Room:** Cukup gunakan 4-digit PIN acak atau pindai QR Code di layar. Terhubung dalam hitungan detik tanpa perlu registrasi atau membuat akun.
- 📡 **Real-Time via Server-Sent Events (SSE):** Begitu file diunggah di pengirim, layar penerima langsung memunculkan kartu dokumen seketika tanpa perlu memuat ulang (*refresh*) halaman.
- 🔔 **Web Audio Chime Notification:** Notifikasi suara dua nada sintetis (E6 ➔ B6) yang lembut dan elegan saat file atau teks masuk (100% Web Audio API murni tanpa perlu mengunduh file audio eksternal). Dilengkapi toggle di pengaturan penerima.
- 📦 **Download Semua sebagai ZIP (1-Klik Streaming):** Tombol dinamis "Unduh Semua (.zip)" muncul otomatis saat ruangan memiliki >= 2 file. Backend mengalirkan (*stream*) arsip ZIP langsung ke browser via `archive/zip` tanpa membengkakkan memori server.
- 🔗 **Salin Tautan Ruangan Instan (Direct Room Link):** Tombol 1-klik di bawah QR code untuk menyalin tautan join langsung (`#join=ROOMID.JOINKEY`), memungkinkan perangkat pengirim langsung masuk ke ruangan tanpa perlu mengetik PIN manual.
- 🖨️ **Direct Silent Print Dialog:** Dokumen PDF dan gambar dapat langsung dicetak (*print*) via invisible iframe tanpa harus tersimpan mengotori folder *Downloads* PC.
- 🔍 **Smart Magic-Bytes Sniffing & Auto-Extension Healing:** Otomatis memeriksa *magic bytes* file (PDF, Office XML: `.docx`, `.xlsx`, `.pptx`, gambar `.png`, `.jpg`, `.webp`, arsip `.zip`, dll). Jika smartphone mengunggah file tanpa ekstensi (seperti format UUID bawaan cache ponsel), sistem otomatis melengkapi ekstensinya secara akurat.
- 📱 **Multi-Source Mobile Picker:** Memisahkan tombol unggah menjadi 3 pilihan: **Pilih Dokumen / File**, **Pilih dari Galeri Foto**, dan **Ambil Foto Langsung (Kamera)**, mencegah browser HP langsung membuka kamera secara paksa saat ingin mengirim dokumen.
- 🚪 **Sinkronisasi Sesi Otomatis (Auto-Exit):** Saat penerima menutup sesi atau ruangan kedaluwarsa, layar pengirim langsung otomatis terputus dan kembali ke beranda.
- 🧹 **Ephemeral & Auto-Purge:** File dan ruangan memiliki masa aktif terbatas (TTL default 15 menit). Background *janitor* rutin membersihkan file fisik dan metadata yang kedaluwarsa dari disk.
- 🔒 **Rotasi PIN & Keamanan Berlapis:** PIN berganti otomatis setiap kali pengirim terhubung, isolasi path traversal (`filepath.Base`), pembatasan kuota disk per ruangan, dan rate limiter token-bucket per IP / IPv6 `/64`.
- 📋 **Kirim Teks & Tautan Kilat (Quick Clipboard Sync):** Mengirim catatan teks, nomor resi, kode sandi, atau tautan URL web dari HP ke PC (atau sebaliknya). Di layar penerima, teks langsung tampil dengan tombol 1-klik **Salin ke Clipboard** dan **Buka Link ↗**.
- 📲 **PWA Ready (Add to Home Screen):** Mendukung Progressive Web App lengkap dengan `manifest.json`, Service Worker offline shell, dan ikon kustom (192px & 512px). Pengguna dapat memasang Oper langsung di layar utama smartphone layaknya aplikasi native.
- 🪶 **Zero External Dependencies & Ultra-Low Memory:** Dibangun 100% menggunakan Go standard library murni. Tanpa database SQL, tanpa Redis, tanpa `node_modules`. Konsumsi memori idle hanya ~6–10 MB RSS.

---

## Arsitektur & Keunggulan Teknis

```text
[ Pengirim: HP / PC ]                           [ Penerima: PC / HP ]
         │                                               │
         │ 1. POST /api/join {pin / joinKey}             │ 0. POST /api/rooms
         │ ──────────────────────► [ Oper Server ] ◄────────────────────
         │   (Dapatkan senderToken)     │ (Go 1.22+)     │ (Dapatkan receiverToken, PIN, QR)
         │                              │                │
         │                              │                │ 2. GET /api/rooms/{id}/events (SSE)
         │                              │                │ ◄──────────────────────────────────
         │ 3. POST /api/rooms/{id}/files│                │
         │    (Direct Disk Streaming)   │                │
         │ ────────────────────────────►│                │
         │                              │ 4. SSE: event "file" (metadata)
         │                              │ ───────────────────────────────────►
         │                              │                │
         │                              │                │ 5. GET /api/rooms/{id}/files/{fid}
         │                              │                │    (Inline Preview / Direct Print)
         │                              │                │    ATAU: GET /api/rooms/{id}/zip
         │                              │                │ ◄──────────────────────────────────
```

1. **Upload Unbuffered Direct-to-Disk:** File yang diunggah tidak ditampung di RAM server, melainkan di-*stream* langsung dari *socket* HTTP ke partisi disk fisik sebagai file `.part` dengan batas kuota ketat (`io.LimitReader`). Jika koneksi terputus di tengah jalan, file `.part` langsung dihapus seketika.
2. **Streaming ZIP Langsung:** Endpoint `/api/rooms/{id}/zip` menggunakan `archive/zip.NewWriter(w)` yang menulis langsung ke socket HTTP respon klien. Seluruh file di-stream dari disk fisik tanpa buffering di memory buffer RAM.
3. **Server-Sent Events (SSE) vs WebSocket:** Menggunakan SSE satu arah (*HTTP/1.1 chunked*) yang sangat ringan dibandingkan WebSocket: tidak memerlukan *handshake upgrade*, ramah terhadap proxy HTTP/2, auto-reconnect bawaan browser, dan mengonsumsi memori per-koneksi yang jauh lebih kecil.

---

## Struktur Direktori

```text
quick-tf-files/
├── go.mod                     # Go module definition (Go 1.22+, 0 dependensi eksternal)
├── main.go                    # Bootstrap server, embed frontend HTML, graceful shutdown
├── config.go                  # Parsing konfigurasi via Environment Variables (OPER_*)
├── store.go                   # In-memory room/file registry, quota reservation, SSE pub/sub, janitor
├── handlers.go                # HTTP API router, SSE streaming, unbuffered upload, ZIP streaming
├── ratelimit.go               # Token-bucket rate limiter per IP / IPv6 /64 prefix
├── sanitize.go                # Sanitasi nama file, sniffing magic-bytes, auto-extension healing
├── sanitize_test.go           # Unit test sanitasi, batas kuota streaming, dan lifecycle store
├── server-dev.js              # Runner server pengujian lokal alternatif berbasis Node.js
├── oper.exe                   # Executable biner Windows (x86_64)
├── oper-linux                 # Executable biner Linux statis (ELF x86_64)
├── web/
│   ├── index.html             # Single-file frontend (Vanilla JS + Tailwind + QR Engine)
│   ├── manifest.json          # Web App Manifest untuk PWA
│   ├── sw.js                  # Service Worker untuk offline app shell
│   ├── icon-192.png           # Ikon PWA 192x192 squircle gradient
│   └── icon-512.png           # Ikon PWA 512x512 squircle gradient
└── deploy/
    ├── nginx/
    │   └── oper.conf          # Konfigurasi reverse proxy Nginx (SSE & streaming upload unbuffered)
    └── systemd/
        └── oper.service       # Service unit systemd dengan batasan memori cgroup 48MB
```

---

## Menjalankan di Lokal (Pengujian Cepat)

### Menggunakan Biner Windows (`oper.exe`)
1. Jalankan aplikasi langsung dari PowerShell:
   ```powershell
   .\oper.exe
   ```
2. Buka browser di PC:
   ```text
   http://localhost:8080/
   ```
3. Jika ingin diakses dari HP dalam satu jaringan Wi-Fi lokal:
   - Cari IP lokal PC via PowerShell: `ipconfig` (misal: `192.168.1.50`).
   - Buka `http://192.168.1.50:8080` dari browser HP.
   - Izinkan port 8080 di Windows Firewall jika koneksi dari HP diblokir:
     ```powershell
     New-NetFirewallRule -DisplayName "Oper 8080" -Direction Inbound -LocalPort 8080 -Protocol TCP -Action Allow
     ```

---

## Kompilasi & Build Biner

Karena tidak memiliki dependensi eksternal, kompilasi hanya memakan waktu 1–2 detik:

### 1. Build untuk Windows (`.exe`)
```powershell
go build -ldflags="-s -w" -o oper.exe .
```

### 2. Cross-Compile untuk VPS Linux (64-bit AMD64)
```powershell
# Dari Windows PowerShell:
$env:CGO_ENABLED="0"; $env:GOOS="linux"; $env:GOARCH="amd64"; go build -ldflags="-s -w" -o oper-linux .

# Dari Linux / macOS Terminal:
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o oper-linux .
```
Flag `-ldflags="-s -w"` membuang simbol debug dan tabel DWARF sehingga ukuran file biner menjadi sangat ramping (~7 MB) dan waktu start-up seketika (*instant boot*).

---

## Panduan Deployment di VPS Linux (oper.bintangarama.cloud)

### 1. Arahkan DNS Subdomain
Tambahkan DNS Record di Cloudflare / DNS Provider domain `bintangarama.cloud`:
- **Type:** `A`
- **Name:** `oper` (sehingga menjadi `oper.bintangarama.cloud`)
- **IPv4 Address:** Masukkan IP publik VPS Linux Anda
- **Proxy Status:** DNS Only atau Proxied

### 2. Siapkan Pengguna & Direktori di VPS
```bash
# Buat system user tanpa shell login
sudo useradd -r -s /bin/false oper

# Buat folder aplikasi dan partisi disk temporer
sudo mkdir -p /opt/oper
sudo mkdir -p /var/tmp/oper

# Salin file binary oper-linux ke VPS (ganti nama jadi oper)
sudo cp oper-linux /opt/oper/oper
sudo chmod +x /opt/oper/oper
sudo chown -R oper:oper /opt/oper /var/tmp/oper
sudo chmod 700 /var/tmp/oper
```

> ⚠️ **PERINGATAN PENTING MENGENAI STORAGE:**  
> **JANGAN PERNAH** mengarahkan `OPER_DATA_DIR` ke filesystem `tmpfs` (RAM-disk seperti `/tmp` bawaan Linux)! File yang diunggah akan memakan memori RAM server. Gunakan partisi disk fisik normal seperti `/var/tmp/oper`.

### 3. Pasang Systemd Service (Isolasi Memori < 48 MB)
Unit file `deploy/systemd/oper.service` telah disesuaikan dengan parameter cgroup Linux:
```bash
sudo cp deploy/systemd/oper.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now oper
sudo systemctl status oper
```

### 4. Pasang Konfigurasi Nginx & Aktifkan HTTPS (SSL)
Konfigurasi `deploy/nginx/oper.conf` mematikan buffering untuk real-time SSE dan streaming upload:
```bash
sudo cp deploy/nginx/oper.conf /etc/nginx/sites-available/oper.conf
sudo ln -s /etc/nginx/sites-available/oper.conf /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
```

Pasang sertifikat SSL gratis menggunakan Let's Encrypt Certbot:
```bash
sudo certbot --nginx -d oper.bintangarama.cloud
```
*(HTTPS wajib aktif agar fitur PWA Add to Home Screen, Service Worker Caching, dan Clipboard API dapat berjalan optimal di browser ponsel).*

---

## Konfigurasi Environment Variables

Semua konfigurasi bersifat opsional dan memiliki nilai *default* yang aman untuk produksi:

| Variabel | Tipe Data | Default | Keterangan |
|---|---|---|---|
| `OPER_ADDR` | String | `:8080` | Alamat & port listen HTTP server |
| `OPER_DATA_DIR` | Path | `os.TempDir()/oper` | Direktori fisik penyimpanan file sementara (wajib non-tmpfs) |
| `OPER_ROOM_TTL` | Duration | `15m` | Batas waktu idle sebelum ruangan otomatis kadaluarsa |
| `OPER_ROOM_MAX_LIFE` | Duration | `2h` | Batas maksimum absolut umur ruangan sejak pertama kali dibuat |
| `OPER_FILE_TTL` | Duration | `15m` | Batas umur file sebelum dihapus otomatis oleh janitor |
| `OPER_UPLOAD_TIMEOUT` | Duration | `10m` | Batas waktu maksimum durasi transfer upload per file |
| `OPER_MAX_FILE_MB` | Integer (MB) | `50` | Batas ukuran maksimum per file |
| `OPER_MAX_ROOM_MB` | Integer (MB) | `200` | Batas akumulasi total ukuran file per ruangan |
| `OPER_MAX_TOTAL_MB` | Integer (MB) | `1024` | Batas kapasitas disk global untuk seluruh ruangan aktif |
| `OPER_MAX_ROOMS` | Integer | `500` | Batas maksimum jumlah ruangan yang dapat aktif bersamaan |
| `OPER_MAX_FILES_PER_ROOM` | Integer | `50` | Batas jumlah file maksimum dalam satu ruangan |
| `OPER_TRUST_PROXY` | Boolean | `false` | Set `true` jika berjalan di belakang Nginx (`X-Real-IP` untuk rate limiter) |
| `OPER_DEV` | Boolean | `false` | Jika `true`, memuat file `web/index.html` langsung dari disk saat request (untuk styling) |

---

## Spesifikasi API & Real-Time SSE

Semua endpoint API mengembalikan format JSON standar dan status HTTP yang jelas.

| Method | Endpoint | Akses | Deskripsi |
|---|---|---|---|
| `POST` | `/api/rooms` | Publik | Membuat ruangan penerima baru (mengembalikan `roomId`, `receiverToken`, `pin`, `joinKey`). |
| `POST` | `/api/join` | Publik | Bergabung ke ruangan sebagai pengirim (body: `{"pin":"1234"}` atau `{"roomId":"..","joinKey":".."}`). |
| `GET` | `/api/rooms/{id}/events?t={token}` | Receiver / Sender | Real-time SSE stream (`file`, `incoming`, `joined`, `expired`, `file_deleted`). |
| `POST` | `/api/rooms/{id}/files` | Sender (`Authorization: Bearer <senderToken>`) | Upload file multipart streaming langsung ke disk. |
| `POST` | `/api/rooms/{id}/text` | Sender (`Authorization: Bearer <senderToken>`) | Berbagi catatan teks, nomor resi, token, atau tautan URL (`{"text":"..."}`). |
| `GET` | `/api/rooms/{id}/files/{fid}?t={receiverToken}&dl={0\|1}` | Receiver | Unduh file (`&dl=1`) atau pratinjau inline langsung di browser (`&dl=0`). |
| `GET` | `/api/rooms/{id}/zip?t={receiverToken}` | Receiver | Unduh seluruh file dalam ruangan sebagai arsip `.zip` tunggal secara streaming. |
| `DELETE` | `/api/rooms/{id}/files/{fid}` | Receiver (`Authorization: Bearer <receiverToken>`) | Menghapus file fisik dari disk sebelum masa TTL habis. |
| `DELETE` | `/api/rooms/{id}` | Receiver (`Authorization: Bearer <receiverToken>`) | Menutup ruangan dan menghapus seluruh file seketika. |
| `GET` | `/manifest.json` | Publik | Manifest PWA untuk instalasi layar utama (Add to Home Screen). |
| `GET` | `/sw.js` | Publik | Service Worker PWA untuk caching shell aplikasi. |
| `GET` | `/icon-192.png` & `/icon-512.png` | Publik | Asset ikon aplikasi untuk PWA dan favicon. |
| `GET` | `/healthz` | Publik / Internal | Endpoint metrik & observabilitas kesehatan memori server. |

---

## Pengujian Unit & Observabilitas

### 1. Menjalankan Unit Test
Proyek dilengkapi pengujian unit komprehensif pada fungsi sanitasi nama file, MIME sniffing, batas kuota streaming, dan siklus hidup registri memori:
```bash
go test -v .
```

### 2. Endpoint Observabilitas `/healthz`
Dapat dipantau secara berkala atau diintegrasikan dengan Prometheus / Uptime Kuma:
```bash
curl -s http://127.0.0.1:8080/healthz | jq
```
Contoh respons metrik:
```json
{
  "ok": true,
  "store": {
    "rooms": 1,
    "files": 3,
    "bytes": 5242880,
    "reserved": 0
  },
  "goroutines": 9,
  "heapAllocKB": 362,
  "sysKB": 6848
}
```
*Heap allocation aktif Go hanya membutuhkan memori ratusan Kilobyte saat idle, menjadikannya salah satu solusi transfer file web paling ringan dan efisien.*
