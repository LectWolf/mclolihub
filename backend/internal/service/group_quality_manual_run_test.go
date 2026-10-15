package service

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

type qualityManualLock struct {
	sync.Mutex
	held       bool
	key, owner string
	owners     map[string]string
}

func (l *qualityManualLock) TryAcquireLeaderLock(_ context.Context, key, owner string, _ time.Duration) (bool, error) {
	l.Lock()
	defer l.Unlock()
	if l.owners == nil {
		l.owners = make(map[string]string)
	}
	if _, exists := l.owners[key]; exists {
		return false, nil
	}
	l.owners[key] = owner
	l.held, l.key, l.owner = true, key, owner
	return true, nil
}
func (l *qualityManualLock) ReleaseLeaderLock(_ context.Context, key, owner string) error {
	l.Lock()
	defer l.Unlock()
	if l.owners[key] == owner {
		delete(l.owners, key)
		l.held = len(l.owners) > 0
	}
	return nil
}

func manualCandidateRows(candidate qualityCandidate) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"group", "account", "version", "model", "status", "streak", "paused"}).
		AddRow(candidate.groupID, candidate.id, candidate.version, candidate.modelID, candidate.lastStatus, candidate.streak, candidate.paused)
}
func expectManualStart(mock sqlmock.Sqlmock, now time.Time, candidate qualityCandidate) {
	mock.ExpectQuery(`SELECT enabled FROM group_quality_check_settings`).WithArgs(candidate.groupID).
		WillReturnRows(sqlmock.NewRows([]string{"enabled"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(qualityManualCandidatesSQL)).WithArgs(candidate.groupID, now).WillReturnRows(manualCandidateRows(candidate))
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO group_quality_check_runs`).WithArgs(candidate.groupID, sqlmock.AnyArg(), 1, now, now.Add(-qualityManualRunTimeout), "manual").
		WillReturnRows(sqlmock.NewRows([]string{"run_id"}).AddRow("saved"))
	mock.ExpectExec(`INSERT INTO group_account_quality_checks`).WithArgs(candidate.groupID, candidate.id, now).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}
func expectManualProgress(mock sqlmock.Sqlmock, now time.Time, run *GroupQualityRun, status, reason string, checked int) {
	mock.ExpectExec(`UPDATE group_quality_check_runs SET`).WithArgs(run.GroupID, run.RunID, status, reason, checked, now).WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestQualityManualRunBypassesGatesAndSurvivesRequestCancellation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	candidate := qualityCandidate{groupID: 7, id: 11, version: 2, modelID: "saved-model", streak: 2, paused: true}
	accounts := &qualityRunAccounts{account: &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}}
	lock := &qualityManualLock{}
	checker := newGroupQualityChecker(db, accounts, nil, lock, "test")
	checker.now = func() time.Time { return now }
	var worker func(context.Context)
	checker.launch = func(fn func(context.Context)) bool { worker = fn; return true }
	checker.probeFunc = func(ctx context.Context, id int64, model string) qualityProbeResult {
		require.NoError(t, ctx.Err())
		require.EqualValues(t, 11, id)
		require.Equal(t, "saved-model", model)
		return qualityProbeResult{Status: "success", Reason: "safe reason", ModelID: model, PreviewPNG: qualityFixturePNG(t)}
	}
	expectManualStart(mock, now, candidate)
	requestCtx, cancel := context.WithCancel(context.Background())
	run, err := checker.StartManualRun(requestCtx, 7)
	require.NoError(t, err)
	require.Equal(t, "running", run.Status)
	require.Equal(t, "manual", run.Source)
	require.Equal(t, 1, run.TotalAccounts)
	require.Equal(t, qualityGroupLockKey(7), lock.key)
	cancel()
	// No DB expectation and no second launch: the shared lock refuses repeats.
	_, err = checker.StartManualRun(context.Background(), 7)
	require.ErrorIs(t, err, ErrQualityRunBusy)
	expectQualitySave(mock, now, candidate, "success", 0, false, qualityFixturePNG(t))
	expectManualProgress(mock, now, run, "running", "", 1)
	expectManualProgress(mock, now, run, "completed", "", 1)
	worker(context.Background())
	require.Equal(t, []int64{11}, accounts.reconciled)
	require.False(t, lock.held)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQualityManualRunDisabledAndUnsupportedAccountsMakeNoRequests(t *testing.T) {
	for _, name := range []string{"disabled", "unsupported", "empty"} {
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			now := time.Now().UTC()
			lock := &qualityManualLock{}
			checker := newGroupQualityChecker(db, &qualityRunAccounts{account: &Account{Platform: PlatformAnthropic}}, nil, lock, "test")
			checker.now = func() time.Time { return now }
			checker.launch = func(func(context.Context)) bool { t.Fatal("must not launch"); return false }
			checker.probeFunc = func(context.Context, int64, string) qualityProbeResult {
				t.Fatal("must not call upstream")
				return qualityProbeResult{}
			}
			mock.ExpectQuery(`SELECT enabled FROM group_quality_check_settings`).WithArgs(int64(7)).
				WillReturnRows(sqlmock.NewRows([]string{"enabled"}).AddRow(name != "disabled"))
			want := ErrQualityRunDisabled
			if name != "disabled" {
				rows := sqlmock.NewRows([]string{"group", "account", "version", "model", "status", "streak", "paused"})
				if name == "unsupported" {
					rows = manualCandidateRows(qualityCandidate{groupID: 7, id: 11, version: 1})
				}
				mock.ExpectQuery(regexp.QuoteMeta(qualityManualCandidatesSQL)).WithArgs(int64(7), now).WillReturnRows(rows)
				want = ErrQualityRunNoAccounts
			}
			_, err = checker.StartManualRun(context.Background(), 7)
			require.ErrorIs(t, err, want)
			require.False(t, lock.held)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestQualityManualRunConfigChangeStopsWithoutReplacingPreview(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	candidate := qualityCandidate{groupID: 7, id: 11, version: 2, modelID: "saved-model"}
	checker := newGroupQualityChecker(db, &qualityRunAccounts{account: &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}}, nil, qualityRunLock{}, "test")
	checker.now = func() time.Time { return now }
	checker.probeFunc = func(context.Context, int64, string) qualityProbeResult { return qualityProbeResult{Status: "success"} }
	run := &GroupQualityRun{RunID: "run", GroupID: 7, StartedAt: now, TotalAccounts: 1}
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT version, enabled`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"version", "enabled"}).AddRow(3, true))
	mock.ExpectRollback()
	expectManualProgress(mock, now, run, "failed", "Quality check settings changed during execution", 0)
	checker.executeQualityRun(context.Background(), run, []qualityCandidate{candidate})
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQualityManualRunStatusRestoresProgressAndLatestResult(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	started := now.Add(-time.Minute)
	mock.ExpectQuery(`SELECT run_id, group_id, status, reason`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"run", "group", "status", "reason", "checked", "total", "started", "finished", "source"}).AddRow("run", 7, "completed", "", 1, 1, started, now, "manual"))
	mock.ExpectQuery(`(?s)SELECT s.group_id, s.enabled.*group_quality_check_latest`).WillReturnRows(sqlmock.NewRows([]string{"group", "enabled", "result", "status", "reason", "model", "checked", "preview", "artwork", "raw", "score"}).AddRow(7, true, "result", "failed", "Artwork generation timed out before completion", "saved-model", now, false, false, false, nil))
	checker := NewGroupQualityReader(db)
	checker.now = func() time.Time { return now }
	run, err := checker.ManualRun(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, "completed", run.Status) // execution completion is separate from the model verdict
	require.Equal(t, "failed", run.LatestQuality.Status)
	require.Equal(t, "result", run.LatestQuality.ResultID)
	require.NotNil(t, run.FinishedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQualityManualRunExpiredStateAndWorkerShutdown(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	started := now.Add(-qualityManualRunTimeout)
	checker := NewGroupQualityReader(db)
	checker.now = func() time.Time { return now }
	run := &GroupQualityRun{RunID: "old", GroupID: 7, StartedAt: started}
	mock.ExpectQuery(`SELECT run_id, group_id, status, reason`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"run", "group", "status", "reason", "checked", "total", "started", "finished", "source"}).AddRow("old", 7, "running", "", 0, 1, started, nil, "automatic"))
	expectManualProgress(mock, now, run, "failed", "Quality check execution stopped or timed out", 0)
	status, err := checker.ManualRun(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, "failed", status.Status)
	require.Equal(t, "automatic", status.Source)
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	expectManualProgress(mock, now, run, "failed", "Quality check execution stopped or timed out", 0)
	checker.executeQualityRun(parent, run, []qualityCandidate{{id: 11}})
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQualityManualRunStartFailureReleasesLock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	lock := &qualityManualLock{}
	checker := newGroupQualityChecker(db, &qualityRunAccounts{account: &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}}, nil, lock, "test")
	checker.now = func() time.Time { return now }
	checker.probeFunc = func(context.Context, int64, string) qualityProbeResult { return qualityProbeResult{} }
	checker.launch = func(func(context.Context)) bool { return false }
	candidate := qualityCandidate{groupID: 7, id: 11, version: 2}
	expectManualStart(mock, now, candidate)
	mock.ExpectExec(`UPDATE group_quality_check_runs SET`).WithArgs(int64(7), sqlmock.AnyArg(), "failed", "Quality check execution stopped", 0, now).WillReturnResult(sqlmock.NewResult(0, 1))
	_, err = checker.StartManualRun(context.Background(), 7)
	require.ErrorIs(t, err, ErrQualityRunUnavailable)
	require.False(t, lock.held)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQualityManualRunDoesNotExposeDatabaseErrorInProgress(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	checker := NewGroupQualityReader(db)
	checker.now = func() time.Time { return now }
	checker.probeFunc = func(context.Context, int64, string) qualityProbeResult { return qualityProbeResult{Status: "success"} }
	mock.ExpectBegin().WillReturnError(errors.New("private db password"))
	run := &GroupQualityRun{RunID: "run", GroupID: 7, StartedAt: now}
	expectManualProgress(mock, now, run, "failed", "Quality check result could not be saved", 0)
	checker.executeQualityRun(context.Background(), run, []qualityCandidate{{groupID: 7, id: 11}})
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGroupHealthManualWorkerLifecycle(t *testing.T) {
	health := NewGroupHealthService(nil, nil, nil, nil, nil)
	started := make(chan struct{})
	require.True(t, health.launchQualityRun(func(ctx context.Context) { close(started); <-ctx.Done() }))
	<-started
	health.Stop()
	require.False(t, health.launchQualityRun(func(context.Context) { t.Fatal("must not launch after stop") }))
}

func TestQualityManualRunProgressCannotReviveFinishedOrReplacedRun(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	checker := NewGroupQualityReader(db)
	checker.now = func() time.Time { return now }
	run := &GroupQualityRun{RunID: "old", GroupID: 7}
	mock.ExpectExec(`(?s)UPDATE group_quality_check_runs SET.*run_id = \$2 AND status = 'running'`).
		WithArgs(int64(7), "old", "running", "", 1, now).WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, checker.saveRunProgress(run, "running", "", 1), ErrQualityConfigChanged)
	require.NoError(t, mock.ExpectationsWereMet())
}
