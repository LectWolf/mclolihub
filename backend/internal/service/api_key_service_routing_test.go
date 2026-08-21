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

func TestResolveRoutingGroupsReadsNewCheapestGroupWithoutKeyUpdate(t *testing.T) {
	repo := &routingGroupRepo{groups: []Group{
		{ID: 2, Name: "A2-Pro", Platform: PlatformOpenAI, RateMultiplier: 0.185, Status: StatusActive},
		{ID: 3, Name: "A3-0.14x", Platform: PlatformOpenAI, RateMultiplier: 0.14, Status: StatusActive},
	}}
	svc := &APIKeyService{groupRepo: repo}
	key := &APIKey{
		ID:            7,
		UserID:        42,
		RouteMode:     RouteModeCheapest,
		RoutePlatform: RoutePlatformOpenAI,
		User:          &User{ID: 42, Status: StatusActive},
	}

	groups, err := svc.ResolveRoutingGroups(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, int64(3), groups[0].ID)

	// The API key is unchanged. A newly-created active group must be considered
	// on the very next request instead of waiting for an API-key edit/cache miss.
	repo.groups = append(repo.groups, Group{
		ID: 1, Name: "A3-0.11x", Platform: PlatformOpenAI, RateMultiplier: 0.11, Status: StatusActive,
	})
	groups, err = svc.ResolveRoutingGroups(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, int64(1), groups[0].ID)
}

func TestResolveRoutingGroupsAutoStaysOnBoundGroupPlatform(t *testing.T) {
	repo := &routingGroupRepo{groups: []Group{
		{ID: 1, Platform: PlatformOpenAI, RateMultiplier: 0.1, Status: StatusActive},
		{ID: 2, Platform: PlatformAnthropic, RateMultiplier: 0.2, Status: StatusActive},
	}}
	svc := &APIKeyService{groupRepo: repo}
	key := &APIKey{
		UserID:    42,
		RouteMode: RouteModeCheapest,
		User:      &User{ID: 42, Status: StatusActive},
		Group:     &Group{ID: 2, Platform: PlatformAnthropic},
	}

	groups, err := svc.ResolveRoutingGroups(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, []int64{groups[0].ID})
}
