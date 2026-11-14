package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/redis/go-redis/v9"
)

const (
	keyReady  = "antifraud:blocklist:ready"
	keyPrefix = "antifraud:blocklist:"
)

type cachedValue struct {
	ID     int64  `json:"id"`
	Reason string `json:"reason"`
}

type reloadGate struct {
	running   atomic.Bool
	mu        sync.Mutex
	lastStart time.Time
}

func (g *reloadGate) canRun(now time.Time) bool {
	if g.running.Load() {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.running.Load() || now.Sub(g.lastStart) < 5*time.Second {
		return false
	}
	g.lastStart = now
	g.running.Store(true)
	return true
}

func (g *reloadGate) done() {
	g.running.Store(false)
}

type cacheHealth struct {
	down atomic.Bool
}

func (h *cacheHealth) recordFailure(err error) {
	if h.down.CompareAndSwap(false, true) {
		slog.Warn("blocklist cache is unavailable, falling back to database", "error", err)
	}
}

func (h *cacheHealth) recordSuccess() {
	if h.down.CompareAndSwap(true, false) {
		slog.Info("blocklist cache is available")
	}
}

// CachedBlocklistStore answers blocklist matches from a Redis mirror of
// blocklist_entries and falls back to the database when the mirror is
// missing or unreachable. Writes go to the database first.
type CachedBlocklistStore struct {
	db     *BlocklistStore
	redis  *redis.Client
	mu     sync.Mutex
	reload reloadGate
	health cacheHealth
	bypass atomic.Bool
}

func NewCachedBlocklistStore(db *BlocklistStore, client *redis.Client) *CachedBlocklistStore {
	return &CachedBlocklistStore{
		db:    db,
		redis: client,
	}
}

// Match checks the cache first, falling back to PostgreSQL on any Redis error
// or while the mirror is being rebuilt.
func (s *CachedBlocklistStore) Match(ctx context.Context, sub models.BlocklistSubject) (models.BlocklistEntry, bool, error) {
	if s.bypass.Load() {
		s.triggerReload()
		return s.db.Match(ctx, sub)
	}
	ok, entry, err := s.matchFromRedis(ctx, sub)
	if err != nil {
		s.health.recordFailure(err)
		return s.db.Match(ctx, sub)
	}
	if !ok {
		return s.db.Match(ctx, sub)
	}
	s.health.recordSuccess()
	return entry, entry.Value != "", nil
}

func (s *CachedBlocklistStore) matchFromRedis(ctx context.Context, sub models.BlocklistSubject) (bool, models.BlocklistEntry, error) {
	pipe := s.redis.Pipeline()
	readyCmd := pipe.Get(ctx, keyReady)

	cardCmd := pipe.HGet(ctx, keyFor(models.BlocklistCard), sub.CardToken)
	playerCmd := pipe.HGet(ctx, keyFor(models.BlocklistPlayer), sub.PlayerID)
	var deviceCmd *redis.StringCmd
	if sub.DeviceID != nil && *sub.DeviceID != "" {
		deviceCmd = pipe.HGet(ctx, keyFor(models.BlocklistDevice), *sub.DeviceID)
	}
	var ipCmd *redis.StringCmd
	if sub.IPAddress != nil {
		ipCmd = pipe.HGet(ctx, keyFor(models.BlocklistIP), sub.IPAddress.String())
	}

	_, err := pipe.Exec(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		return false, models.BlocklistEntry{}, err
	}
	if readyCmd.Val() != "1" {
		s.triggerReload()
		return false, models.BlocklistEntry{}, nil
	}

	entry, _ := lowestMatch([]matchCandidate{
		parseCandidate(models.BlocklistCard, sub.CardToken, cardCmd),
		parseCandidate(models.BlocklistPlayer, sub.PlayerID, playerCmd),
		parseCandidate(models.BlocklistDevice, optString(sub.DeviceID), deviceCmd),
		parseCandidate(models.BlocklistIP, ipString(sub.IPAddress), ipCmd),
	})
	return true, entry, nil
}

func (s *CachedBlocklistStore) triggerReload() {
	if !s.reload.canRun(time.Now()) {
		return
	}
	go func() {
		defer s.reload.done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Reload(ctx); err != nil {
			slog.Warn("background blocklist reload failed", "error", err)
		}
	}()
}

// Reload rebuilds the whole mirror from PostgreSQL in one transaction.
func (s *CachedBlocklistStore) Reload(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reloadLocked(ctx)
}

func (s *CachedBlocklistStore) reloadLocked(ctx context.Context) error {
	entries, err := s.db.List(ctx)
	if err != nil {
		_ = s.redis.Del(ctx, keyReady).Err()
		return fmt.Errorf("list blocklist entries for reload: %w", err)
	}

	pipe := s.redis.TxPipeline()
	pipe.Del(ctx, keyReady)
	pipe.Del(ctx, keyFor(models.BlocklistCard), keyFor(models.BlocklistPlayer), keyFor(models.BlocklistDevice), keyFor(models.BlocklistIP))

	for _, e := range entries {
		raw, err := json.Marshal(cachedValue{ID: e.ID, Reason: e.Reason})
		if err != nil {
			return fmt.Errorf("encode blocklist entry: %w", err)
		}
		pipe.HSet(ctx, keyFor(e.Type), e.Value, raw)
	}
	pipe.Set(ctx, keyReady, "1", 0)

	if _, err := pipe.Exec(ctx); err != nil {
		_ = s.redis.Del(ctx, keyReady).Err()
		return fmt.Errorf("rebuild blocklist cache in redis: %w", err)
	}
	s.bypass.Store(false)
	s.health.recordSuccess()
	return nil
}

// Refresh copies the database state of one value into Redis.
func (s *CachedBlocklistStore) Refresh(ctx context.Context, t models.BlocklistType, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, reason, found, err := s.db.Find(ctx, t, value)
	if err != nil {
		s.markStale(ctx)
		return
	}
	if found {
		raw, _ := json.Marshal(cachedValue{ID: id, Reason: reason})
		if err := s.redis.HSet(ctx, keyFor(t), value, raw).Err(); err != nil {
			s.markStale(ctx)
		}
		return
	}
	if err := s.redis.HDel(ctx, keyFor(t), value).Err(); err != nil {
		s.markStale(ctx)
	}
}

func (s *CachedBlocklistStore) markStale(ctx context.Context) {
	_ = s.redis.Del(ctx, keyReady).Err()
}

// Add saves the entry in PostgreSQL and refreshes Redis after commit.
func (s *CachedBlocklistStore) Add(ctx context.Context, e models.BlocklistEntry, operatorID string) (models.StoredBlocklistEntry, error) {
	saved, err := s.db.Add(ctx, e, operatorID)
	if err != nil {
		return models.StoredBlocklistEntry{}, err
	}
	s.Refresh(ctx, saved.Type, saved.Value)
	return saved, nil
}

// Remove deletes the entry in PostgreSQL and refreshes Redis after commit.
func (s *CachedBlocklistStore) Remove(ctx context.Context, id int64, operatorID string) error {
	entry, err := s.db.remove(ctx, id, operatorID)
	if err != nil {
		return err
	}
	s.Refresh(ctx, entry.Type, entry.Value)
	return nil
}

func (s *CachedBlocklistStore) List(ctx context.Context) ([]models.StoredBlocklistEntry, error) {
	return s.db.List(ctx)
}

// ListPage reads the current PostgreSQL page without changing the Redis mirror.
func (s *CachedBlocklistStore) ListPage(ctx context.Context, query models.BlocklistQuery) (models.BlocklistPage, error) {
	return s.db.ListPage(ctx, query)
}

type matchCandidate struct {
	entry models.BlocklistEntry
	id    int64
	found bool
}

func parseCandidate(t models.BlocklistType, val string, cmd *redis.StringCmd) matchCandidate {
	if cmd == nil || cmd.Err() != nil {
		return matchCandidate{}
	}
	var cv cachedValue
	if err := json.Unmarshal([]byte(cmd.Val()), &cv); err != nil {
		return matchCandidate{}
	}
	return matchCandidate{
		entry: models.BlocklistEntry{Type: t, Value: val, Reason: cv.Reason},
		id:    cv.ID,
		found: true,
	}
}

func lowestMatch(candidates []matchCandidate) (models.BlocklistEntry, bool) {
	var best models.BlocklistEntry
	var minID int64
	var found bool
	for _, c := range candidates {
		if !c.found {
			continue
		}
		if !found || c.id < minID {
			best = c.entry
			minID = c.id
			found = true
		}
	}
	return best, found
}

func optString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func ipString(addr *netip.Addr) string {
	if addr == nil {
		return ""
	}
	return addr.String()
}

func keyFor(t models.BlocklistType) string {
	return keyPrefix + string(t)
}
