package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
	ErrQuota     = errors.New("room quota exceeded")
	ErrCapacity  = errors.New("server at capacity")
	ErrTooMany   = errors.New("too many subscribers")
)

// FileMeta tidak berubah setelah di-commit, jadi aman dibagikan antar goroutine.
type FileMeta struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	Mime      string    `json:"mime"`
	Inline    bool      `json:"inline"`
	Text      string    `json:"text,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	path      string
}

type Event struct {
	Type string
	Data any
}

type Room struct {
	id, pin, joinKey           string
	receiverToken, senderToken string
	dir                        string
	createdAt, expiresAt       time.Time
	files                      map[string]*FileMeta
	bytes, reserved            int64
	senders                    int
	subs                       map[chan Event]struct{}
}

// RoomInfo dikirim HANYA ke receiver (PC). receiverToken hanya diisi saat create.
type RoomInfo struct {
	RoomID        string    `json:"roomId"`
	PIN           string    `json:"pin"`
	JoinKey       string    `json:"joinKey"`
	ReceiverToken string    `json:"receiverToken,omitempty"`
	ExpiresAt     time.Time `json:"expiresAt"`
	Now           time.Time `json:"now"` // untuk koreksi selisih jam klien
	TTLSeconds    int       `json:"ttlSeconds"`
	MaxFileBytes  int64     `json:"maxFileBytes"`
	Senders       int       `json:"senders"`
}

type JoinResult struct {
	RoomID       string    `json:"roomId"`
	SenderToken  string    `json:"senderToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
	MaxFileBytes int64     `json:"maxFileBytes"`
}

type Snapshot struct {
	Room  RoomInfo    `json:"room"`
	Files []*FileMeta `json:"files"`
}

type Stats struct {
	Rooms    int   `json:"rooms"`
	Files    int   `json:"files"`
	Bytes    int64 `json:"bytes"`
	Reserved int64 `json:"reserved"`
}

// Store menyimpan seluruh state di RAM, dijaga oleh satu mutex.
// Untuk skala ratusan room ini lebih cepat & sederhana daripada lock per room.
type Store struct {
	cfg           Config
	mu            sync.Mutex
	rooms         map[string]*Room
	pins          map[string]*Room
	totalBytes    int64
	totalReserved int64
}

func NewStore(cfg Config) (*Store, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{cfg: cfg, rooms: make(map[string]*Room), pins: make(map[string]*Room)}
	s.PurgeDisk()
	return s, nil
}

// PurgeDisk menghapus sisa folder room (misalnya setelah crash). Demi keamanan,
// hanya folder dengan format ID room (32 hex) yang disentuh.
func (s *Store) PurgeDisk() {
	entries, err := os.ReadDir(s.cfg.DataDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && isRoomID(e.Name()) {
			_ = os.RemoveAll(filepath.Join(s.cfg.DataDir, e.Name()))
		}
	}
}

func isRoomID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !((s[i] >= '0' && s[i] <= '9') || (s[i] >= 'a' && s[i] <= 'f')) {
			return false
		}
	}
	return true
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func tokenEq(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// newPINLocked menghasilkan PIN 4 digit yang belum dipakai room aktif lain.
func (s *Store) newPINLocked() (string, bool) {
	if len(s.pins) >= 5000 { // ruang PIN terlalu padat → tebakan jadi terlalu mudah
		return "", false
	}
	for i := 0; i < 64; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(10000))
		if err != nil {
			panic(err)
		}
		pin := fmt.Sprintf("%04d", n.Int64())
		if _, used := s.pins[pin]; !used {
			return pin, true
		}
	}
	return "", false
}

func (r *Room) info(cfg Config) RoomInfo {
	return RoomInfo{
		RoomID: r.id, PIN: r.pin, JoinKey: r.joinKey, ExpiresAt: r.expiresAt, Now: time.Now(),
		TTLSeconds: int(cfg.RoomTTL.Seconds()), MaxFileBytes: cfg.MaxFileBytes, Senders: r.senders,
	}
}

// touch memperpanjang umur room (sliding TTL) sampai batas RoomMaxLife.
func (s *Store) touchLocked(r *Room, now time.Time) {
	exp := now.Add(s.cfg.RoomTTL)
	if limit := r.createdAt.Add(s.cfg.RoomMaxLife); exp.After(limit) {
		exp = limit
	}
	if exp.After(r.expiresAt) {
		r.expiresAt = exp
	}
}

func (s *Store) liveLocked(id string) *Room {
	r := s.rooms[id]
	if r == nil || time.Now().After(r.expiresAt) {
		return nil
	}
	return r
}

func (s *Store) authLocked(id, token string, receiver bool) (*Room, error) {
	r := s.liveLocked(id)
	if r == nil {
		return nil, ErrNotFound
	}
	want := r.senderToken
	if receiver {
		want = r.receiverToken
	}
	if !tokenEq(token, want) {
		return nil, ErrForbidden
	}
	return r, nil
}

func (s *Store) CreateRoom() (RoomInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.rooms) >= s.cfg.MaxRooms {
		return RoomInfo{}, ErrCapacity
	}
	pin, ok := s.newPINLocked()
	if !ok {
		return RoomInfo{}, ErrCapacity
	}
	now := time.Now()
	r := &Room{
		id: randHex(16), pin: pin, joinKey: randHex(16),
		receiverToken: randHex(24), senderToken: randHex(24),
		createdAt: now, expiresAt: now.Add(s.cfg.RoomTTL),
		files: make(map[string]*FileMeta), subs: make(map[chan Event]struct{}),
	}
	r.dir = filepath.Join(s.cfg.DataDir, r.id)
	if err := os.Mkdir(r.dir, 0o700); err != nil {
		return RoomInfo{}, err
	}
	s.rooms[r.id] = r
	s.pins[pin] = r
	info := r.info(s.cfg)
	info.ReceiverToken = r.receiverToken
	return info, nil
}

func (s *Store) JoinByPIN(pin string) (JoinResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.pins[pin]
	if r == nil || s.liveLocked(r.id) == nil {
		return JoinResult{}, ErrNotFound
	}
	return s.joinLocked(r), nil
}

func (s *Store) JoinByKey(id, key string) (JoinResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.liveLocked(id)
	if r == nil || !tokenEq(key, r.joinKey) {
		return JoinResult{}, ErrNotFound
	}
	return s.joinLocked(r), nil
}

// joinLocked: kode pairing (PIN & joinKey) bersifat SEKALI PAKAI dan langsung
// dirotasi. Layar PC menerima kode baru via SSE, jadi UX tidak terganggu.
func (s *Store) joinLocked(r *Room) JoinResult {
	newPin, ok := s.newPINLocked()
	delete(s.pins, r.pin)
	r.pin = ""
	if ok {
		r.pin = newPin
		s.pins[newPin] = r
	}
	r.joinKey = randHex(16)
	r.senders++
	s.touchLocked(r, time.Now())
	s.broadcastLocked(r, Event{"joined", r.info(s.cfg)})
	return JoinResult{RoomID: r.id, SenderToken: r.senderToken, ExpiresAt: r.expiresAt, MaxFileBytes: s.cfg.MaxFileBytes}
}

// Reserve memesan kuota sebesar Content-Length sebelum streaming dimulai,
// sehingga upload paralel tidak bisa bersama-sama melampaui batas disk.
func (s *Store) Reserve(id, senderToken string, n int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.authLocked(id, senderToken, false)
	if err != nil {
		return "", err
	}
	if len(r.files) >= s.cfg.MaxFilesPerRoom || r.bytes+r.reserved+n > s.cfg.MaxRoomBytes {
		return "", ErrQuota
	}
	if s.totalBytes+s.totalReserved+n > s.cfg.MaxTotalBytes {
		return "", ErrCapacity
	}
	r.reserved += n
	s.totalReserved += n
	return r.dir, nil
}

func (s *Store) Release(id string, n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.rooms[id]; r != nil {
		r.reserved -= n
		s.totalReserved -= n
	}
}

// Commit mendaftarkan file yang sudah selesai ditulis. Reservasi selalu dilepas.
func (s *Store) Commit(id string, m *FileMeta, reserved int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[id]
	if r == nil {
		return ErrNotFound // room sudah dihapus; reservasi ikut terhapus
	}
	r.reserved -= reserved
	s.totalReserved -= reserved
	if s.liveLocked(id) == nil {
		return ErrNotFound
	}
	now := time.Now()
	m.CreatedAt, m.ExpiresAt = now, now.Add(s.cfg.FileTTL)
	r.files[m.ID] = m
	r.bytes += m.Size
	s.totalBytes += m.Size
	s.touchLocked(r, now)
	s.broadcastLocked(r, Event{"file", m})
	s.broadcastLocked(r, Event{"room", r.info(s.cfg)})
	return nil
}

// Notify mengirim event ke receiver yang terhubung pada room yang masih hidup.
func (s *Store) Notify(id string, ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.liveLocked(id); r != nil {
		s.broadcastLocked(r, ev)
	}
}

func (s *Store) ListFiles(id, receiverToken string) ([]FileMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.authLocked(id, receiverToken, true)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	res := make([]FileMeta, 0, len(r.files))
	for _, m := range r.files {
		if now.Before(m.ExpiresAt) {
			res = append(res, *m)
		}
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].CreatedAt.Before(res[j].CreatedAt)
	})
	return res, nil
}

func (s *Store) GetFile(id, fid, receiverToken string) (FileMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.authLocked(id, receiverToken, true)
	if err != nil {
		return FileMeta{}, err
	}
	m := r.files[fid]
	if m == nil || time.Now().After(m.ExpiresAt) {
		return FileMeta{}, ErrNotFound
	}
	return *m, nil
}

func (s *Store) DeleteFile(id, fid, receiverToken string) error {
	s.mu.Lock()
	r, err := s.authLocked(id, receiverToken, true)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	m := r.files[fid]
	if m == nil {
		s.mu.Unlock()
		return ErrNotFound
	}
	s.dropFileLocked(r, m)
	s.mu.Unlock()
	_ = os.Remove(m.path)
	return nil
}

func (s *Store) dropFileLocked(r *Room, m *FileMeta) {
	delete(r.files, m.ID)
	r.bytes -= m.Size
	s.totalBytes -= m.Size
	s.broadcastLocked(r, Event{"file_deleted", map[string]string{"id": m.ID}})
}

func (s *Store) DeleteRoom(id, receiverToken string) error {
	s.mu.Lock()
	r, err := s.authLocked(id, receiverToken, true)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	dir := s.removeLocked(r, "closed")
	s.mu.Unlock()
	return os.RemoveAll(dir)
}

func (s *Store) removeLocked(r *Room, reason string) string {
	delete(s.rooms, r.id)
	if r.pin != "" && s.pins[r.pin] == r {
		delete(s.pins, r.pin)
	}
	s.totalBytes -= r.bytes
	s.totalReserved -= r.reserved
	for ch := range r.subs {
		select {
		case ch <- Event{"expired", map[string]string{"reason": reason}}:
		default:
		}
		close(ch)
	}
	r.subs = nil
	return r.dir
}

// Subscribe mendaftarkan listener SSE (receiver PC maupun sender HP).
func (s *Store) Subscribe(id, token string, max int) (<-chan Event, Snapshot, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.liveLocked(id)
	if r == nil {
		return nil, Snapshot{}, nil, ErrNotFound
	}
	isReceiver := tokenEq(token, r.receiverToken)
	isSender := tokenEq(token, r.senderToken)
	if !isReceiver && !isSender {
		return nil, Snapshot{}, nil, ErrForbidden
	}
	if len(r.subs) >= max {
		return nil, Snapshot{}, nil, ErrTooMany
	}
	ch := make(chan Event, 16)
	r.subs[ch] = struct{}{}
	s.touchLocked(r, time.Now())

	var snap Snapshot
	if isReceiver {
		files := make([]*FileMeta, 0, len(r.files))
		for _, m := range r.files {
			files = append(files, m)
		}
		sort.Slice(files, func(i, j int) bool { return files[i].CreatedAt.Before(files[j].CreatedAt) })
		snap = Snapshot{Room: r.info(s.cfg), Files: files}
	} else {
		// Sender HP: snapshot status dasar tanpa membocorkan PIN/joinKey
		snap = Snapshot{Room: RoomInfo{RoomID: r.id, ExpiresAt: r.expiresAt, TTLSeconds: int(s.cfg.RoomTTL.Seconds())}}
	}

	unsub := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := r.subs[ch]; ok {
			delete(r.subs, ch)
			close(ch)
		}
	}
	return ch, snap, unsub, nil
}

// broadcastLocked tidak pernah memblokir: subscriber yang lambat di-drop,
// lalu browser reconnect otomatis dan menerima snapshot baru.
func (s *Store) broadcastLocked(r *Room, ev Event) {
	for ch := range r.subs {
		select {
		case ch <- ev:
		default:
			delete(r.subs, ch)
			close(ch)
		}
	}
}

// RunJanitor menyapu room/file kedaluwarsa secara berkala.
func (s *Store) RunJanitor(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.Sweep(now)
		}
	}
}

func (s *Store) Sweep(now time.Time) {
	var dirs, files []string
	s.mu.Lock()
	for _, r := range s.rooms {
		if now.After(r.expiresAt) {
			dirs = append(dirs, s.removeLocked(r, "expired"))
			continue
		}
		for _, m := range r.files {
			if now.After(m.ExpiresAt) {
				s.dropFileLocked(r, m)
				files = append(files, m.path)
			}
		}
	}
	s.mu.Unlock()

	// I/O disk dilakukan di luar lock.
	for _, p := range files {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("janitor remove file", "err", err)
		}
	}
	for _, d := range dirs {
		if err := os.RemoveAll(d); err != nil {
			slog.Warn("janitor remove room", "err", err)
		}
	}
	if len(files)+len(dirs) > 0 {
		slog.Info("janitor sweep", "rooms", len(dirs), "files", len(files))
	}
}

// Shutdown menutup semua stream SSE agar http.Server.Shutdown tidak tertahan.
func (s *Store) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rooms {
		for ch := range r.subs {
			delete(r.subs, ch)
			close(ch)
		}
	}
}

func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Stats{Rooms: len(s.rooms), Bytes: s.totalBytes, Reserved: s.totalReserved}
	for _, r := range s.rooms {
		st.Files += len(r.files)
	}
	return st
}
