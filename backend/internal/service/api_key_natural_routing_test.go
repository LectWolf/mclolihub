package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type naturalRouteStatsRepo struct {
	GroupRepository
	stats map[int64]GroupRouteUsageStats
}

func (r *naturalRouteStatsRepo) GetUserRouteUsageStats(_ context.Context, _ int64, _ string, groupIDs []int64) (map[int64]GroupRouteUsageStats, error) {
	out := make(map[int64]GroupRouteUsageStats, len(groupIDs))
	for _, groupID := range groupIDs {
		if stats, ok := r.stats[groupID]; ok {
			out[groupID] = stats
		}
	}
	return out, nil
}

func newNaturalRouteCostService(stats GroupRouteUsageStats) *APIKeyService {
	svc := &APIKeyService{
		groupRepo:              &naturalRouteStatsRepo{stats: map[int64]GroupRouteUsageStats{2: stats}},
		naturalRoutes:          make(map[string]naturalRouteState),
		naturalRouteLatest:     make(map[int64]string),
		naturalRouteUsageCache: make(map[string]naturalRouteUsageCacheEntry),
	}
	billing := &BillingService{fallbackPrices: map[string]*ModelPricing{
		"gpt-5.6-sol": {InputPricePerToken: 1, OutputPricePerToken: 1, CacheReadPricePerToken: .1},
	}}
	svc.SetNaturalRouteBillingService(billing)
	return svc
}

func TestNaturalRouteKeepsModeratelyMoreExpensiveFallback(t *testing.T) {
	svc := &APIKeyService{}
	key := &APIKey{ID: 41, RouteMode: RouteModeCheapest, NaturalRevertEnabled: true}
	candidates := []Group{
		{ID: 1, RateMultiplier: .05, RouteEffectiveMultiplier: .05},
		{ID: 2, RateMultiplier: .09, RouteEffectiveMultiplier: .09},
	}

	svc.RememberNaturalRoute(key, "gpt-5.6-sol", "session-a", 2)
	got, info := svc.ApplyNaturalRoute(context.Background(), key, "gpt-5.6-sol", "session-a", candidates)

	require.NotNil(t, info)
	require.Equal(t, int64(2), got[0].ID, "0.09x should remain briefly to retain a warm cache")
}

func TestNaturalRouteReturnsImmediatelyForLargePriceSaving(t *testing.T) {
	svc := &APIKeyService{}
	key := &APIKey{ID: 42, RouteMode: RouteModeCheapest, NaturalRevertEnabled: true}
	candidates := []Group{
		{ID: 1, RateMultiplier: .05, RouteEffectiveMultiplier: .05},
		{ID: 2, RateMultiplier: .16, RouteEffectiveMultiplier: .16},
	}

	svc.RememberNaturalRoute(key, "gpt-5.6-sol", "session-a", 2)
	got, info := svc.ApplyNaturalRoute(context.Background(), key, "gpt-5.6-sol", "session-a", candidates)

	require.Nil(t, info)
	require.Equal(t, int64(1), got[0].ID, "0.16x -> 0.05x should not wait for the cache hold")
}

func TestNaturalRouteReturnsImmediatelyForMuchFasterPreferredGroup(t *testing.T) {
	svc := &APIKeyService{}
	key := &APIKey{ID: 43, RouteMode: RouteModeFastest, NaturalRevertEnabled: true}
	candidates := []Group{
		{ID: 1, RouteRealTTFTP50MS: 6000},
		{ID: 2, RouteRealTTFTP50MS: 14000},
	}

	svc.RememberNaturalRoute(key, "gpt-5.6-sol", "session-a", 2)
	got, info := svc.ApplyNaturalRoute(context.Background(), key, "gpt-5.6-sol", "session-a", candidates)

	require.Nil(t, info)
	require.Equal(t, int64(1), got[0].ID)
}

func TestClearNaturalRoutesRemovesAllModelsForKey(t *testing.T) {
	svc := &APIKeyService{}
	key := &APIKey{ID: 44, RouteMode: RouteModeCheapest, NaturalRevertEnabled: true}
	svc.RememberNaturalRoute(key, "gpt-5.6-sol", "session-a", 2)
	svc.RememberNaturalRoute(key, "gpt-5.6-mini", "session-b", 3)

	svc.ClearNaturalRoutes(key.ID)

	require.Nil(t, svc.LatestNaturalRoute(key.ID))
}

func TestNaturalRouteCostComparisonKeepsValuableWarmCache(t *testing.T) {
	svc := newNaturalRouteCostService(GroupRouteUsageStats{
		SuccessfulRequests: 10, InputTokens: 1000, CacheReadTokens: 9000,
	})
	key := &APIKey{ID: 45, UserID: 7, RouteMode: RouteModeCheapest, NaturalRevertEnabled: true}
	candidates := []Group{
		{ID: 1, RateMultiplier: .05, RouteEffectiveMultiplier: .05},
		{ID: 2, RateMultiplier: .09, RouteEffectiveMultiplier: .09},
	}

	svc.RememberNaturalRoute(key, "gpt-5.6-sol", "session-a", 2)
	got, info := svc.ApplyNaturalRoute(context.Background(), key, "gpt-5.6-sol", "session-a", candidates)

	require.Equal(t, int64(2), got[0].ID)
	require.NotNil(t, info)
	require.Equal(t, "fallback_cache_cost_hold", info.Reason)
	require.Equal(t, 3, info.ComparisonRequests)
	require.Equal(t, int64(10), info.UsageSamples)
	require.Less(t, info.EstimatedKeepCost, info.EstimatedReturnCost)
}

func TestNaturalRouteCostComparisonReturnsWhenMultiplierSavingWins(t *testing.T) {
	svc := newNaturalRouteCostService(GroupRouteUsageStats{
		SuccessfulRequests: 10, InputTokens: 9000, CacheReadTokens: 1000,
	})
	key := &APIKey{ID: 46, UserID: 7, RouteMode: RouteModeCheapest, NaturalRevertEnabled: true}
	candidates := []Group{
		{ID: 1, RateMultiplier: .05, RouteEffectiveMultiplier: .05},
		{ID: 2, RateMultiplier: .09, RouteEffectiveMultiplier: .09},
	}

	svc.RememberNaturalRoute(key, "gpt-5.6-sol", "session-a", 2)
	got, info := svc.ApplyNaturalRoute(context.Background(), key, "gpt-5.6-sol", "session-a", candidates)

	require.Equal(t, int64(1), got[0].ID)
	require.Nil(t, info)
}
