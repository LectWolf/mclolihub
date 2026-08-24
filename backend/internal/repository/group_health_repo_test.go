package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
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
	require.Contains(t, query, "2099-12-31")
	require.NotContains(t, query, "9999-12-31")
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
	require.Contains(t, normalized, "$1::timestamptz - interval '6 hours'")
	require.Contains(t, normalized, "ul.created_at >= $1::timestamptz - interval '6 hours'")
	require.Contains(t, normalized, "from group_health_states")
	require.Contains(t, normalized, "left join probe")
	require.Contains(t, normalized, "left join real_success")
	require.Contains(t, normalized, "left join account_groups ag")
	require.Contains(t, normalized, "coalesce(ul.group_id, ag.group_id)")
	require.NotContains(t, normalized, "full join")
	require.Contains(t, normalized, "union")
	require.Contains(t, normalized, "insert into group_health_states")
	require.NoError(t, mock.ExpectationsWereMet(), fmt.Sprintf("query: %s", query))
}

func TestObservedUserMetricsFillRealTTFTWithoutHealthState(t *testing.T) {
	var query string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureSQLMatcher(&query)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	rows := sqlmock.NewRows([]string{
		"group_id", "cache_overall", "cache_6h", "real_p50", "real_avg", "real_p95", "real_samples", "real_total",
	}).AddRow(int64(9), 0.25, 0.4, 320, 350, 480, 4, 900)
	mock.ExpectQuery("capture").WillReturnRows(rows)

	out := map[int64]service.GroupHealthSnapshot{}
	err = (&groupHealthStore{db: db}).overlayObservedUserMetrics(context.Background(), []int64{9}, out)
	require.NoError(t, err)
	require.Equal(t, 320, out[9].RealTTFTP50MS)
	require.Equal(t, 350, out[9].RealTTFTAvgMS)
	require.Equal(t, 480, out[9].RealTTFTP95MS)
	require.Equal(t, 4, out[9].RealTTFTSamples)
	require.Equal(t, 900, out[9].RealTotalAvgMS)
	require.InDelta(t, 0.25, out[9].CacheRateOverall, 0.0001)
	require.InDelta(t, 0.4, out[9].CacheRate6h, 0.0001)
	normalized := strings.ToLower(query)
	require.Contains(t, normalized, "first_token_ms")
	require.Contains(t, normalized, "usage_logs")
	require.NotContains(t, normalized, "probe_enabled")
	require.NotContains(t, normalized, "group_health_states")
	require.NoError(t, mock.ExpectationsWereMet())
}
