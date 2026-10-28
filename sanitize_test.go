package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestSanitizeName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"normal.pdf", "normal.pdf"},
		{"../../../etc/passwd", "passwd"},
		{"..\\..\\windows\\system32\\cmd.exe", "cmd.exe"},
		{"laporan:rahasia*v1?.docx", "laporan_rahasia_v1_.docx"},
		{"dokumen \u202Efdp.exe", "dokumen fdp.exe"}, // RTL override dibuang
		{"   spasi   banyak   .pdf   ", "spasi banyak .pdf"},
		{"", "file"},
		{"...", "file"},
		{strings.Repeat("a", 200) + ".pdf", strings.Repeat("a", 116) + ".pdf"},
	}

	for _, c := range cases {
		got := sanitizeName(c.in)
		if got != c.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDetectMimeAndInline(t *testing.T) {
	pdfHead := []byte("%PDF-1.4\n%...")
	pngHead := []byte("\x89PNG\r\n\x1a\n...")
	htmlHead := []byte("<!DOCTYPE html><html><body><script>alert(1)</script></body></html>")
	txtHead := []byte("Hello world plain text document")

	if m := detectMime(pdfHead); m != "application/pdf" || !isInline(m) {
		t.Errorf("PDF detect failed: %s (inline: %v)", m, isInline(m))
	}
	if m := detectMime(pngHead); m != "image/png" || !isInline(m) {
		t.Errorf("PNG detect failed: %s (inline: %v)", m, isInline(m))
	}
	// HTML TIDAK boleh diizinkan inline demi mencegah script execution
	if m := detectMime(htmlHead); isInline(m) {
		t.Errorf("HTML should NOT be inline: %s", m)
	}
	if m := detectMime(txtHead); !strings.HasPrefix(m, "text/plain") || !isInline(m) {
		t.Errorf("TXT detect failed: %s (inline: %v)", m, isInline(m))
	}
}

func TestStoreLifecycle(t *testing.T) {
	cfg := Config{
		DataDir:         t.TempDir(),
		RoomTTL:         500 * time.Millisecond,
		RoomMaxLife:     2 * time.Second,
		FileTTL:         500 * time.Millisecond,
		MaxFileBytes:    10 << 20,
		MaxRoomBytes:    20 << 20,
		MaxTotalBytes:   100 << 20,
		MaxRooms:        10,
		MaxFilesPerRoom: 5,
	}

	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// 1. Buat Room
	roomInfo, err := store.CreateRoom()
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}
	if len(roomInfo.PIN) != 4 {
		t.Errorf("Expected 4-digit PIN, got %s", roomInfo.PIN)
	}

	// 2. Join via PIN
	joinRes, err := store.JoinByPIN(roomInfo.PIN)
	if err != nil {
		t.Fatalf("JoinByPIN failed: %v", err)
	}
	if joinRes.RoomID != roomInfo.RoomID {
		t.Errorf("RoomID mismatch: %s != %s", joinRes.RoomID, roomInfo.RoomID)
	}

	// 3. PIN lama harus sudah hangus (sekali pakai)
	if _, err := store.JoinByPIN(roomInfo.PIN); err == nil {
		t.Errorf("Expected join with used PIN to fail, but succeeded")
	}

	// 4. Reserve Kuota & Commit File
	fileContent := []byte("%PDF-1.7 demo content")
	fileSize := int64(len(fileContent))
	dir, err := store.Reserve(roomInfo.RoomID, joinRes.SenderToken, fileSize)
	if err != nil {
		t.Fatalf("Reserve quota failed: %v", err)
	}
	if dir == "" {
		t.Fatalf("Expected non-empty room dir")
	}

	meta := &FileMeta{
		ID:     "testfid12345",
		Name:   "dokumen.pdf",
		Size:   fileSize,
		Mime:   "application/pdf",
		Inline: true,
	}
	if err := store.Commit(roomInfo.RoomID, meta, fileSize); err != nil {
		t.Fatalf("Commit file failed: %v", err)
	}

	// 5. Verifikasi file bisa diakses receiver
	gotFile, err := store.GetFile(roomInfo.RoomID, meta.ID, roomInfo.ReceiverToken)
	if err != nil {
		t.Fatalf("GetFile failed: %v", err)
	}
	if gotFile.Name != "dokumen.pdf" {
		t.Errorf("Filename mismatch: %s", gotFile.Name)
	}

	// 6. Tunggu TTL dan sweep
	time.Sleep(600 * time.Millisecond)
	store.Sweep(time.Now())

	if _, err := store.GetFile(roomInfo.RoomID, meta.ID, roomInfo.ReceiverToken); err == nil {
		t.Errorf("Expected expired file to be swept, but was still found")
	}
}

func TestStreamToFileLimit(t *testing.T) {
	data := bytes.Repeat([]byte("A"), 1024)
	var dst bytes.Buffer
	size, _, err := streamToFile(&dst, bytes.NewReader(data), 512)
	if err != errTooLarge {
		t.Errorf("Expected errTooLarge, got %v (size: %d)", err, size)
	}
}
