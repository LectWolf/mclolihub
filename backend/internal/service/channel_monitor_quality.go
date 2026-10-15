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

type channelMonitorQualityHistoryReader interface {
	LoadGroupQualityHistory(context.Context, []int64) (map[int64][]*GroupQualityResult, error)
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
	history := make(map[int64][]*GroupQualityResult)
	if historyLoader, ok := s.repo.(channelMonitorQualityHistoryReader); ok {
		history, err = historyLoader.LoadGroupQualityHistory(ctx, ids)
		if err != nil {
			return err
		}
	}
	for i := range matrix.Items {
		matrix.Items[i].QualityHistory = make([]*GroupQualityResult, 0, 5)
		id := matrix.Items[i].GroupID
		if id == nil {
			continue
		}
		matrix.Items[i].QualityEnabled = enabled[*id]
		if enabled[*id] {
			matrix.Items[i].LatestQuality = results[*id]
			if len(history[*id]) > 0 {
				matrix.Items[i].QualityHistory = history[*id]
			} else if results[*id] != nil {
				matrix.Items[i].QualityHistory = append(matrix.Items[i].QualityHistory, results[*id])
			}
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

type channelMonitorQualityRawOutputReader interface {
	LoadGroupQualityRawOutput(context.Context, int64, string) (string, error)
}

func (s *ChannelMonitorV2Service) QualityRawOutput(ctx context.Context, groupID int64, resultID string) (string, error) {
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
	loader, ok := s.repo.(channelMonitorQualityRawOutputReader)
	if !ok {
		return "", sql.ErrNoRows
	}
	return loader.LoadGroupQualityRawOutput(ctx, groupID, resultID)
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

func (s *ChannelMonitorV2Service) QualityHistory(ctx context.Context, groupID int64) ([]*GroupQualityResult, error) {
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
	loader, ok := s.repo.(channelMonitorQualityHistoryReader)
	if !ok {
		return nil, sql.ErrNoRows
	}
	history, err := loader.LoadGroupQualityHistory(ctx, []int64{groupID})
	if err != nil {
		return nil, err
	}
	items := history[groupID]
	if items == nil {
		items = make([]*GroupQualityResult, 0)
	}
	return items, nil
}
