package service

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestQualityAccountEligibilityDoesNotUsePlatformOrType(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformGemini, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformAnthropic, PlatformGrok, PlatformAntigravity, PlatformQoder, PlatformOpenCodeGo} {
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeServiceAccount, ""} {
			account := &Account{Platform: platform, Type: kind}
			require.True(t, accountAcceptsQualityPrompt(account), "%s/%s", platform, kind)
		}
	}
	for _, protocol := range []string{APIProtocolChatCompletions, APIProtocolResponses, APIProtocolAdaptive, APIProtocolAnthropic, "invalid"} {
		account := &Account{Platform: PlatformKimi, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": protocol}}
		require.True(t, accountAcceptsQualityPrompt(account), protocol)
	}
	require.False(t, accountAcceptsQualityPrompt(&Account{Platform: PlatformGemini, Type: AccountTypeAPIKey, Extra: map[string]any{"synthetic_ui_test": true}}))
}

func TestQualityCandidateSQLUsesSharedEligibilityBeforeLimit(t *testing.T) {
	for _, query := range []string{qualityCandidatesSQL, qualityManualCandidatesSQL} {
		require.Contains(t, query, "("+QualityAccountEligibilitySQL+")")
	}
	for _, query := range []string{qualityCandidatesSQL, qualityManualCandidatesSQL, QualityAccountEligibilitySQL} {
		require.NotContains(t, query, "a.type")
		require.NotContains(t, query, "a.platform")
		require.NotContains(t, query, "api_protocol")
	}
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	mock.ExpectExec(`(?s)INSERT INTO group_account_quality_checks.*` + regexp.QuoteMeta(QualityAccountEligibilitySQL)).WithArgs(now).WillReturnResult(sqlmock.NewResult(0, 0))
	checker := newGroupQualityChecker(db, nil, nil, nil, "test")
	require.NoError(t, checker.initializeWindows(context.Background(), now))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQualityGeminiManualAndAutomaticEligibility(t *testing.T) {
	for _, source := range []string{"manual", "automatic"} {
		for _, name := range []string{"apikey", "oauth", "serviceaccount", "image-model", "synthetic"} {
			t.Run(source+"/"+name, func(t *testing.T) {
				db, mock, err := sqlmock.New()
				require.NoError(t, err)
				defer db.Close()
				now := time.Now().UTC()
				candidate := qualityCandidate{groupID: 7, id: 11, version: 2, modelID: "alias"}
				account := &Account{ID: 11, Platform: PlatformGemini, Type: AccountTypeAPIKey}
				switch name {
				case "oauth":
					account.Type = AccountTypeOAuth
				case "serviceaccount":
					account.Type = AccountTypeServiceAccount
				case "image-model":
					account.Credentials = map[string]any{"model_mapping": map[string]any{"alias": "gemini-image"}}
				case "synthetic":
					account.Extra = map[string]any{"synthetic_ui_test": true}
				}
				checker := newQualityTestChecker(db, &qualityRunAccounts{account: account}, nil, &qualityManualLock{}, "test")
				checker.now = func() time.Time { return now }
				probes := 0
				checker.probeFunc = func(_ context.Context, id int64, model string) qualityProbeResult {
					probes++
					require.Equal(t, candidate.id, id)
					require.Equal(t, "alias", model)
					return qualityProbeResult{Status: "success", ModelID: model, Reason: "safe reason"}
				}
				var worker func(context.Context)
				checker.launch = func(fn func(context.Context)) bool { worker = fn; return true }
				eligible := name != "synthetic"
				if source == "manual" {
					if eligible {
						expectManualStart(mock, now, candidate)
					} else {
						mock.ExpectQuery(`SELECT enabled FROM group_quality_check_settings`).WithArgs(candidate.groupID).WillReturnRows(sqlmock.NewRows([]string{"enabled"}).AddRow(true))
						mock.ExpectQuery(regexp.QuoteMeta(qualityManualCandidatesSQL)).WithArgs(candidate.groupID, now).WillReturnRows(manualCandidateRows(candidate))
					}
					run, err := checker.StartManualRun(context.Background(), candidate.groupID)
					if eligible {
						require.NoError(t, err)
						expectQualitySave(mock, now, candidate, "success", 0, false, nil)
						expectManualProgress(mock, now, run, "running", "", 1)
						expectManualProgress(mock, now, run, "completed", "", 1)
						worker(context.Background())
					} else {
						require.ErrorIs(t, err, ErrQualityRunNoAccounts)
						require.Nil(t, worker)
					}
				} else {
					expectQualityRunStart(mock, now, candidate)
					if eligible {
						expectAutomaticTask(mock, now, candidate.groupID, 1)
						expectQualitySave(mock, now, candidate, "success", 0, false, nil)
						expectAutomaticProgress(mock, now, candidate.groupID, "running", 1)
						expectAutomaticProgress(mock, now, candidate.groupID, "completed", 1)
					} else {
						mock.ExpectExec(`UPDATE group_account_quality_checks SET retry_after`).WithArgs(candidate.groupID, candidate.id, now.Add(5*time.Minute)).WillReturnResult(sqlmock.NewResult(0, 1))
					}
					checker.Run(context.Background())
				}
				require.Equal(t, eligible, probes == 1)
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}
