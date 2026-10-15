package service

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image/png"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const (
	qualityCheckMinAge      = 30 * time.Minute
	qualityCheckMinSpend    = 5.0
	qualityRecoveryInterval = 30 * time.Minute
	qualitySuspectRatio     = 0.5
	qualityProbesPerTick    = 3
	qualityProbeTimeout     = 20 * time.Minute
	qualityLockTTL          = 65 * time.Minute
	qualityLockKey          = "group-quality:scan"
	qualityPreviewMaxBytes  = 2 << 20
)

var ErrQualityInvalidConfig = errors.New("invalid quality check configuration")
var ErrQualityConfigChanged = errors.New("quality check configuration changed during probe")

// GroupQualityConfig supports partial updates without resetting saved thresholds.
type GroupQualityConfig struct {
	Enabled            bool     `json:"enabled"`
	MinSpendUSD        *float64 `json:"min_spend_usd"`
	MinIntervalMinutes *int     `json:"min_interval_minutes"`
	ModelID            *string  `json:"model_id"`
}

type GroupQualityStatus struct {
	GroupID            int64      `json:"group_id"`
	Enabled            bool       `json:"enabled"`
	MinSpendUSD        float64    `json:"min_spend_usd"`
	MinIntervalMinutes int        `json:"min_interval_minutes"`
	ModelID            string     `json:"model_id"`
	DefaultPrompt      string     `json:"default_prompt"`
	Status             string     `json:"status"`
	CheckedAccounts    int        `json:"checked_accounts"`
	DegradedAccounts   int        `json:"degraded_accounts"`
	LastRunAt          *time.Time `json:"last_run_at"`
}

type GroupQualityResult struct {
	ResultID     string                   `json:"result_id"`
	Status       string                   `json:"status"`
	Reason       string                   `json:"reason"`
	ModelID      string                   `json:"model_id"`
	CheckedAt    time.Time                `json:"checked_at"`
	HasPreview   bool                     `json:"has_preview"`
	HasArtwork   bool                     `json:"has_artwork"`
	HasRawOutput bool                     `json:"has_raw_output"`
	Score        *float64                 `json:"score"`
	Passed       *bool                    `json:"passed"`
	IsDegraded   *bool                    `json:"is_degraded"`
	Diagnostics  *GroupQualityDiagnostics `json:"diagnostics,omitempty"`
}

type qualityProbeResult struct {
	Status      string
	Reason      string
	ModelID     string
	Artwork     string
	RawOutput   string
	PreviewPNG  []byte
	Score       *float64
	Diagnostics *GroupQualityDiagnostics
}

type qualitySchedulingRepository interface {
	ReconcileGroupQualityScheduling(context.Context, int64) error
}

type GroupQualityChecker struct {
	db        *sql.DB
	accounts  AccountRepository
	tests     *AccountTestService
	lockCache LeaderLockCache
	owner     string
	now       func() time.Time
	probeFunc func(context.Context, int64, string) qualityProbeResult
	launch    func(func(context.Context)) bool
}

func newGroupQualityChecker(db *sql.DB, accounts AccountRepository, tests *AccountTestService, lockCache LeaderLockCache, owner string) *GroupQualityChecker {
	return &GroupQualityChecker{db: db, accounts: accounts, tests: tests, lockCache: lockCache, owner: owner, now: time.Now}
}

func qualityWindowDue(now, anchor time.Time, spend float64) bool {
	return qualityConfiguredWindowDue(now, anchor, spend, qualityCheckMinSpend, qualityCheckMinAge)
}

func qualityConfiguredWindowDue(now, anchor time.Time, spend, minimum float64, interval time.Duration) bool {
	return !anchor.IsZero() && now.Sub(anchor) >= interval && spend >= minimum
}

func qualityShouldPause(previous, current string) bool {
	return previous == "degraded" && current == "degraded"
}
func qualityShouldResume(paused bool, current string) bool { return paused && current == "success" }

func accountAcceptsQualityPrompt(account *Account) bool {
	return account != nil && !account.IsSyntheticUITest()
}

func validateQualityConfig(cfg GroupQualityConfig) error {
	if cfg.MinSpendUSD != nil && (math.IsNaN(*cfg.MinSpendUSD) || math.IsInf(*cfg.MinSpendUSD, 0) || *cfg.MinSpendUSD < 0.01 || *cfg.MinSpendUSD > 1000000) {
		return fmt.Errorf("%w: min_spend_usd must be between 0.01 and 1000000", ErrQualityInvalidConfig)
	}
	if cfg.MinIntervalMinutes != nil && (*cfg.MinIntervalMinutes < 1 || *cfg.MinIntervalMinutes > 10080) {
		return fmt.Errorf("%w: min_interval_minutes must be between 1 and 10080", ErrQualityInvalidConfig)
	}
	if cfg.ModelID != nil && (len(*cfg.ModelID) > 200 || strings.ContainsAny(*cfg.ModelID, "\r\n\x00")) {
		return fmt.Errorf("%w: invalid model_id", ErrQualityInvalidConfig)
	}
	return nil
}

func (c *GroupQualityChecker) SetEnabled(ctx context.Context, groupID int64, enabled bool) (*GroupQualityStatus, error) {
	return c.SetConfig(ctx, groupID, GroupQualityConfig{Enabled: enabled})
}

func (c *GroupQualityChecker) SetConfig(ctx context.Context, groupID int64, cfg GroupQualityConfig) (*GroupQualityStatus, error) {
	if c == nil || c.db == nil || groupID <= 0 {
		return nil, ErrGroupNotFound
	}
	if err := validateQualityConfig(cfg); err != nil {
		return nil, err
	}
	if cfg.ModelID != nil {
		model := strings.TrimSpace(*cfg.ModelID)
		cfg.ModelID = &model
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
        INSERT INTO group_quality_check_settings (group_id, enabled, min_spend_usd, min_interval_minutes, model_id)
        VALUES ($1, $2, COALESCE($3::numeric, 5), COALESCE($4, 30), COALESCE($5, ''))
        ON CONFLICT (group_id) DO UPDATE SET
            enabled = EXCLUDED.enabled,
            min_spend_usd = COALESCE($3::numeric, group_quality_check_settings.min_spend_usd),
            min_interval_minutes = COALESCE($4, group_quality_check_settings.min_interval_minutes),
            model_id = COALESCE($5, group_quality_check_settings.model_id),
            version = group_quality_check_settings.version + 1, updated_at = NOW()`,
		groupID, cfg.Enabled, cfg.MinSpendUSD, cfg.MinIntervalMinutes, cfg.ModelID)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		if _, err := tx.ExecContext(ctx, `UPDATE group_account_quality_checks SET paused = FALSE, degraded_streak = 0 WHERE group_id = $1`, groupID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		c.reconcileScheduling(ctx)
	}
	items, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].GroupID == groupID {
			return &items[i], nil
		}
	}
	return nil, ErrGroupNotFound
}

func (c *GroupQualityChecker) List(ctx context.Context) ([]GroupQualityStatus, error) {
	if c == nil || c.db == nil {
		return []GroupQualityStatus{}, nil
	}
	rows, err := c.db.QueryContext(ctx, `
        SELECT s.group_id, s.enabled, s.min_spend_usd::float8, s.min_interval_minutes, s.model_id,
            COUNT(q.account_id) FILTER (WHERE q.last_checked_at >= NOW() - INTERVAL '1 hour' AND q.last_status IN ('success', 'degraded')),
            COUNT(q.account_id) FILTER (WHERE q.last_checked_at >= NOW() - INTERVAL '1 hour' AND q.last_status = 'degraded'),
            MAX(q.last_checked_at)
        FROM group_quality_check_settings s
        LEFT JOIN account_groups ag ON ag.group_id = s.group_id
        LEFT JOIN accounts a ON a.id = ag.account_id AND a.deleted_at IS NULL
        LEFT JOIN group_account_quality_checks q ON q.group_id = s.group_id AND q.account_id = a.id
        GROUP BY s.group_id, s.enabled, s.min_spend_usd, s.min_interval_minutes, s.model_id ORDER BY s.group_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]GroupQualityStatus, 0)
	for rows.Next() {
		var item GroupQualityStatus
		var last sql.NullTime
		if err := rows.Scan(&item.GroupID, &item.Enabled, &item.MinSpendUSD, &item.MinIntervalMinutes, &item.ModelID, &item.CheckedAccounts, &item.DegradedAccounts, &last); err != nil {
			return nil, err
		}
		item.DefaultPrompt = DefaultScheduledTestPrompt
		item.Status = qualityAggregateStatus(item.Enabled, item.CheckedAccounts, item.DegradedAccounts)
		if last.Valid {
			item.LastRunAt = &last.Time
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func qualityAggregateStatus(enabled bool, checked, degraded int) string {
	if !enabled || checked <= 0 {
		return "unknown"
	}
	if degraded == 0 || float64(degraded)/float64(checked) < qualitySuspectRatio {
		return "healthy"
	}
	return "suspect"
}

type qualityCandidate struct {
	groupID    int64
	id         int64
	version    int64
	modelID    string
	lastStatus string
	streak     int
	paused     bool
}

// Spend eligibility is evaluated before LIMIT so idle accounts cannot starve busy accounts.
const qualityCandidatesSQL = `
    SELECT q.group_id, q.account_id, s.version, s.model_id, q.last_status, q.degraded_streak, q.paused
    FROM group_account_quality_checks q
    JOIN account_groups ag ON ag.group_id = q.group_id AND ag.account_id = q.account_id
    JOIN group_quality_check_settings s ON s.group_id = q.group_id AND s.enabled
    JOIN accounts a ON a.id = q.account_id AND a.deleted_at IS NULL AND a.status = 'active'
    LEFT JOIN account_quality_checks ownership ON ownership.account_id = a.id
    WHERE (` + QualityAccountEligibilitySQL + `)
      AND (a.schedulable OR COALESCE(ownership.paused, FALSE))
      AND (a.expires_at IS NULL OR a.expires_at > $1 OR NOT a.auto_pause_on_expired)
      AND (q.retry_after IS NULL OR q.retry_after <= $1)
      AND (
        (COALESCE(ownership.paused, FALSE) AND (q.last_attempt_at IS NULL OR q.last_attempt_at <= $1 - INTERVAL '30 minutes'))
        OR (a.schedulable
            AND q.window_started_at <= $1 - make_interval(mins => s.min_interval_minutes)
            AND COALESCE((SELECT SUM(u.actual_cost) FROM usage_logs u
                WHERE u.group_id = q.group_id AND u.account_id = q.account_id AND u.created_at >= q.window_started_at), 0) >= s.min_spend_usd)
      )
    ORDER BY q.last_attempt_at NULLS FIRST, q.group_id, q.account_id LIMIT $2`

func (c *GroupQualityChecker) initializeWindows(ctx context.Context, now time.Time) error {
	_, err := c.db.ExecContext(ctx, `
        INSERT INTO group_account_quality_checks (group_id, account_id, window_started_at)
        SELECT s.group_id, a.id, $1
        FROM group_quality_check_settings s
        JOIN account_groups ag ON ag.group_id = s.group_id
        JOIN accounts a ON a.id = ag.account_id AND a.deleted_at IS NULL AND a.status = 'active'
        WHERE s.enabled AND (`+QualityAccountEligibilitySQL+`)
          AND (a.schedulable OR EXISTS (SELECT 1 FROM account_quality_checks p WHERE p.account_id = a.id AND p.paused))
        ON CONFLICT (group_id, account_id) DO NOTHING`, now)
	return err
}

func (c *GroupQualityChecker) listCandidates(ctx context.Context, now time.Time) ([]qualityCandidate, error) {
	rows, err := c.db.QueryContext(ctx, qualityCandidatesSQL, now, qualityProbesPerTick)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var items []qualityCandidate
	for rows.Next() {
		var item qualityCandidate
		if err := rows.Scan(&item.groupID, &item.id, &item.version, &item.modelID, &item.lastStatus, &item.streak, &item.paused); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (c *GroupQualityChecker) Run(ctx context.Context) {
	if c == nil || c.db == nil || c.accounts == nil || (c.tests == nil && c.probeFunc == nil) || ctx.Err() != nil {
		return
	}
	release, acquired := tryAcquireSingletonLeaderLock(ctx, c.lockCache, c.db, qualityLockKey, c.owner, qualityLockTTL)
	if !acquired {
		return
	}
	defer release()
	// The bound is shorter than the lease, including all generation and review requests.
	ctx, cancel := context.WithTimeout(ctx, qualityManualRunTimeout)
	defer cancel()
	c.reconcileScheduling(ctx)
	now := c.now()
	if err := c.initializeWindows(ctx, now); err != nil {
		slog.Warn("group_quality: initialize windows failed", "error", err)
		return
	}
	candidates, err := c.listCandidates(ctx, now)
	if err != nil {
		slog.Warn("group_quality: candidates failed", "error", err)
		return
	}
	// Batch this tick's candidates by group while retaining their scheduler order.
	byGroup := make(map[int64][]qualityCandidate)
	var groups []int64
	for _, candidate := range candidates {
		if _, exists := byGroup[candidate.groupID]; !exists {
			groups = append(groups, candidate.groupID)
		}
		byGroup[candidate.groupID] = append(byGroup[candidate.groupID], candidate)
	}
	for _, groupID := range groups {
		if ctx.Err() != nil {
			return
		}
		c.runAutomaticGroup(ctx, groupID, byGroup[groupID])
	}
}

func (c *GroupQualityChecker) runAutomaticGroup(ctx context.Context, groupID int64, candidates []qualityCandidate) {
	release, acquired := tryAcquireSingletonLeaderLock(ctx, c.lockCache, c.db, qualityGroupLockKey(groupID), uuid.NewString(), qualityLockTTL)
	if !acquired {
		return
	}
	defer release()
	eligible := make([]qualityCandidate, 0, 1)
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return
		}
		account, err := c.accounts.GetByID(ctx, candidate.id)
		if err != nil || account == nil {
			continue
		}
		if !accountAcceptsQualityPrompt(account) {
			// A skip is not a completed check and does not move the spend window.
			if _, err := c.db.ExecContext(ctx, `UPDATE group_account_quality_checks SET retry_after = $3, degraded_streak = 0 WHERE group_id = $1 AND account_id = $2`, candidate.groupID, candidate.id, c.now().Add(time.Hour)); err != nil {
				slog.Warn("group_quality: save skip failed", "group_id", groupID)
			}
			continue
		}
		eligible = append(eligible, candidate)
		break
	}
	if len(eligible) == 0 {
		return
	}
	run, err := c.createQualityRun(ctx, groupID, "automatic", eligible)
	if err != nil {
		slog.Warn("group_quality: create automatic run failed", "group_id", groupID)
		return
	}
	c.executeQualityRun(ctx, run, eligible)
}

func qualityNextState(streak int, paused bool, status string) (int, bool) {
	switch status {
	case "success":
		return 0, false
	case "degraded":
		streak++
		if streak > 2 {
			streak = 2
		}
		return streak, paused || streak >= 2
	default:
		return 0, paused
	}
}

func validQualityPreview(data []byte) bool {
	if len(data) == 0 || len(data) > qualityPreviewMaxBytes {
		return false
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	return err == nil && cfg.Width > 0 && cfg.Height > 0 && cfg.Width <= 1920 && cfg.Height <= 1280
}

func qualityArtworkValue(document string) any {
	if len(document) == 0 || len(document) > pelicanDocumentLimit || strings.TrimSpace(document) == "" {
		return nil
	}
	return document
}

func qualityRawOutputValue(output string) any {
	if len(output) == 0 || len(output) > pelicanDocumentLimit {
		return nil
	}
	return output
}

func (c *GroupQualityChecker) finishCheck(ctx context.Context, candidate qualityCandidate, now time.Time, result qualityProbeResult) error {
	switch result.Status {
	case "success", "degraded", "unknown", "failed":
	default:
		result.Status = "unknown"
		result.Reason = "Quality evaluation unavailable"
	}
	// Score columns remain readable for historical results; new checks do not score HTML.
	result.Score = nil
	if len(result.Reason) > 2000 {
		result.Reason = "Quality evaluation unavailable"
	}
	if len(result.ModelID) > 200 {
		result.ModelID = candidate.modelID
	}
	var preview any
	if result.Status != "failed" && validQualityPreview(result.PreviewPNG) {
		preview = result.PreviewPNG
	}
	retry := now
	if result.Status == "failed" {
		retry = now.Add(5 * time.Minute)
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var version int64
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT version, enabled FROM group_quality_check_settings WHERE group_id = $1 FOR UPDATE`, candidate.groupID).Scan(&version, &enabled); err != nil {
		return err
	}
	if !enabled || version != candidate.version {
		return ErrQualityConfigChanged
	}
	var currentStreak int
	var currentPaused bool
	var lastModel string
	if err := tx.QueryRowContext(ctx, `SELECT degraded_streak, paused, last_model_id FROM group_account_quality_checks WHERE group_id = $1 AND account_id = $2 FOR UPDATE`, candidate.groupID, candidate.id).Scan(&currentStreak, &currentPaused, &lastModel); err != nil {
		return err
	}
	if lastModel != result.ModelID {
		currentStreak = 0
	}
	streak, paused := qualityNextState(currentStreak, currentPaused, result.Status)
	// Keep the old consumption anchor for a failed request: it produced no usable test.
	_, err = tx.ExecContext(ctx, `
        UPDATE group_account_quality_checks SET
            window_started_at = CASE WHEN $4 = 'failed' THEN window_started_at ELSE $3 END,
            last_checked_at = $3, last_attempt_at = $3, last_status = $4, degraded_streak = $5, paused = $6, retry_after = $7, last_model_id = $8
        WHERE group_id = $1 AND account_id = $2`, candidate.groupID, candidate.id, now, result.Status, streak, paused, retry, result.ModelID)
	if err != nil {
		return err
	}
	resultID := uuid.NewString()
	_, err = tx.ExecContext(ctx, `
        INSERT INTO group_quality_check_latest (group_id, account_id, result_id, status, reason, model_id, checked_at, preview_png, artwork_html, raw_output, score)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
        ON CONFLICT (group_id) DO UPDATE SET account_id = EXCLUDED.account_id, result_id = EXCLUDED.result_id,
            status = EXCLUDED.status, reason = EXCLUDED.reason, model_id = EXCLUDED.model_id,
            checked_at = EXCLUDED.checked_at, preview_png = EXCLUDED.preview_png, artwork_html = EXCLUDED.artwork_html, raw_output = EXCLUDED.raw_output, score = EXCLUDED.score`,
		candidate.groupID, candidate.id, resultID, result.Status, encodeQualityReason(result), result.ModelID, now, preview, qualityArtworkValue(result.Artwork), qualityRawOutputValue(result.RawOutput), result.Score)
	if err != nil {
		return err
	}
	if err := retainQualityHistory(ctx, tx, candidate, resultID, now, result); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *GroupQualityChecker) reconcileAccount(ctx context.Context, accountID int64) {
	repo, ok := c.accounts.(qualitySchedulingRepository)
	if !ok {
		return
	}
	if err := repo.ReconcileGroupQualityScheduling(ctx, accountID); err != nil {
		slog.Warn("group_quality: reconcile scheduling failed", "account_id", accountID, "error", err)
	}
}

func (c *GroupQualityChecker) reconcileScheduling(ctx context.Context) {
	rows, err := c.db.QueryContext(ctx, `
        SELECT account_id FROM account_quality_checks WHERE paused
        UNION SELECT q.account_id FROM group_account_quality_checks q
        JOIN group_quality_check_settings s ON s.group_id = q.group_id AND s.enabled WHERE q.paused`)
	if err != nil {
		slog.Warn("group_quality: list scheduling failed", "error", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return
	}
	for _, id := range ids {
		c.reconcileAccount(ctx, id)
	}
}

// Matrix responses contain metadata only; assets and raw output are loaded on demand.
func (c *GroupQualityChecker) Latest(ctx context.Context, groupIDs []int64) (map[int64]bool, map[int64]*GroupQualityResult, error) {
	enabled := make(map[int64]bool)
	results := make(map[int64]*GroupQualityResult)
	if c == nil || c.db == nil || len(groupIDs) == 0 {
		return enabled, results, nil
	}
	rows, err := c.db.QueryContext(ctx, `
        SELECT s.group_id, s.enabled, l.result_id::text, l.status, l.reason, l.model_id, l.checked_at,
            COALESCE(octet_length(l.preview_png) > 0, FALSE), COALESCE(octet_length(l.artwork_html) > 0, FALSE), COALESCE(octet_length(l.raw_output) > 0, FALSE), l.score
        FROM group_quality_check_settings s
        LEFT JOIN group_quality_check_latest l ON l.group_id = s.group_id
          AND EXISTS (SELECT 1 FROM accounts a JOIN account_groups ag ON ag.account_id = a.id
              WHERE a.id = l.account_id AND a.deleted_at IS NULL AND ag.group_id = s.group_id)
        WHERE s.group_id = ANY($1)`, pq.Array(groupIDs))
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var on, preview, artwork, hasRawOutput bool
		var rid, status, reason, model sql.NullString
		var checked sql.NullTime
		var score sql.NullFloat64
		if err := rows.Scan(&id, &on, &rid, &status, &reason, &model, &checked, &preview, &artwork, &hasRawOutput, &score); err != nil {
			return nil, nil, err
		}
		enabled[id] = on
		if on && rid.Valid && checked.Valid {
			displayReason, diagnostics := decodeQualityReason(reason.String)
			results[id] = &GroupQualityResult{ResultID: rid.String, Status: status.String, Reason: displayReason, ModelID: model.String, CheckedAt: checked.Time, HasPreview: preview, HasArtwork: artwork, HasRawOutput: hasRawOutput, Score: qualityScorePointer(score), Diagnostics: diagnostics}
			applyQualityStatusVerdict(results[id])
		}
	}
	return enabled, results, rows.Err()
}

func (c *GroupQualityChecker) Preview(ctx context.Context, groupID int64, resultID string) ([]byte, error) {
	if c == nil || c.db == nil {
		return nil, sql.ErrNoRows
	}
	if _, err := uuid.Parse(resultID); err != nil {
		return nil, sql.ErrNoRows
	}
	var data []byte
	err := c.db.QueryRowContext(ctx, `
        SELECT l.preview_png FROM group_quality_check_latest l
        JOIN group_quality_check_settings s ON s.group_id = l.group_id AND s.enabled
        JOIN accounts a ON a.id = l.account_id AND a.deleted_at IS NULL
        JOIN account_groups ag ON ag.account_id = a.id AND ag.group_id = l.group_id
        WHERE l.group_id = $1 AND l.result_id = $2 AND l.preview_png IS NOT NULL`, groupID, resultID).Scan(&data)
	if err != nil {
		return nil, err
	}
	if !validQualityPreview(data) {
		return nil, sql.ErrNoRows
	}
	return data, nil
}

func (c *GroupQualityChecker) Artwork(ctx context.Context, groupID int64, resultID string) (string, error) {
	if c == nil || c.db == nil {
		return "", sql.ErrNoRows
	}
	if _, err := uuid.Parse(resultID); err != nil {
		return "", sql.ErrNoRows
	}
	var document string
	err := c.db.QueryRowContext(ctx, `
        SELECT l.artwork_html FROM group_quality_check_history l
        JOIN group_quality_check_settings s ON s.group_id = l.group_id AND s.enabled
        JOIN accounts a ON a.id = l.account_id AND a.deleted_at IS NULL
        JOIN account_groups ag ON ag.account_id = a.id AND ag.group_id = l.group_id
        WHERE l.group_id = $1 AND l.result_id = $2 AND l.artwork_html IS NOT NULL`, groupID, resultID).Scan(&document)
	if err != nil {
		return "", err
	}
	if qualityArtworkValue(document) == nil {
		return "", sql.ErrNoRows
	}
	return document, nil
}

func (c *GroupQualityChecker) RawOutput(ctx context.Context, groupID int64, resultID string) (string, error) {
	if c == nil || c.db == nil {
		return "", sql.ErrNoRows
	}
	if _, err := uuid.Parse(resultID); err != nil {
		return "", sql.ErrNoRows
	}
	var output string
	err := c.db.QueryRowContext(ctx, `
        SELECT l.raw_output FROM group_quality_check_history l
        JOIN group_quality_check_settings s ON s.group_id = l.group_id AND s.enabled
        JOIN accounts a ON a.id = l.account_id AND a.deleted_at IS NULL
        JOIN account_groups ag ON ag.account_id = a.id AND ag.group_id = l.group_id
        WHERE l.group_id = $1 AND l.result_id = $2 AND l.raw_output IS NOT NULL`, groupID, resultID).Scan(&output)
	if err != nil {
		return "", err
	}
	if qualityRawOutputValue(output) == nil {
		return "", sql.ErrNoRows
	}
	return output, nil
}
