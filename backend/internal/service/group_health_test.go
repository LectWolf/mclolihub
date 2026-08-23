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
	require.False(t, account.IsSchedulable(), "reaching next_probe_at must not expose a probing account to user traffic")
	account.HealthRuntimeStatus = AccountRuntimeUnavailable
	require.False(t, account.IsSchedulable())
	account.HealthRuntimeStatus = AccountRuntimeActive
	require.True(t, account.IsSchedulable())
}

func TestGroupPolicyDispatchErrorDoesNotBecomeHealthFailure(t *testing.T) {
	require.True(t, IsGroupPolicyDispatchError("Access forbidden (403): This group does not allow /v1/messages dispatch"))
	require.True(t, IsGroupPolicyDispatchError("this group does not allow /v1/messages dispatch"))
	require.False(t, IsGroupPolicyDispatchError("Access forbidden (403): invalid api key"))
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

func TestProbeNowDoesNotReportInternalErrorAfterProbeCompleted(t *testing.T) {
	healthRepo := &immediateRefreshHealthRepo{rollingMetrics: errors.New("rolling metrics unavailable")}
	accountRepo := &immediateRefreshAccountRepo{}
	service := NewGroupHealthService(healthRepo, accountRepo, &AccountTestService{}, nil, nil)

	require.NoError(t, service.ProbeNow(context.Background(), 8))
	require.NotNil(t, healthRepo.snapshot, "the completed probe result must still be persisted")
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
