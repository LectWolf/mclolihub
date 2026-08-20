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

func TestGroupHealthClaimImmediateProbePersistsTenMinuteThrottle(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	mock.ExpectExec("INSERT INTO account_health_states").
		WithArgs(int64(11), int64(22), now, "600.000000 seconds").
		WillReturnResult(sqlmock.NewResult(0, 1))

	claimed, err := (&groupHealthStore{db: db}).ClaimImmediateProbe(context.Background(), 11, 22, now, 10*time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
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
	require.NotContains(t, normalized, "full join")
	require.NoError(t, mock.ExpectationsWereMet(), fmt.Sprintf("query: %s", query))
}
