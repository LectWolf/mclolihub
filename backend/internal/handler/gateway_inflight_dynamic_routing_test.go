package handler

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type routingInflightEstimator struct {
	costs map[int64]float64
	seen  []int64
}

func (e *routingInflightEstimator) EstimateInflightReservation(ctx context.Context, key *service.APIKey, _ service.InflightEstimateRequest) (float64, bool) {
	group, _ := ctx.Value(ctxkey.Group).(*service.Group)
	if group == nil || group.ID != key.Group.ID || *key.GroupID != group.ID {
		return 0, false
	}
	e.seen = append(e.seen, group.ID)
	cost, priced := e.costs[group.ID]
	return cost, priced
}

func TestDynamicRoutingInflightReservationUsesMaximumCandidate(t *testing.T) {
	groups := []service.Group{
		{ID: 1, Platform: service.PlatformOpenAI, RateMultiplier: 1},
		{ID: 2, Platform: service.PlatformOpenAI, RateMultiplier: 4},
	}
	key := cloneAPIKeyWithGroup(&service.APIKey{User: &service.User{ID: 5}}, &groups[0])
	c := newInflightTestGinContext()
	bindInflightRoutingGroups(c, groups)
	est := &routingInflightEstimator{costs: map[int64]float64{1: 0.2, 2: 0.8}}
	cache := newHandlerInflightCache(10)
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	done, err := reserveInflightBalance(c, billing, est, key, nil, tokenInflightEstimate("m", nil))
	require.NoError(t, err)
	defer done()
	require.Equal(t, []int64{1, 2}, est.seen)
	require.Equal(t, 0.8, service.InflightReservationFromContext(c.Request.Context()).Amount())
	require.Equal(t, 1, cache.count())
	require.Equal(t, int64(1), key.Group.ID, "estimation must not mutate the serving group")
	task, abandon := wrapUsageRecordTaskContext(c.Request.Context(), func(context.Context) {})
	done()
	require.Equal(t, 1, cache.count(), "async billing retains the single reservation")
	task(context.Background())
	abandon()
	require.Zero(t, cache.count())
}

func TestDynamicRoutingInflightEstimateSkipsSubscriptionCandidates(t *testing.T) {
	groups := []service.Group{
		{ID: 1, Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeSubscription},
		{ID: 2, Platform: service.PlatformOpenAI},
	}
	key := cloneAPIKeyWithGroup(&service.APIKey{User: &service.User{ID: 5}}, &groups[0])
	c := newInflightTestGinContext()
	bindInflightRoutingGroups(c, groups)
	est := &routingInflightEstimator{costs: map[int64]float64{2: 0.8}}
	reservationKey, subscription, amount, priced := inflightRoutingEstimate(c.Request.Context(), est, key, &service.UserSubscription{}, tokenInflightEstimate("m", nil))
	require.Equal(t, int64(2), reservationKey.Group.ID)
	require.Nil(t, subscription)
	require.Equal(t, 0.8, amount)
	require.True(t, priced)
	require.Equal(t, []int64{2}, est.seen)
	bindInflightRoutingGroups(c, groups[:1])
	reservationKey, _, amount, priced = inflightRoutingEstimate(c.Request.Context(), est, key, &service.UserSubscription{}, tokenInflightEstimate("m", nil))
	require.Nil(t, reservationKey)
	require.Zero(t, amount)
	require.True(t, priced)
}

func TestDynamicRoutingInflightEstimateTracksUnpricedCandidate(t *testing.T) {
	groups := []service.Group{{ID: 1, Platform: service.PlatformOpenAI}, {ID: 2, Platform: service.PlatformOpenAI}}
	key := cloneAPIKeyWithGroup(&service.APIKey{User: &service.User{ID: 5}}, &groups[0])
	c := newInflightTestGinContext()
	bindInflightRoutingGroups(c, groups)
	est := &routingInflightEstimator{costs: map[int64]float64{1: 0.2}}
	_, _, amount, priced := inflightRoutingEstimate(c.Request.Context(), est, key, nil, tokenInflightEstimate("m", nil))
	require.Equal(t, 0.2, amount)
	require.False(t, priced, "strict unpriced policy must see every balance candidate")
}
