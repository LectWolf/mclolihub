package service

import (
	"context"
	"database/sql"
	"math"
	"time"

	"github.com/lib/pq"
)

func retainQualityHistory(ctx context.Context, tx *sql.Tx, candidate qualityCandidate, resultID string, now time.Time, result qualityProbeResult) error {
	_, err := tx.ExecContext(ctx, `
        INSERT INTO group_quality_check_history
            (group_id, account_id, result_id, status, reason, model_id, checked_at, score, artwork_html, raw_output)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		candidate.groupID, candidate.id, resultID, result.Status, encodeQualityReason(result), result.ModelID, now, result.Score, qualityArtworkValue(result.Artwork), qualityRawOutputValue(result.RawOutput))
	if err != nil {
		return err
	}
	// The settings row is locked by finishCheck, so saves for a group serialize.
	_, err = tx.ExecContext(ctx, `
        DELETE FROM group_quality_check_history
        WHERE group_id = $1 AND result_id IN (
            SELECT result_id FROM group_quality_check_history WHERE group_id = $1
            ORDER BY checked_at DESC, result_id DESC OFFSET 5
        )`, candidate.groupID)
	return err
}

func qualityScorePointer(value sql.NullFloat64) *float64 {
	if !value.Valid || math.IsNaN(value.Float64) || math.IsInf(value.Float64, 0) || value.Float64 < 0 || value.Float64 > 100 {
		return nil
	}
	score := value.Float64
	return &score
}

// Decisions follow the saved generation status, never a score threshold.
func applyQualityStatusVerdict(result *GroupQualityResult) {
	if result == nil || (result.Status != "success" && result.Status != "degraded") {
		return
	}
	passed := result.Status == "success"
	degraded := result.Status == "degraded"
	result.Passed = &passed
	result.IsDegraded = &degraded
}

// History returns metadata only; HTML is loaded separately under the same scope.
func (c *GroupQualityChecker) History(ctx context.Context, groupIDs []int64) (map[int64][]*GroupQualityResult, error) {
	results := make(map[int64][]*GroupQualityResult, len(groupIDs))
	for _, id := range groupIDs {
		results[id] = make([]*GroupQualityResult, 0, 5)
	}
	if c == nil || c.db == nil || len(groupIDs) == 0 {
		return results, nil
	}
	rows, err := c.db.QueryContext(ctx, `
        SELECT s.group_id, h.result_id::text, h.status, h.reason, h.model_id, h.checked_at,
            COALESCE(octet_length(h.artwork_html) > 0, FALSE), COALESCE(octet_length(h.raw_output) > 0, FALSE), h.score
        FROM group_quality_check_settings s
        JOIN LATERAL (
            SELECT l.result_id, l.status, l.reason, l.model_id, l.checked_at, l.artwork_html, l.raw_output, l.score
            FROM group_quality_check_history l
            WHERE l.group_id = s.group_id
              AND EXISTS (SELECT 1 FROM accounts a JOIN account_groups ag ON ag.account_id = a.id
                  WHERE a.id = l.account_id AND a.deleted_at IS NULL AND ag.group_id = s.group_id)
            ORDER BY l.checked_at DESC, l.result_id DESC LIMIT 5
        ) h ON TRUE
        WHERE s.enabled AND s.group_id = ANY($1)
        ORDER BY s.group_id, h.checked_at DESC, h.result_id DESC`, pq.Array(groupIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var groupID int64
		var result GroupQualityResult
		var rawReason string
		var score sql.NullFloat64
		var hasRawOutput bool
		if err := rows.Scan(&groupID, &result.ResultID, &result.Status, &rawReason, &result.ModelID, &result.CheckedAt, &result.HasArtwork, &hasRawOutput, &score); err != nil {
			return nil, err
		}
		result.HasRawOutput = hasRawOutput
		result.Reason, result.Diagnostics = decodeQualityReason(rawReason)
		result.Score = qualityScorePointer(score)
		applyQualityStatusVerdict(&result)
		results[groupID] = append(results[groupID], &result)
	}
	return results, rows.Err()
}
