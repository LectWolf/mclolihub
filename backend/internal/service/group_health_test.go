package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type healthStoreStub struct {
	snapshot *GroupHealthSnapshot
	events   []GroupHealthEventInput
}

type balanceRestoreHealthRepo struct {
	GroupHealthRepository
	snapshot *GroupHealthSnapshot
	restored bool
	events   []GroupHealthEventInput
}

func (s *balanceRestoreHealthRepo) RestoreAccountBalance(context.Context, int64, time.Time) error {
	s.restored = true
	return nil
}
func (s *balanceRestoreHealthRepo) RefreshDerivedGroupHealth(context.Context, int64, time.Time) error {
	return nil
}
func (s *balanceRestoreHealthRepo) Load(context.Context, int64) (*GroupHealthSnapshot, error) {
	return s.snapshot, nil
}
func (s *balanceRestoreHealthRepo) Save(_ context.Context, snapshot *GroupHealthSnapshot) error {
	s.snapshot = snapshot
	return nil
}
func (s *balanceRestoreHealthRepo) RecordEvent(_ context.Context, event GroupHealthEventInput) error {
	s.events = append(s.events, event)
	return nil
}

type balanceRestoreAccountRepo struct {
	AccountRepository
	account *Account
}

func (s *balanceRestoreAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return s.account, nil
}

func (s *healthStoreStub) Load(context.Context, int64) (*GroupHealthSnapshot, error) {
	return s.snapshot, nil
}
func (s *healthStoreStub) Save(_ context.Context, snapshot *GroupHealthSnapshot) error {
	s.snapshot = snapshot
	return nil
}
func (s *healthStoreStub) RecordEvent(_ context.Context, event GroupHealthEventInput) error {
	s.events = append(s.events, event)
	return nil
}

func TestAccountProbeScheduleAndThrottle(t *testing.T) {
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	wants := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute}
	for step, want := range wants {
		require.Equal(t, now.Add(want), NextAccountProbeAt(now, step, 10*time.Minute))
	}
	last := now.Add(-9 * time.Minute)
	require.False(t, CanTriggerImmediateProbe(&last, now))
	last = now.Add(-10 * time.Minute)
	require.True(t, CanTriggerImmediateProbe(&last, now))
}

func TestNormalizeGroupProbeConfig(t *testing.T) {
	model, seconds, err := NormalizeGroupProbeConfig("", 0)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.6-sol", model)
	require.Equal(t, 600, seconds)

	model, seconds, err = NormalizeGroupProbeConfig(" custom-model ", 30)
	require.NoError(t, err)
	require.Equal(t, "custom-model", model)
	require.Equal(t, 30, seconds)

	_, _, err = NormalizeGroupProbeConfig("model", 29)
	require.Error(t, err)
}

func TestDeriveGroupHealth(t *testing.T) {
	require.Equal(t, GroupHealthHealthy, DeriveGroupHealth([]AccountHealth{{RuntimeStatus: AccountRuntimeBalance}, {RuntimeStatus: AccountRuntimeActive, Schedulable: true}}))
	require.Equal(t, GroupHealthBalanceInsufficient, DeriveGroupHealth([]AccountHealth{{RuntimeStatus: AccountRuntimeFailed}, {RuntimeStatus: AccountRuntimeBalance}}))
	require.Equal(t, GroupHealthUnavailable, DeriveGroupHealth([]AccountHealth{{RuntimeStatus: AccountRuntimeFailed}}))
}

func TestRankGroupCandidates(t *testing.T) {
	candidates := []GroupRouteCandidate{
		{GroupID: 1, RateMultiplier: 1, Healthy: true, ProbeEnabled: true, RealTTFTP50MS: 6000, RealTTFTSamples: 2, ProbeTTFTMS: 100},
		{GroupID: 2, RateMultiplier: .5, Healthy: true, ProbeEnabled: true, ProbeTTFTMS: 50},
		{GroupID: 3, RateMultiplier: .2, Healthy: false, ProbeEnabled: true},
	}
	got, err := RankGroupCandidates(RouteModeFastest, nil, candidates)
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2}, []int64{got[0].GroupID, got[1].GroupID}, "real samples must always precede probe-only candidates")
	max := .75
	got, err = RankGroupCandidates(RouteModeCheapest, &max, candidates)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, []int64{got[0].GroupID})
}

func TestFixedBypassesHealthButNotMaxRate(t *testing.T) {
	max := 1.0
	got, err := RankGroupCandidates(RouteModeFixed, &max, []GroupRouteCandidate{{GroupID: 1, RateMultiplier: .9, Healthy: false, ProbeEnabled: true}, {GroupID: 2, RateMultiplier: 1.1}})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(1), got[0].GroupID)
	require.True(t, CanFailover(true, true, false, true))
	require.False(t, CanFailover(true, true, true, true))
}

func TestCanFailoverOnlyBeforeSemanticOutput(t *testing.T) {
	require.True(t, CanFailover(true, true, false, true))
	require.False(t, CanFailover(false, true, false, true), "fixed routing must keep its existing account behavior")
	require.False(t, CanFailover(true, false, false, true), "non-text endpoints must not be replayed")
	require.False(t, CanFailover(true, true, true, true), "semantic output makes replay unsafe")
	require.False(t, CanFailover(true, true, false, false), "non-retryable errors must be returned unchanged")
}

func TestStoppedGroupHealthServiceRejectsNewTasks(t *testing.T) {
	service := NewGroupHealthService(nil, nil, nil, nil, nil)
	service.Stop()
	require.False(t, service.submitProbeTask(func() { t.Fatal("task must not run after Stop") }))
}

func TestRestoreBalanceImmediatelyRefreshesHealthyGroup(t *testing.T) {
	healthRepo := &balanceRestoreHealthRepo{snapshot: &GroupHealthSnapshot{GroupID: 8, Status: GroupHealthHealthy}}
	accountRepo := &balanceRestoreAccountRepo{account: &Account{ID: 4, GroupIDs: []int64{8}}}
	service := NewGroupHealthService(healthRepo, accountRepo, nil, nil, nil)

	require.NoError(t, service.RestoreBalance(context.Background(), 4))
	require.True(t, healthRepo.restored)
	require.Equal(t, "admin_balance_restored", healthRepo.snapshot.Reason)
	require.NotNil(t, healthRepo.snapshot.LastSuccessAt)
	require.Equal(t, []GroupHealthEventInput{{
		GroupID: 8, AccountID: 4, Kind: "admin_recovery", Success: true,
		ErrorCategory: "balance_restored", ErrorMessage: "admin_balance_restored",
		ObservedAt: *healthRepo.snapshot.LastSuccessAt,
	}}, healthRepo.events)
}

func TestProbeRoundStopsAtFirstSuccess(t *testing.T) {
	store := &healthStoreStub{}
	runtime := NewGroupHealthRuntime(store)
	var called []int64
	snapshot, err := runtime.ProbeRound(context.Background(), 10, "gpt-5.6-sol", []AccountHealth{{ID: 1, RuntimeStatus: AccountRuntimeActive, Schedulable: true}, {ID: 2, RuntimeStatus: AccountRuntimeActive, Schedulable: true}}, func(_ context.Context, _, accountID int64, _ string) (time.Duration, time.Duration, error) {
		called = append(called, accountID)
		if accountID == 1 {
			return 0, 0, errors.New("down")
		}
		return 1200 * time.Millisecond, 2 * time.Second, nil
	}, time.Now())
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2}, called)
	require.Equal(t, GroupHealthHealthy, snapshot.Status)
	require.Len(t, store.events, 2)
}
