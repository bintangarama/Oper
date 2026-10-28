# Oper ⚡

> **Ultra-lightweight, zero-login, ephemeral file & text transfer between devices via PIN / QR.**  
> *Solusi transfer dokumen, foto, teks, dan tautan kilat tanpa ribet login, tanpa database, dan tanpa meninggalkan jejak sesi di komputer kantor/publik.*

---

## 💡 Masalah yang Diselesaikan

Di lingkungan kantor, rental komputer, atau perangkat publik, sering kali kita harus **login WhatsApp Web atau Google Drive hanya untuk mengirim dokumen PDF atau foto yang ingin dicetak (*print*)**. 

Kebiasaan ini menimbulkan risiko nyata:
- ❌ **Sesi tertinggal / lupa logout:** Riwayat chat pribadi dan kontak bisa dibaca oleh pengguna berikutnya.
- ❌ **Notifikasi pesan pribadi muncul di layar:** Menimbulkan rasa canggung (*awkward*) saat layar dilihat rekan kerja.
- ❌ **Folder unduhan penuh:** File kiriman menumpuk dan tertinggal di folder *Downloads* komputer umum.

**Oper** hadir sebagai solusi langsung:
- ✅ **Buka web, masukkan PIN 4 digit atau scan QR, kirim file/teks, selesai.**
- ✅ **Tanpa login, tanpa database, dan otomatis terhapus (*ephemeral*).**
- ✅ **Langsung cetak (*direct print*) via dialog browser tanpa mengotori folder unduhan.**
- ✅ **Hemat memori ekstrem:** Konsumsi RAM idle hanya ~6–10 MB RSS.

---

## ✨ Fitur Utama

- 🔄 **Dua Arah & Bebas Perangkat:** Kirim atau terima file dari HP ke PC, PC ke HP, laptop ke HP, atau antar ponsel.
- ⚡ **Zero-Login & Kode Ruangan Instan:** Terhubung dalam 2 detik menggunakan PIN 4 digit acak atau QR Code di layar.
- 📡 **Real-Time via Server-Sent Events (SSE):** Layar penerima otomatis menampilkan kartu dokumen begitu file selesai diunggah tanpa perlu *refresh*.
- 🔔 **Web Audio Chime Notification:** Notifikasi suara dua nada sintetis (E6 ➔ B6) yang lembut dan elegan saat kiriman tiba (100% Web Audio API murni tanpa file audio eksternal).
- 📦 **Unduh Semua File sebagai ZIP:** Tombol streaming ZIP unbuffered muncul saat ada $\ge$ 2 file, langsung dikemas tanpa membebani RAM server.
- 🔗 **Salin Tautan Ruangan Instan:** Sekali klik untuk membagikan tautan langsung (`#join=...`) agar perangkat lain terhubung otomatis tanpa mengetik PIN manual.
- 🖨️ **Direct Silent Print Dialog:** Dokumen PDF dan gambar dapat langsung dicetak (*print*) via invisible iframe.
- 🔍 **Smart Magic-Bytes Sniffing:** Mendeteksi tipe asli file dan melengkapi ekstensi yang hilang (seperti UUID cache bawaan ponsel) secara otomatis.
- 📱 **Multi-Source Mobile Picker:** Tombol terpisah untuk Dokumen, Galeri Foto, dan Kamera langsung di ponsel.
- 📋 **Kirim Catatan & Tautan Kilat (Clipboard Sync):** Mengirim teks, catatan, kode, atau tautan URL dengan tombol 1-klik *Salin* dan *Buka Link*.
- 📲 **PWA Ready (Add to Home Screen):** Mendukung Progressive Web App dengan Service Worker offline shell untuk dipasang di layar utama smartphone.
- 🧹 **Ephemeral & Auto-Purge:** File dan ruangan memiliki masa kedaluwarsa otomatis (default 15 menit) dan dibersihkan dari disk oleh background *janitor*.
- 🪶 **Zero External Dependencies:** Dibangun 100% menggunakan Go standard library murni. Tanpa SQL, tanpa Redis, tanpa `node_modules`.

---

## 🏗️ Arsitektur Sistem

```text
[ Pengirim: HP / PC ]                           [ Penerima: PC / HP ]
         │                                               │
         │ 1. POST /api/join {pin / joinKey}             │ 0. POST /api/rooms
         │ ──────────────────────► [ Oper Server ] ◄─────────────────────────
         │   (Dapatkan senderToken)     │ (Go 1.22+)     │ (Dapatkan receiverToken, PIN, QR)
         │                              │                │
         │                              │                │ 2. GET /api/rooms/{id}/events (SSE)
         │                              │                │ ◄─────────────────────────────────
         │ 3. POST /api/rooms/{id}/files│                │
         │    (Direct Disk Streaming)   │                │
         │ ────────────────────────────►│                │
         │                              │ 4. SSE: event "file" (metadata)
         │                              │ ──────────────────────────────────►
         │                              │                │
         │                              │                │ 5. GET /api/rooms/{id}/files/{fid}
         │                              │                │    (Inline Preview / Direct Print)
         │                              │                │    ATAU: GET /api/rooms/{id}/zip
         │                              │                │ ◄─────────────────────────────────
```

1. **Upload Unbuffered Direct-to-Disk:** File yang diunggah tidak ditampung di memori RAM server, melainkan di-*stream* langsung dari *socket* HTTP ke partisi disk fisik sebagai file `.part` dengan batas kuota ketat (`io.LimitReader`).
2. **Streaming ZIP Langsung:** Arsip `.zip` di-stream langsung ke socket respon HTTP tanpa buffering di memori.
3. **Server-Sent Events (SSE) vs WebSocket:** Menggunakan SSE satu arah (*HTTP/1.1 chunked*) yang sangat ringan dibandingkan WebSocket: ramah proxy HTTP/2, auto-reconnect bawaan browser, dan konsumsi memori per koneksi yang sangat minim.

---

## 🚀 Memulai Cepat (Pengujian Lokal)

### Prasyarat
- [Go 1.22+](https://go.dev/dl/) (opsional, jika ingin mengompilasi dari source code)
- Atau cukup unduh executable binary yang telah dikompilasi.

### 1. Jalankan Langsung dari Source Code
```bash
go run .
```

### 2. Kompilasi Biner Sendiri
```bash
# Windows
go build -ldflags="-s -w" -o oper.exe .
.\oper.exe

# Linux / macOS
go build -ldflags="-s -w" -o oper .
./oper
```

Buka browser di:
```text
http://localhost:8080/
```

---

## 🌐 Panduan Deployment & Pembaruan Versi (*Update*) di VPS

Oper dirancang untuk dapat di-*self-host* dengan sangat mudah di VPS Linux dengan konsumsi RAM di bawah 50 MB menggunakan **Systemd** dan **Nginx**.

📖 **Panduan teknis lengkap langkah demi langkah untuk setup domain, instalasi Nginx, SSL Let's Encrypt, hingga alur pembaruan versi (*update*) dapat dibaca di:**  
👉 **[DEPLOY.md](DEPLOY.md)**

---

## ⚙️ Konfigurasi (Environment Variables)

Semua konfigurasi bersifat opsional dan memiliki nilai bawaan yang aman:

| Variabel | Tipe Data | Default | Keterangan |
|---|---|---|---|
| `OPER_ADDR` | String | `:8080` | Alamat & port listen HTTP server |
| `OPER_DATA_DIR` | Path | `os.TempDir()/oper` | Direktori fisik penyimpanan file sementara (wajib non-tmpfs) |
| `OPER_ROOM_TTL` | Duration | `15m` | Batas waktu idle sebelum ruangan otomatis kedaluwarsa |
| `OPER_ROOM_MAX_LIFE` | Duration | `2h` | Batas maksimum absolut umur ruangan sejak pertama kali dibuat |
| `OPER_FILE_TTL` | Duration | `15m` | Batas umur file sebelum dihapus otomatis oleh janitor |
| `OPER_UPLOAD_TIMEOUT` | Duration | `10m` | Batas waktu maksimum durasi transfer upload per file |
| `OPER_MAX_FILE_MB` | Integer | `50` | Batas ukuran maksimum per file (MB) |
| `OPER_MAX_ROOM_MB` | Integer | `200` | Batas total ukuran file per ruangan (MB) |
| `OPER_MAX_TOTAL_MB` | Integer | `1024` | Batas kapasitas disk global untuk seluruh ruangan aktif (MB) |
| `OPER_MAX_ROOMS` | Integer | `500` | Batas maksimum jumlah ruangan aktif bersamaan |
| `OPER_MAX_FILES_PER_ROOM` | Integer | `50` | Batas jumlah file maksimum dalam satu ruangan |
| `OPER_TRUST_PROXY` | Boolean | `false` | Set `true` jika berjalan di belakang reverse proxy Nginx |
| `OPER_DEV` | Boolean | `false` | Jika `true`, memuat file `web/index.html` langsung dari disk (mode styling) |

---

## 📡 Spesifikasi API & Real-Time SSE

Semua endpoint mengembalikan format JSON standar:

| Method | Endpoint | Akses | Deskripsi |
|---|---|---|---|
| `POST` | `/api/rooms` | Publik | Membuat ruangan penerima baru (mengembalikan `roomId`, `receiverToken`, `pin`, `joinKey`). |
| `POST` | `/api/join` | Publik | Bergabung ke ruangan sebagai pengirim (`{"pin":"1234"}` atau `{"roomId":"..","joinKey":".."}`). |
| `GET` | `/api/rooms/{id}/events?t={token}` | Receiver / Sender | Real-time SSE stream (`file`, `incoming`, `joined`, `expired`, `file_deleted`). |
| `POST` | `/api/rooms/{id}/files` | Sender (`Bearer <token>`) | Upload file multipart streaming langsung ke disk. |
| `POST` | `/api/rooms/{id}/text` | Sender (`Bearer <token>`) | Berbagi catatan teks, token, atau tautan URL (`{"text":"..."}`). |
| `GET` | `/api/rooms/{id}/files/{fid}?t={token}&dl={0\|1}` | Receiver | Unduh file (`&dl=1`) atau pratinjau inline (`&dl=0`). |
| `GET` | `/api/rooms/{id}/zip?t={token}` | Receiver | Unduh seluruh file dalam ruangan sebagai arsip `.zip` tunggal secara streaming. |
| `DELETE` | `/api/rooms/{id}/files/{fid}` | Receiver (`Bearer <token>`) | Menghapus file fisik dari disk sebelum masa TTL habis. |
| `DELETE` | `/api/rooms/{id}` | Receiver (`Bearer <token>`) | Menutup ruangan dan menghapus seluruh file seketika. |
| `GET` | `/manifest.json` | Publik | Web App Manifest untuk instalasi PWA di smartphone. |
| `GET` | `/sw.js` | Publik | Service Worker untuk offline app shell caching. |
| `GET` | `/healthz` | Publik / Internal | Endpoint metrik & observabilitas kesehatan memori server. |

---

## 🧪 Pengujian Unit

Pengujian unit pada fungsi sanitasi nama file, MIME sniffing, kuota disk streaming, dan siklus hidup ruangan:

```bash
go test -v .
```

---

## 📄 Lisensi

Didistribusikan di bawah lisensi [MIT](LICENSE). Bebas digunakan, dimodifikasi, dan di-*self-host* untuk kebutuhan pribadi maupun komersial.
