package main

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxNameRunes = 120

// sanitizeName menghasilkan nama tampilan yang aman. Nama ini TIDAK PERNAH
// dipakai sebagai path di disk (di disk selalu dipakai ID acak).
func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == utf8.RuneError, unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			// buang karakter kontrol & format (RTL override U+202E, zero-width, dll.)
		case strings.ContainsRune(`<>:"|?*`, r):
			b.WriteRune('_')
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	clean := strings.Trim(strings.Join(strings.Fields(b.String()), " "), " .")
	if clean == "" {
		return "file"
	}
	return truncateKeepExt(clean, maxNameRunes)
}

func truncateKeepExt(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	ext := []rune(filepath.Ext(s))
	if len(ext) > 16 {
		ext = nil
	}
	base := strings.TrimRight(string(r[:max-len(ext)]), " .")
	return base + string(ext)
}

// detectMime menentukan MIME dari isi file (bukan dari header klien).
func detectMime(head []byte) string {
	if len(head) >= 4 {
		// Office OpenXML / ZIP: PK\x03\x04
		if head[0] == 0x50 && head[1] == 0x4B && head[2] == 0x03 && head[3] == 0x04 {
			s := string(head)
			switch {
			case strings.Contains(s, "xl/"):
				return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
			case strings.Contains(s, "word/"):
				return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
			case strings.Contains(s, "ppt/"):
				return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
			}
			return "application/zip"
		}
		// Legacy MS Office: \xD0\xCF\x11\xE0
		if head[0] == 0xD0 && head[1] == 0xCF && head[2] == 0x11 && head[3] == 0xE0 {
			return "application/msword"
		}
	}
	mt, params, err := mime.ParseMediaType(http.DetectContentType(head))
	if err != nil {
		return "application/octet-stream"
	}
	if mt == "text/plain" {
		if cs := params["charset"]; cs != "" && cs != "utf-8" {
			return "application/octet-stream" // UTF-16 dsb. → unduh saja
		}
		return "text/plain; charset=utf-8"
	}
	return mt
}

// repairFilename melengkapi ekstensi file jika klien HP mengirim file tanpa ekstensi (misal UUID cache)
func repairFilename(name, mt string) string {
	ext := filepath.Ext(name)
	if ext != "" {
		return name
	}
	switch mt {
	case "application/pdf":
		return name + ".pdf"
	case "image/jpeg":
		return name + ".jpg"
	case "image/png":
		return name + ".png"
	case "image/webp":
		return name + ".webp"
	case "image/gif":
		return name + ".gif"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return name + ".xlsx"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return name + ".docx"
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return name + ".pptx"
	case "application/msword":
		return name + ".doc"
	case "application/zip":
		return name + ".zip"
	case "text/plain; charset=utf-8":
		return name + ".txt"
	}
	return name
}

// isInline: hanya tipe yang aman dirender & dicetak di browser.
// HTML/SVG/XML sengaja TIDAK termasuk (risiko XSS) dan selalu diunduh.
func isInline(mt string) bool {
	switch mt {
	case "application/pdf", "image/png", "image/jpeg", "image/gif", "image/webp", "text/plain; charset=utf-8":
		return true
	}
	return false
}

func contentDisposition(attachment bool, name string) string {
	kind := "inline"
	if attachment {
		kind = "attachment"
	}
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, kind, asciiFallback(name), rfc5987(name))
}

func asciiFallback(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' || r == '%' {
			b.WriteByte('_')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func rfc5987(s string) string {
	const hexd = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			strings.IndexByte("!#$&+-.^_`|~", c) >= 0 {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hexd[c>>4])
			b.WriteByte(hexd[c&0x0f])
		}
	}
	return b.String()
}

var (
	errTooLarge = errors.New("file too large")
	errEmpty    = errors.New("empty file")
)

// streamToFile menyalin src ke dst dengan batas ukuran, sambil mengendus
// MIME dari 512 byte pertama. Memori yang dipakai konstan (~32 KB buffer).
func streamToFile(dst io.Writer, src io.Reader, max int64) (int64, string, error) {
	head := make([]byte, 512)
	n, err := io.ReadFull(src, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return 0, "", err
	}
	if n == 0 {
		return 0, "", errEmpty
	}
	head = head[:n]
	ctype := detectMime(head)
	if _, err := dst.Write(head); err != nil {
		return 0, "", err
	}
	rest, err := io.Copy(dst, io.LimitReader(src, max-int64(n)+1))
	total := int64(n) + rest
	if err != nil {
		return total, ctype, err
	}
	if total > max {
		return total, ctype, errTooLarge
	}
	return total, ctype, nil
}
