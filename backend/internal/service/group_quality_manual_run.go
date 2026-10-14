package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

const qualityManualRunTimeout = qualityProbesPerTick*qualityProbeTimeout + 2*time.Minute

var (
	ErrQualityRunBusy        = errors.New("another quality check is running; try again after it finishes")
	ErrQualityRunDisabled    = errors.New("save and enable quality checks before running manually")
	ErrQualityRunNoAccounts  = errors.New("no eligible text and vision accounts in this group")
	ErrQualityRunUnavailable = errors.New("quality check execution is unavailable")
)

type GroupQualityRun struct {
	RunID           string              `json:"run_id"`
	GroupID         int64               `json:"group_id"`
	Source          string              `json:"source"`
	Status          string              `json:"status"`
	Reason          string              `json:"reason"`
	CheckedAccounts int                 `json:"checked_accounts"`
	TotalAccounts   int                 `json:"total_accounts"`
	StartedAt       time.Time           `json:"started_at"`
	FinishedAt      *time.Time          `json:"finished_at"`
	LatestQuality   *GroupQualityResult `json:"latest_quality,omitempty"`
}

// Manual execution bypasses spend, interval and retry gates, while retaining
// account eligibility and the current settings version used by finishCheck.
const qualityManualCandidatesSQL = `
    SELECT ag.group_id, a.id, s.version, s.model_id,
        COALESCE(q.last_status, ''), COALESCE(q.degraded_streak, 0), COALESCE(q.paused, FALSE)
    FROM account_groups ag
    JOIN group_quality_check_settings s ON s.group_id = ag.group_id AND s.enabled
    JOIN accounts a ON a.id = ag.account_id AND a.deleted_at IS NULL AND a.status = 'active'
    LEFT JOIN group_account_quality_checks q ON q.group_id = ag.group_id AND q.account_id = a.id
    LEFT JOIN account_quality_checks ownership ON ownership.account_id = a.id
    WHERE ag.group_id = $1
      AND (a.schedulable OR COALESCE(ownership.paused, FALSE))
      AND (a.expires_at IS NULL OR a.expires_at > $2 OR NOT a.auto_pause_on_expired)
    ORDER BY a.priority, a.id`

func (c *GroupQualityChecker) StartManualRun(ctx context.Context, groupID int64) (*GroupQualityRun, error) {
	if c == nil || c.db == nil || c.accounts == nil || (c.tests == nil && c.probeFunc == nil) || c.launch == nil {
		return nil, ErrQualityRunUnavailable
	}
	if groupID <= 0 {
		return nil, ErrGroupNotFound
	}
	// Manual and automatic execution share a group lock, independent of scan scheduling.
	release, acquired := tryAcquireSingletonLeaderLock(ctx, c.lockCache, c.db, qualityGroupLockKey(groupID), uuid.NewString(), qualityLockTTL)
	if !acquired {
		return nil, ErrQualityRunBusy
	}
	transferred := false
	defer func() {
		if !transferred {
			release()
		}
	}()
	var enabled bool
	err := c.db.QueryRowContext(ctx, `SELECT enabled FROM group_quality_check_settings WHERE group_id = $1`, groupID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !enabled) {
		return nil, ErrQualityRunDisabled
	}
	if err != nil {
		return nil, err
	}
	now := c.now()
	rows, err := c.db.QueryContext(ctx, qualityManualCandidatesSQL, groupID, now)
	if err != nil {
		return nil, err
	}
	var candidates []qualityCandidate
	for rows.Next() {
		var item qualityCandidate
		if err := rows.Scan(&item.groupID, &item.id, &item.version, &item.modelID, &item.lastStatus, &item.streak, &item.paused); err != nil {
			_ = rows.Close()
			return nil, err
		}
		candidates = append(candidates, item)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	eligible := make([]qualityCandidate, 0, 1)
	for _, candidate := range candidates {
		account, err := c.accounts.GetByID(ctx, candidate.id)
		if err != nil {
			return nil, err
		}
		if pelicanVisualProtocolSupported(account) && pelicanTextModel(account.GetMappedModel(candidate.modelID)) {
			eligible = append(eligible, candidate)
			break
		}
	}
	if len(eligible) == 0 {
		return nil, ErrQualityRunNoAccounts
	}
	run, err := c.createQualityRun(ctx, groupID, "manual", eligible)
	if err != nil {
		return nil, err
	}
	if !c.launch(func(parent context.Context) {
		defer release()
		c.executeQualityRun(parent, run, eligible)
	}) {
		c.saveRunProgress(run, "failed", "Quality check execution stopped", 0)
		return nil, ErrQualityRunUnavailable
	}
	transferred = true
	return run, nil
}

func qualityGroupLockKey(groupID int64) string {
	return fmt.Sprintf("group-quality:run:%d", groupID)
}

// Commit the task before any upstream request so both sources are observable.
func (c *GroupQualityChecker) createQualityRun(ctx context.Context, groupID int64, source string, candidates []qualityCandidate) (*GroupQualityRun, error) {
	now := c.now()
	run := &GroupQualityRun{RunID: uuid.NewString(), GroupID: groupID, Source: source, Status: "running", TotalAccounts: len(candidates), StartedAt: now}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var savedRunID string
	err = tx.QueryRowContext(ctx, `
        INSERT INTO group_quality_check_runs (group_id, run_id, status, total_accounts, started_at, source)
        VALUES ($1, $2, 'running', $3, $4, $6)
        ON CONFLICT (group_id) DO UPDATE SET run_id = EXCLUDED.run_id, status = 'running',
            reason = '', checked_accounts = 0, total_accounts = EXCLUDED.total_accounts,
            started_at = EXCLUDED.started_at, finished_at = NULL, source = EXCLUDED.source
        WHERE group_quality_check_runs.status <> 'running' OR group_quality_check_runs.started_at <= $5
        RETURNING run_id`, groupID, run.RunID, run.TotalAccounts, now, now.Add(-qualityManualRunTimeout), source).Scan(&savedRunID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrQualityRunBusy
	}
	if err != nil {
		return nil, err
	}
	if source == "manual" {
		for _, candidate := range candidates {
			if _, err := tx.ExecContext(ctx, `INSERT INTO group_account_quality_checks (group_id, account_id, window_started_at)
            VALUES ($1, $2, $3) ON CONFLICT (group_id, account_id) DO NOTHING`, groupID, candidate.id, now); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return run, nil
}

func (c *GroupQualityChecker) executeQualityRun(parent context.Context, run *GroupQualityRun, candidates []qualityCandidate) {
	ctx, cancel := context.WithTimeout(parent, qualityManualRunTimeout-c.now().Sub(run.StartedAt))
	defer cancel()
	checked := 0
	defer func() {
		if recovered := recover(); recovered != nil {
			// Do not log recovered values: upstream artifacts may contain secrets.
			slog.Error("group_quality: execution panicked", "group_id", run.GroupID)
			c.saveRunProgress(run, "failed", "Quality check execution failed", checked)
		}
	}()
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			c.saveRunProgress(run, "failed", "Quality check execution stopped or timed out", checked)
			return
		}
		probeCtx, stop := context.WithTimeout(ctx, qualityProbeTimeout)
		var result qualityProbeResult
		if c.probeFunc != nil {
			result = c.probeFunc(probeCtx, candidate.id, candidate.modelID)
		} else {
			result = c.tests.runPelicanQualityCheck(probeCtx, candidate.id, candidate.modelID)
		}
		stop()
		if err := c.finishCheck(ctx, candidate, c.now(), result); err != nil {
			reason := "Quality check result could not be saved"
			if errors.Is(err, ErrQualityConfigChanged) {
				reason = "Quality check settings changed during execution"
			}
			c.saveRunProgress(run, "failed", reason, checked)
			return
		}
		c.reconcileAccount(ctx, candidate.id)
		checked++
		if err := c.saveRunProgress(run, "running", "", checked); err != nil {
			c.saveRunProgress(run, "failed", "Quality check progress could not be saved", checked)
			return
		}
	}
	c.saveRunProgress(run, "completed", "", checked)
}

func (c *GroupQualityChecker) saveRunProgress(run *GroupQualityRun, status, reason string, checked int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := c.db.ExecContext(ctx, `UPDATE group_quality_check_runs SET status = $3::varchar, reason = $4,
        checked_accounts = $5, finished_at = CASE WHEN $3::varchar = 'running' THEN NULL ELSE $6::timestamptz END
        WHERE group_id = $1 AND run_id = $2 AND status = 'running'`, run.GroupID, run.RunID, status, reason, checked, c.now())
	if err != nil {
		slog.Warn("group_quality: save progress failed", "group_id", run.GroupID)
		return err
	}
	updated, err := result.RowsAffected()
	if err == nil && updated == 0 {
		return ErrQualityConfigChanged
	}
	return err
}

// ManualRun retains the existing API name and reads the latest run from either source.
func (c *GroupQualityChecker) ManualRun(ctx context.Context, groupID int64) (*GroupQualityRun, error) {
	if c == nil || c.db == nil {
		return nil, ErrQualityRunUnavailable
	}
	var run GroupQualityRun
	var finished sql.NullTime
	err := c.db.QueryRowContext(ctx, `SELECT run_id, group_id, status, reason, checked_accounts,
        total_accounts, started_at, finished_at, source FROM group_quality_check_runs WHERE group_id = $1`, groupID).
		Scan(&run.RunID, &run.GroupID, &run.Status, &run.Reason, &run.CheckedAccounts, &run.TotalAccounts, &run.StartedAt, &finished, &run.Source)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if finished.Valid {
		run.FinishedAt = &finished.Time
	}
	// A crash cannot leave the UI permanently disabled. The deadline is shorter
	// than the shared lock lease; stale workers cannot overwrite a newer run ID.
	if run.Status == "running" && !c.now().Before(run.StartedAt.Add(qualityManualRunTimeout)) {
		err := c.saveRunProgress(&run, "failed", "Quality check execution stopped or timed out", run.CheckedAccounts)
		if err != nil {
			return nil, err
		}
		run.Status, run.Reason = "failed", "Quality check execution stopped or timed out"
		finishedAt := c.now()
		run.FinishedAt = &finishedAt
	}
	if run.CheckedAccounts > 0 {
		_, latest, err := c.Latest(ctx, []int64{groupID})
		if err != nil {
			return nil, err
		}
		if result := latest[groupID]; result != nil && !result.CheckedAt.Before(run.StartedAt) {
			run.LatestQuality = result
		}
	}
	return &run, nil
}
