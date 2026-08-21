package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func captureSQLMatcher(target *string) sqlmock.QueryMatcherFunc {
	return func(_, actual string) error {
		*target = actual
		return nil
	}
}

func TestGroupHealthClaimImmediateProbePersistsTwoMinuteThrottleWithoutMarkingFailed(t *testing.T) {
	var query string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureSQLMatcher(&query)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	mock.ExpectExec("capture").
		WithArgs(int64(11), int64(22), now, "120.000000 seconds").
		WillReturnResult(sqlmock.NewResult(0, 1))

	claimed, err := (&groupHealthStore{db: db}).ClaimImmediateProbe(context.Background(), 11, 22, now, 2*time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NotContains(t, strings.ToLower(query), "'failed'",
		"a user request failure must stay schedulable until its confirmation probe also fails")
	require.Contains(t, strings.ToLower(query), "'probing'",
		"the immediate verification must quarantine the account before the async probe runs")
	require.Contains(t, strings.ToLower(query), "scheduler_outbox")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshDerivedGroupHealthDoesNotForgeProbeSuccessTime(t *testing.T) {
	var query string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureSQLMatcher(&query)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectExec("capture").WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))

	err = (&groupHealthStore{db: db}).RefreshDerivedGroupHealth(context.Background(), 7, time.Now())
	require.NoError(t, err)
	require.NotContains(t, strings.ToLower(query), "last_success_at", "only a successful probe may advance the routing freshness timestamp")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRollingMetricsIncludesEveryHealthStateForWindowReset(t *testing.T) {
	var query string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureSQLMatcher(&query)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now()
	mock.ExpectExec("capture").WithArgs(now).WillReturnResult(sqlmock.NewResult(0, 3))

	err = (&groupHealthStore{db: db}).UpdateRollingMetrics(context.Background(), now)
	require.NoError(t, err)
	normalized := strings.ToLower(strings.Join(strings.Fields(query), " "))
	require.Contains(t, normalized, "from group_health_states s")
	require.Contains(t, normalized, "left join probe")
	require.Contains(t, normalized, "left join real_success")
	require.Contains(t, normalized, "left join account_groups ag")
	require.Contains(t, normalized, "coalesce(ul.group_id, ag.group_id)")
	require.NotContains(t, normalized, "full join")
	require.NoError(t, mock.ExpectationsWereMet(), fmt.Sprintf("query: %s", query))
}
