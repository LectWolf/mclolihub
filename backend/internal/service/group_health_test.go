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

type immediateRefreshHealthRepo struct {
	GroupHealthRepository
	snapshot       *GroupHealthSnapshot
	rollingMetrics error
}

func (s *immediateRefreshHealthRepo) GetProbeGroup(context.Context, int64) (GroupProbeTarget, error) {
	return GroupProbeTarget{GroupID: 8, Model: "gpt-5.6-sol", Interval: 10 * time.Minute, ProbeEnabled: true}, nil
}
func (s *immediateRefreshHealthRepo) LoadAccountHealth(context.Context, []int64) (map[int64]AccountHealthState, error) {
	return map[int64]AccountHealthState{}, nil
}
func (s *immediateRefreshHealthRepo) RefreshDerivedGroupHealth(context.Context, int64, time.Time) error {
	return nil
}
func (s *immediateRefreshHealthRepo) Load(context.Context, int64) (*GroupHealthSnapshot, error) {
	return s.snapshot, nil
}
func (s *immediateRefreshHealthRepo) Save(_ context.Context, snapshot *GroupHealthSnapshot) error {
	s.snapshot = snapshot
	return nil
}
func (s *immediateRefreshHealthRepo) UpdateRollingMetrics(context.Context, time.Time) error {
	return s.rollingMetrics
}

type occupancyHealthRepo struct {
	immediateRefreshHealthRepo
	recent bool
	since  time.Time
}

func (s *occupancyHealthRepo) HasRecentUserSuccess(_ context.Context, _ int64, since time.Time) (bool, error) {
	s.since = since
	return s.recent, nil
}

type immediateRefreshAccountRepo struct {
	AccountRepository
	accounts []Account
}

func (s *immediateRefreshAccountRepo) ListByGroup(context.Context, int64) ([]Account, error) {
	return s.accounts, nil
}
func (s *immediateRefreshAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	for i := range s.accounts {
		if s.accounts[i].ID == id {
			return &s.accounts[i], nil
		}
	}
	return nil, ErrAccountNotFound
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
	wants := []time.Duration{30 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute}
	for step, want := range wants {
		require.Equal(t, now.Add(want), NextAccountProbeAt(now, step, 10*time.Minute))
	}
	last := now.Add(-119 * time.Second)
	require.False(t, CanTriggerImmediateProbe(&last, now))
	last = now.Add(-2 * time.Minute)
	require.True(t, CanTriggerImmediateProbe(&last, now))
}

func TestGroupProbeSlotBoundsAlignToFiveMinutes(t *testing.T) {
	start, end := groupProbeSlotBounds(time.Date(2026, 9, 6, 12, 4, 50, 0, time.UTC))
	require.Equal(t, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC), start)
	require.Equal(t, time.Date(2026, 9, 6, 12, 5, 0, 0, time.UTC), end)
}

func TestDecideScheduledGroupProbeDefersTrafficToSlotEnd(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 2, 0, 0, time.UTC)
	last := time.Date(2026, 9, 6, 11, 0, 0, 0, time.UTC)
	skip, next, reason := decideScheduledGroupProbe(now, 10*time.Minute, true, &last)
	require.True(t, skip)
	require.Equal(t, time.Date(2026, 9, 6, 12, 5, 0, 0, time.UTC), next)
	require.Equal(t, "skipped_recent_user_traffic", reason)
}

func TestDecideScheduledGroupProbeDefersRecentProbeToHeartbeat(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 6, 0, 0, time.UTC)
	last := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	skip, next, reason := decideScheduledGroupProbe(now, 10*time.Minute, false, &last)
	require.True(t, skip)
	require.Equal(t, time.Date(2026, 9, 6, 12, 10, 0, 0, time.UTC), next)
	require.Equal(t, "skipped_recent_probe", reason)
}

func TestDecideScheduledGroupProbeRunsWhenIdleAndIntervalElapsed(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 5, 0, 0, time.UTC)
	last := time.Date(2026, 9, 6, 11, 50, 0, 0, time.UTC)
	skip, next, reason := decideScheduledGroupProbe(now, 10*time.Minute, false, &last)
	require.False(t, skip)
	require.True(t, next.IsZero())
	require.Empty(t, reason)
}

func TestDecideScheduledGroupProbeWakesAtSlotEndWhenTrafficOverlapsStaleProbe(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 2, 0, 0, time.UTC)
	last := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	skip, next, reason := decideScheduledGroupProbe(now, 10*time.Minute, true, &last)
	require.True(t, skip)
	require.Equal(t, time.Date(2026, 9, 6, 12, 5, 0, 0, time.UTC), next, "re-ask at the next bar, not lastProbe+interval")
	require.Equal(t, "skipped_recent_user_traffic", reason)
}

func TestScheduledProbeCanInspectEveryRuntimeStateWithoutAdvancingRecovery(t *testing.T) {
	require.True(t, CanRunScheduledProbe(AccountHealthState{RuntimeStatus: AccountRuntimeProbing}),
		"the ten-minute sweep may recover an account during its own probing schedule")
	require.True(t, CanRunScheduledProbe(AccountHealthState{RuntimeStatus: AccountRuntimeUnavailable}),
		"the ten-minute sweep is the recovery path for exhausted accounts")
	require.False(t, CanRunScheduledProbe(AccountHealthState{RuntimeStatus: AccountRuntimeBalance}))
	require.True(t, CanRunScheduledProbe(AccountHealthState{RuntimeStatus: AccountRuntimeActive}))
	require.True(t, CanRunScheduledProbe(AccountHealthState{}))
}

func TestProbeFailureProgressionEndsInUnavailable(t *testing.T) {
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	wants := []struct {
		step   int
		status string
		delay  time.Duration
	}{
		{step: 0, status: AccountRuntimeProbing, delay: 30 * time.Second},
		{step: 1, status: AccountRuntimeProbing, delay: 30 * time.Second},
		{step: 2, status: AccountRuntimeProbing, delay: time.Minute},
		{step: 3, status: AccountRuntimeProbing, delay: 2 * time.Minute},
		{step: 4, status: AccountRuntimeProbing, delay: 5 * time.Minute},
	}
	for _, want := range wants {
		status, next := NextAccountProbeState(now, want.step)
		require.Equal(t, want.status, status)
		if want.delay == 0 {
			require.Nil(t, next)
		} else {
			require.Equal(t, now.Add(want.delay), *next)
		}
	}
}

func TestGroupRouteHealthDefaultsAvailableUntilExplicitFailure(t *testing.T) {
	require.True(t, IsGroupRouteHealthy(""))
	require.True(t, IsGroupRouteHealthy(GroupHealthUnknown))
	require.True(t, IsGroupRouteHealthy(GroupHealthHealthy))
	require.False(t, IsGroupRouteHealthy(GroupHealthUnavailable))
	require.False(t, IsGroupRouteHealthy(GroupHealthBalanceInsufficient))
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
	require.Equal(t, GroupHealthBalanceInsufficient, DeriveGroupHealth([]AccountHealth{{RuntimeStatus: AccountRuntimeProbing}, {RuntimeStatus: AccountRuntimeBalance}}))
	require.Equal(t, GroupHealthUnavailable, DeriveGroupHealth([]AccountHealth{{RuntimeStatus: AccountRuntimeProbing}}))
	require.Equal(t, GroupHealthUnavailable, DeriveGroupHealth([]AccountHealth{{RuntimeStatus: AccountRuntimeUnavailable}}))
}

func TestHealthRuntimeBlocksUserSchedulingUntilProbeSuccess(t *testing.T) {
	account := &Account{Status: StatusActive, Schedulable: true, HealthRuntimeStatus: AccountRuntimeProbing}
	require.True(t, account.IsSchedulable(), "fixed single-group keys keep using probe-quarantined accounts")
	require.True(t, account.IsSchedulableForRequest(context.Background()), "fixed requests do not apply the probe gate")
	dynamic := WithGroupHealthAccountGate(context.Background(), true)
	require.False(t, account.IsSchedulableForRequest(dynamic), "dynamic routing must hide probing accounts")
	account.HealthRuntimeStatus = AccountRuntimeUnavailable
	require.True(t, account.IsSchedulable())
	require.False(t, account.IsSchedulableForRequest(dynamic))
	account.HealthRuntimeStatus = AccountRuntimeActive
	require.True(t, account.IsSchedulable())
	require.True(t, account.IsSchedulableForRequest(dynamic))
	future := time.Now().Add(time.Hour)
	account.TempUnschedulableUntil = &future
	account.TempUnschedulableReason = "group_health_probe: waiting for recovery probe"
	require.True(t, account.IsSchedulable(), "legacy probe temp-unsched must not block fixed keys")
	require.False(t, account.IsSchedulableForRequest(dynamic))
}

func TestGroupPolicyDispatchErrorDoesNotBecomeHealthFailure(t *testing.T) {
	require.True(t, IsGroupPolicyDispatchError("Access forbidden (403): This group does not allow /v1/messages dispatch"))
	require.True(t, IsGroupPolicyDispatchError("this group does not allow /v1/messages dispatch"))
	require.False(t, IsGroupPolicyDispatchError("Access forbidden (403): invalid api key"))
}

func TestRankGroupCandidates(t *testing.T) {
	candidates := []GroupRouteCandidate{
		{GroupID: 1, RateMultiplier: 1, Healthy: true, ProbeEnabled: true, CustomPosition: 1},
		{GroupID: 2, RateMultiplier: .5, Healthy: true, ProbeEnabled: true, CustomPosition: 0},
		{GroupID: 3, RateMultiplier: .2, Healthy: false, ProbeEnabled: true, CustomPosition: 2},
	}
	got, err := RankGroupCandidates(RouteModeSmart, nil, candidates)
	require.NoError(t, err)
	require.Equal(t, []int64{2, 1}, []int64{got[0].GroupID, got[1].GroupID}, "smart routing follows position and skips unhealthy probed groups")
	max := .75
	got, err = RankGroupCandidates(RouteModeSmart, &max, candidates)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, []int64{got[0].GroupID})
}

func TestRankGroupCandidatesIncludesEqualMaxRate(t *testing.T) {
	max := 0.09
	got, err := RankGroupCandidates(RouteModeSmart, &max, []GroupRouteCandidate{
		{GroupID: 1, RateMultiplier: 0.09, Healthy: true, ProbeEnabled: true, CustomPosition: 0},
		{GroupID: 2, RateMultiplier: 0.0901, Healthy: true, ProbeEnabled: true, CustomPosition: 1},
	})
	require.NoError(t, err)
	require.Equal(t, []int64{1}, []int64{got[0].GroupID})
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

func TestZeroMaxRateMultiplierIsUnlimited(t *testing.T) {
	zero := 0.0
	candidates := []GroupRouteCandidate{
		{GroupID: 1, RateMultiplier: 1.5, Healthy: true, ProbeEnabled: true},
		{GroupID: 2, RateMultiplier: .2, Healthy: true, ProbeEnabled: true},
	}
	got, err := RankGroupCandidates(RouteModeSmart, &zero, candidates)
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2}, []int64{got[0].GroupID, got[1].GroupID})
	got, err = RankGroupCandidates(RouteModeFixed, &zero, []GroupRouteCandidate{{GroupID: 3, RateMultiplier: 9}})
	require.NoError(t, err)
	require.Equal(t, []int64{3}, []int64{got[0].GroupID})
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

func TestProbeNowDoesNotReportInternalErrorAfterProbeCompleted(t *testing.T) {
	healthRepo := &immediateRefreshHealthRepo{rollingMetrics: errors.New("rolling metrics unavailable")}
	accountRepo := &immediateRefreshAccountRepo{}
	service := NewGroupHealthService(healthRepo, accountRepo, &AccountTestService{}, nil, nil)

	require.NoError(t, service.ProbeNow(context.Background(), 8))
	require.NotNil(t, healthRepo.snapshot, "the completed probe result must still be persisted")
}

func TestScheduledProbeDefersToSlotEndWhenCurrentBarHasTraffic(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 2, 0, 0, time.UTC)
	healthRepo := &occupancyHealthRepo{recent: true}
	svc := NewGroupHealthService(healthRepo, &immediateRefreshAccountRepo{}, &AccountTestService{}, nil, nil)
	require.NoError(t, svc.probeGroup(context.Background(), GroupProbeTarget{GroupID: 8, Interval: 10 * time.Minute, ProbeEnabled: true}, now, false))
	require.Equal(t, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC), healthRepo.since)
	require.Equal(t, "skipped_recent_user_traffic", healthRepo.snapshot.Reason)
	require.Equal(t, time.Date(2026, 9, 6, 12, 5, 0, 0, time.UTC), healthRepo.snapshot.NextProbeAt.UTC())
	require.Nil(t, healthRepo.snapshot.LastProbeAt)
}

func TestScheduledProbeDefersWhenIntervalHasNotElapsed(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 6, 0, 0, time.UTC)
	last := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	healthRepo := &occupancyHealthRepo{immediateRefreshHealthRepo: immediateRefreshHealthRepo{snapshot: &GroupHealthSnapshot{GroupID: 8, LastProbeAt: &last}}}
	svc := NewGroupHealthService(healthRepo, &immediateRefreshAccountRepo{}, &AccountTestService{}, nil, nil)
	require.NoError(t, svc.probeGroup(context.Background(), GroupProbeTarget{GroupID: 8, Interval: 10 * time.Minute, ProbeEnabled: true}, now, false))
	require.Equal(t, "skipped_recent_probe", healthRepo.snapshot.Reason)
	require.Equal(t, time.Date(2026, 9, 6, 12, 10, 0, 0, time.UTC), healthRepo.snapshot.NextProbeAt.UTC())
	require.Equal(t, last, *healthRepo.snapshot.LastProbeAt)
}

func TestProbeNowIgnoresOccupancySkip(t *testing.T) {
	healthRepo := &occupancyHealthRepo{recent: true}
	svc := NewGroupHealthService(healthRepo, &immediateRefreshAccountRepo{}, &AccountTestService{}, nil, nil)
	require.NoError(t, svc.ProbeNow(context.Background(), 8))
	require.NotNil(t, healthRepo.snapshot.LastProbeAt)
	require.NotEqual(t, "skipped_recent_user_traffic", healthRepo.snapshot.Reason)
}

func TestOpenAIGroupProbeNeverDispatchesAnthropicAccount(t *testing.T) {
	accountRepo := &immediateRefreshAccountRepo{accounts: []Account{{
		ID: 41, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true,
	}}}
	service := NewGroupHealthService(&immediateRefreshHealthRepo{}, accountRepo, &AccountTestService{}, nil, nil)

	result, ran := service.runAccountProbe(
		context.Background(), 8, 41, PlatformOpenAI, "gpt-5.6-sol",
	)

	require.False(t, ran, "OpenAI group probes must reject non-OpenAI accounts before protocol dispatch")
	require.Nil(t, result)
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
