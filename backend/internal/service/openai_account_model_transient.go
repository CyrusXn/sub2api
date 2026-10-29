package service

import (
	"context"
	"strings"
	"sync"
	"time"
)

const (
	// openAIModelTransientStreakTTL bounds how long a failure streak survives
	// without a new failure. It exists only so the map does not keep state for
	// account+model pairs that stopped being used; a streak is otherwise reset
	// by recordSuccess alone.
	//
	// It must stay well above the cooldowns. Resetting the streak on a short
	// wall-clock window makes the breaker's sensitivity depend on request rate:
	// a gateway called less often than the window never reaches streak 2, so a
	// broken upstream is never cooled down and every request pays a failed
	// attempt plus a failover before reaching a healthy account. Low-traffic
	// deployments were hit hardest, which is the opposite of what a breaker
	// should do.
	openAIModelTransientStreakTTL      = 30 * time.Minute
	openAIModelTransientShortCooldown  = 10 * time.Second
	openAIModelTransientLongCooldown   = 45 * time.Second
	openAIModelTransientDefaultMax     = 4096
	openAIModelTransientMaxModelBytes  = 512
	openAIModelFailureDisableThreshold = 3
)

// OpenAIModelFailureStreakCache 在多个 API 实例间共享连续故障计数，真实成功才清零。
type OpenAIModelFailureStreakCache interface {
	RecordOpenAIModelResult(context.Context, int64, string, bool, time.Duration) (int, error)
}

type openAIAccountModelKey struct {
	AccountID int64
	Model     string
}

type openAIAccountModelTransientEntry struct {
	failureStreak        int
	accountFailureStreak int
	lastFailure          time.Time
	blockUntil           time.Time
	lastTouched          time.Time
}

type openAIAccountModelTransientDecision struct {
	FailureStreak        int
	AccountFailureStreak int
	Cooldown             time.Duration
	BlockUntil           time.Time
}

type openAIAccountModelTransientState struct {
	mu         sync.Mutex
	entries    map[openAIAccountModelKey]openAIAccountModelTransientEntry
	maxEntries int
}

func newOpenAIAccountModelTransientState(maxEntries int) *openAIAccountModelTransientState {
	if maxEntries <= 0 {
		maxEntries = openAIModelTransientDefaultMax
	}
	return &openAIAccountModelTransientState{
		entries:    make(map[openAIAccountModelKey]openAIAccountModelTransientEntry),
		maxEntries: maxEntries,
	}
}

func normalizeOpenAIAccountModelTransientModel(model string) string {
	model = strings.TrimSpace(model)
	if len(model) > openAIModelTransientMaxModelBytes {
		return ""
	}
	return strings.ToLower(model)
}

func openAIAccountModelTransientKey(accountID int64, model string) (openAIAccountModelKey, bool) {
	model = normalizeOpenAIAccountModelTransientModel(model)
	if accountID <= 0 || model == "" {
		return openAIAccountModelKey{}, false
	}
	return openAIAccountModelKey{AccountID: accountID, Model: model}, true
}

func (s *openAIAccountModelTransientState) recordFailure(accountID int64, model string, now time.Time, accountFailure ...bool) openAIAccountModelTransientDecision {
	key, ok := openAIAccountModelTransientKey(accountID, model)
	if s == nil || !ok {
		return openAIAccountModelTransientDecision{}
	}
	if now.IsZero() {
		now = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[openAIAccountModelKey]openAIAccountModelTransientEntry)
	}
	if s.maxEntries <= 0 {
		s.maxEntries = openAIModelTransientDefaultMax
	}

	entry, exists := s.entries[key]
	if !exists {
		s.evictOldestLocked()
	}
	// The streak is cleared by recordSuccess. Only drop it here when the entry
	// is stale beyond the TTL, or when the clock moved backwards.
	if !exists || entry.lastFailure.IsZero() || now.Sub(entry.lastFailure) > openAIModelTransientStreakTTL || now.Before(entry.lastFailure) {
		entry.failureStreak = 0
		entry.accountFailureStreak = 0
		entry.blockUntil = time.Time{}
	}
	entry.failureStreak++
	if len(accountFailure) == 0 || accountFailure[0] {
		entry.accountFailureStreak++
	}
	entry.lastFailure = now
	entry.lastTouched = now

	// 首次故障就短暂避开该账号，防止下一次客户端重连再次命中。
	cooldown := openAIModelTransientShortCooldown
	switch {
	case entry.failureStreak >= 3:
		cooldown = openAIModelTransientLongCooldown
	}
	if cooldown > 0 {
		entry.blockUntil = now.Add(cooldown)
	} else {
		entry.blockUntil = time.Time{}
	}
	s.entries[key] = entry
	return openAIAccountModelTransientDecision{
		FailureStreak:        entry.failureStreak,
		AccountFailureStreak: entry.accountFailureStreak,
		Cooldown:             cooldown,
		BlockUntil:           entry.blockUntil,
	}
}

func (s *openAIAccountModelTransientState) recordSuccess(accountID int64, model string) {
	key, ok := openAIAccountModelTransientKey(accountID, model)
	if s == nil || !ok {
		return
	}
	s.mu.Lock()
	delete(s.entries, key)
	s.mu.Unlock()
}

func (s *openAIAccountModelTransientState) isBlocked(accountID int64, model string, now time.Time) bool {
	key, ok := openAIAccountModelTransientKey(accountID, model)
	if s == nil || !ok {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.entries[key]
	if !exists {
		return false
	}
	if !entry.lastFailure.IsZero() && now.Sub(entry.lastFailure) > openAIModelTransientStreakTTL {
		delete(s.entries, key)
		return false
	}
	entry.lastTouched = now
	s.entries[key] = entry
	return !entry.blockUntil.IsZero() && now.Before(entry.blockUntil)
}

func (s *openAIAccountModelTransientState) size() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

func (s *openAIAccountModelTransientState) evictOldestLocked() {
	if len(s.entries) < s.maxEntries {
		return
	}
	var oldestKey openAIAccountModelKey
	var oldestTime time.Time
	found := false
	for key, entry := range s.entries {
		if !found || entry.lastTouched.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.lastTouched
			found = true
		}
	}
	if found {
		delete(s.entries, oldestKey)
	}
}
