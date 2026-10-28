# Panduan Deployment & Setup Produksi Oper ⚡

Dokumen ini berisi panduan teknis langkah demi langkah untuk melakukan *self-hosting* dan *deployment* Oper pada server VPS Linux (Ubuntu / Debian / CentOS / Rocky Linux) menggunakan **Systemd** dan **Nginx Reverse Proxy**.

---

## 1. Persiapan DNS Domain / Subdomain

Sebelum melakukan konfigurasi di server, arahkan domain atau subdomain Anda (misalnya: `oper.yourdomain.com` atau `oper.bintangarama.cloud`):

1. Buka dashboard DNS provider Anda (misalnya Cloudflare, Namecheap, Niagahoster, dll).
2. Tambahkan **DNS Record**:
   - **Type:** `A`
   - **Name / Host:** `oper` (atau subdomain pilihan Anda)
   - **IPv4 Address:** Masukkan IP publik server VPS Anda
   - **TTL:** Auto atau 3600 (1 jam)
   - **Proxy Status (khusus Cloudflare):** Boleh *DNS Only* (abu-abu) saat pertama kali setup SSL Let's Encrypt, lalu ubah ke *Proxied* (oranye) jika diinginkan.

---

## 2. Kompilasi Biner Linux (Cross-Compile)

Oper dibangun menggunakan **100% Go standard library** murni tanpa ketergantungan library C (`CGO_ENABLED=0`), sehingga file biner yang dihasilkan bersifat *fully static standalone binary* yang dapat langsung dijalankan di semua distro Linux 64-bit tanpa perlu menginstal runtime Go di server.

Jalankan perintah kompilasi dari komputer pengembang:

```bash
# Dari Windows PowerShell:
$env:CGO_ENABLED="0"; $env:GOOS="linux"; $env:GOARCH="amd64"; go build -ldflags="-s -w" -o oper-linux .

# Dari Linux / macOS Terminal:
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o oper-linux .
```

*Ukuran biner hanya ~7 MB dan dapat langsung disalin ke server via SCP atau SFTP.*

---

## 3. Setup Pengguna & Direktori di VPS

Login ke server VPS via SSH, lalu siapkan pengguna sistem tanpa hak login (*system account*) dan folder penyimpanan:

```bash
# 1. Buat system user dan group terisolasi
sudo useradd -r -s /bin/false oper

# 2. Buat direktori aplikasi dan direktori penyimpanan file sementara
sudo mkdir -p /opt/oper
sudo mkdir -p /var/tmp/oper

# 3. Salin biner oper-linux ke folder aplikasi
# (Ganti nama menjadi 'oper' dan berikan izin eksekusi)
sudo cp oper-linux /opt/oper/oper
sudo chmod +x /opt/oper/oper

# 4. Atur kepemilikan dan hak akses direktori
sudo chown -R oper:oper /opt/oper /var/tmp/oper
sudo chmod 700 /var/tmp/oper
```

> ⚠️ **PERINGATAN PENTING MENGENAI STORAGE:**  
> **JANGAN PERNAH** mengarahkan `OPER_DATA_DIR` ke filesystem `tmpfs` (RAM-disk seperti `/tmp` bawaan beberapa distro Linux)! File yang diunggah akan memakan memori RAM server. Gunakan partisi disk fisik normal seperti `/var/tmp/oper`.

---

## 4. Konfigurasi Systemd Service

Unit service systemd di [`deploy/systemd/oper.service`](deploy/systemd/oper.service) telah dilengkapi pembatasan *cgroup* Linux untuk menjamin konsumsi RAM aplikasi tetap berada di bawah **48 MB**.

1. Salin unit file ke systemd:
   ```bash
   sudo cp deploy/systemd/oper.service /etc/systemd/system/oper.service
   ```

2. Muat ulang daemon systemd dan aktifkan service:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable --now oper
   ```

3. Periksa status service:
   ```bash
   sudo systemctl status oper
   ```

4. Untuk melihat log realtime:
   ```bash
   sudo journalctl -u oper -f
   ```

---

## 5. Konfigurasi Nginx Reverse Proxy

Konfigurasi Nginx di [`deploy/nginx/oper.conf`](deploy/nginx/oper.conf) dirancang khusus agar:
- **Server-Sent Events (SSE):** Buffering dimatikan (`proxy_buffering off`) agar event data langsung terdorong ke browser seketika tanpa jeda (*delay*).
- **Streaming Upload:** `proxy_request_buffering off` agar file besar langsung dialirkan ke socket Go tanpa menumpuk di buffer disk Nginx.
- **Privasi:** Token sensitif di query string (`?t=...`) tidak dicatat ke file `access.log`.

### Langkah Pemasangan:

1. Buka file konfigurasi dan sesuaikan `server_name` dengan domain Anda:
   ```bash
   sudo cp deploy/nginx/oper.conf /etc/nginx/sites-available/oper.conf
   ```

2. Aktifkan konfigurasi dengan membuat symlink:
   ```bash
   sudo ln -s /etc/nginx/sites-available/oper.conf /etc/nginx/sites-enabled/
   ```

3. Uji sintaks Nginx dan muat ulang:
   ```bash
   sudo nginx -t
   sudo systemctl reload nginx
   ```

---

## 6. Pasang Sertifikat SSL / HTTPS (Let's Encrypt)

HTTPS wajib aktif agar fitur browser modern seperti **PWA (Add to Home Screen)**, **Service Worker Caching**, dan **Clipboard API (1-Klik Salin)** dapat berfungsi:

```bash
# Pasang Certbot jika belum ada
sudo apt update && sudo apt install -y certbot python3-certbot-nginx

# Terbitkan dan pasang SSL otomatis
sudo certbot --nginx -d oper.yourdomain.com
```

Certbot akan otomatis memperbarui konfigurasi Nginx dan mengatur perpanjangan (*auto-renewal*) sertifikat setiap 90 hari.

---

## 7. Variabel Lingkungan (*Environment Variables*)

Konfigurasi dapat disesuaikan pada file `/etc/systemd/system/oper.service` di bagian `[Service]`:

| Variabel | Default | Deskripsi |
|---|---|---|
| `OPER_ADDR` | `:8080` | Alamat listen HTTP server lokal |
| `OPER_DATA_DIR` | `/var/tmp/oper` | Direktori fisik penyimpanan file sementara (wajib non-tmpfs) |
| `OPER_ROOM_TTL` | `15m` | Waktu idle sebelum ruangan hangus otomatis |
| `OPER_ROOM_MAX_LIFE` | `2h` | Batas maksimum umur ruangan sejak dibuat |
| `OPER_FILE_TTL` | `15m` | Batas umur file sebelum dihapus otomatis oleh janitor |
| `OPER_MAX_FILE_MB` | `50` | Batas ukuran per file (MB) |
| `OPER_MAX_ROOM_MB` | `200` | Batas akumulasi ukuran file per ruangan (MB) |
| `OPER_MAX_TOTAL_MB` | `1024` | Kuota disk global untuk seluruh ruangan aktif |
| `OPER_TRUST_PROXY` | `true` | Percayai header `X-Real-IP` dari Nginx untuk rate limiter |

---

## 8. Pemantauan & Observabilitas (`/healthz`)

Periksa kondisi memori dan goroutine aplikasi secara berkala:

```bash
curl -s http://127.0.0.1:8080/healthz | jq
```

Contoh keluaran JSON:
```json
{
  "ok": true,
  "store": {
    "rooms": 1,
    "files": 2,
    "bytes": 3145728,
    "reserved": 0
  },
  "goroutines": 9,
  "heapAllocKB": 384,
  "sysKB": 8192
}
```
Heap allocation Go yang sangat kecil (<1 MB) membuktikan Oper sangat hemat sumber daya dan aman berjalan berdampingan dengan aplikasi lain di VPS dengan spesifikasi 1 vCPU / 512MB RAM sekalipun.
