package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type qualityTransactionStarter interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

// Account row locks serialize quality ownership changes with explicit admin overrides.
func (r *accountRepository) ReconcileGroupQualityScheduling(ctx context.Context, id int64) error {
	starter, ok := r.sql.(qualityTransactionStarter)
	if !ok {
		return errors.New("quality scheduling requires a transactional database")
	}
	tx, err := starter.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var schedulable, usable bool
	err = tx.QueryRowContext(ctx, `SELECT schedulable, status = 'active' AND (expires_at IS NULL OR expires_at > NOW() OR NOT auto_pause_on_expired)
        FROM accounts WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&schedulable, &usable)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var owned, held bool
	err = tx.QueryRowContext(ctx, `SELECT
        EXISTS (SELECT 1 FROM account_quality_checks WHERE account_id = $1 AND paused),
        EXISTS (SELECT 1 FROM group_account_quality_checks q
            JOIN group_quality_check_settings s ON s.group_id = q.group_id AND s.enabled
            JOIN account_groups ag ON ag.group_id = q.group_id AND ag.account_id = q.account_id
            JOIN groups g ON g.id = q.group_id AND g.deleted_at IS NULL
            JOIN accounts a ON a.id = q.account_id
            WHERE q.account_id = $1 AND q.paused AND (`+service.QualityAccountEligibilitySQL+`))`, id).Scan(&owned, &held)
	if err != nil {
		return err
	}
	change := false
	if held && usable && schedulable {
		schedulable = false
		owned = true
		change = true
	} else if !held && owned && usable {
		schedulable = true
		owned = false
		change = true
	}
	if !change {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET schedulable = $2, updated_at = NOW() WHERE id = $1`, id, schedulable); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_quality_checks (account_id, window_started_at, paused)
        VALUES ($1, NOW(), $2) ON CONFLICT (account_id) DO UPDATE SET paused = EXCLUDED.paused, updated_at = NOW()`, id, owned); err != nil {
		return err
	}
	if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &id, nil, nil); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if !schedulable {
		r.syncSchedulerAccountSnapshot(ctx, id)
	}
	return nil
}

// SetManualQualitySchedulable relinquishes quality ownership atomically with the admin action.
func (r *accountRepository) SetManualQualitySchedulable(ctx context.Context, ids []int64, schedulable bool) error {
	starter, ok := r.sql.(qualityTransactionStarter)
	if !ok {
		return errors.New("quality scheduling requires a transactional database")
	}
	tx, err := starter.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range ids {
		var exists int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&exists); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET schedulable = $2, updated_at = NOW() WHERE id = $1`, id, schedulable); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE account_quality_checks SET paused = FALSE, updated_at = NOW() WHERE account_id = $1`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE group_account_quality_checks SET paused = FALSE, degraded_streak = 0 WHERE account_id = $1`, id); err != nil {
			return err
		}
		if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &id, nil, nil); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if !schedulable {
		r.syncSchedulerAccountSnapshots(ctx, ids)
	}
	return nil
}
