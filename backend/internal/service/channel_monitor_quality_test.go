package service

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

type channelQualityRepoStub struct {
	channelMonitorV2RepoStub
	enabled      map[int64]bool
	results      map[int64]*GroupQualityResult
	ids          []int64
	previewReads int
	rawReads     int
}

func (r *channelQualityRepoStub) LoadGroupQualityLatest(_ context.Context, ids []int64) (map[int64]bool, map[int64]*GroupQualityResult, error) {
	r.ids = ids
	return r.enabled, r.results, nil
}
func (r *channelQualityRepoStub) LoadGroupQualityPreview(context.Context, int64, string) ([]byte, error) {
	r.previewReads++
	return []byte("fixture"), nil
}

func (r *channelQualityRepoStub) LoadGroupQualityRawOutput(context.Context, int64, string) (string, error) {
	r.rawReads++
	return "raw fixture", nil
}

func TestQualityMatrixAddsLatestActualResultWithoutImageBytes(t *testing.T) {
	id := int64(7)
	repo := &channelQualityRepoStub{channelMonitorV2RepoStub: channelMonitorV2RepoStub{config: ChannelMonitorV2Config{Enabled: true}, matrix: &ChannelMonitorV2Matrix{Items: []ChannelMonitorV2MatrixRow{{GroupID: &id}, {GroupID: &id}}}}, enabled: map[int64]bool{7: true}, results: map[int64]*GroupQualityResult{7: {Status: "failed", Reason: "Request failed", ResultID: "latest", HasPreview: false}}}
	matrix, err := NewChannelMonitorV2Service(repo).Matrix(context.Background(), ChannelMonitorV2Filter{}, ChannelMonitorV2GroupByPlatformGroup, true)
	require.NoError(t, err)
	require.Equal(t, []int64{7}, repo.ids)
	require.True(t, matrix.Items[0].QualityEnabled)
	require.Equal(t, "failed", matrix.Items[0].LatestQuality.Status)
	require.Equal(t, "latest", matrix.Items[1].LatestQuality.ResultID)
	require.Zero(t, repo.previewReads)
}

func TestQualityPreviewRespectsMonitorSelectedGroups(t *testing.T) {
	repo := &channelQualityRepoStub{channelMonitorV2RepoStub: channelMonitorV2RepoStub{config: ChannelMonitorV2Config{Enabled: true, GroupIDs: []int64{3}}}}
	_, err := NewChannelMonitorV2Service(repo).QualityPreview(context.Background(), 7, "latest")
	require.ErrorIs(t, err, sql.ErrNoRows)
	require.Zero(t, repo.previewReads)
	data, err := NewChannelMonitorV2Service(repo).QualityPreview(context.Background(), 3, "latest")
	require.NoError(t, err)
	require.Equal(t, []byte("fixture"), data)
	require.Equal(t, 1, repo.previewReads)
}

func TestQualityRawOutputRespectsMonitorSelectedGroups(t *testing.T) {
	repo := &channelQualityRepoStub{channelMonitorV2RepoStub: channelMonitorV2RepoStub{config: ChannelMonitorV2Config{Enabled: true, GroupIDs: []int64{3}}}}
	monitor := NewChannelMonitorV2Service(repo)
	_, err := monitor.QualityRawOutput(context.Background(), 7, "latest")
	require.ErrorIs(t, err, sql.ErrNoRows)
	require.Zero(t, repo.rawReads)
	output, err := monitor.QualityRawOutput(context.Background(), 3, "latest")
	require.NoError(t, err)
	require.Equal(t, "raw fixture", output)
	require.Equal(t, 1, repo.rawReads)
}
