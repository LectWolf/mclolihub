package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestQualitySchedulingOwnershipAndSharedHolds(t *testing.T) {
	cases := []struct {
		name                                                      string
		schedulable, usable, owned, held, change, next, nextOwned bool
	}{
		{name: "manual disabled not resumed", usable: true},
		{name: "other group still holds pause", usable: true, owned: true, held: true},
		{name: "expired owned pause not resumed", owned: true},
		{name: "second degraded pauses", schedulable: true, usable: true, held: true, change: true, nextOwned: true},
		{name: "all group holds released", usable: true, owned: true, change: true, next: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT schedulable, status`).WithArgs(int64(11)).WillReturnRows(sqlmock.NewRows([]string{"schedulable", "usable"}).AddRow(tc.schedulable, tc.usable))
			mock.ExpectQuery(`(?s)SELECT.*EXISTS.*account_quality_checks.*EXISTS.*group_account_quality_checks`).WithArgs(int64(11)).WillReturnRows(sqlmock.NewRows([]string{"owned", "held"}).AddRow(tc.owned, tc.held))
			if tc.change {
				mock.ExpectExec(`UPDATE accounts SET schedulable`).WithArgs(int64(11), tc.next).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec(`INSERT INTO account_quality_checks`).WithArgs(int64(11), tc.nextOwned).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec(`INSERT INTO scheduler_outbox`).WithArgs(service.SchedulerOutboxEventAccountChanged, int64(11), nil, nil, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			repo := newAccountRepositoryWithSQL(nil, db, nil)
			require.NoError(t, repo.ReconcileGroupQualityScheduling(context.Background(), 11))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestQualityManualDisableClearsOwnershipAndStreakAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id FROM accounts`).WithArgs(int64(11)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(11))
	mock.ExpectExec(`UPDATE accounts SET schedulable`).WithArgs(int64(11), false).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE account_quality_checks SET paused = FALSE`).WithArgs(int64(11)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE group_account_quality_checks SET paused = FALSE, degraded_streak = 0`).WithArgs(int64(11)).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(`INSERT INTO scheduler_outbox`).WithArgs(service.SchedulerOutboxEventAccountChanged, int64(11), nil, nil, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	repo := newAccountRepositoryWithSQL(nil, db, nil)
	require.NoError(t, repo.SetManualQualitySchedulable(context.Background(), []int64{11}, false))
	require.NoError(t, mock.ExpectationsWereMet())
}
