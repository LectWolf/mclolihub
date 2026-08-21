package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/grouphealthstate"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type groupHealthStore struct {
	client *dbent.Client
	db     *sql.DB
}

func NewGroupHealthStore(client *dbent.Client, db *sql.DB) service.GroupHealthRepository {
	return &groupHealthStore{client: client, db: db}
}

func (r *groupHealthStore) Load(ctx context.Context, groupID int64) (*service.GroupHealthSnapshot, error) {
	state, err := r.client.GroupHealthState.Query().Where(grouphealthstate.GroupIDEQ(groupID)).Only(ctx)
	if dbent.IsNotFound(err) {
		return &service.GroupHealthSnapshot{GroupID: groupID, Status: service.GroupHealthUnknown}, nil
	}
	if err != nil {
		return nil, err
	}
	snapshot := groupHealthEntityToSnapshot(state)
	return &snapshot, nil
}

func (r *groupHealthStore) Save(ctx context.Context, snapshot *service.GroupHealthSnapshot) error {
	if snapshot == nil {
		return nil
	}
	create := r.client.GroupHealthState.Create().SetGroupID(snapshot.GroupID).SetStatus(snapshot.Status).SetFailureCount(snapshot.FailureCount).SetProbeTtftMs(snapshot.ProbeTTFTMS).SetProbeAvailability6h(snapshot.ProbeAvailability6h).SetProbeTtftAvgMs(snapshot.ProbeTTFTAvgMS).SetProbeTtftP95Ms(snapshot.ProbeTTFTP95MS).SetProbeSamples(snapshot.ProbeSamples).SetRealTtftP50Ms(snapshot.RealTTFTP50MS).SetRealTtftAvgMs(snapshot.RealTTFTAvgMS).SetRealTtftP95Ms(snapshot.RealTTFTP95MS).SetRealTtftSamples(snapshot.RealTTFTSamples).SetRealAvailability6h(snapshot.RealAvailability6h).SetRealTotalAvgMs(snapshot.RealTotalAvgMS).SetNillableLastProbeAt(snapshot.LastProbeAt).SetNillableLastSuccessAt(snapshot.LastSuccessAt).SetNillableNextProbeAt(snapshot.NextProbeAt)
	if snapshot.Reason != "" {
		create.SetReason(snapshot.Reason)
	}
	return create.OnConflictColumns(grouphealthstate.FieldGroupID).UpdateNewValues().Exec(ctx)
}

func (r *groupHealthStore) RecordEvent(ctx context.Context, event service.GroupHealthEventInput) error {
	observedAt := event.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	create := r.client.GroupHealthEvent.Create().SetGroupID(event.GroupID).SetNillableAccountID(func() *int64 {
		if event.AccountID <= 0 {
			return nil
		}
		return &event.AccountID
	}()).SetKind(event.Kind).SetSuccess(event.Success).SetIsProbe(event.IsProbe).SetSemanticStarted(event.SemanticStarted).SetTtftMs(int(event.TTFT / time.Millisecond)).SetTotalMs(int(event.Total / time.Millisecond)).SetObservedAt(observedAt)
	if event.ErrorCategory != "" {
		create.SetErrorCategory(event.ErrorCategory)
	}
	if event.ErrorMessage != "" {
		create.SetErrorMessage(event.ErrorMessage)
	}
	return create.Exec(ctx)
}

func groupHealthEntityToSnapshot(state *dbent.GroupHealthState) service.GroupHealthSnapshot {
	if state == nil {
		return service.GroupHealthSnapshot{Status: service.GroupHealthUnknown}
	}
	return service.GroupHealthSnapshot{
		GroupID: state.GroupID, Status: state.Status, Reason: derefString(state.Reason),
		LastProbeAt: state.LastProbeAt, LastSuccessAt: state.LastSuccessAt, NextProbeAt: state.NextProbeAt,
		FailureCount: state.FailureCount, ProbeTTFTMS: state.ProbeTtftMs,
		ProbeAvailability6h: state.ProbeAvailability6h, ProbeTTFTAvgMS: state.ProbeTtftAvgMs,
		ProbeTTFTP95MS: state.ProbeTtftP95Ms, ProbeSamples: state.ProbeSamples,
		RealTTFTP50MS: state.RealTtftP50Ms, RealTTFTAvgMS: state.RealTtftAvgMs,
		RealTTFTP95MS: state.RealTtftP95Ms, RealTTFTSamples: state.RealTtftSamples,
		RealAvailability6h: state.RealAvailability6h, RealTotalAvgMS: state.RealTotalAvgMs,
	}
}

func (r *groupHealthStore) ListDueProbeGroups(ctx context.Context, now time.Time, limit int) ([]service.GroupProbeTarget, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT g.id, g.platform, g.probe_model, g.probe_interval_seconds
		FROM groups g
		LEFT JOIN group_health_states s ON s.group_id = g.id
		WHERE g.deleted_at IS NULL AND g.status = 'active' AND g.probe_enabled IS TRUE
		  AND (s.next_probe_at IS NULL OR s.next_probe_at <= $1)
		ORDER BY COALESCE(s.next_probe_at, '-infinity'::timestamptz), g.sort_order, g.id
		LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]service.GroupProbeTarget, 0)
	for rows.Next() {
		var item service.GroupProbeTarget
		var seconds int
		if err := rows.Scan(&item.GroupID, &item.Platform, &item.Model, &seconds); err != nil {
			return nil, err
		}
		item.Interval = normalizedProbeInterval(seconds)
		item.ProbeEnabled = true
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *groupHealthStore) ListDueAccountProbes(ctx context.Context, now time.Time, limit int) ([]service.AccountProbeTarget, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.account_id, s.probe_group_id, g.platform, g.probe_model, g.probe_interval_seconds, s.retry_step
		FROM account_health_states s
		JOIN accounts a ON a.id = s.account_id AND a.deleted_at IS NULL
		JOIN groups g ON g.id = s.probe_group_id AND g.deleted_at IS NULL
		WHERE s.runtime_status = 'failed' AND s.next_probe_at IS NOT NULL AND s.next_probe_at <= $1
		  AND g.status = 'active' AND g.probe_enabled IS TRUE
		ORDER BY s.next_probe_at, s.account_id
		LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]service.AccountProbeTarget, 0)
	for rows.Next() {
		var item service.AccountProbeTarget
		var seconds int
		if err := rows.Scan(&item.AccountID, &item.GroupID, &item.Platform, &item.Model, &seconds, &item.RetryStep); err != nil {
			return nil, err
		}
		item.Interval = normalizedProbeInterval(seconds)
		out = append(out, item)
	}
	return out, rows.Err()
}

func normalizedProbeInterval(seconds int) time.Duration {
	if seconds < 30 || seconds > 3600 {
		seconds = 600
	}
	return time.Duration(seconds) * time.Second
}

func (r *groupHealthStore) GetProbeGroup(ctx context.Context, groupID int64) (service.GroupProbeTarget, error) {
	var target service.GroupProbeTarget
	var seconds int
	err := r.db.QueryRowContext(ctx, `SELECT id,platform,probe_model,probe_interval_seconds,probe_enabled FROM groups WHERE id=$1 AND deleted_at IS NULL`, groupID).
		Scan(&target.GroupID, &target.Platform, &target.Model, &seconds, &target.ProbeEnabled)
	if err != nil {
		return target, err
	}
	target.Interval = normalizedProbeInterval(seconds)
	return target, nil
}

func (r *groupHealthStore) LoadAccountHealth(ctx context.Context, accountIDs []int64) (map[int64]service.AccountHealthState, error) {
	out := make(map[int64]service.AccountHealthState, len(accountIDs))
	if len(accountIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT account_id, probe_group_id, runtime_status, COALESCE(reason,''), retry_step,
		       next_probe_at, last_probe_at, last_success_at, last_failure_at, last_immediate_probe_at
		FROM account_health_states WHERE account_id = ANY($1)`, pq.Array(accountIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var state service.AccountHealthState
		if err := rows.Scan(&state.AccountID, &state.ProbeGroupID, &state.RuntimeStatus, &state.Reason, &state.RetryStep, &state.NextProbeAt, &state.LastProbeAt, &state.LastSuccessAt, &state.LastFailureAt, &state.LastImmediateProbeAt); err != nil {
			return nil, err
		}
		out[state.AccountID] = state
	}
	return out, rows.Err()
}

func (r *groupHealthStore) SaveAccountHealth(ctx context.Context, state service.AccountHealthState) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO account_health_states
		(account_id, probe_group_id, runtime_status, reason, retry_step, next_probe_at, last_probe_at, last_success_at, last_failure_at, last_immediate_probe_at)
		VALUES ($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10)
		ON CONFLICT (account_id) DO UPDATE SET
		probe_group_id=EXCLUDED.probe_group_id, runtime_status=EXCLUDED.runtime_status,
		reason=EXCLUDED.reason, retry_step=EXCLUDED.retry_step, next_probe_at=EXCLUDED.next_probe_at,
		last_probe_at=EXCLUDED.last_probe_at, last_success_at=EXCLUDED.last_success_at,
		last_failure_at=EXCLUDED.last_failure_at,
		last_immediate_probe_at=COALESCE(EXCLUDED.last_immediate_probe_at, account_health_states.last_immediate_probe_at),
		updated_at=NOW()`, state.AccountID, state.ProbeGroupID, state.RuntimeStatus, state.Reason, state.RetryStep,
		state.NextProbeAt, state.LastProbeAt, state.LastSuccessAt, state.LastFailureAt, state.LastImmediateProbeAt)
	if err != nil {
		return err
	}
	if state.RuntimeStatus == service.AccountRuntimeActive {
		_, err = r.db.ExecContext(ctx, `UPDATE accounts SET temp_unschedulable_until=NULL, temp_unschedulable_reason=NULL, updated_at=NOW() WHERE id=$1 AND status='active' AND deleted_at IS NULL`, state.AccountID)
	} else if state.RuntimeStatus == service.AccountRuntimeFailed && state.NextProbeAt != nil {
		_, err = r.db.ExecContext(ctx, `UPDATE accounts SET temp_unschedulable_until=$2, temp_unschedulable_reason=$3, updated_at=NOW() WHERE id=$1 AND status='active' AND deleted_at IS NULL`, state.AccountID, *state.NextProbeAt, state.Reason)
	}
	if err == nil {
		_ = enqueueSchedulerOutbox(ctx, r.db, service.SchedulerOutboxEventAccountChanged, &state.AccountID, nil, nil)
	}
	return err
}

func (r *groupHealthStore) MarkAccountBalanceInsufficient(ctx context.Context, accountID, groupID int64, reason string, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO account_health_states(account_id,probe_group_id,runtime_status,reason,retry_step,last_probe_at,last_failure_at)
		VALUES($1,$2,'balance_insufficient',$3,0,$4,$4)
		ON CONFLICT(account_id) DO UPDATE SET probe_group_id=$2,runtime_status='balance_insufficient',reason=$3,
		retry_step=0,next_probe_at=NULL,last_probe_at=$4,last_failure_at=$4,updated_at=NOW()`, accountID, groupID, reason, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE accounts SET status=$2,error_message=$3,temp_unschedulable_until=NULL,temp_unschedulable_reason=NULL,updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, accountID, service.StatusBalanceInsufficient, reason); err != nil {
		return err
	}
	if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *groupHealthStore) RestoreAccountBalance(ctx context.Context, accountID int64, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE accounts SET status='active',error_message='',temp_unschedulable_until=NULL,temp_unschedulable_reason=NULL,updated_at=$2 WHERE id=$1 AND status='balance_insufficient' AND deleted_at IS NULL`, accountID, now)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return service.ErrAccountNotFound
	}
	if _, err = tx.ExecContext(ctx, `UPDATE account_health_states SET runtime_status='active',reason=NULL,retry_step=0,next_probe_at=NULL,last_success_at=$2,updated_at=$2 WHERE account_id=$1`, accountID, now); err != nil {
		return err
	}
	if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *groupHealthStore) ClaimImmediateProbe(ctx context.Context, accountID, groupID int64, now time.Time, cooldown time.Duration) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO account_health_states(account_id,probe_group_id,runtime_status,retry_step,last_failure_at,last_immediate_probe_at)
		VALUES($1,$2,'active',0,$3,$3)
		ON CONFLICT(account_id) DO UPDATE SET probe_group_id=$2,
		last_failure_at=$3,last_immediate_probe_at=$3,updated_at=NOW()
		WHERE account_health_states.runtime_status <> 'balance_insufficient'
		  AND (account_health_states.last_immediate_probe_at IS NULL OR account_health_states.last_immediate_probe_at <= $3 - $4::interval)`, accountID, groupID, now, fmt.Sprintf("%f seconds", cooldown.Seconds()))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (r *groupHealthStore) RefreshDerivedGroupHealth(ctx context.Context, groupID int64, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		WITH counts AS (
		  SELECT COUNT(*) FILTER (WHERE a.status='active' AND a.schedulable IS TRUE AND COALESCE(h.runtime_status,'active')='active') AS healthy,
		         COUNT(*) FILTER (WHERE a.status='balance_insufficient' OR h.runtime_status='balance_insufficient') AS balance
		  FROM account_groups ag JOIN accounts a ON a.id=ag.account_id AND a.deleted_at IS NULL
		  LEFT JOIN account_health_states h ON h.account_id=a.id WHERE ag.group_id=$1
		), derived AS (
		  SELECT CASE WHEN healthy>0 THEN 'healthy' WHEN balance>0 THEN 'balance_insufficient' ELSE 'unavailable' END AS status FROM counts
		)
		INSERT INTO group_health_states(group_id,status,reason,created_at,updated_at)
		SELECT $1,status,CASE status WHEN 'healthy' THEN 'healthy_account_available' WHEN 'balance_insufficient' THEN 'no_healthy_account_balance_insufficient' ELSE 'no_healthy_account' END,
		       NOW(),NOW() FROM derived
		ON CONFLICT(group_id) DO UPDATE SET status=EXCLUDED.status,reason=EXCLUDED.reason,
		updated_at=NOW()`, groupID)
	return err
}

func (r *groupHealthStore) UpdateRollingMetrics(ctx context.Context, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		WITH probe AS (
		 SELECT group_id, COUNT(*) AS samples,
		  COALESCE(100.0*COUNT(*) FILTER(WHERE success)/NULLIF(COUNT(*),0),0) AS availability,
		  COALESCE(AVG(ttft_ms) FILTER(WHERE success AND ttft_ms>0),0)::int AS avg_ttft,
		  COALESCE(percentile_cont(0.95) WITHIN GROUP(ORDER BY ttft_ms) FILTER(WHERE success AND ttft_ms>0),0)::int AS p95_ttft
		 FROM group_health_events WHERE is_probe IS TRUE AND observed_at >= $1 - interval '6 hours' GROUP BY group_id
	), real_success AS (
		 SELECT COALESCE(ul.group_id, ag.group_id) AS group_id,COUNT(*) AS successes,
		  COALESCE(percentile_cont(0.5) WITHIN GROUP(ORDER BY first_token_ms),0)::int AS p50_ttft,
		  COALESCE(AVG(first_token_ms),0)::int AS avg_ttft,
		  COALESCE(percentile_cont(0.95) WITHIN GROUP(ORDER BY first_token_ms),0)::int AS p95_ttft,
		  COALESCE(AVG(duration_ms),0)::int AS avg_total
		 FROM usage_logs ul
		 LEFT JOIN account_groups ag ON ag.account_id = ul.account_id AND ul.group_id IS NULL
		 WHERE first_token_ms IS NOT NULL AND created_at >= $1 - interval '6 hours'
		 GROUP BY COALESCE(ul.group_id, ag.group_id)
	), real_failure AS (
		 SELECT group_id,COUNT(*) AS failures FROM group_health_events
		 WHERE is_probe IS FALSE AND success IS FALSE AND observed_at >= $1 - interval '6 hours' GROUP BY group_id
	), metrics AS (
		 SELECT s.group_id,
		  COALESCE(p.availability,0) AS probe_availability,
		  COALESCE(p.avg_ttft,0) AS probe_avg_ttft,
		  COALESCE(p.p95_ttft,0) AS probe_p95_ttft,
		  COALESCE(p.samples,0) AS probe_samples,
		  COALESCE(rs.p50_ttft,0) AS real_p50_ttft,
		  COALESCE(rs.avg_ttft,0) AS real_avg_ttft,
		  COALESCE(rs.p95_ttft,0) AS real_p95_ttft,
		  COALESCE(rs.successes,0) AS real_samples,
		  COALESCE(rs.avg_total,0) AS real_avg_total,
		  COALESCE(rf.failures,0) AS real_failures
		 FROM group_health_states s
		 LEFT JOIN probe p ON p.group_id=s.group_id
		 LEFT JOIN real_success rs ON rs.group_id=s.group_id
		 LEFT JOIN real_failure rf ON rf.group_id=s.group_id
	)
	UPDATE group_health_states s SET
	 probe_availability_6h=m.probe_availability,probe_ttft_avg_ms=m.probe_avg_ttft,probe_ttft_p95_ms=m.probe_p95_ttft,probe_samples=m.probe_samples,
	 real_ttft_p50_ms=m.real_p50_ttft,real_ttft_avg_ms=m.real_avg_ttft,real_ttft_p95_ms=m.real_p95_ttft,real_ttft_samples=m.real_samples,real_total_avg_ms=m.real_avg_total,
	 real_availability_6h=CASE WHEN m.real_samples+m.real_failures=0 THEN 0 ELSE 100.0*m.real_samples/(m.real_samples+m.real_failures) END,
	 updated_at=NOW()
	FROM metrics m
	WHERE s.group_id=m.group_id`, now)
	return err
}

func (r *groupHealthStore) LoadMetrics(ctx context.Context, groupIDs []int64) (map[int64]service.GroupHealthSnapshot, error) {
	out := make(map[int64]service.GroupHealthSnapshot, len(groupIDs))
	if len(groupIDs) == 0 {
		return out, nil
	}
	states, err := r.client.GroupHealthState.Query().Where(grouphealthstate.GroupIDIn(groupIDs...)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, state := range states {
		out[state.GroupID] = groupHealthEntityToSnapshot(state)
	}
	return out, nil
}

func (r *groupHealthStore) LoadTrend(ctx context.Context, groupIDs []int64, start, end time.Time) (map[int64][]service.GroupHealthTrendBucket, error) {
	out := make(map[int64][]service.GroupHealthTrendBucket, len(groupIDs))
	if len(groupIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		WITH event_buckets AS (
		 SELECT group_id,date_bin('10 minutes',observed_at,TIMESTAMPTZ '2000-01-01') AS bucket,
		 COUNT(*) FILTER(WHERE is_probe AND success)::int AS probe_success,
		 COUNT(*) FILTER(WHERE is_probe AND NOT success)::int AS probe_failure,
		 COUNT(*) FILTER(WHERE NOT is_probe AND NOT success)::int AS real_failure,
		 COALESCE(AVG(ttft_ms) FILTER(WHERE is_probe AND success AND ttft_ms>0),0)::int AS probe_ttft
		 FROM group_health_events WHERE group_id=ANY($1) AND observed_at >= $2 AND observed_at < $3 GROUP BY group_id,bucket
	), usage_buckets AS (
		 SELECT COALESCE(ul.group_id, ag.group_id) AS group_id,date_bin('10 minutes',ul.created_at,TIMESTAMPTZ '2000-01-01') AS bucket,
		 COUNT(*)::int AS real_success,COALESCE(AVG(first_token_ms),0)::int AS real_ttft
		 FROM usage_logs ul
		 LEFT JOIN account_groups ag ON ag.account_id = ul.account_id AND ul.group_id IS NULL
		 WHERE COALESCE(ul.group_id, ag.group_id)=ANY($1) AND ul.first_token_ms IS NOT NULL AND ul.created_at >= $2 AND ul.created_at < $3
		 GROUP BY COALESCE(ul.group_id, ag.group_id),bucket
	)
	SELECT COALESCE(e.group_id,u.group_id),COALESCE(e.bucket,u.bucket),COALESCE(e.probe_success,0),COALESCE(e.probe_failure,0),COALESCE(u.real_success,0),COALESCE(e.real_failure,0),COALESCE(e.probe_ttft,0),COALESCE(u.real_ttft,0)
	FROM event_buckets e FULL JOIN usage_buckets u USING(group_id,bucket) ORDER BY 1,2`, pq.Array(groupIDs), start, end)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var groupID int64
		var bucket service.GroupHealthTrendBucket
		if err := rows.Scan(&groupID, &bucket.StartedAt, &bucket.ProbeSuccess, &bucket.ProbeFailure, &bucket.RealSuccess, &bucket.RealFailure, &bucket.ProbeTTFTMS, &bucket.RealTTFTMS); err != nil {
			return nil, err
		}
		out[groupID] = append(out[groupID], bucket)
	}
	return out, rows.Err()
}
