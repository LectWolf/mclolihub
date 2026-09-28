package service

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	qualityCheckMinAge       = 30 * time.Minute
	qualityCheckMinSpend     = 5.0
	qualitySuspectRatio      = 0.5
	qualityProbesPerTick     = 3
	qualityCandidatesPerTick = 20
	qualityProbeTimeout      = 90 * time.Second
	qualityLockTTL           = 10 * time.Minute
	qualityLockKey           = "group-quality:scan"
)

// GroupQualityStatus is one group's aggregated degradation state.
type GroupQualityStatus struct {
	GroupID          int64      `json:"group_id"`
	Enabled          bool       `json:"enabled"`
	Status           string     `json:"status"`
	CheckedAccounts  int        `json:"checked_accounts"`
	DegradedAccounts int        `json:"degraded_accounts"`
	LastRunAt        *time.Time `json:"last_run_at"`
}

// GroupQualityChecker runs degradation probes and stores the per-group toggle.
// A probe is sent only when both the time and spend gates are open.
type GroupQualityChecker struct {
	db        *sql.DB
	accounts  AccountRepository
	tests     *AccountTestService
	lockCache LeaderLockCache
	owner     string
	now       func() time.Time
}

func newGroupQualityChecker(db *sql.DB, accounts AccountRepository, tests *AccountTestService, lockCache LeaderLockCache, owner string) *GroupQualityChecker {
	return &GroupQualityChecker{db: db, accounts: accounts, tests: tests, lockCache: lockCache, owner: owner, now: time.Now}
}

func qualityWindowDue(now, anchor time.Time, spend float64) bool {
	if anchor.IsZero() || now.Sub(anchor) < qualityCheckMinAge {
		return false
	}
	return spend >= qualityCheckMinSpend
}

func qualityShouldPause(previous, current string) bool {
	return previous == "degraded" && current == "degraded"
}

func qualityShouldResume(paused bool, current string) bool {
	return paused && current == "success"
}

// accountAcceptsQualityPrompt reports whether the account connection test will
// actually send the pelican prompt. Claude's built-in test ignores it, and
// scoring that short reply would pause healthy accounts.
func accountAcceptsQualityPrompt(account *Account) bool {
	if account == nil {
		return false
	}
	if account.IsCNProvider() {
		return account.GetAPIProtocol() != APIProtocolAnthropic
	}
	return account.IsQoder() || account.IsOpenAI() || account.IsGemini() || account.IsGrok() || account.IsCodeBuddy() || account.Platform == PlatformAntigravity || account.IsOpenCodeGo()
}

func (c *GroupQualityChecker) SetEnabled(ctx context.Context, groupID int64, enabled bool) (*GroupQualityStatus, error) {
	if c == nil || c.db == nil || groupID <= 0 {
		return nil, ErrGroupNotFound
	}
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO group_quality_check_settings (group_id, enabled)
		VALUES ($1, $2)
		ON CONFLICT (group_id) DO UPDATE
		SET enabled = EXCLUDED.enabled, updated_at = NOW()`, groupID, enabled)
	if err != nil {
		return nil, err
	}
	if !enabled {
		c.clearPauses(ctx, groupID)
	}
	return c.statusFor(ctx, groupID)
}

func (c *GroupQualityChecker) List(ctx context.Context) ([]GroupQualityStatus, error) {
	if c == nil || c.db == nil {
		return []GroupQualityStatus{}, nil
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT s.group_id, s.enabled,
			COUNT(q.account_id) FILTER (
				WHERE q.last_checked_at >= NOW() - INTERVAL '1 hour'
				  AND q.last_status IN ('success', 'degraded')
			),
			COUNT(q.account_id) FILTER (
				WHERE q.last_checked_at >= NOW() - INTERVAL '1 hour'
				  AND q.last_status = 'degraded'
			),
			MAX(q.last_checked_at)
		FROM group_quality_check_settings s
		LEFT JOIN account_groups ag ON ag.group_id = s.group_id
		LEFT JOIN account_quality_checks q ON q.account_id = ag.account_id
		GROUP BY s.group_id, s.enabled
		ORDER BY s.group_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]GroupQualityStatus, 0)
	for rows.Next() {
		var item GroupQualityStatus
		var last sql.NullTime
		if err := rows.Scan(&item.GroupID, &item.Enabled, &item.CheckedAccounts, &item.DegradedAccounts, &last); err != nil {
			return nil, err
		}
		item.Status = qualityAggregateStatus(item.Enabled, item.CheckedAccounts, item.DegradedAccounts)
		if last.Valid {
			t := last.Time
			item.LastRunAt = &t
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

func (c *GroupQualityChecker) statusFor(ctx context.Context, groupID int64) (*GroupQualityStatus, error) {
	items, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].GroupID == groupID {
			return &items[i], nil
		}
	}
	return &GroupQualityStatus{GroupID: groupID, Status: "unknown"}, nil
}

func (c *GroupQualityChecker) clearPauses(ctx context.Context, groupID int64) {
	if c.accounts == nil {
		return
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT q.account_id
		FROM account_quality_checks q
		JOIN account_groups ag ON ag.account_id = q.account_id AND ag.group_id = $1
		WHERE q.paused
		  AND NOT EXISTS (
			SELECT 1
			FROM account_groups other
			JOIN group_quality_check_settings s ON s.group_id = other.group_id AND s.enabled
			WHERE other.account_id = q.account_id AND other.group_id <> $1
		  )`, groupID)
	if err != nil {
		slog.Warn("group_quality: list pauses failed", "group_id", groupID, "error", err)
		return
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			slog.Warn("group_quality: scan pause failed", "group_id", groupID, "error", err)
			return
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("group_quality: list pauses failed", "group_id", groupID, "error", err)
		return
	}
	for _, id := range ids {
		if err := c.accounts.SetSchedulable(ctx, id, true); err != nil {
			slog.Warn("group_quality: re-enable failed", "group_id", groupID, "account_id", id, "error", err)
			continue
		}
		if _, err := c.db.ExecContext(ctx, `UPDATE account_quality_checks SET paused = FALSE, updated_at = NOW() WHERE account_id = $1`, id); err != nil {
			slog.Warn("group_quality: clear pause flag failed", "account_id", id, "error", err)
		}
	}
}

type qualityCandidate struct {
	id          int64
	hasRow      bool
	windowStart sql.NullTime
	lastChecked sql.NullTime
	lastStatus  string
	paused      bool
	schedulable bool
}

func (c *GroupQualityChecker) Run(ctx context.Context) {
	if c == nil || c.db == nil || c.accounts == nil || c.tests == nil || ctx.Err() != nil {
		return
	}
	release, acquired := tryAcquireSingletonLeaderLock(ctx, c.lockCache, c.db, qualityLockKey, c.owner, qualityLockTTL)
	if !acquired {
		return
	}
	defer release()
	now := c.now()
	candidates, err := c.listCandidates(ctx, now.Add(-qualityCheckMinAge))
	if err != nil {
		slog.Warn("group_quality: list candidates failed", "error", err)
		return
	}
	probes := 0
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return
		}
		if !candidate.hasRow {
			c.openWindow(ctx, candidate.id, now)
			continue
		}
		anchor := candidate.windowStart.Time
		if candidate.lastChecked.Valid {
			anchor = candidate.lastChecked.Time
		}
		spend, err := c.spendSince(ctx, candidate.id, anchor)
		if err != nil {
			slog.Warn("group_quality: spend lookup failed", "account_id", candidate.id, "error", err)
			continue
		}
		if !qualityWindowDue(now, anchor, spend) {
			continue
		}
		account, err := c.accounts.GetByID(ctx, candidate.id)
		if err != nil || account == nil {
			slog.Warn("group_quality: account lookup failed", "account_id", candidate.id, "error", err)
			continue
		}
		if !accountAcceptsQualityPrompt(account) {
			c.finishCheck(ctx, candidate, now, candidate.lastStatus, candidate.paused)
			continue
		}
		if probes >= qualityProbesPerTick {
			return
		}
		probes++
		verdict := c.probe(ctx, candidate.id)
		nextStatus := candidate.lastStatus
		if verdict == "success" || verdict == "degraded" {
			nextStatus = verdict
		}
		paused := candidate.paused
		if qualityShouldPause(candidate.lastStatus, nextStatus) && candidate.schedulable {
			if err := c.accounts.SetSchedulable(ctx, candidate.id, false); err != nil {
				slog.Warn("group_quality: pause failed", "account_id", candidate.id, "error", err)
				continue
			}
			paused = true
			slog.Info("group_quality: paused after consecutive degraded checks", "account_id", candidate.id)
		}
		if qualityShouldResume(candidate.paused, nextStatus) {
			if err := c.accounts.SetSchedulable(ctx, candidate.id, true); err != nil {
				slog.Warn("group_quality: resume failed", "account_id", candidate.id, "error", err)
				continue
			}
			paused = false
		}
		c.finishCheck(ctx, candidate, now, nextStatus, paused)
	}
}

func (c *GroupQualityChecker) listCandidates(ctx context.Context, dueBefore time.Time) ([]qualityCandidate, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT a.id,
			q.account_id IS NOT NULL,
			q.window_started_at,
			q.last_checked_at,
			COALESCE(q.last_status, ''),
			COALESCE(q.paused, FALSE),
			a.schedulable
		FROM accounts a
		JOIN account_groups ag ON ag.account_id = a.id
		JOIN group_quality_check_settings s ON s.group_id = ag.group_id AND s.enabled
		LEFT JOIN account_quality_checks q ON q.account_id = a.id
		WHERE a.deleted_at IS NULL
		  AND a.status = 'active'
		  AND (a.schedulable OR COALESCE(q.paused, FALSE))
		  AND (
			q.account_id IS NULL
			OR COALESCE(q.last_checked_at, q.window_started_at) <= $1
		  )
		GROUP BY a.id, q.account_id, q.window_started_at, q.last_checked_at, q.last_status, q.paused, a.schedulable
		ORDER BY COALESCE(q.last_checked_at, q.window_started_at) NULLS FIRST, a.id
		LIMIT $2`, dueBefore, qualityCandidatesPerTick)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []qualityCandidate
	for rows.Next() {
		var item qualityCandidate
		if err := rows.Scan(&item.id, &item.hasRow, &item.windowStart, &item.lastChecked, &item.lastStatus, &item.paused, &item.schedulable); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (c *GroupQualityChecker) openWindow(ctx context.Context, accountID int64, now time.Time) {
	if _, err := c.db.ExecContext(ctx, `
		INSERT INTO account_quality_checks (account_id, window_started_at, last_status, paused, updated_at)
		VALUES ($1, $2, '', FALSE, $2)
		ON CONFLICT (account_id) DO NOTHING`, accountID, now); err != nil {
		slog.Warn("group_quality: open window failed", "account_id", accountID, "error", err)
	}
}

func (c *GroupQualityChecker) spendSince(ctx context.Context, accountID int64, since time.Time) (float64, error) {
	var spend float64
	err := c.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(actual_cost), 0)::float8
		FROM usage_logs
		WHERE account_id = $1 AND created_at >= $2`, accountID, since).Scan(&spend)
	return spend, err
}

func (c *GroupQualityChecker) finishCheck(ctx context.Context, candidate qualityCandidate, now time.Time, status string, paused bool) {
	if _, err := c.db.ExecContext(ctx, `
		UPDATE account_quality_checks
		SET window_started_at = $2, last_checked_at = $2, last_status = $3, paused = $4, updated_at = $2
		WHERE account_id = $1`, candidate.id, now, status, paused); err != nil {
		slog.Warn("group_quality: save check failed", "account_id", candidate.id, "error", err)
	}
}

func (c *GroupQualityChecker) probe(ctx context.Context, accountID int64) string {
	probeCtx, cancel := context.WithTimeout(ctx, qualityProbeTimeout)
	defer cancel()
	text, err := runQualityProbe(probeCtx, c.tests, accountID)
	if err != nil {
		slog.Warn("group_quality: probe failed", "account_id", accountID, "error", err)
		return "failed"
	}
	status, reason := assessScheduledTestQuality(text, DefaultScheduledTestPrompt)
	if status != "success" {
		slog.Info("group_quality: probe verdict", "account_id", accountID, "status", status, "reason", reason)
	}
	return status
}

func runQualityProbe(ctx context.Context, tests *AccountTestService, accountID int64) (string, error) {
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = (&http.Request{}).WithContext(ctx)
	err := tests.TestAccountConnection(ginCtx, accountID, "", DefaultScheduledTestPrompt, AccountTestModeDefault)
	text, errMsg := parseTestSSEOutput(recorder.Body.String())
	if err != nil || errMsg != "" {
		if errMsg != "" {
			return "", errString(errMsg)
		}
		return "", err
	}
	return text, nil
}

type errString string

func (e errString) Error() string { return string(e) }
