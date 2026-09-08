package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// routingGroupRepo embeds the full repository contract so this regression test
// only has to model the live active-group read used by dynamic routing.
type routingGroupRepo struct {
	GroupRepository
	groups []Group
}

func (r *routingGroupRepo) ListActive(context.Context) ([]Group, error) {
	return append([]Group(nil), r.groups...), nil
}

func TestResolveRoutingGroupsUsesSmartOrderAndSkipsUnlistedGroups(t *testing.T) {
	repo := &routingGroupRepo{groups: []Group{
		{ID: 2, Name: "A2-Pro", Platform: PlatformOpenAI, RateMultiplier: 0.185, Status: StatusActive},
		{ID: 3, Name: "A3-0.14x", Platform: PlatformOpenAI, RateMultiplier: 0.14, Status: StatusActive},
	}}
	svc := &APIKeyService{groupRepo: repo}
	key := &APIKey{
		ID:            7,
		UserID:        42,
		RouteMode:     RouteModeSmart,
		RoutePlatform: RoutePlatformOpenAI,
		User:          &User{ID: 42, Status: StatusActive},
		GroupPreferences: []APIKeyGroupPreference{
			{GroupID: 3, Position: 0},
			{GroupID: 2, Position: 1},
		},
	}

	groups, err := svc.ResolveRoutingGroups(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, []int64{3, 2}, []int64{groups[0].ID, groups[1].ID})

	repo.groups = append(repo.groups, Group{
		ID: 1, Name: "A3-0.11x", Platform: PlatformOpenAI, RateMultiplier: 0.11, Status: StatusActive,
	})
	groups, err = svc.ResolveRoutingGroups(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, []int64{3, 2}, []int64{groups[0].ID, groups[1].ID}, "smart routing must not auto-include groups the key did not list")
}

func TestResolveRoutingGroupsLegacyAutoUsesOpenAI(t *testing.T) {
	repo := &routingGroupRepo{groups: []Group{
		{ID: 1, Platform: PlatformOpenAI, RateMultiplier: 0.1, Status: StatusActive},
		{ID: 2, Platform: PlatformAnthropic, RateMultiplier: 0.2, Status: StatusActive},
	}}
	svc := &APIKeyService{groupRepo: repo}
	key := &APIKey{
		UserID:        42,
		RouteMode:     RouteModeSmart,
		RoutePlatform: RoutePlatformAuto,
		User:          &User{ID: 42, Status: StatusActive},
		Group:         &Group{ID: 2, Platform: PlatformAnthropic},
		GroupPreferences: []APIKeyGroupPreference{
			{GroupID: 1, Position: 0},
			{GroupID: 2, Position: 1},
		},
	}

	groups, err := svc.ResolveRoutingGroups(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, []int64{1}, []int64{groups[0].ID})
	require.Equal(t, RoutePlatformOpenAI, NormalizeRoutePlatform(RoutePlatformAuto))
	require.Equal(t, RoutePlatformOpenAI, NormalizeRoutePlatform(""))
	require.Error(t, ValidateRoutePlatform(RoutePlatformAuto))
	require.NoError(t, ValidateRoutePlatform(""))
}

func TestResolveRoutingGroupsSkipsOverMaxRateThenUsesNext(t *testing.T) {
	repo := &routingGroupRepo{groups: []Group{
		{ID: 1, Platform: PlatformOpenAI, RateMultiplier: 2, Status: StatusActive},
		{ID: 2, Platform: PlatformOpenAI, RateMultiplier: 0.5, Status: StatusActive},
	}}
	svc := &APIKeyService{groupRepo: repo}
	max := 1.0
	key := &APIKey{
		UserID:            42,
		RouteMode:         RouteModeSmart,
		RoutePlatform:     RoutePlatformOpenAI,
		MaxRateMultiplier: &max,
		User:              &User{ID: 42, Status: StatusActive},
		GroupPreferences: []APIKeyGroupPreference{
			{GroupID: 1, Position: 0},
			{GroupID: 2, Position: 1},
		},
	}
	groups, err := svc.ResolveRoutingGroups(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, []int64{groups[0].ID})
}

func TestResolveRoutingGroupsSkipsUnhealthyProbedGroup(t *testing.T) {
	repo := &routingHealthRepo{
		routingGroupRepo: routingGroupRepo{groups: []Group{
			{ID: 1, Platform: PlatformOpenAI, RateMultiplier: 0.2, Status: StatusActive, ProbeEnabled: true},
			{ID: 2, Platform: PlatformOpenAI, RateMultiplier: 0.4, Status: StatusActive, ProbeEnabled: true},
		}},
		health: map[int64]GroupRouteHealth{
			1: {Healthy: false},
			2: {Healthy: true},
		},
	}
	svc := &APIKeyService{groupRepo: repo}
	key := &APIKey{
		UserID:        42,
		RouteMode:     RouteModeSmart,
		RoutePlatform: RoutePlatformOpenAI,
		User:          &User{ID: 42, Status: StatusActive},
		GroupPreferences: []APIKeyGroupPreference{
			{GroupID: 1, Position: 0},
			{GroupID: 2, Position: 1},
		},
	}
	groups, err := svc.ResolveRoutingGroups(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, []int64{groups[0].ID})
}

type routingHealthRepo struct {
	routingGroupRepo
	health map[int64]GroupRouteHealth
}

func (r *routingHealthRepo) GetRouteHealth(context.Context, []int64) (map[int64]GroupRouteHealth, error) {
	return r.health, nil
}
