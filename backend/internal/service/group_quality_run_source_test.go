package service

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestQualityRunsDifferentGroupsOverlapWithoutScanLock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	lock := &qualityManualLock{}
	acquired, err := lock.TryAcquireLeaderLock(context.Background(), qualityLockKey, "scheduler", qualityLockTTL)
	require.NoError(t, err)
	require.True(t, acquired)
	checker := newGroupQualityChecker(db, &qualityRunAccounts{account: &Account{Platform: PlatformOpenAI}}, nil, lock, "test")
	checker.now = func() time.Time { return now }
	var workers []func(context.Context)
	checker.launch = func(fn func(context.Context)) bool { workers = append(workers, fn); return true }
	checker.probeFunc = func(context.Context, int64, string) qualityProbeResult {
		t.Fatal("cancelled workers must not probe")
		return qualityProbeResult{}
	}
	var runs []*GroupQualityRun
	for _, groupID := range []int64{7, 8} {
		expectManualStart(mock, now, qualityCandidate{groupID: groupID, id: 11, version: 1})
		run, err := checker.StartManualRun(context.Background(), groupID)
		require.NoError(t, err)
		require.NoError(t, uuid.Validate(run.RunID))
		runs = append(runs, run)
	}
	require.Len(t, lock.owners, 3)
	require.NotEqual(t, runs[0].RunID, runs[1].RunID)
	_, err = checker.StartManualRun(context.Background(), 7)
	require.ErrorIs(t, err, ErrQualityRunBusy)
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	for i, worker := range workers {
		expectManualProgress(mock, now, runs[i], "failed", "Quality check execution stopped or timed out", 0)
		worker(parent)
	}
	require.Equal(t, map[string]string{qualityLockKey: "scheduler"}, lock.owners)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQualityAutomaticSkipsManualGroupAndChecksAnotherGroup(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	lock := &qualityManualLock{}
	checker := newGroupQualityChecker(db, &qualityRunAccounts{account: &Account{Platform: PlatformOpenAI}}, nil, lock, "test")
	checker.now = func() time.Time { return now }
	var worker func(context.Context)
	checker.launch = func(fn func(context.Context)) bool { worker = fn; return true }
	checker.probeFunc = func(_ context.Context, id int64, model string) qualityProbeResult {
		require.EqualValues(t, 12, id)
		_, err := checker.StartManualRun(context.Background(), 8)
		require.ErrorIs(t, err, ErrQualityRunBusy)
		// Automatic scanning still rejects a duplicate global scheduler.
		checker.Run(context.Background())
		return qualityProbeResult{Status: "success", Reason: "safe reason", ModelID: model}
	}
	manual := qualityCandidate{groupID: 7, id: 11, version: 1}
	expectManualStart(mock, now, manual)
	run, err := checker.StartManualRun(context.Background(), 7)
	require.NoError(t, err)
	automatic := qualityCandidate{groupID: 8, id: 12, version: 1, modelID: "text-model"}
	mock.ExpectQuery(`SELECT account_id FROM account_quality_checks`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec(`INSERT INTO group_account_quality_checks`).WithArgs(now).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(qualityCandidatesSQL)).WithArgs(now, qualityProbesPerTick).
		WillReturnRows(manualCandidateRows(manual).AddRow(automatic.groupID, automatic.id, automatic.version, automatic.modelID, "", 0, false))
	expectAutomaticTask(mock, now, 8, 1)
	expectQualitySave(mock, now, automatic, "success", 0, false, nil)
	expectAutomaticProgress(mock, now, 8, "running", 1)
	expectAutomaticProgress(mock, now, 8, "completed", 1)
	checker.Run(context.Background())
	require.Len(t, lock.owners, 1)
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	expectManualProgress(mock, now, run, "failed", "Quality check execution stopped or timed out", 0)
	worker(parent)
	require.Empty(t, lock.owners)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQualityAutomaticTaskReportsSourceAndBatchProgress(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	lock := &qualityManualLock{}
	checker := newGroupQualityChecker(db, &qualityRunAccounts{account: &Account{Platform: PlatformOpenAI}}, nil, lock, "test")
	checker.now = func() time.Time { return now }
	candidates := []qualityCandidate{{groupID: 7, id: 11, version: 1, modelID: "text-model"}, {groupID: 7, id: 12, version: 1, modelID: "text-model"}}
	expectAutomaticTask(mock, now, 7, 2)
	for i, candidate := range candidates {
		mock.ExpectQuery(`SELECT run_id, group_id, status, reason`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"run", "group", "status", "reason", "checked", "total", "started", "finished", "source"}).AddRow("automatic-id", 7, "running", "", i, 2, now, nil, "automatic"))
		if i > 0 {
			mock.ExpectQuery(`(?s)SELECT s.group_id, s.enabled.*group_quality_check_latest`).WillReturnRows(sqlmock.NewRows([]string{"group", "enabled", "result", "status", "reason", "model", "checked", "preview", "artwork"}).AddRow(7, true, "result", "success", "safe reason", "text-model", now, false, false))
		}
		expectQualitySave(mock, now, candidate, "success", 0, false, nil)
		expectAutomaticProgress(mock, now, 7, "running", i+1)
	}
	expectAutomaticProgress(mock, now, 7, "completed", 2)
	probes := 0
	checker.probeFunc = func(ctx context.Context, id int64, model string) qualityProbeResult {
		run, err := checker.ManualRun(ctx, 7)
		require.NoError(t, err)
		require.Equal(t, "automatic", run.Source)
		require.Equal(t, "running", run.Status)
		require.Equal(t, 2, run.TotalAccounts)
		require.Equal(t, probes, run.CheckedAccounts)
		probes++
		return qualityProbeResult{Status: "success", Reason: "safe reason", ModelID: model}
	}
	checker.runAutomaticGroup(context.Background(), 7, candidates)
	require.Equal(t, 2, probes)
	require.Empty(t, lock.owners)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQualityAutomaticCannotProbeWithoutPersistentTask(t *testing.T) {
	for _, failure := range []string{"begin", "insert", "conflict", "commit"} {
		t.Run(failure, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			now := time.Now().UTC()
			lock := &qualityManualLock{}
			checker := newGroupQualityChecker(db, &qualityRunAccounts{account: &Account{Platform: PlatformOpenAI}}, nil, lock, "test")
			checker.now = func() time.Time { return now }
			checker.probeFunc = func(context.Context, int64, string) qualityProbeResult {
				t.Fatal("must not execute without committed task")
				return qualityProbeResult{}
			}
			if failure == "begin" {
				mock.ExpectBegin().WillReturnError(errors.New("private database error"))
			} else {
				mock.ExpectBegin()
				insert := mock.ExpectQuery(`INSERT INTO group_quality_check_runs`).WithArgs(int64(7), sqlmock.AnyArg(), 1, now, now.Add(-qualityManualRunTimeout), "automatic")
				switch failure {
				case "insert":
					insert.WillReturnError(errors.New("private database error"))
					mock.ExpectRollback()
				case "conflict":
					insert.WillReturnRows(sqlmock.NewRows([]string{"run_id"}))
					mock.ExpectRollback()
				case "commit":
					insert.WillReturnRows(sqlmock.NewRows([]string{"run_id"}).AddRow("saved"))
					mock.ExpectCommit().WillReturnError(errors.New("private database error"))
				}
			}
			checker.runAutomaticGroup(context.Background(), 7, []qualityCandidate{{groupID: 7, id: 11, version: 1}})
			require.Empty(t, lock.owners)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestQualityAutomaticPanicMarksFailureAndReleasesLocks(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	lock := &qualityManualLock{}
	checker := newGroupQualityChecker(db, &qualityRunAccounts{account: &Account{Platform: PlatformOpenAI}}, nil, lock, "test")
	checker.now = func() time.Time { return now }
	checker.probeFunc = func(context.Context, int64, string) qualityProbeResult { panic("private upstream artifact") }
	candidate := qualityCandidate{groupID: 7, id: 11, version: 1}
	expectQualityRunStart(mock, now, candidate)
	expectAutomaticTask(mock, now, 7, 1)
	mock.ExpectExec(`UPDATE group_quality_check_runs SET`).WithArgs(int64(7), sqlmock.AnyArg(), "failed", "Quality check execution failed", 0, now).WillReturnResult(sqlmock.NewResult(0, 1))
	checker.Run(context.Background())
	require.Empty(t, lock.owners)
	require.NoError(t, mock.ExpectationsWereMet())
}
