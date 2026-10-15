package service

import (
	"bytes"
	"context"
	"database/sql"
	"image"
	"image/png"
	"math"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

type qualityRunAccounts struct {
	AccountRepository
	account    *Account
	reconciled []int64
}

func (r *qualityRunAccounts) GetByID(context.Context, int64) (*Account, error) { return r.account, nil }
func (r *qualityRunAccounts) ReconcileGroupQualityScheduling(_ context.Context, id int64) error {
	r.reconciled = append(r.reconciled, id)
	return nil
}

type qualityRunLock struct{}

func (qualityRunLock) TryAcquireLeaderLock(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}
func (qualityRunLock) ReleaseLeaderLock(context.Context, string, string) error { return nil }

func qualityFixturePNG(t *testing.T) []byte {
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4))))
	return b.Bytes()
}

func expectQualityRunStart(mock sqlmock.Sqlmock, now time.Time, candidate qualityCandidate) {
	mock.ExpectQuery(`SELECT account_id FROM account_quality_checks`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec(`INSERT INTO group_account_quality_checks`).WithArgs(now).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(qualityCandidatesSQL)).WithArgs(now, qualityProbesPerTick).
		WillReturnRows(sqlmock.NewRows([]string{"group", "account", "version", "model", "status", "streak", "paused"}).
			AddRow(candidate.groupID, candidate.id, candidate.version, candidate.modelID, candidate.lastStatus, candidate.streak, candidate.paused))
}

func expectAutomaticTask(mock sqlmock.Sqlmock, now time.Time, groupID int64, total int) {
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO group_quality_check_runs`).WithArgs(groupID, sqlmock.AnyArg(), total, now, now.Add(-qualityManualRunTimeout), "automatic").
		WillReturnRows(sqlmock.NewRows([]string{"run_id"}).AddRow("saved"))
	mock.ExpectCommit()
}

func expectAutomaticProgress(mock sqlmock.Sqlmock, now time.Time, groupID int64, status string, checked int) {
	mock.ExpectExec(`UPDATE group_quality_check_runs SET`).WithArgs(groupID, sqlmock.AnyArg(), status, "", checked, now).WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectQualitySave(mock sqlmock.Sqlmock, now time.Time, candidate qualityCandidate, status string, streak int, paused bool, preview any, rawOutput ...string) {
	var raw any
	if len(rawOutput) > 0 {
		raw = rawOutput[0]
	}
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT version, enabled`).WithArgs(candidate.groupID).
		WillReturnRows(sqlmock.NewRows([]string{"version", "enabled"}).AddRow(candidate.version, true))
	mock.ExpectQuery(`SELECT degraded_streak, paused`).WithArgs(candidate.groupID, candidate.id).
		WillReturnRows(sqlmock.NewRows([]string{"streak", "paused", "model"}).AddRow(candidate.streak, candidate.paused, candidate.modelID))
	retry := now
	if status == "failed" {
		retry = now.Add(5 * time.Minute)
	}
	mock.ExpectExec(`UPDATE group_account_quality_checks SET`).
		WithArgs(candidate.groupID, candidate.id, now, status, streak, paused, retry, candidate.modelID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO group_quality_check_latest`).
		WithArgs(candidate.groupID, candidate.id, sqlmock.AnyArg(), status, "safe reason", candidate.modelID, now, preview, nil, raw, nil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO group_quality_check_history`).
		WithArgs(candidate.groupID, candidate.id, sqlmock.AnyArg(), status, "safe reason", candidate.modelID, now, nil, nil, raw).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM group_quality_check_history`).WithArgs(candidate.groupID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func TestQualityRunFailedAndUnknownDoNotRepeatOldDegraded(t *testing.T) {
	for _, verdict := range []string{"failed", "unknown"} {
		t.Run(verdict, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			now := time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC)
			candidate := qualityCandidate{groupID: 7, id: 11, version: 2, modelID: "text-model", lastStatus: "degraded", streak: 1}
			expectQualityRunStart(mock, now, candidate)
			expectAutomaticTask(mock, now, candidate.groupID, 1)
			expectQualitySave(mock, now, candidate, verdict, 0, false, nil)
			expectAutomaticProgress(mock, now, candidate.groupID, "running", 1)
			expectAutomaticProgress(mock, now, candidate.groupID, "completed", 1)
			accounts := &qualityRunAccounts{account: &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}}
			checker := newGroupQualityChecker(db, accounts, nil, qualityRunLock{}, "test")
			checker.now = func() time.Time { return now }
			checker.probeFunc = func(_ context.Context, id int64, model string) qualityProbeResult {
				require.EqualValues(t, 11, id)
				require.Equal(t, "text-model", model)
				return qualityProbeResult{Status: verdict, Reason: "safe reason", ModelID: model}
			}
			checker.Run(context.Background())
			require.Equal(t, []int64{11}, accounts.reconciled)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestQualityRunSecondDegradedPersistsPauseAndPreview(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	candidate := qualityCandidate{groupID: 7, id: 11, version: 2, modelID: "text-model", streak: 1}
	picture := qualityFixturePNG(t)
	expectQualityRunStart(mock, now, candidate)
	expectAutomaticTask(mock, now, candidate.groupID, 1)
	expectQualitySave(mock, now, candidate, "degraded", 2, true, picture)
	expectAutomaticProgress(mock, now, candidate.groupID, "running", 1)
	expectAutomaticProgress(mock, now, candidate.groupID, "completed", 1)
	accounts := &qualityRunAccounts{account: &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}}
	checker := newGroupQualityChecker(db, accounts, nil, qualityRunLock{}, "test")
	checker.now = func() time.Time { return now }
	checker.probeFunc = func(context.Context, int64, string) qualityProbeResult {
		return qualityProbeResult{Status: "degraded", Reason: "safe reason", ModelID: "text-model", PreviewPNG: picture}
	}
	checker.Run(context.Background())
	require.Equal(t, []int64{11}, accounts.reconciled)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQualityRunSkipDoesNotWriteCheckedTimeOrSpendWindow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	candidate := qualityCandidate{groupID: 7, id: 11, version: 1}
	expectQualityRunStart(mock, now, candidate)
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE group_account_quality_checks SET retry_after = $3, degraded_streak = 0 WHERE group_id = $1 AND account_id = $2`)).
		WithArgs(int64(7), int64(11), now.Add(time.Hour)).WillReturnResult(sqlmock.NewResult(0, 1))
	accounts := &qualityRunAccounts{account: &Account{ID: 11, Platform: PlatformAnthropic, Extra: map[string]any{"synthetic_ui_test": true}}}
	checker := newGroupQualityChecker(db, accounts, nil, qualityRunLock{}, "test")
	checker.now = func() time.Time { return now }
	checker.probeFunc = func(context.Context, int64, string) qualityProbeResult {
		t.Fatal("synthetic UI account must not make a request")
		return qualityProbeResult{}
	}
	checker.Run(context.Background())
	require.Empty(t, accounts.reconciled)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQualityConfigChangedDuringProbeRejectsOldResult(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT version, enabled`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"version", "enabled"}).AddRow(3, true))
	mock.ExpectRollback()
	checker := newGroupQualityChecker(db, nil, nil, nil, "test")
	require.ErrorIs(t, checker.finishCheck(context.Background(), qualityCandidate{groupID: 7, id: 11, version: 2}, time.Now(), qualityProbeResult{Status: "degraded"}), ErrQualityConfigChanged)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQualityConfiguredGatesAndValidation(t *testing.T) {
	now := time.Now()
	require.False(t, qualityConfiguredWindowDue(now, now.Add(-59*time.Minute), 20, 10, time.Hour))
	require.False(t, qualityConfiguredWindowDue(now, now.Add(-time.Hour), 9, 10, time.Hour))
	require.True(t, qualityConfiguredWindowDue(now, now.Add(-time.Hour), 10, 10, time.Hour))
	require.True(t, qualityConfiguredWindowDue(now, now.Add(-time.Minute), 0.01, 0.01, time.Minute))
	for _, amount := range []float64{0, -1, math.NaN(), math.Inf(1), 1000001} {
		require.ErrorIs(t, validateQualityConfig(GroupQualityConfig{MinSpendUSD: &amount}), ErrQualityInvalidConfig)
	}
	for _, minutes := range []int{0, -1, 10081} {
		require.ErrorIs(t, validateQualityConfig(GroupQualityConfig{MinIntervalMinutes: &minutes}), ErrQualityInvalidConfig)
	}
	require.NoError(t, validateQualityConfig(GroupQualityConfig{Enabled: true}))
	require.Contains(t, qualityCandidatesSQL, "u.group_id = q.group_id")
	require.Less(t, bytes.Index([]byte(qualityCandidatesSQL), []byte("SUM(u.actual_cost)")), bytes.Index([]byte(qualityCandidatesSQL), []byte("LIMIT")))
	require.Contains(t, qualityCandidatesSQL, "COALESCE(ownership.paused, FALSE) AND")
}

func TestQualitySuccessReleasesOnlyItsGroupHold(t *testing.T) {
	streak, hold := qualityNextState(2, true, "success")
	require.Zero(t, streak)
	require.False(t, hold)
	streak, hold = qualityNextState(1, true, "unknown")
	require.Zero(t, streak)
	require.True(t, hold)
}

func TestQualityLatestReturnsNewestFailureAndKeepsPausedAccountVisible(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	mock.ExpectQuery(`(?s)SELECT s.group_id, s.enabled.*group_quality_check_latest.*s.group_id = ANY`).
		WillReturnRows(sqlmock.NewRows([]string{"group", "enabled", "result", "status", "reason", "model", "checked", "preview", "artwork", "raw", "score"}).
			AddRow(7, true, "latest-id", "failed", "Request failed", "text-model", now, false, false, true, nil).
			AddRow(8, false, "old-id", "success", "", "text-model", now, true, false, false, nil))
	enabled, results, err := NewGroupQualityReader(db).Latest(context.Background(), []int64{7, 8})
	require.NoError(t, err)
	require.True(t, enabled[7])
	require.Equal(t, "failed", results[7].Status)
	require.False(t, results[7].HasPreview)
	require.True(t, results[7].HasRawOutput)
	require.Nil(t, results[8])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQualityPreviewRejectsInvalidIDAndStaleResult(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	reader := NewGroupQualityReader(db)
	_, err = reader.Preview(context.Background(), 7, "not-an-id")
	require.ErrorIs(t, err, sql.ErrNoRows)
	mock.ExpectQuery(`(?s)SELECT l.preview_png.*l.result_id = \$2`).
		WithArgs(int64(7), "12345678-1234-4234-8234-123456789abc").WillReturnRows(sqlmock.NewRows([]string{"preview"}))
	_, err = reader.Preview(context.Background(), 7, "12345678-1234-4234-8234-123456789abc")
	require.ErrorIs(t, err, sql.ErrNoRows)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQualityConfigPartialUpdateKeepsSavedThresholds(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectExec(`(?s)INSERT INTO group_quality_check_settings.*COALESCE\(\$3::numeric, group_quality_check_settings.min_spend_usd\).*COALESCE\(\$4, group_quality_check_settings.min_interval_minutes\)`).
		WithArgs(int64(7), true, nil, nil, nil).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery(`SELECT s.group_id, s.enabled, s.min_spend_usd`).WillReturnRows(sqlmock.NewRows([]string{"id", "enabled", "spend", "minutes", "model", "checked", "degraded", "last"}).AddRow(7, true, 12.5, 60, "selected-model", 0, 0, nil))
	status, err := newGroupQualityChecker(db, nil, nil, nil, "test").SetEnabled(context.Background(), 7, true)
	require.NoError(t, err)
	require.Equal(t, 12.5, status.MinSpendUSD)
	require.Equal(t, 60, status.MinIntervalMinutes)
	require.Equal(t, "selected-model", status.ModelID)
	require.Equal(t, DefaultScheduledTestPrompt, status.DefaultPrompt)
	require.NoError(t, mock.ExpectationsWereMet())
}
