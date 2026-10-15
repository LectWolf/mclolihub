//go:build unit

package service

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type qualityGatewayTestRepo struct{ schedulerTestOpenAIAccountRepo }

func (r qualityGatewayTestRepo) ListSchedulableByGroupIDAndPlatforms(_ context.Context, groupID int64, platforms []string) ([]Account, error) {
	var result []Account
	for _, account := range r.accounts {
		for _, platform := range platforms {
			if account.Platform == platform && openAIStickyAccountMatchesGroup(&account, &groupID) {
				result = append(result, account)
				break
			}
		}
	}
	return result, nil
}

func qualityGatewaySchedulerFixture(platform string, advanced bool) (*gatewayQualityAccountScheduler, *Group, *[]int64) {
	group := &Group{ID: 7, Name: "quality-group", Platform: platform, Status: StatusActive}
	until := time.Now().Add(time.Hour)
	accounts := []Account{
		{ID: 109, Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, RateLimitResetAt: &until},
		{ID: 110, Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, Credentials: map[string]any{"model_mapping": map[string]any{"other": "other"}}},
		{ID: 111, Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 20},
		{ID: 112, Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 5},
	}
	for i := range accounts {
		accounts[i].AccountGroups = []AccountGroup{{GroupID: 7, AccountID: accounts[i].ID}}
	}
	repo := qualityGatewayTestRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	cfg.Gateway.OpenAIWS.LBTopK = 1
	released := &[]int64{}
	concurrency := NewConcurrencyService(schedulerTestConcurrencyCache{releasedIDs: released})
	gateway := &GatewayService{accountRepo: repo, groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{7: group}}, cfg: cfg, concurrencyService: concurrency}
	openai := &OpenAIGatewayService{accountRepo: repo, cfg: cfg, cache: &schedulerTestGatewayCache{}, concurrencyService: concurrency}
	if advanced {
		openai.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
	} else {
		resetOpenAIAdvancedSchedulerSettingCacheForTest()
	}
	return &gatewayQualityAccountScheduler{gateway: gateway, openai: openai, concurrency: concurrency}, group, released
}

func TestQualityGroupSchedulerUsesExistingPriorityAndCooldownGates(t *testing.T) {
	for _, tc := range []struct {
		name, platform string
		advanced       bool
	}{
		{"openai-legacy", PlatformOpenAI, false},
		{"openai-advanced", PlatformOpenAI, true},
		{"anthropic", PlatformAnthropic, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheduler, _, released := qualityGatewaySchedulerFixture(tc.platform, tc.advanced)
			selected, err := scheduler.Select(context.Background(), 7, "requested", map[int64]struct{}{})
			require.NoError(t, err)
			require.Equal(t, int64(112), selected.Account.ID, "priority must come from the gateway; 109 is cooling down and 110 rejects the model")
			require.Equal(t, "requested", selected.Model)
			releaseQualityAccount(selected)
			releaseQualityAccount(selected)
			require.Equal(t, []int64{112}, *released, "release must be idempotent")
			selected, err = scheduler.Select(context.Background(), 7, "requested", map[int64]struct{}{112: {}})
			require.NoError(t, err)
			require.Equal(t, int64(111), selected.Account.ID)
			releaseQualityAccount(selected)
		})
	}
}

func TestQualityGroupSchedulerHonorsGroupModelRoute(t *testing.T) {
	scheduler, group, _ := qualityGatewaySchedulerFixture(PlatformAnthropic, false)
	group.ModelRoutingEnabled = true
	group.ModelRouting = map[string][]int64{"requested": {111}}
	selected, err := scheduler.Select(context.Background(), 7, "requested", map[int64]struct{}{})
	require.NoError(t, err)
	defer releaseQualityAccount(selected)
	require.Equal(t, int64(111), selected.Account.ID, "configured model route must take precedence over ordinary priority")
}

func TestQualityGroupSchedulerUsesChannelMappingBeforeAccountMapping(t *testing.T) {
	scheduler, _, _ := qualityGatewaySchedulerFixture(PlatformOpenAI, false)
	channel := &Channel{ID: 5, Status: StatusActive, GroupIDs: []int64{7}, ModelMapping: map[string]map[string]string{PlatformOpenAI: {"public-model": "requested"}}}
	service := &ChannelService{}
	service.cache.Store(populateChannelCache([]Channel{*channel}, map[int64]string{7: PlatformOpenAI}))
	scheduler.gateway.channelService = service
	scheduler.openai.channelService = service
	selected, err := scheduler.Select(context.Background(), 7, "public-model", map[int64]struct{}{})
	require.NoError(t, err)
	defer releaseQualityAccount(selected)
	require.Equal(t, "requested", selected.Model)
}

func TestQualityGroupSchedulerFailureSwitchesAndReleasesSlots(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	scheduler, _, released := qualityGatewaySchedulerFixture(PlatformOpenAI, false)
	checker := newGroupQualityChecker(db, scheduler.gateway.accountRepo, nil, qualityRunLock{}, "test")
	checker.scheduler = scheduler
	now := time.Now().UTC()
	checker.now = func() time.Time { return now }
	first := qualityCandidate{groupID: 7, id: 112, version: 2, modelID: "requested"}
	second := first
	second.id = 111
	run := &GroupQualityRun{RunID: "run", GroupID: 7, StartedAt: now, TotalAccounts: 1}
	probes := []int64{}
	checker.probeFunc = func(ctx context.Context, id int64, model string) qualityProbeResult {
		require.Equal(t, true, ctx.Value(qualityScheduledRequestContextKey{}))
		probes = append(probes, id)
		if id == 112 {
			return qualityProbeResult{Status: "failed", Reason: "safe reason", ModelID: model}
		}
		require.Contains(t, *released, int64(112), "previous account slot must be released before failover")
		return qualityProbeResult{Status: "success", Reason: "safe reason", ModelID: model}
	}
	expectQualitySave(mock, now, first, "failed", 0, false, nil)
	expectManualProgress(mock, now, run, "running", "", 1)
	expectQualitySave(mock, now, second, "success", 0, false, nil)
	expectManualProgress(mock, now, run, "running", "", 2)
	expectManualProgress(mock, now, run, "completed", "", 2)
	checker.executeQualityRun(context.Background(), run, []qualityCandidate{first, second})
	require.Equal(t, []int64{112, 111}, probes)
	require.Equal(t, []int64{112, 111}, *released)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQualityGroupSchedulerGenericChannelMappingKeepsPublicAllowlist(t *testing.T) {
	scheduler, _, _ := qualityGatewaySchedulerFixture(PlatformAnthropic, false)
	repo := scheduler.gateway.accountRepo.(qualityGatewayTestRepo)
	repo.accounts[3].Credentials = map[string]any{"model_mapping": map[string]any{"public-model": "public-model"}, "api_key": "mock-secret", "base_url": "http://chat-test.example/v1"}
	channel := Channel{ID: 5, Status: StatusActive, GroupIDs: []int64{7}, ModelMapping: map[string]map[string]string{PlatformAnthropic: {"public-model": "channel-target"}}}
	channelService := &ChannelService{}
	channelService.cache.Store(populateChannelCache([]Channel{channel}, map[int64]string{7: PlatformAnthropic}))
	scheduler.gateway.channelService = channelService
	selected, err := scheduler.Select(context.Background(), 7, "public-model", make(map[int64]struct{}))
	require.NoError(t, err)
	defer releaseQualityAccount(selected)
	require.Equal(t, int64(112), selected.Account.ID)
	require.Equal(t, "channel-target", selected.Model)
	require.Equal(t, "public-model", qualityPolicyModel(selected.Context, selected.Account, selected.Model))
	svc, upstream := pelicanMockService(selected.Account, pelicanMockResponse("<html>ok</html>", "actual", true))
	result := svc.runPelicanQualityCheck(selected.Context, selected.Account.ID, selected.Model)
	require.Equal(t, "success", result.Status, result.Reason)
	require.Len(t, upstream.requests, 1)
	requirePelicanStudioRequest(t, upstream.requests[0], "channel-target", DefaultScheduledTestPrompt)
}

func TestQualityGroupSchedulerManualInitializesCompleteFailoverPool(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	scheduler, _, _ := qualityGatewaySchedulerFixture(PlatformOpenAI, false)
	checker := newGroupQualityChecker(db, scheduler.gateway.accountRepo, nil, qualityRunLock{}, "test")
	checker.scheduler = scheduler
	now := time.Now().UTC()
	checker.now = func() time.Time { return now }
	checker.probeFunc = func(context.Context, int64, string) qualityProbeResult { return qualityProbeResult{} }
	var worker func(context.Context)
	checker.launch = func(fn func(context.Context)) bool { worker = fn; return true }
	first := qualityCandidate{groupID: 7, id: 112, version: 2, modelID: "requested"}
	second := first
	second.id = 111
	mock.ExpectQuery(`SELECT enabled FROM group_quality_check_settings`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"enabled"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(qualityManualCandidatesSQL)).WithArgs(int64(7), now).WillReturnRows(manualCandidateRows(first).AddRow(second.groupID, second.id, second.version, second.modelID, "", 0, false))
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO group_quality_check_runs`).WithArgs(int64(7), sqlmock.AnyArg(), 1, now, now.Add(-qualityManualRunTimeout), "manual").WillReturnRows(sqlmock.NewRows([]string{"run_id"}).AddRow("saved"))
	mock.ExpectExec(`INSERT INTO group_account_quality_checks`).WithArgs(int64(7), first.id, now).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO group_account_quality_checks`).WithArgs(int64(7), second.id, now).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	run, err := checker.StartManualRun(context.Background(), 7)
	require.NoError(t, err, "all failover state must exist before launching the worker")
	require.NotNil(t, run)
	require.NotNil(t, worker)
	require.NoError(t, mock.ExpectationsWereMet())
}
