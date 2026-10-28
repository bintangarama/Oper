package main

import (
	"math"
	"sync"
	"time"
)

// Limiter adalah token bucket in-memory per key (biasanya IP atau prefix /64).
// Bucket yang sudah penuh kembali dan idle akan dibuang oleh GC, sehingga
// pemakaian memori sebanding dengan jumlah IP yang aktif.
type Limiter struct {
	mu    sync.Mutex
	rate  float64 // token per detik
	burst float64
	m     map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter membuat limiter dengan laju perMinute token/menit dan kapasitas burst.
func NewLimiter(perMinute float64, burst int) *Limiter {
	return &Limiter{rate: perMinute / 60, burst: float64(burst), m: make(map[string]*bucket)}
}

func (l *Limiter) take(key string, consume bool) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b := l.m[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.m[key] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	if consume {
		b.tokens--
	}
	return true
}

// Allow mengonsumsi 1 token jika tersedia.
func (l *Limiter) Allow(key string) bool { return l.take(key, true) }

// Peek mengecek ketersediaan token tanpa mengonsumsi (dipakai untuk "hanya hitung kegagalan").
func (l *Limiter) Peek(key string) bool { return l.take(key, false) }

// Penalize mengonsumsi 1 token (misalnya setelah PIN salah).
func (l *Limiter) Penalize(key string) { l.take(key, true) }

// GC membuang bucket yang sudah terisi penuh kembali.
func (l *Limiter) GC() {
	l.mu.Lock()
	defer l.mu.Unlock()
	refill := time.Duration(l.burst / l.rate * float64(time.Second))
	cutoff := time.Now().Add(-refill)
	for k, b := range l.m {
		if b.last.Before(cutoff) {
			delete(l.m, k)
		}
	}
}
