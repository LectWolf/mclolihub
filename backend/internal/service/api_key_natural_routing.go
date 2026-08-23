package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

const (
	naturalRouteSessionMinHold  = 90 * time.Second
	naturalRouteKeyMinHold      = 45 * time.Second
	naturalRouteMaxHold         = 3 * time.Minute
	naturalRouteMaxRequests     = 8
	naturalRouteUsageCacheTTL   = time.Minute
	naturalRouteUsageMinSamples = 5
)

// NaturalRouteInfo is the short-lived fallback route shown to the API-key owner.
// It is intentionally in-memory: it protects locality during a transient outage
// without making an old fallback survive a process restart or a configuration edit.
type NaturalRouteInfo struct {
	GroupID                int64     `json:"group_id"`
	Model                  string    `json:"model"`
	SessionScoped          bool      `json:"session_scoped"`
	StartedAt              time.Time `json:"started_at"`
	ExpiresAt              time.Time `json:"expires_at"`
	Reason                 string    `json:"reason"`
	ComparisonRequests     int       `json:"comparison_requests,omitempty"`
	UsageSamples           int64     `json:"usage_samples,omitempty"`
	EstimatedKeepCost      float64   `json:"estimated_keep_cost,omitempty"`
	EstimatedReturnCost    float64   `json:"estimated_return_cost,omitempty"`
	EstimatedSavingPercent float64   `json:"estimated_saving_percent,omitempty"`
}

type naturalRouteState struct {
	NaturalRouteInfo
	requests int
}

type naturalRouteUsageCacheEntry struct {
	stats     GroupRouteUsageStats
	expiresAt time.Time
}

func naturalRouteKey(keyID int64, model, sessionID string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if sessionID == "" {
		return strconv.FormatInt(keyID, 10) + ":" + model
	}
	sum := sha256.Sum256([]byte(sessionID))
	return strconv.FormatInt(keyID, 10) + ":" + model + ":" + hex.EncodeToString(sum[:8])
}

func naturalRouteImmediateReturn(mode string, current, preferred Group) bool {
	currentRate := current.RouteEffectiveMultiplier
	if currentRate <= 0 {
		currentRate = current.RateMultiplier
	}
	preferredRate := preferred.RouteEffectiveMultiplier
	if preferredRate <= 0 {
		preferredRate = preferred.RateMultiplier
	}
	if mode == RouteModeCheapest && currentRate > 0 {
		// A 65%+ saving (for example 0.16x -> 0.05x) outweighs a warm cache.
		return (currentRate-preferredRate)/currentRate >= 0.65
	}
	if mode == RouteModeFastest {
		currentTTFT := current.RouteRealTTFTP50MS
		if currentTTFT <= 0 {
			currentTTFT = current.RouteProbeTTFTMS
		}
		preferredTTFT := preferred.RouteRealTTFTP50MS
		if preferredTTFT <= 0 {
			preferredTTFT = preferred.RouteProbeTTFTMS
		}
		if currentTTFT > 0 && preferredTTFT > 0 {
			return currentTTFT-preferredTTFT >= 3000 && preferredTTFT*100 <= currentTTFT*65
		}
	}
	return false
}

// ApplyNaturalRoute moves the healthy fallback currently serving this model to
// the front only while its short hold is justified. candidates must already be
// the normal health-filtered, theoretically optimal ordering.
func (s *APIKeyService) ApplyNaturalRoute(ctx context.Context, key *APIKey, model, sessionID string, candidates []Group) ([]Group, *NaturalRouteInfo) {
	if s == nil || key == nil || !key.NaturalRevertEnabled || len(candidates) < 2 {
		return candidates, nil
	}
	now := time.Now()
	stateKey := naturalRouteKey(key.ID, model, sessionID)
	s.naturalRouteMu.Lock()
	state, ok := s.naturalRoutes[stateKey]
	if !ok || !now.Before(state.ExpiresAt) {
		if ok {
			s.deleteNaturalRouteLocked(key.ID, stateKey)
		}
		s.naturalRouteMu.Unlock()
		return candidates, nil
	}
	s.naturalRouteMu.Unlock()
	currentIndex := -1
	for i := range candidates {
		if candidates[i].ID == state.GroupID {
			currentIndex = i
			break
		}
	}
	if currentIndex <= 0 {
		// The target is unavailable, or it is again the normal winner.
		s.deleteNaturalRouteIfCurrent(key.ID, stateKey, state)
		return candidates, nil
	}
	if state.requests >= naturalRouteMaxRequests {
		s.deleteNaturalRouteIfCurrent(key.ID, stateKey, state)
		return candidates, nil
	}

	// Low-price routing can use this user's actual recent token/cache mix. This
	// turns a cache hit back into ordinary input for the first request after a
	// return, then compares the next 2/3 requests instead of guessing from the
	// multiplier alone. Any missing data falls back to the conservative rules.
	costDecision := s.naturalRouteCostReturn(ctx, key, model, state.SessionScoped, candidates[currentIndex], candidates[0])
	costReturn, costCompared := costDecision.returnNow, costDecision.compared
	immediateReturn := naturalRouteImmediateReturn(key.RouteMode, candidates[currentIndex], candidates[0])
	if key.RouteMode == RouteModeFastest && immediateReturn || key.RouteMode == RouteModeCheapest && ((costCompared && costReturn) || (!costCompared && immediateReturn)) {
		s.deleteNaturalRouteIfCurrent(key.ID, stateKey, state)
		return candidates, nil
	}
	minHold := naturalRouteKeyMinHold
	if state.SessionScoped {
		minHold = naturalRouteSessionMinHold
	}
	if !costCompared && now.Sub(state.StartedAt) >= minHold {
		// The maximum duration remains the final guard. Before that point a
		// route is retained only while it is still close enough to the winner.
		preferredRate := candidates[0].RouteEffectiveMultiplier
		if preferredRate <= 0 {
			preferredRate = candidates[0].RateMultiplier
		}
		currentRate := candidates[currentIndex].RouteEffectiveMultiplier
		if currentRate <= 0 {
			currentRate = candidates[currentIndex].RateMultiplier
		}
		if key.RouteMode == RouteModeCheapest && preferredRate > 0 && currentRate/preferredRate >= 2 {
			s.deleteNaturalRouteIfCurrent(key.ID, stateKey, state)
			return candidates, nil
		}
	}

	// A stats query runs outside the route lock. Revalidate here so a concurrent
	// failover cannot resurrect an older fallback affinity.
	s.naturalRouteMu.Lock()
	defer s.naturalRouteMu.Unlock()
	currentState, ok := s.naturalRoutes[stateKey]
	if !ok || currentState.GroupID != state.GroupID || !currentState.StartedAt.Equal(state.StartedAt) || !now.Before(currentState.ExpiresAt) {
		return candidates, nil
	}
	if costCompared {
		currentState.Reason = "fallback_cache_cost_hold"
		currentState.ComparisonRequests = costDecision.horizon
		currentState.UsageSamples = costDecision.samples
		currentState.EstimatedKeepCost = costDecision.keepCost
		currentState.EstimatedReturnCost = costDecision.returnCost
		if costDecision.returnCost > 0 {
			currentState.EstimatedSavingPercent = (costDecision.returnCost - costDecision.keepCost) / costDecision.returnCost * 100
		}
	}
	currentState.requests++
	s.naturalRoutes[stateKey] = currentState
	reordered := append([]Group(nil), candidates...)
	selected := reordered[currentIndex]
	copy(reordered[1:currentIndex+1], reordered[:currentIndex])
	reordered[0] = selected
	info := currentState.NaturalRouteInfo
	return reordered, &info
}

func (s *APIKeyService) deleteNaturalRouteIfCurrent(keyID int64, stateKey string, expected naturalRouteState) {
	s.naturalRouteMu.Lock()
	defer s.naturalRouteMu.Unlock()
	state, ok := s.naturalRoutes[stateKey]
	if ok && state.GroupID == expected.GroupID && state.StartedAt.Equal(expected.StartedAt) {
		s.deleteNaturalRouteLocked(keyID, stateKey)
	}
}

func naturalRouteUsageCacheKey(userID int64, model string, groupID int64) string {
	return strconv.FormatInt(userID, 10) + ":" + strings.ToLower(strings.TrimSpace(model)) + ":" + strconv.FormatInt(groupID, 10)
}

func (s *APIKeyService) naturalRouteUsageStats(ctx context.Context, userID int64, model string, groupID int64) (GroupRouteUsageStats, bool) {
	if s == nil || s.groupRepo == nil || userID <= 0 || groupID <= 0 {
		return GroupRouteUsageStats{}, false
	}
	reader, ok := s.groupRepo.(groupRouteUsageStatsReader)
	if !ok {
		return GroupRouteUsageStats{}, false
	}
	now := time.Now()
	cacheKey := naturalRouteUsageCacheKey(userID, model, groupID)
	s.naturalRouteUsageMu.Lock()
	entry, cached := s.naturalRouteUsageCache[cacheKey]
	s.naturalRouteUsageMu.Unlock()
	if cached && now.Before(entry.expiresAt) {
		return entry.stats, true
	}
	statsByGroup, err := reader.GetUserRouteUsageStats(ctx, userID, model, []int64{groupID})
	if err != nil {
		return GroupRouteUsageStats{}, false
	}
	stats, found := statsByGroup[groupID]
	if !found {
		stats = GroupRouteUsageStats{}
	}
	s.naturalRouteUsageMu.Lock()
	if s.naturalRouteUsageCache == nil {
		s.naturalRouteUsageCache = make(map[string]naturalRouteUsageCacheEntry)
	}
	s.naturalRouteUsageCache[cacheKey] = naturalRouteUsageCacheEntry{stats: stats, expiresAt: now.Add(naturalRouteUsageCacheTTL)}
	s.naturalRouteUsageMu.Unlock()
	return stats, true
}

func naturalRouteAverageTokens(stats GroupRouteUsageStats) UsageTokens {
	if stats.SuccessfulRequests <= 0 {
		return UsageTokens{}
	}
	div := stats.SuccessfulRequests
	return UsageTokens{
		InputTokens:         int(stats.InputTokens / div),
		OutputTokens:        int(stats.OutputTokens / div),
		CacheCreationTokens: int(stats.CacheCreationTokens / div),
		CacheReadTokens:     int(stats.CacheReadTokens / div),
	}
}

func naturalRouteRateMultiplier(group Group) float64 {
	if group.RouteEffectiveMultiplier > 0 {
		return group.RouteEffectiveMultiplier
	}
	return group.RateMultiplier
}

func (s *APIKeyService) naturalRouteEstimatedCost(ctx context.Context, model string, group Group, tokens UsageTokens) (float64, bool) {
	if s == nil || s.naturalRouteBilling == nil || s.naturalRoutePricing == nil {
		return 0, false
	}
	breakdown, err := s.naturalRouteBilling.CalculateCostUnified(CostInput{
		Ctx: ctx, Model: model, Group: &group, Tokens: tokens, RequestCount: 1,
		RateMultiplier: naturalRouteRateMultiplier(group), Resolver: s.naturalRoutePricing, PricingAt: time.Now(),
	})
	if err != nil || breakdown == nil || breakdown.ActualCost <= 0 {
		return 0, false
	}
	return breakdown.ActualCost, true
}

// naturalRouteCostReturn reports whether returning to preferred costs less over
// the next requests than retaining the currently warm fallback. It is only
// meaningful for the cheapest route mode and real per-user cache observations.
type naturalRouteCostDecision struct {
	returnNow  bool
	compared   bool
	keepCost   float64
	returnCost float64
	horizon    int
	samples    int64
}

func (s *APIKeyService) naturalRouteCostReturn(ctx context.Context, key *APIKey, model string, sessionScoped bool, current, preferred Group) naturalRouteCostDecision {
	if key == nil || key.RouteMode != RouteModeCheapest {
		return naturalRouteCostDecision{}
	}
	stats, ok := s.naturalRouteUsageStats(ctx, key.UserID, model, current.ID)
	if !ok || stats.SuccessfulRequests < naturalRouteUsageMinSamples || stats.CacheReadTokens <= 0 {
		return naturalRouteCostDecision{}
	}
	warmTokens := naturalRouteAverageTokens(stats)
	if warmTokens.CacheReadTokens <= 0 {
		return naturalRouteCostDecision{}
	}
	coldTokens := warmTokens
	coldTokens.InputTokens += coldTokens.CacheReadTokens
	coldTokens.CacheReadTokens = 0
	currentWarm, ok := s.naturalRouteEstimatedCost(ctx, model, current, warmTokens)
	if !ok {
		return naturalRouteCostDecision{}
	}
	preferredWarm, ok := s.naturalRouteEstimatedCost(ctx, model, preferred, warmTokens)
	if !ok {
		return naturalRouteCostDecision{}
	}
	preferredCold, ok := s.naturalRouteEstimatedCost(ctx, model, preferred, coldTokens)
	if !ok {
		return naturalRouteCostDecision{}
	}
	horizon := 2
	if sessionScoped {
		horizon = 3
	}
	keepCost := float64(horizon) * currentWarm
	returnCost := preferredCold + float64(horizon-1)*preferredWarm
	return naturalRouteCostDecision{
		returnNow:  returnCost < keepCost,
		compared:   true,
		keepCost:   keepCost,
		returnCost: returnCost,
		horizon:    horizon,
		samples:    stats.SuccessfulRequests,
	}
}

// RememberNaturalRoute is called only after the gateway has moved to a healthy
// fallback candidate. A later error clears it before another group is tried.
func (s *APIKeyService) RememberNaturalRoute(key *APIKey, model, sessionID string, groupID int64) {
	if s == nil || key == nil || !key.NaturalRevertEnabled || key.ID <= 0 || groupID <= 0 || key.RouteMode == RouteModeFixed {
		return
	}
	now := time.Now()
	stateKey := naturalRouteKey(key.ID, model, sessionID)
	state := naturalRouteState{NaturalRouteInfo: NaturalRouteInfo{
		GroupID: groupID, Model: strings.TrimSpace(model), SessionScoped: sessionID != "",
		StartedAt: now, ExpiresAt: now.Add(naturalRouteMaxHold), Reason: "fallback_cache_hold",
	}}
	s.naturalRouteMu.Lock()
	defer s.naturalRouteMu.Unlock()
	if s.naturalRoutes == nil {
		s.naturalRoutes = make(map[string]naturalRouteState)
		s.naturalRouteLatest = make(map[int64]string)
	}
	s.naturalRoutes[stateKey] = state
	s.naturalRouteLatest[key.ID] = stateKey
}

func (s *APIKeyService) ClearNaturalRoute(keyID int64, model, sessionID string) {
	if s == nil || keyID <= 0 {
		return
	}
	stateKey := naturalRouteKey(keyID, model, sessionID)
	s.naturalRouteMu.Lock()
	defer s.naturalRouteMu.Unlock()
	s.deleteNaturalRouteLocked(keyID, stateKey)
}

// ClearNaturalRoutes is the user-facing immediate-return action.
func (s *APIKeyService) ClearNaturalRoutes(keyID int64) {
	if s == nil || keyID <= 0 {
		return
	}
	prefix := strconv.FormatInt(keyID, 10) + ":"
	s.naturalRouteMu.Lock()
	defer s.naturalRouteMu.Unlock()
	for stateKey := range s.naturalRoutes {
		if strings.HasPrefix(stateKey, prefix) {
			delete(s.naturalRoutes, stateKey)
		}
	}
	delete(s.naturalRouteLatest, keyID)
}

func (s *APIKeyService) LatestNaturalRoute(keyID int64) *NaturalRouteInfo {
	if s == nil || keyID <= 0 {
		return nil
	}
	s.naturalRouteMu.Lock()
	defer s.naturalRouteMu.Unlock()
	stateKey, ok := s.naturalRouteLatest[keyID]
	if !ok {
		return nil
	}
	state, ok := s.naturalRoutes[stateKey]
	if !ok || !time.Now().Before(state.ExpiresAt) {
		s.deleteNaturalRouteLocked(keyID, stateKey)
		return nil
	}
	info := state.NaturalRouteInfo
	return &info
}

func (s *APIKeyService) deleteNaturalRouteLocked(keyID int64, stateKey string) {
	delete(s.naturalRoutes, stateKey)
	if s.naturalRouteLatest[keyID] == stateKey {
		delete(s.naturalRouteLatest, keyID)
	}
}
