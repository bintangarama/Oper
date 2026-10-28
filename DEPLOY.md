# Panduan Deployment & Pemeliharaan Oper ⚡

Dokumen teknis komprehensif untuk instalasi perdana (*fresh install*), konfigurasi domain, pengamanan SSL, hingga prosedur **pembaruan versi (*update/upgrade*)** Oper di server VPS Linux (Ubuntu / Debian / CentOS / Rocky Linux).

---

## Daftar Isi
1. [Persiapan DNS Domain / Subdomain](#1-persiapan-dns-domain--subdomain)
2. [Instalasi Perdana di VPS](#2-instalasi-perdana-di-vps)
   - [Opsi A: Unduh Biner dari GitHub Release (Rekomendasi)](#opsi-a-unduh-biner-dari-github-release-paling-praktis)
   - [Opsi B: Kompilasi Lokal & Kirim via SCP](#opsi-b-kompilasi-lokal--kirim-via-scp)
3. [Konfigurasi Systemd Service (RAM < 48 MB)](#3-konfigurasi-systemd-service)
4. [Konfigurasi Nginx Reverse Proxy](#4-konfigurasi-nginx-reverse-proxy)
5. [Aktivasi SSL / HTTPS Otomatis (Let's Encrypt)](#5-aktivasi-ssl--https-otomatis-lets-encrypt)
6. [Panduan Update / Pembaruan Proyek ke Versi Terbaru](#6-panduan-update--pembaruan-proyek)
   - [Alur Update Standar (GitHub Release)](#alur-1-update-cepat-lewat-github-release-rekomendasi)
   - [Alur Update Langsung dari PC Developer (SCP)](#alur-2-update-langsung-dari-pc-developer-via-scp)
   - [Memperbarui Konfigurasi Nginx atau Systemd](#memperbarui-konfigurasi-nginx-atau-systemd)
   - [Verifikasi Setelah Update](#verifikasi-setelah-update)
7. [Variabel Lingkungan (*Environment Variables*)](#7-variabel-lingkungan-environment-variables)
8. [Pemantauan & Observabilitas (`/healthz`)](#8-pemantauan--observabilitas-healthz)

---

## 1. Persiapan DNS Domain / Subdomain

Sebelum mengatur server, hubungkan domain/subdomain Anda (misalnya `oper.yourdomain.com` atau `oper.bintangarama.cloud`):

1. Buka dashboard DNS provider domain Anda (Cloudflare, Namecheap, Niagahoster, dll).
2. Tambahkan **DNS Record**:
   - **Type:** `A`
   - **Name:** `oper` (atau subdomain pilihan Anda)
   - **IPv4 Address:** Masukkan IP publik server VPS Anda
   - **TTL:** Auto atau 3600 (1 jam)
   - **Proxy Status (Cloudflare):** Disarankan *DNS Only* (ikon abu-abu) saat pertama kali menerbitkan sertifikat SSL Certbot, setelahnya dapat diubah menjadi *Proxied* (oranye).

---

## 2. Instalasi Perdana di VPS

Oper dibangun 100% menggunakan Go standard library murni tanpa dependensi C (`CGO_ENABLED=0`). Biner yang dihasilkan bersifat *fully static standalone* yang dapat langsung dieksekusi di server tanpa perlu menginstal Go runtime di VPS.

Login ke terminal VPS via SSH, lalu ikuti salah satu opsi di bawah:

### Opsi A: Unduh Biner dari GitHub Release (Paling Praktis)
Metode ini mengambil biner resmi yang sudah terunggah di GitHub Releases tanpa perlu menyalin file manual dari komputer:

```bash
# 1. Buat system user khusus 'oper' tanpa hak login shell (keamanan)
sudo useradd -r -s /bin/false oper

# 2. Buat folder aplikasi dan partisi disk temporer
sudo mkdir -p /opt/oper /var/tmp/oper

# 3. Unduh biner Linux terbaru langsung dari GitHub Release
sudo curl -sL https://github.com/bintangarama/Oper/releases/latest/download/oper-linux -o /opt/oper/oper

# 4. Berikan izin eksekusi dan kepemilikan folder
sudo chmod +x /opt/oper/oper
sudo chown -R oper:oper /opt/oper /var/tmp/oper
sudo chmod 700 /var/tmp/oper
```

---

### Opsi B: Kompilasi Lokal & Kirim via SCP
Jika Anda melakukan perubahan kode di komputer lokal dan ingin mengompilasi sendiri:

1. **Kompilasi biner Linux dari komputer Anda:**
   ```powershell
   # Dari Windows PowerShell (di folder proyek):
   $env:CGO_ENABLED="0"; $env:GOOS="linux"; $env:GOARCH="amd64"; go build -ldflags="-s -w" -o oper-linux .
   ```
2. **Kirim biner ke VPS menggunakan `scp`:**
   ```powershell
   scp oper-linux user@IP_VPS_ANDA:/tmp/oper
   ```
3. **Pindahkan biner di terminal VPS:**
   ```bash
   sudo mkdir -p /opt/oper /var/tmp/oper
   sudo mv /tmp/oper /opt/oper/oper
   sudo chmod +x /opt/oper/oper
   sudo chown -R oper:oper /opt/oper /var/tmp/oper
   sudo chmod 700 /var/tmp/oper
   ```

> ⚠️ **PERINGATAN PENTING MENGENAI STORAGE:**  
> **JANGAN PERNAH** mengarahkan `OPER_DATA_DIR` ke filesystem `tmpfs` (RAM-disk seperti `/tmp` bawaan beberapa distro Linux)! File upload akan memakan RAM server. Gunakan partisi disk fisik normal seperti `/var/tmp/oper`.

---

## 3. Konfigurasi Systemd Service

Unit file [`deploy/systemd/oper.service`](deploy/systemd/oper.service) telah dilengkapi isolasi cgroup Linux untuk menjamin alokasi RAM aplikasi **maksimal < 48 MB**.

1. Unduh unit file service ke direktori systemd VPS:
   ```bash
   sudo curl -sL https://raw.githubusercontent.com/bintangarama/Oper/main/deploy/systemd/oper.service -o /etc/systemd/system/oper.service
   ```

2. Muat ulang daemon systemd dan aktifkan service:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable --now oper
   ```

3. Periksa status service (pastikan hijau / `active (running)`):
   ```bash
   sudo systemctl status oper
   ```

---

## 4. Konfigurasi Nginx Reverse Proxy

Konfigurasi Nginx di [`deploy/nginx/oper.conf`](deploy/nginx/oper.conf) dirancang khusus agar:
- **Server-Sent Events (SSE):** `proxy_buffering off` agar event file/teks langsung terdorong ke browser tanpa delay.
- **Streaming Upload:** `proxy_request_buffering off` agar upload file besar langsung di-stream ke Go tanpa menumpuk di buffer disk Nginx.
- **Privasi:** Token di URL query string (`?t=...`) tidak dicatat ke file log Nginx.

### Langkah Pemasangan:

1. Unduh template konfigurasi Nginx:
   ```bash
   sudo curl -sL https://raw.githubusercontent.com/bintangarama/Oper/main/deploy/nginx/oper.conf -o /etc/nginx/sites-available/oper.conf
   ```

2. Buka file konfigurasi dan sesuaikan `server_name` dengan domain Anda:
   ```bash
   sudo nano /etc/nginx/sites-available/oper.conf
   # Pastikan baris: server_name oper.yourdomain.com; sesuai domain Anda
   ```

3. Buat symlink ke `sites-enabled`:
   ```bash
   sudo ln -sf /etc/nginx/sites-available/oper.conf /etc/nginx/sites-enabled/
   ```

4. Uji sintaks Nginx dan reload:
   ```bash
   sudo nginx -t
   sudo systemctl reload nginx
   ```

---

## 5. Aktivasi SSL / HTTPS Otomatis (Let's Encrypt)

HTTPS wajib aktif agar fitur **PWA (Add to Home Screen)**, **Service Worker Caching**, dan **Clipboard API (1-Klik Salin)** dapat berfungsi di browser mobile:

```bash
# Pasang Certbot jika belum ada (Ubuntu/Debian)
sudo apt update && sudo apt install -y certbot python3-certbot-nginx

# Terbitkan sertifikat SSL otomatis
sudo certbot --nginx -d oper.yourdomain.com
```

Certbot akan otomatis memperbarui konfigurasi Nginx dan memperpanjang masa berlaku sertifikat setiap 90 hari.

---

## 6. Panduan Update / Pembaruan Proyek

Setiap kali Anda menambahkan fitur baru, memperbaiki bug, atau memperbarui tampilan frontend, gunakan prosedur di bawah ini untuk memperbarui aplikasi di VPS dengan downtime minimal (< 1 detik).

### Alur 1: Update Cepat Lewat GitHub Release (Rekomendasi)
Jika Anda sudah membuat tag rilis baru di GitHub (misal `v1.0.1` atau rilis *latest*):

Jalankan perintah 3-baris ini di terminal VPS Anda:

```bash
# 1. Hentikan service sementara
sudo systemctl stop oper

# 2. Unduh biner rilis terbaru menggantikan biner lama
sudo curl -sL https://github.com/bintangarama/Oper/releases/latest/download/oper-linux -o /opt/oper/oper
sudo chmod +x /opt/oper/oper

# 3. Jalankan kembali service
sudo systemctl start oper
```

---

### Alur 2: Update Langsung dari PC Developer via SCP
Jika Anda baru saja mengubah kode di komputer lokal dan ingin segera mengujinya di VPS tanpa membuat rilis GitHub terlebih dahulu:

1. **Kompilasi biner baru di PC Anda:**
   ```powershell
   # Di Windows PowerShell:
   $env:CGO_ENABLED="0"; $env:GOOS="linux"; $env:GOARCH="amd64"; go build -ldflags="-s -w" -o oper-linux .
   
   # Kirim langsung ke folder sementara di VPS:
   scp oper-linux user@IP_VPS_ANDA:/tmp/oper-new
   ```

2. **Ganti biner di terminal VPS:**
   ```bash
   sudo systemctl stop oper
   sudo mv /tmp/oper-new /opt/oper/oper
   sudo chmod +x /opt/oper/oper
   sudo chown oper:oper /opt/oper/oper
   sudo systemctl start oper
   ```

---

### Memperbarui Konfigurasi Nginx atau Systemd
Lakukan langkah ini **hanya jika** terdapat perubahan pada file konfigurasi Nginx atau Systemd:

```bash
# Jika ada update file Nginx:
sudo curl -sL https://raw.githubusercontent.com/bintangarama/Oper/main/deploy/nginx/oper.conf -o /etc/nginx/sites-available/oper.conf
sudo nginx -t && sudo systemctl reload nginx

# Jika ada update file Systemd (misalnya limit RAM baru):
sudo curl -sL https://raw.githubusercontent.com/bintangarama/Oper/main/deploy/systemd/oper.service -o /etc/systemd/system/oper.service
sudo systemctl daemon-reload && sudo systemctl restart oper
```

---

### Verifikasi Setelah Update
Pastikan service berjalan normal dan log tidak menampilkan error:

```bash
# 1. Cek status aktif service
sudo systemctl status oper

# 2. Lihat 20 baris log terakhir
sudo journalctl -u oper -n 20 --no-pager

# 3. Uji endpoint observabilitas
curl -s http://127.0.0.1:8080/healthz | jq
```

---

## 7. Variabel Lingkungan (*Environment Variables*)

Konfigurasi batas kuota dan timeout dapat disesuaikan pada file `/etc/systemd/system/oper.service` di bagian `[Service]`:

| Variabel | Default | Deskripsi |
|---|---|---|
| `OPER_ADDR` | `127.0.0.1:8080` | Alamat & port listen internal di balik Nginx |
| `OPER_DATA_DIR` | `/var/tmp/oper` | Direktori fisik penyimpanan file sementara (wajib non-tmpfs) |
| `OPER_ROOM_TTL` | `15m` | Batas waktu idle sebelum ruangan otomatis kedaluwarsa |
| `OPER_ROOM_MAX_LIFE` | `2h` | Batas absolut umur ruangan sejak pertama kali dibuat |
| `OPER_FILE_TTL` | `15m` | Batas umur file sebelum dihapus otomatis oleh janitor |
| `OPER_UPLOAD_TIMEOUT` | `10m` | Batas waktu transfer upload per file |
| `OPER_MAX_FILE_MB` | `50` | Batas ukuran per file (MB) |
| `OPER_MAX_ROOM_MB` | `200` | Batas akumulasi ukuran file per ruangan (MB) |
| `OPER_MAX_TOTAL_MB` | `1024` | Kuota disk global seluruh ruangan aktif (MB) |
| `OPER_TRUST_PROXY` | `true` | Percayai header `X-Real-IP` dari Nginx untuk rate limiter |

*Setelah mengubah file service, selalu jalankan:*  
`sudo systemctl daemon-reload && sudo systemctl restart oper`

---

## 8. Pemantauan & Observabilitas (`/healthz`)

Oper menyediakan endpoint `/healthz` internal tanpa autentikasi untuk memantau penggunaan memori heap runtime Go, goroutine aktif, dan jumlah ruangan/file yang sedang tersimpan:

```bash
curl -s http://127.0.0.1:8080/healthz | jq
```

Contoh respon:
```json
{
  "ok": true,
  "store": {
    "rooms": 1,
    "files": 2,
    "bytes": 2048576,
    "reserved": 0
  },
  "goroutines": 9,
  "heapAllocKB": 384,
  "sysKB": 8192
}
```
*Dengan heap allocation aktif di bawah 1 MB, Oper dapat berjalan sangat stabil bahkan di server VPS dengan spesifikasi terendah sekalipun.*
