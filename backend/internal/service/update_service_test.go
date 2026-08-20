//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type updateServiceCacheStub struct {
	data string
}

func (s *updateServiceCacheStub) GetUpdateInfo(context.Context) (string, error) {
	if s.data == "" {
		return "", errors.New("cache miss")
	}
	return s.data, nil
}

func (s *updateServiceCacheStub) SetUpdateInfo(_ context.Context, data string, _ time.Duration) error {
	s.data = data
	return nil
}

type updateServiceGitHubClientStub struct {
	release        *GitHubRelease
	recentReleases []*GitHubRelease
	recentErr      error
	latestRepos    []string
	recentRepos    []string
}

func (s *updateServiceGitHubClientStub) FetchLatestRelease(_ context.Context, repo string) (*GitHubRelease, error) {
	s.latestRepos = append(s.latestRepos, repo)
	return s.release, nil
}

func (s *updateServiceGitHubClientStub) FetchRecentReleases(_ context.Context, repo string, _ int) ([]*GitHubRelease, error) {
	s.recentRepos = append(s.recentRepos, repo)
	return s.recentReleases, s.recentErr
}

func (s *updateServiceGitHubClientStub) DownloadFile(context.Context, string, string, int64) error {
	panic("DownloadFile should not be called when no update is available")
}

func (s *updateServiceGitHubClientStub) FetchChecksumFile(context.Context, string) ([]byte, error) {
	panic("FetchChecksumFile should not be called when no update is available")
}

func TestUpdateServicePerformUpdateNoUpdateReturnsSentinel(t *testing.T) {
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{
			release: &GitHubRelease{
				TagName: "v0.1.132",
				Name:    "v0.1.132",
			},
			recentReleases: []*GitHubRelease{{
				TagName: "custom-v0.1.132.1",
				Name:    "custom-v0.1.132.1",
			}},
		},
		"0.1.132.1",
		"0.1.132",
		"release",
	)

	err := svc.PerformUpdate(context.Background())

	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNoUpdateAvailable))
	require.ErrorIs(t, err, ErrNoUpdateAvailable)
}

func newRollbackTestService(current string, releases []*GitHubRelease) *UpdateService {
	return NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentReleases: releases},
		current,
		"0.1.179",
		"release",
	)
}

func TestUpdateServiceListRollbackVersionsFiltersAndCaps(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "custom-v0.1.179.8", PublishedAt: "2026-07-09T00:00:00Z"},                   // newer than current: excluded
		{TagName: "custom-v0.1.179.7", PublishedAt: "2026-07-08T00:00:00Z"},                   // current: excluded
		{TagName: "custom-v0.1.179.6", PublishedAt: "2026-07-07T12:00:00Z", Prerelease: true}, // prerelease: excluded
		{TagName: "custom-v0.1.179.6", PublishedAt: "2026-07-07T00:00:00Z"},
		{TagName: "custom-v0.1.179.5", PublishedAt: "2026-07-06T00:00:00Z", Draft: true}, // draft: excluded
		{TagName: "custom-v0.1.179.4", PublishedAt: "2026-07-05T00:00:00Z"},
		{TagName: "custom-v0.1.179.4", PublishedAt: "2026-07-05T00:00:00Z"}, // duplicate: excluded
		{TagName: "custom-v0.1.179.3", PublishedAt: "2026-07-04T00:00:00Z"},
		{TagName: "custom-v0.1.179.2", PublishedAt: "2026-07-03T00:00:00Z"}, // beyond cap of 3: excluded
	}
	svc := newRollbackTestService("0.1.179.7", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.179.6", versions[0].Version)
	require.Equal(t, "0.1.179.4", versions[1].Version)
	require.Equal(t, "0.1.179.3", versions[2].Version)
}

func TestUpdateServiceListRollbackVersionsSortsUnorderedInput(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "custom-v0.1.179.4"},
		{TagName: "custom-v0.1.179.6"},
		{TagName: "custom-v0.1.179.5"},
	}
	svc := newRollbackTestService("0.1.179.7", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.179.6", versions[0].Version)
	require.Equal(t, "0.1.179.5", versions[1].Version)
	require.Equal(t, "0.1.179.4", versions[2].Version)
}

func TestUpdateServiceListRollbackVersionsEmptyWhenNoneOlder(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "custom-v0.1.179.7"},
		{TagName: "custom-v0.1.179.8"},
	}
	svc := newRollbackTestService("0.1.179.7", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Empty(t, versions)
}

func TestUpdateServiceListRollbackVersionsPropagatesFetchError(t *testing.T) {
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentErr: errors.New("github unavailable")},
		"0.1.179.7",
		"0.1.179",
		"release",
	)

	_, err := svc.ListRollbackVersions(context.Background())

	require.Error(t, err)
	require.Contains(t, err.Error(), "github unavailable")
}

func TestUpdateServiceRollbackToVersionRejectsDisallowedTargets(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "custom-v0.1.179.8"},
		{TagName: "custom-v0.1.179.7"},
		{TagName: "custom-v0.1.179.6"},
		{TagName: "custom-v0.1.179.5"},
		{TagName: "custom-v0.1.179.4"},
		{TagName: "custom-v0.1.179.3"},
		{TagName: "custom-v0.1.179.2"},
	}
	svc := newRollbackTestService("0.1.179.7", releases)

	for _, target := range []string{
		"",                  // empty
		"0.1.179.7",         // current version
		"custom-v0.1.179.7", // current version with prefix
		"0.1.179.8",         // newer than current
		"0.1.179.2",         // older than the 3 most recent
		"9.9.9",             // nonexistent
	} {
		err := svc.RollbackToVersion(context.Background(), target)
		require.ErrorIs(t, err, ErrRollbackVersionNotAllowed, "target %q should be rejected", target)
	}
}

func TestUpdateServiceRollbackToVersionAcceptsVPrefix(t *testing.T) {
	// No platform asset in the release: the target passes the allowlist check
	// and fails later at asset lookup, proving the version itself was accepted.
	releases := []*GitHubRelease{
		{TagName: "custom-v0.1.179.7"},
		{TagName: "custom-v0.1.179.6"},
	}
	svc := newRollbackTestService("0.1.179.7", releases)

	err := svc.RollbackToVersion(context.Background(), "custom-v0.1.179.6")

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrRollbackVersionNotAllowed)
	require.Contains(t, err.Error(), "no compatible release found")
}

func TestCompareCustomVersionsUsesFourthSegment(t *testing.T) {
	require.Less(t, compareCustomVersions("0.1.179.1", "0.1.179.2"), 0)
	require.Less(t, compareCustomVersions("0.1.179.9", "0.1.180.1"), 0)
	require.Greater(t, compareCustomVersions("0.1.180.1", "0.1.179.99"), 0)
	require.Zero(t, compareCustomVersions("0.1.179", "0.1.179.0"))
}

func TestUpdateServiceCheckUpdateReturnsIndependentChannels(t *testing.T) {
	client := &updateServiceGitHubClientStub{
		release: &GitHubRelease{TagName: "v0.1.180", Name: "Sub2API 0.1.180"},
		recentReleases: []*GitHubRelease{
			{TagName: "v0.1.999", Name: "ignored upstream-style fork release"},
			{TagName: "custom-v0.1.179.2", Name: "Custom 0.1.179.2"},
		},
	}
	svc := NewUpdateService(&updateServiceCacheStub{}, client, "0.1.179.1", "0.1.179", "release")

	info, err := svc.CheckUpdate(context.Background(), true)

	require.NoError(t, err)
	require.Equal(t, "0.1.179.1", info.Custom.CurrentVersion)
	require.Equal(t, "0.1.179.2", info.Custom.LatestVersion)
	require.True(t, info.Custom.HasUpdate)
	require.Equal(t, "0.1.179", info.Upstream.CurrentVersion)
	require.Equal(t, "0.1.180", info.Upstream.LatestVersion)
	require.True(t, info.Upstream.HasUpdate)
	require.Equal(t, info.Custom.CurrentVersion, info.CurrentVersion)
	require.Equal(t, []string{customGitHubRepo}, client.recentRepos)
	require.Equal(t, []string{upstreamGitHubRepo}, client.latestRepos)
}
