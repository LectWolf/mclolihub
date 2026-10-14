package service

import (
	"context"
	"database/sql"
	"time"
)

// NewGroupQualityReader shares the quality read contract with passive monitor repositories.
func NewGroupQualityReader(db *sql.DB) *GroupQualityChecker {
	return &GroupQualityChecker{db: db, now: time.Now}
}

type channelMonitorQualityReader interface {
	LoadGroupQualityLatest(context.Context, []int64) (map[int64]bool, map[int64]*GroupQualityResult, error)
	LoadGroupQualityPreview(context.Context, int64, string) ([]byte, error)
}

func (s *ChannelMonitorV2Service) attachQualityResults(ctx context.Context, matrix *ChannelMonitorV2Matrix) error {
	loader, ok := s.repo.(channelMonitorQualityReader)
	if !ok || matrix == nil {
		return nil
	}
	seen := make(map[int64]bool)
	var ids []int64
	for _, row := range matrix.Items {
		if row.GroupID != nil && *row.GroupID > 0 && !seen[*row.GroupID] {
			seen[*row.GroupID] = true
			ids = append(ids, *row.GroupID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	enabled, results, err := loader.LoadGroupQualityLatest(ctx, ids)
	if err != nil {
		return err
	}
	for i := range matrix.Items {
		id := matrix.Items[i].GroupID
		if id == nil {
			continue
		}
		matrix.Items[i].QualityEnabled = enabled[*id]
		if enabled[*id] {
			matrix.Items[i].LatestQuality = results[*id]
		}
	}
	return nil
}

func (s *ChannelMonitorV2Service) QualityPreview(ctx context.Context, groupID int64, resultID string) ([]byte, error) {
	cfg, err := s.getEnabledConfig(ctx)
	if err != nil {
		return nil, err
	}
	if len(cfg.GroupIDs) > 0 {
		allowed := false
		for _, id := range cfg.GroupIDs {
			if id == groupID {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, sql.ErrNoRows
		}
	}
	loader, ok := s.repo.(channelMonitorQualityReader)
	if !ok {
		return nil, sql.ErrNoRows
	}
	return loader.LoadGroupQualityPreview(ctx, groupID, resultID)
}

type channelMonitorQualityArtworkReader interface {
	LoadGroupQualityArtwork(context.Context, int64, string) (string, error)
}

func (s *ChannelMonitorV2Service) QualityArtwork(ctx context.Context, groupID int64, resultID string) (string, error) {
	cfg, err := s.getEnabledConfig(ctx)
	if err != nil {
		return "", err
	}
	if len(cfg.GroupIDs) > 0 {
		allowed := false
		for _, id := range cfg.GroupIDs {
			if id == groupID {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", sql.ErrNoRows
		}
	}
	loader, ok := s.repo.(channelMonitorQualityArtworkReader)
	if !ok {
		return "", sql.ErrNoRows
	}
	return loader.LoadGroupQualityArtwork(ctx, groupID, resultID)
}
