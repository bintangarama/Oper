package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	multipartOverhead = 64 << 10 // ruang untuk header multipart di atas MaxFileBytes
	jsonBodyLimit     = 1 << 10
	sseHeartbeat      = 25 * time.Second
	maxSubsPerRoom    = 10
)

const indexCSP = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com https://cdn.jsdelivr.net; " +
	"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src https://fonts.gstatic.com; img-src 'self' data: blob:; connect-src 'self'; " +
	"frame-src 'self'; worker-src 'self'; manifest-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

type Handlers struct {
	cfg   Config
	store *Store

	createRL        *Limiter // pembuatan room per IP
	joinRL          *Limiter // semua percobaan join per IP
	pinFailRL       *Limiter // HANYA PIN salah per IP
	pinFailGlobalRL *Limiter // PIN salah global (anti botnet)
	uploadRL        *Limiter
}

func NewHandlers(cfg Config, s *Store) *Handlers {
	return &Handlers{
		cfg: cfg, store: s,
		createRL:        NewLimiter(10, 5),
		joinRL:          NewLimiter(20, 10),
		pinFailRL:       NewLimiter(0.5, 5), // 5x salah, lalu 1 percobaan / 2 menit
		pinFailGlobalRL: NewLimiter(60, 120),
		uploadRL:        NewLimiter(30, 15),
	}
}

func (h *Handlers) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.index)
	mux.HandleFunc("GET /manifest.json", h.manifest)
	mux.HandleFunc("GET /sw.js", h.serviceWorker)
	mux.HandleFunc("GET /icon-192.png", h.icon192)
	mux.HandleFunc("GET /icon-512.png", h.icon512)
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("POST /api/rooms", h.createRoom)
	mux.HandleFunc("POST /api/join", h.join)
	mux.HandleFunc("DELETE /api/rooms/{id}", h.deleteRoom)
	mux.HandleFunc("GET /api/rooms/{id}/events", h.events)
	mux.HandleFunc("POST /api/rooms/{id}/files", h.upload)
	mux.HandleFunc("POST /api/rooms/{id}/text", h.sendText)
	mux.HandleFunc("GET /api/rooms/{id}/files/{fid}", h.download)
	mux.HandleFunc("GET /api/rooms/{id}/zip", h.downloadZip)
	mux.HandleFunc("DELETE /api/rooms/{id}/files/{fid}", h.deleteFile)
	return secureHeaders(mux)
}

// RunMaintenance membersihkan bucket rate limiter yang idle.
func (h *Handlers) RunMaintenance(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, l := range []*Limiter{h.createRL, h.joinRL, h.pinFailRL, h.pinFailGlobalRL, h.uploadRL} {
				l.GC()
			}
		}
	}
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer") // token di query tidak bocor via Referer
		hd.Set("X-Frame-Options", "SAMEORIGIN")  // iframe cetak = same-origin
		hd.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		next.ServeHTTP(w, r)
	})
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func storeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "Ruangan atau file tidak ditemukan / sudah kedaluwarsa")
	case errors.Is(err, ErrForbidden):
		writeErr(w, http.StatusForbidden, "Akses ditolak")
	case errors.Is(err, ErrQuota):
		writeErr(w, http.StatusRequestEntityTooLarge, "Kuota ruangan penuh, hapus beberapa file dulu")
	case errors.Is(err, ErrCapacity):
		writeErr(w, http.StatusServiceUnavailable, "Server sedang penuh, coba lagi sebentar")
	case errors.Is(err, ErrTooMany):
		writeErr(w, http.StatusTooManyRequests, "Terlalu banyak tab terbuka untuk ruangan ini")
	default:
		slog.Error("internal error", "err", err)
		writeErr(w, http.StatusInternalServerError, "Terjadi kesalahan internal")
	}
}

func bearer(r *http.Request) string {
	if v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// clientIP: IPv6 dikelompokkan per /64 agar rotasi alamat tidak bisa mengakali limiter.
func (h *Handlers) clientIP(r *http.Request) string {
	raw := ""
	if h.cfg.TrustProxy {
		raw = strings.TrimSpace(r.Header.Get("X-Real-IP"))
	}
	if raw == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		raw = host
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return raw
	}
	addr = addr.Unmap()
	if addr.Is6() {
		p, _ := addr.Prefix(64)
		return p.String()
	}
	return addr.String()
}

func validPIN(p string) bool {
	if len(p) != 4 {
		return false
	}
	for i := 0; i < 4; i++ {
		if p[i] < '0' || p[i] > '9' {
			return false
		}
	}
	return true
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// ---------- handlers ----------

func (h *Handlers) index(w http.ResponseWriter, r *http.Request) {
	body := indexHTML
	if h.cfg.Dev {
		if b, err := os.ReadFile(filepath.Join("web", "index.html")); err == nil {
			body = b
		}
	}
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Cache-Control", "no-cache")
	hd.Set("Content-Security-Policy", indexCSP)
	_, _ = w.Write(body)
}

func (h *Handlers) manifest(w http.ResponseWriter, r *http.Request) {
	body := manifestJSON
	if h.cfg.Dev {
		if b, err := os.ReadFile(filepath.Join("web", "manifest.json")); err == nil {
			body = b
		}
	}
	hd := w.Header()
	hd.Set("Content-Type", "application/manifest+json; charset=utf-8")
	hd.Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(body)
}

func (h *Handlers) serviceWorker(w http.ResponseWriter, r *http.Request) {
	body := swJS
	if h.cfg.Dev {
		if b, err := os.ReadFile(filepath.Join("web", "sw.js")); err == nil {
			body = b
		}
	}
	hd := w.Header()
	hd.Set("Content-Type", "application/javascript; charset=utf-8")
	hd.Set("Cache-Control", "no-cache")
	_, _ = w.Write(body)
}

func (h *Handlers) icon192(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	hd.Set("Content-Type", "image/png")
	hd.Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(icon192PNG)
}

func (h *Handlers) icon512(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	hd.Set("Content-Type", "image/png")
	hd.Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(icon512PNG)
}

func (h *Handlers) health(w http.ResponseWriter, r *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "store": h.store.Stats(), "goroutines": runtime.NumGoroutine(),
		"heapAllocKB": ms.HeapAlloc >> 10, "sysKB": ms.Sys >> 10,
	})
}

func (h *Handlers) createRoom(w http.ResponseWriter, r *http.Request) {
	if !h.createRL.Allow(h.clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "Terlalu sering membuat ruangan, tunggu sebentar")
		return
	}
	info, err := h.store.CreateRoom()
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

func (h *Handlers) join(w http.ResponseWriter, r *http.Request) {
	ip := h.clientIP(r)
	if !h.joinRL.Allow(ip) {
		writeErr(w, http.StatusTooManyRequests, "Terlalu banyak percobaan, tunggu sebentar")
		return
	}
	var req struct {
		PIN     string `json:"pin"`
		RoomID  string `json:"roomId"`
		JoinKey string `json:"joinKey"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, jsonBodyLimit)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "Permintaan tidak valid")
		return
	}

	// Jalur QR: joinKey 128-bit, brute force tidak realistis.
	if req.RoomID != "" {
		res, err := h.store.JoinByKey(req.RoomID, req.JoinKey)
		if err != nil {
			writeErr(w, http.StatusNotFound, "QR sudah tidak berlaku. Scan ulang QR terbaru di layar PC.")
			return
		}
		writeJSON(w, http.StatusOK, res)
		return
	}

	// Jalur PIN: yang dihitung hanya kegagalan, jadi user normal tidak terhambat.
	if !validPIN(req.PIN) {
		writeErr(w, http.StatusBadRequest, "PIN harus 4 digit angka")
		return
	}
	if !h.pinFailRL.Peek(ip) {
		writeErr(w, http.StatusTooManyRequests, "Terlalu banyak PIN salah. Tunggu 2 menit atau scan QR di layar PC.")
		return
	}
	if !h.pinFailGlobalRL.Peek("*") {
		writeErr(w, http.StatusServiceUnavailable, "Login PIN sementara dinonaktifkan. Silakan scan QR di layar PC.")
		return
	}
	res, err := h.store.JoinByPIN(req.PIN)
	if err != nil {
		h.pinFailRL.Penalize(ip)
		h.pinFailGlobalRL.Penalize("*")
		slog.Info("pin join failed", "ip", ip)
		writeErr(w, http.StatusNotFound, "PIN salah atau ruangan sudah kedaluwarsa")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handlers) deleteRoom(w http.ResponseWriter, r *http.Request) {
	if err := h.store.DeleteRoom(r.PathValue("id"), bearer(r)); err != nil {
		storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// events: stream SSE untuk receiver. Token lewat query karena EventSource
// tidak bisa mengirim header kustom.
func (h *Handlers) events(w http.ResponseWriter, r *http.Request) {
	ch, snap, unsub, err := h.store.Subscribe(r.PathValue("id"), r.URL.Query().Get("t"), maxSubsPerRoom)
	if err != nil {
		storeErr(w, err) // non-200 → EventSource berhenti retry → frontend buat room baru
		return
	}
	defer unsub()

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // stream jangka panjang

	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream")
	hd.Set("Cache-Control", "no-cache, no-transform")
	hd.Set("X-Accel-Buffering", "no") // Nginx: jangan buffer
	w.WriteHeader(http.StatusOK)

	if _, err := io.WriteString(w, "retry: 3000\n\n"); err != nil {
		return
	}
	if writeSSE(w, Event{"snapshot", snap}) != nil || rc.Flush() != nil {
		return
	}

	ping := time.NewTicker(sseHeartbeat)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if writeSSE(w, ev) != nil || rc.Flush() != nil {
				return
			}
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

func writeSSE(w io.Writer, ev Event) error {
	data, err := json.Marshal(ev.Data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
	return err
}

func firstFilePart(r *http.Request) (*multipart.Part, error) {
	mr, err := r.MultipartReader() // streaming, TIDAK mem-buffer seperti ParseMultipartForm
	if err != nil {
		return nil, err
	}
	for i := 0; i < 5; i++ {
		p, err := mr.NextPart()
		if err != nil {
			return nil, err
		}
		if p.FormName() == "file" && p.FileName() != "" {
			return p, nil
		}
		_ = p.Close()
	}
	return nil, errors.New("no file part")
}

// upload: satu file per request, di-stream langsung ke disk.
func (h *Handlers) upload(w http.ResponseWriter, r *http.Request) {
	if !h.uploadRL.Allow(h.clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "Terlalu banyak unggahan, tunggu sebentar")
		return
	}
	id := r.PathValue("id")
	if r.ContentLength <= 0 {
		writeErr(w, http.StatusLengthRequired, "Content-Length wajib diisi")
		return
	}
	limit := h.cfg.MaxFileBytes + multipartOverhead
	if r.ContentLength > limit {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("Ukuran file maksimal %d MB", h.cfg.MaxFileBytes>>20))
		return
	}

	// 1) Pesan kuota berdasarkan Content-Length (auth sender sekaligus dicek di sini).
	dir, err := h.store.Reserve(id, bearer(r), r.ContentLength)
	if err != nil {
		storeErr(w, err)
		return
	}
	reserved := r.ContentLength
	defer func() {
		if reserved > 0 {
			h.store.Release(id, reserved)
		}
	}()

	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(h.cfg.UploadTimeout))
	r.Body = http.MaxBytesReader(w, r.Body, limit)

	part, err := firstFilePart(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Form unggahan tidak valid (field 'file' wajib)")
		return
	}
	defer part.Close()

	meta := &FileMeta{ID: randHex(12), Name: sanitizeName(part.FileName())}
	tmpPath := filepath.Join(dir, meta.ID+".part")
	finalPath := filepath.Join(dir, meta.ID)

	// 2) Tulis ke .part. Apa pun kegagalannya, defer di bawah membersihkan.
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		storeErr(w, ErrNotFound) // folder room sudah dihapus (expired/ditutup)
		return
	}
	cleanup := tmpPath
	defer func() {
		_ = f.Close()
		if cleanup != "" {
			_ = os.Remove(cleanup)
			h.store.Notify(id, Event{"incoming_failed", map[string]string{"id": meta.ID}})
		}
	}()

	h.store.Notify(id, Event{"incoming", map[string]any{"id": meta.ID, "name": meta.Name, "size": r.ContentLength}})

	size, ctype, err := streamToFile(f, part, h.cfg.MaxFileBytes)
	var mbe *http.MaxBytesError
	switch {
	case errors.Is(err, errTooLarge), errors.As(err, &mbe):
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("Ukuran file maksimal %d MB", h.cfg.MaxFileBytes>>20))
		return
	case errors.Is(err, errEmpty):
		writeErr(w, http.StatusBadRequest, "File kosong")
		return
	case err != nil:
		slog.Warn("upload aborted", "room", short(id), "err", err)
		writeErr(w, http.StatusBadRequest, "Unggahan terputus")
		return
	}
	if err := f.Close(); err != nil {
		storeErr(w, err)
		return
	}

	// 3) Rename atomik → commit metadata → broadcast ke penerima.
	if err := os.Rename(tmpPath, finalPath); err != nil {
		storeErr(w, ErrNotFound)
		return
	}
	cleanup = finalPath
	meta.Name = repairFilename(meta.Name, ctype)
	meta.Size, meta.Mime, meta.Inline, meta.path = size, ctype, isInline(ctype), finalPath
	err = h.store.Commit(id, meta, reserved)
	reserved = 0 // Commit selalu melepas reservasi
	if err != nil {
		storeErr(w, err)
		return
	}
	cleanup = ""
	slog.Info("file uploaded", "room", short(id), "size", size, "mime", ctype)
	writeJSON(w, http.StatusCreated, meta)
}

type sendTextReq struct {
	Text string `json:"text"`
}

func (h *Handlers) sendText(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	token := bearer(r)
	if token == "" {
		writeErr(w, http.StatusUnauthorized, "Token pengirim wajib disertakan")
		return
	}

	var req sendTextReq
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10) // batas 128 KB
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "Format JSON tidak valid")
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeErr(w, http.StatusBadRequest, "Teks tidak boleh kosong")
		return
	}

	textBytes := []byte(text)
	textLen := int64(len(textBytes))

	dir, err := h.store.Reserve(id, token, textLen)
	if err != nil {
		storeErr(w, err)
		return
	}
	defer h.store.Release(id, textLen)

	fid := randHex(12)
	tmpPath := filepath.Join(dir, fid+".part")
	finalPath := filepath.Join(dir, fid)

	if err := os.WriteFile(tmpPath, textBytes, 0o600); err != nil {
		storeErr(w, err)
		return
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		storeErr(w, err)
		return
	}

	name := "Catatan Teks"
	isURL := strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://")
	if isURL && !strings.ContainsAny(text, " \t\r\n") {
		name = "Tautan: " + text
		if len(name) > 60 {
			name = name[:57] + "…"
		}
	} else {
		firstLine := strings.SplitN(text, "\n", 2)[0]
		firstLine = strings.TrimSpace(firstLine)
		if len(firstLine) > 50 {
			firstLine = firstLine[:47] + "…"
		}
		if firstLine != "" {
			name = firstLine
		}
	}

	meta := &FileMeta{
		ID:     fid,
		Name:   name,
		Size:   textLen,
		Mime:   "text/plain; charset=utf-8",
		Inline: true,
		Text:   text,
		path:   finalPath,
	}

	if err := h.store.Commit(id, meta, textLen); err != nil {
		_ = os.Remove(finalPath)
		storeErr(w, err)
		return
	}

	slog.Info("text shared", "room", short(id), "len", textLen)
	writeJSON(w, http.StatusCreated, meta)
}

func (h *Handlers) download(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	m, err := h.store.GetFile(r.PathValue("id"), r.PathValue("fid"), q.Get("t"))
	if err != nil {
		storeErr(w, err)
		return
	}
	f, err := os.Open(m.path)
	if err != nil {
		storeErr(w, ErrNotFound)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		storeErr(w, err)
		return
	}

	attach := q.Get("dl") == "1" || !m.Inline
	ctype := m.Mime
	if !m.Inline {
		ctype = "application/octet-stream"
	}
	hd := w.Header()
	hd.Set("Content-Type", ctype)
	dlName := m.Name
	if m.Text != "" {
		if strings.HasPrefix(m.Text, "http://") || strings.HasPrefix(m.Text, "https://") {
			dlName = "tautan.txt"
		} else {
			dlName = sanitizeName(dlName)
			if dlName == "" || dlName == "_" {
				dlName = "catatan"
			}
			if !strings.HasSuffix(strings.ToLower(dlName), ".txt") {
				dlName += ".txt"
			}
		}
	}
	hd.Set("Content-Disposition", contentDisposition(attach, dlName))
	hd.Set("Cache-Control", "private, no-store")
	if m.Mime != "application/pdf" {
		// Gambar/teks: sandbox total. PDF dikecualikan agar viewer bawaan browser tetap jalan.
		hd.Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; sandbox")
	}
	http.ServeContent(w, r, "", st.ModTime(), f) // mendukung Range (dipakai PDF viewer)
}

func (h *Handlers) deleteFile(w http.ResponseWriter, r *http.Request) {
	if err := h.store.DeleteFile(r.PathValue("id"), r.PathValue("fid"), bearer(r)); err != nil {
		storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) downloadZip(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	token := r.URL.Query().Get("t")

	files, err := h.store.ListFiles(id, token)
	if err != nil {
		storeErr(w, err)
		return
	}
	if len(files) == 0 {
		writeErr(w, http.StatusNotFound, "Tidak ada file untuk diunduh")
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="oper-files.zip"`)
	w.Header().Set("Cache-Control", "no-cache")

	zw := zip.NewWriter(w)
	defer zw.Close()

	usedNames := make(map[string]int)

	for _, f := range files {
		diskFile, err := os.Open(f.path)
		if err != nil {
			continue
		}

		name := sanitizeName(f.Name)
		if f.Text != "" && !strings.Contains(name, ".") {
			name += ".txt"
		}
		if count := usedNames[name]; count > 0 {
			ext := filepath.Ext(name)
			base := strings.TrimSuffix(name, ext)
			name = fmt.Sprintf("%s (%d)%s", base, count, ext)
		}
		usedNames[name]++

		hdr := &zip.FileHeader{
			Name:     name,
			Method:   zip.Deflate,
			Modified: f.CreatedAt,
		}
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			diskFile.Close()
			continue
		}
		_, _ = io.Copy(fw, diskFile)
		diskFile.Close()
	}
}
