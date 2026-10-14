package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *channelMonitorV2Repository) LoadGroupQualityLatest(ctx context.Context, groupIDs []int64) (map[int64]bool, map[int64]*service.GroupQualityResult, error) {
	return service.NewGroupQualityReader(r.db).Latest(ctx, groupIDs)
}

func (r *channelMonitorV2Repository) LoadGroupQualityPreview(ctx context.Context, groupID int64, resultID string) ([]byte, error) {
	return service.NewGroupQualityReader(r.db).Preview(ctx, groupID, resultID)
}

func (r *channelMonitorV2Repository) LoadGroupQualityArtwork(ctx context.Context, groupID int64, resultID string) (string, error) {
	return service.NewGroupQualityReader(r.db).Artwork(ctx, groupID, resultID)
}
