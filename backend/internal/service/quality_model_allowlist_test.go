//go:build unit

package service

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestPelicanQualityModelAllowlistBlocksUnlistedRequests(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformGemini, "custom-provider"} {
		t.Run(platform, func(t *testing.T) {
			account := pelicanChatAccount()
			account.Platform = platform
			account.Credentials["model_mapping"] = map[string]any{"allowed-model": "allowed-model"}
			svc, upstream := pelicanMockService(account, pelicanMockResponse("<html>must not be requested</html>", "gpt-6.1-sol", true))
			result := svc.runPelicanQualityCheck(context.Background(), account.ID, "gpt-6.1-sol")
			require.Empty(t, upstream.requests, "an unlisted quality model must never be sent upstream")
			require.Equal(t, "failed", result.Status)
			require.Equal(t, "model_not_allowed", result.Diagnostics.FailureCode)
		})
	}
}

type qualityAllowlistAccounts struct {
	qualityRunAccounts
	byID map[int64]*Account
}

func (r *qualityAllowlistAccounts) GetByID(_ context.Context, id int64) (*Account, error) {
	return r.byID[id], nil
}

func TestQualityModelAllowlistSelectionSkipsUnlistedAccount(t *testing.T) {
	for _, source := range []string{"manual", "automatic"} {
		t.Run(source, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			now := time.Now().UTC()
			denied := qualityCandidate{groupID: 7, id: 109, version: 2, modelID: "gpt-6.1-sol"}
			allowed := denied
			allowed.id = 110
			accounts := &qualityAllowlistAccounts{byID: map[int64]*Account{
				denied.id:  {ID: denied.id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"other-model": "other-model"}}},
				allowed.id: {ID: allowed.id, Platform: PlatformGemini, Type: AccountTypeOAuth, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6.1-sol": "provider-model"}}},
			}}
			checker := newGroupQualityChecker(db, accounts, nil, &qualityManualLock{}, "test")
			checker.now = func() time.Time { return now }
			probes := []int64{}
			checker.probeFunc = func(_ context.Context, id int64, model string) qualityProbeResult {
				probes = append(probes, id)
				require.Equal(t, allowed.id, id)
				require.Equal(t, "gpt-6.1-sol", model)
				return qualityProbeResult{Status: "success", Reason: "safe reason", ModelID: model}
			}
			if source == "manual" {
				var worker func(context.Context)
				checker.launch = func(fn func(context.Context)) bool { worker = fn; return true }
				mock.ExpectQuery(`SELECT enabled FROM group_quality_check_settings`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"enabled"}).AddRow(true))
				mock.ExpectQuery(regexp.QuoteMeta(qualityManualCandidatesSQL)).WithArgs(int64(7), now).WillReturnRows(manualCandidateRows(denied).AddRow(allowed.groupID, allowed.id, allowed.version, allowed.modelID, "", 0, false))
				mock.ExpectBegin()
				mock.ExpectQuery(`INSERT INTO group_quality_check_runs`).WithArgs(int64(7), sqlmock.AnyArg(), 1, now, now.Add(-qualityManualRunTimeout), "manual").WillReturnRows(sqlmock.NewRows([]string{"run_id"}).AddRow("saved"))
				mock.ExpectExec(`INSERT INTO group_account_quality_checks`).WithArgs(int64(7), allowed.id, now).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
				run, err := checker.StartManualRun(context.Background(), 7)
				require.NoError(t, err)
				expectQualitySave(mock, now, allowed, "success", 0, false, nil)
				expectManualProgress(mock, now, run, "running", "", 1)
				expectManualProgress(mock, now, run, "completed", "", 1)
				worker(context.Background())
			} else {
				mock.ExpectExec(`UPDATE group_account_quality_checks SET retry_after = \$3 WHERE group_id = \$1 AND account_id = \$2`).WithArgs(denied.groupID, denied.id, now.Add(time.Hour)).WillReturnResult(sqlmock.NewResult(0, 1))
				expectAutomaticTask(mock, now, 7, 1)
				expectQualitySave(mock, now, allowed, "success", 0, false, nil)
				expectAutomaticProgress(mock, now, 7, "running", 1)
				expectAutomaticProgress(mock, now, 7, "completed", 1)
				checker.runAutomaticGroup(context.Background(), 7, []qualityCandidate{denied, allowed})
			}
			require.Equal(t, []int64{allowed.id}, probes)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestPelicanQualityModelAllowlistUsesRequestedNameAndWildcards(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformGemini} {
		for _, tc := range []struct {
			name, requested, wantModel string
			mapping                    map[string]any
			allowed, passthrough       bool
		}{
			{name: "exact", requested: "gpt-6.1-sol", wantModel: "gpt-6.1-sol", mapping: map[string]any{"gpt-6.1-sol": "gpt-6.1-sol"}, allowed: true},
			{name: "alias-once", requested: "alias", wantModel: "gpt-6.1-sol", mapping: map[string]any{"alias": "gpt-6.1-sol", "gpt-6.1-sol": "wrong-second-mapping"}, allowed: true},
			{name: "target-is-not-an-allowed-name", requested: "gpt-6.1-sol", mapping: map[string]any{"alias": "gpt-6.1-sol"}},
			{name: "wildcard", requested: "gpt-6.1-sol", wantModel: "provider-model", mapping: map[string]any{"gpt-*": "provider-model"}, allowed: true},
			{name: "no-restrictions", requested: "gpt-6.1-sol", wantModel: "gpt-6.1-sol", allowed: true},
			{name: "default-is-restricted", mapping: map[string]any{"unrelated-model": "unrelated-model"}},
			{name: "default-wildcard", wantModel: "provider-default", mapping: map[string]any{"*": "provider-default"}, allowed: true},
			{name: "explicit-passthrough", requested: "gpt-6.1-sol", wantModel: "gpt-6.1-sol", mapping: map[string]any{"old-model": "old-model"}, allowed: true, passthrough: true},
		} {
			t.Run(platform+"/"+tc.name, func(t *testing.T) {
				account := pelicanChatAccount()
				account.Platform = platform
				account.Credentials["model_mapping"] = tc.mapping
				if tc.passthrough {
					account.Extra = map[string]any{"openai_passthrough": true}
				}
				if tc.passthrough && platform != PlatformOpenAI {
					t.Skip("OpenAI passthrough is an OpenAI account setting")
				}
				svc, upstream := pelicanMockService(account, pelicanMockResponse("<html>ok</html>", "actual", true))
				result := svc.runPelicanQualityCheck(context.Background(), account.ID, tc.requested)
				if !tc.allowed {
					require.Empty(t, upstream.requests)
					require.Equal(t, "model_not_allowed", result.Diagnostics.FailureCode)
					return
				}
				require.Equal(t, "success", result.Status, result.Reason)
				require.Len(t, upstream.requests, 1)
				requirePelicanStudioRequest(t, upstream.requests[0], tc.wantModel, DefaultScheduledTestPrompt)
			})
		}
	}
}

func TestQualityModelAllowlistNoMatchingAccountDoesNotLaunch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	candidate := qualityCandidate{groupID: 7, id: 109, version: 2, modelID: "gpt-6.1-sol"}
	account := pelicanChatAccount()
	account.Credentials["model_mapping"] = map[string]any{"other": "other"}
	checker := newGroupQualityChecker(db, &qualityRunAccounts{account: account}, nil, &qualityManualLock{}, "test")
	checker.now = func() time.Time { return now }
	checker.launch = func(func(context.Context)) bool { t.Fatal("must not create a worker"); return false }
	checker.probeFunc = func(context.Context, int64, string) qualityProbeResult {
		t.Fatal("must not probe")
		return qualityProbeResult{}
	}
	mock.ExpectQuery(`SELECT enabled FROM group_quality_check_settings`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"enabled"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(qualityManualCandidatesSQL)).WithArgs(int64(7), now).WillReturnRows(manualCandidateRows(candidate))
	run, err := checker.StartManualRun(context.Background(), 7)
	require.ErrorIs(t, err, ErrQualityRunModelNotAllowed)
	require.Nil(t, run)
	require.NoError(t, mock.ExpectationsWereMet())
}

type qualityAllowlistChangingRepo struct {
	*openAIAccountTestRepo
	calls int
}

func (r *qualityAllowlistChangingRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	r.calls++
	account, err := r.openAIAccountTestRepo.GetByID(ctx, id)
	if r.calls == 2 {
		account.Credentials["model_mapping"] = map[string]any{"other": "other"}
	}
	return account, err
}

func TestPelicanQualityModelAllowlistRechecksBeforeRequest(t *testing.T) {
	account := pelicanChatAccount()
	account.Credentials["model_mapping"] = map[string]any{"gpt-6.1-sol": "gpt-6.1-sol"}
	svc, upstream := pelicanMockService(account, pelicanMockResponse("<html>must not request</html>", "actual", true))
	repo := &qualityAllowlistChangingRepo{openAIAccountTestRepo: svc.accountRepo.(*openAIAccountTestRepo)}
	svc.accountRepo = repo
	result := svc.runPelicanQualityCheck(context.Background(), account.ID, "gpt-6.1-sol")
	require.GreaterOrEqual(t, repo.calls, 2)
	require.Empty(t, upstream.requests)
	require.Equal(t, "model_not_allowed", result.Diagnostics.FailureCode)
}

func TestPelicanQualityModelAllowlistUsesSelectedShadowPolicy(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformGemini} {
		t.Run(platform, func(t *testing.T) {
			parent := pelicanChatAccount()
			parent.ID = 902
			parent.Credentials["model_mapping"] = map[string]any{"gpt-6.1-sol": "gpt-6.1-sol"}
			selected := &Account{ID: 901, Platform: platform, Type: AccountTypeOAuth, ParentAccountID: &parent.ID, Credentials: map[string]any{"model_mapping": map[string]any{"other": "other"}}}
			svc, upstream := pelicanMockService(selected)
			svc.accountRepo.(*openAIAccountTestRepo).accountsByID[parent.ID] = parent
			result := svc.runPelicanQualityCheck(context.Background(), selected.ID, "gpt-6.1-sol")
			require.Empty(t, upstream.requests)
			require.Equal(t, "model_not_allowed", result.Diagnostics.FailureCode)
		})
	}
}

func TestPelicanQualityModelAllowlistGeminiDirectEntry(t *testing.T) {
	account := pelicanGeminiAccount()
	account.Credentials["model_mapping"] = map[string]any{"other": "other"}
	svc, upstream := pelicanMockService(account)
	c, _ := newTestContext()
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), pelicanQualityContextKey{}, true))
	err := svc.testGeminiQualityChatCompletionsConnection(c, account, "gpt-6.1-sol", DefaultScheduledTestPrompt)
	require.EqualError(t, err, qualityModelNotAllowedReason)
	require.Empty(t, upstream.requests)
}

func TestQualityModelAllowlistAutomaticSkipPreservesHistory(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	candidate := qualityCandidate{groupID: 7, id: 109, version: 2, modelID: "gpt-6.1-sol"}
	account := pelicanChatAccount()
	account.Credentials["model_mapping"] = map[string]any{"other": "other"}
	checker := newGroupQualityChecker(db, &qualityRunAccounts{account: account}, nil, &qualityManualLock{}, "test")
	checker.now = func() time.Time { return now }
	checker.probeFunc = func(context.Context, int64, string) qualityProbeResult {
		t.Fatal("must not probe an unlisted model")
		return qualityProbeResult{}
	}
	mock.ExpectExec(`^UPDATE group_account_quality_checks SET retry_after = \$3 WHERE group_id = \$1 AND account_id = \$2$`).WithArgs(candidate.groupID, candidate.id, now.Add(time.Hour)).WillReturnResult(sqlmock.NewResult(0, 1))
	checker.runAutomaticGroup(context.Background(), candidate.groupID, []qualityCandidate{candidate})
	require.NoError(t, mock.ExpectationsWereMet())
}
