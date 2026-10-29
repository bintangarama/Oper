package main

import (
	"context"
	_ "embed"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed web/index.html
var indexHTML []byte

//go:embed web/manifest.json
var manifestJSON []byte

//go:embed web/sw.js
var swJS []byte

//go:embed web/icon-192.png
var icon192PNG []byte

//go:embed web/icon-512.png
var icon512PNG []byte

//go:embed web/favicon.svg
var faviconSVG []byte

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	cfg := loadConfig()

	store, err := NewStore(cfg)
	if err != nil {
		slog.Error("init store", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	h := NewHandlers(cfg, store)
	go store.RunJanitor(ctx, 30*time.Second)
	go h.RunMaintenance(ctx)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           h.Routes(),
		ReadHeaderTimeout: 10 * time.Second, // anti slowloris; body upload diatur per-request
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	go func() {
		<-ctx.Done()
		slog.Info("shutting down")
		store.Shutdown() // tutup stream SSE dulu
		shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shCtx)
	}()

	slog.Info("oper listening", "addr", cfg.Addr, "dataDir", cfg.DataDir,
		"maxFileMB", cfg.MaxFileBytes>>20, "roomTTL", cfg.RoomTTL, "trustProxy", cfg.TrustProxy)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
	store.PurgeDisk() // state di RAM hilang saat restart → file fisik ikut dihapus
	slog.Info("bye")
}
