package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config dibaca dari environment variable berawalan OPER_ (atau QD_ untuk kompatibilitas).
type Config struct {
	Addr            string        // OPER_ADDR
	DataDir         string        // OPER_DATA_DIR (JANGAN tmpfs: file akan memakan RAM)
	RoomTTL         time.Duration // OPER_ROOM_TTL: room hangus setelah idle selama ini
	RoomMaxLife     time.Duration // OPER_ROOM_MAX_LIFE: batas umur maksimum room
	FileTTL         time.Duration // OPER_FILE_TTL: file dihapus setelah selama ini
	UploadTimeout   time.Duration // OPER_UPLOAD_TIMEOUT
	MaxFileBytes    int64         // OPER_MAX_FILE_MB
	MaxRoomBytes    int64         // OPER_MAX_ROOM_MB
	MaxTotalBytes   int64         // OPER_MAX_TOTAL_MB
	MaxRooms        int           // OPER_MAX_ROOMS
	MaxFilesPerRoom int           // OPER_MAX_FILES_PER_ROOM
	TrustProxy      bool          // OPER_TRUST_PROXY: percayai X-Real-IP (hanya di belakang Nginx!)
	Dev             bool          // OPER_DEV: baca web/index.html dari disk setiap request
}

func loadConfig() Config {
	return Config{
		Addr:            env("OPER_ADDR", "QD_ADDR", ":8080"),
		DataDir:         env("OPER_DATA_DIR", "QD_DATA_DIR", filepath.Join(os.TempDir(), "oper")),
		RoomTTL:         envDur("OPER_ROOM_TTL", "QD_ROOM_TTL", 15*time.Minute),
		RoomMaxLife:     envDur("OPER_ROOM_MAX_LIFE", "QD_ROOM_MAX_LIFE", 2*time.Hour),
		FileTTL:         envDur("OPER_FILE_TTL", "QD_FILE_TTL", 15*time.Minute),
		UploadTimeout:   envDur("OPER_UPLOAD_TIMEOUT", "QD_UPLOAD_TIMEOUT", 10*time.Minute),
		MaxFileBytes:    envInt("OPER_MAX_FILE_MB", "QD_MAX_FILE_MB", 50) << 20,
		MaxRoomBytes:    envInt("OPER_MAX_ROOM_MB", "QD_MAX_ROOM_MB", 200) << 20,
		MaxTotalBytes:   envInt("OPER_MAX_TOTAL_MB", "QD_MAX_TOTAL_MB", 1024) << 20,
		MaxRooms:        int(envInt("OPER_MAX_ROOMS", "QD_MAX_ROOMS", 500)),
		MaxFilesPerRoom: int(envInt("OPER_MAX_FILES_PER_ROOM", "QD_MAX_FILES_PER_ROOM", 50)),
		TrustProxy:      envBool("OPER_TRUST_PROXY", "QD_TRUST_PROXY"),
		Dev:             envBool("OPER_DEV", "QD_DEV"),
	}
}

func getEnv(primary, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(primary)); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv(fallback))
}

func env(primary, fallback, def string) string {
	if v := getEnv(primary, fallback); v != "" {
		return v
	}
	return def
}

func envInt(primary, fallback string, def int64) int64 {
	v := getEnv(primary, fallback)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		slog.Warn("invalid int env, using default", "primary", primary, "fallback", fallback, "value", v)
		return def
	}
	return n
}

func envDur(primary, fallback string, def time.Duration) time.Duration {
	v := getEnv(primary, fallback)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		slog.Warn("invalid duration env, using default", "primary", primary, "fallback", fallback, "value", v)
		return def
	}
	return d
}

func envBool(primary, fallback string) bool {
	switch strings.ToLower(getEnv(primary, fallback)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
