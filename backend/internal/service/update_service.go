package service

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrNoUpdateAvailable         = infraerrors.Conflict("ALREADY_UP_TO_DATE", "no update available; current version is latest")
	ErrRollbackVersionNotAllowed = infraerrors.BadRequest("ROLLBACK_VERSION_NOT_ALLOWED", "version is not in the allowed rollback list")
)

const (
	updateCacheKey     = "update_check_cache"
	updateCacheTTL     = 1200 // 20 minutes
	upstreamGitHubRepo = "Wei-Shaw/sub2api"
	customGitHubRepo   = "LectWolf/mclolihub"
	customTagPrefix    = "custom-v"

	// Security: allowed download domains for updates
	allowedDownloadHost = "github.com"
	allowedAssetHost    = "objects.githubusercontent.com"

	// Security: max download size (500MB)
	maxDownloadSize = 500 * 1024 * 1024

	// Rollback: expose at most the 3 most recent versions older than current
	maxRollbackVersions = 3
	// Fetch enough entries to find custom tags even if this fork also carries upstream releases.
	customReleaseFetchPageSize = 100
)

// UpdateCache defines cache operations for update service
type UpdateCache interface {
	GetUpdateInfo(ctx context.Context) (string, error)
	SetUpdateInfo(ctx context.Context, data string, ttl time.Duration) error
}

// GitHubReleaseClient 获取 GitHub release 信息的接口
type GitHubReleaseClient interface {
	FetchLatestRelease(ctx context.Context, repo string) (*GitHubRelease, error)
	FetchRecentReleases(ctx context.Context, repo string, perPage int) ([]*GitHubRelease, error)
	DownloadFile(ctx context.Context, url, dest string, maxSize int64) error
	FetchChecksumFile(ctx context.Context, url string) ([]byte, error)
}

// UpdateService handles software updates
type UpdateService struct {
	cache                  UpdateCache
	githubClient           GitHubReleaseClient
	currentVersion         string
	currentUpstreamVersion string
	buildType              string // "source" for manual builds, "release" for CI builds
}

// NewUpdateService creates a new UpdateService
func NewUpdateService(cache UpdateCache, githubClient GitHubReleaseClient, version, upstreamVersion, buildType string) *UpdateService {
	return &UpdateService{
		cache:                  cache,
		githubClient:           githubClient,
		currentVersion:         version,
		currentUpstreamVersion: upstreamVersion,
		buildType:              buildType,
	}
}

// UpdateChannelInfo describes one independent update stream.
type UpdateChannelInfo struct {
	CurrentVersion string       `json:"current_version"`
	LatestVersion  string       `json:"latest_version"`
	HasUpdate      bool         `json:"has_update"`
	ReleaseInfo    *ReleaseInfo `json:"release_info,omitempty"`
	Warning        string       `json:"warning,omitempty"`
}

// UpdateInfo contains update information
type UpdateInfo struct {
	CurrentVersion string            `json:"current_version"`
	LatestVersion  string            `json:"latest_version"`
	HasUpdate      bool              `json:"has_update"`
	ReleaseInfo    *ReleaseInfo      `json:"release_info,omitempty"`
	Cached         bool              `json:"cached"`
	Warning        string            `json:"warning,omitempty"`
	BuildType      string            `json:"build_type"` // "source" or "release"
	Upstream       UpdateChannelInfo `json:"upstream"`
	Custom         UpdateChannelInfo `json:"custom"`
}

// ReleaseInfo contains GitHub release details
type ReleaseInfo struct {
	Name        string  `json:"name"`
	Body        string  `json:"body"`
	PublishedAt string  `json:"published_at"`
	HTMLURL     string  `json:"html_url"`
	Assets      []Asset `json:"assets,omitempty"`
}

// Asset represents a release asset
type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
	Size        int64  `json:"size"`
}

// GitHubRelease represents GitHub API response
type GitHubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	PublishedAt string        `json:"published_at"`
	HTMLURL     string        `json:"html_url"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	Assets      []GitHubAsset `json:"assets"`
}

// RollbackVersion describes a release version the system can roll back to
type RollbackVersion struct {
	Version     string `json:"version"` // without "v" prefix, e.g. "0.1.146"
	PublishedAt string `json:"published_at"`
	HTMLURL     string `json:"html_url"`
}

type GitHubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// CheckUpdate checks for available updates
func (s *UpdateService) CheckUpdate(ctx context.Context, force bool) (*UpdateInfo, error) {
	// Try cache first
	if !force {
		if cached, err := s.getFromCache(ctx); err == nil && cached != nil {
			return cached, nil
		}
	}

	info := s.emptyUpdateInfo()
	cached, _ := s.getFromCache(ctx)

	custom, customErr := s.fetchLatestCustomRelease(ctx)
	if customErr == nil {
		info.Custom = *custom
	} else if cached != nil {
		info.Custom = cached.Custom
		info.Custom.Warning = customErr.Error()
	} else {
		info.Custom.Warning = customErr.Error()
	}

	upstream, upstreamErr := s.fetchLatestUpstreamRelease(ctx)
	if upstreamErr == nil {
		info.Upstream = *upstream
	} else if cached != nil {
		info.Upstream = cached.Upstream
		info.Upstream.Warning = upstreamErr.Error()
	} else {
		info.Upstream.Warning = upstreamErr.Error()
	}

	info.syncCustomCompatibilityFields()
	warnings := make([]string, 0, 2)
	if customErr != nil {
		warnings = append(warnings, "custom: "+customErr.Error())
	}
	if upstreamErr != nil {
		warnings = append(warnings, "upstream: "+upstreamErr.Error())
	}
	info.Warning = strings.Join(warnings, "; ")

	// Cache result
	s.saveToCache(ctx, info)
	return info, nil
}

// PerformUpdate downloads and applies the update
// Uses atomic file replacement pattern for safe in-place updates
func (s *UpdateService) PerformUpdate(ctx context.Context) error {
	info, err := s.CheckUpdate(ctx, true)
	if err != nil {
		return err
	}

	if !info.HasUpdate {
		return ErrNoUpdateAvailable
	}

	if info.Custom.ReleaseInfo == nil {
		return fmt.Errorf("custom release information is unavailable")
	}
	return s.applyReleaseAssets(ctx, info.Custom.ReleaseInfo.Assets)
}

// applyReleaseAssets downloads the platform archive from the given release assets,
// verifies its checksum, and atomically swaps the running binary.
// Shared by PerformUpdate (latest) and RollbackToVersion (specific older version).
func (s *UpdateService) applyReleaseAssets(ctx context.Context, releaseAssets []Asset) error {
	// Find matching archive and checksum for current platform
	archiveName := s.getArchiveName()
	var downloadURL string
	var checksumURL string

	for _, asset := range releaseAssets {
		if strings.Contains(asset.Name, archiveName) && !strings.HasSuffix(asset.Name, ".txt") {
			downloadURL = asset.DownloadURL
		}
		if asset.Name == "checksums.txt" {
			checksumURL = asset.DownloadURL
		}
	}

	if downloadURL == "" {
		return fmt.Errorf("no compatible release found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	// SECURITY: Validate download URL is from trusted domain
	if err := validateDownloadURL(downloadURL); err != nil {
		return fmt.Errorf("invalid download URL: %w", err)
	}
	if checksumURL != "" {
		if err := validateDownloadURL(checksumURL); err != nil {
			return fmt.Errorf("invalid checksum URL: %w", err)
		}
	}

	// Get current executable path
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	exeDir := filepath.Dir(exePath)

	// Create temp directory in the SAME directory as executable
	// This ensures os.Rename is atomic (same filesystem)
	tempDir, err := os.MkdirTemp(exeDir, ".sub2api-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Download archive
	archivePath := filepath.Join(tempDir, filepath.Base(downloadURL))
	if err := s.downloadFile(ctx, downloadURL, archivePath); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	// Verify checksum if available
	if checksumURL != "" {
		if err := s.verifyChecksum(ctx, archivePath, checksumURL); err != nil {
			return fmt.Errorf("checksum verification failed: %w", err)
		}
	}

	// Extract binary from archive
	newBinaryPath := filepath.Join(tempDir, "sub2api")
	if err := s.extractBinary(archivePath, newBinaryPath); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	// Set executable permission before replacement
	if err := os.Chmod(newBinaryPath, 0755); err != nil {
		return fmt.Errorf("chmod failed: %w", err)
	}

	// Atomic replacement using rename pattern:
	// 1. Rename current -> backup (atomic on Unix)
	// 2. Rename new -> current (atomic on Unix, same filesystem)
	// If step 2 fails, restore backup
	backupPath := exePath + ".backup"

	// Remove old backup if exists
	_ = os.Remove(backupPath)

	// Step 1: Move current binary to backup
	if err := os.Rename(exePath, backupPath); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	// Step 2: Move new binary to target location (atomic, same filesystem)
	if err := os.Rename(newBinaryPath, exePath); err != nil {
		// Restore backup on failure
		if restoreErr := os.Rename(backupPath, exePath); restoreErr != nil {
			return fmt.Errorf("replace failed and restore failed: %w (restore error: %v)", err, restoreErr)
		}
		return fmt.Errorf("replace failed (restored backup): %w", err)
	}

	// Success - backup file is kept for rollback capability
	// It will be cleaned up on next successful update
	return nil
}

// Rollback restores the previous version
func (s *UpdateService) Rollback() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	backupFile := exePath + ".backup"
	if _, err := os.Stat(backupFile); os.IsNotExist(err) {
		return fmt.Errorf("no backup found")
	}

	// Replace current with backup
	if err := os.Rename(backupFile, exePath); err != nil {
		return fmt.Errorf("rollback failed: %w", err)
	}

	return nil
}

// ListRollbackVersions returns up to maxRollbackVersions release versions that are
// strictly older than the current version (the current version itself is excluded),
// newest first. Draft and prerelease entries are skipped.
func (s *UpdateService) ListRollbackVersions(ctx context.Context) ([]RollbackVersion, error) {
	releases, err := s.fetchRollbackCandidates(ctx)
	if err != nil {
		return nil, err
	}

	versions := make([]RollbackVersion, 0, len(releases))
	for _, r := range releases {
		version, _ := parseCustomTag(r.TagName)
		versions = append(versions, RollbackVersion{
			Version:     version,
			PublishedAt: r.PublishedAt,
			HTMLURL:     r.HTMLURL,
		})
	}
	return versions, nil
}

// RollbackToVersion downloads and installs a specific older version.
// The target must be one of the versions returned by ListRollbackVersions;
// anything else (including the current version) is rejected.
func (s *UpdateService) RollbackToVersion(ctx context.Context, version string) error {
	target := normalizeCustomVersion(strings.TrimSpace(version))
	if target == "" {
		return ErrRollbackVersionNotAllowed
	}

	releases, err := s.fetchRollbackCandidates(ctx)
	if err != nil {
		return err
	}

	var match *GitHubRelease
	for _, r := range releases {
		candidateVersion, _ := parseCustomTag(r.TagName)
		if candidateVersion == target {
			match = r
			break
		}
	}
	if match == nil {
		return ErrRollbackVersionNotAllowed
	}

	assets := make([]Asset, len(match.Assets))
	for i, a := range match.Assets {
		assets[i] = Asset{
			Name:        a.Name,
			DownloadURL: a.BrowserDownloadURL,
			Size:        a.Size,
		}
	}

	return s.applyReleaseAssets(ctx, assets)
}

// fetchRollbackCandidates fetches recent releases and keeps the newest
// maxRollbackVersions entries strictly older than the current version.
func (s *UpdateService) fetchRollbackCandidates(ctx context.Context) ([]*GitHubRelease, error) {
	releases, err := s.githubClient.FetchRecentReleases(ctx, customGitHubRepo, customReleaseFetchPageSize)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(releases))
	candidates := make([]*GitHubRelease, 0, maxRollbackVersions)
	for _, r := range releases {
		if r == nil || r.Draft || r.Prerelease {
			continue
		}
		v, ok := parseCustomTag(r.TagName)
		if !ok || seen[v] {
			continue
		}
		// Only versions strictly older than current (also excludes current itself)
		if compareCustomVersions(v, s.currentVersion) >= 0 {
			continue
		}
		seen[v] = true
		candidates = append(candidates, r)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		left, _ := parseCustomTag(candidates[i].TagName)
		right, _ := parseCustomTag(candidates[j].TagName)
		return compareCustomVersions(
			left,
			right,
		) > 0
	})

	if len(candidates) > maxRollbackVersions {
		candidates = candidates[:maxRollbackVersions]
	}
	return candidates, nil
}

func (s *UpdateService) fetchLatestUpstreamRelease(ctx context.Context) (*UpdateChannelInfo, error) {
	release, err := s.githubClient.FetchLatestRelease(ctx, upstreamGitHubRepo)
	if err != nil {
		return nil, err
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	return s.buildChannelInfo(release, s.currentUpstreamVersion, latestVersion, compareUpstreamVersions), nil
}

func (s *UpdateService) fetchLatestCustomRelease(ctx context.Context) (*UpdateChannelInfo, error) {
	releases, err := s.githubClient.FetchRecentReleases(ctx, customGitHubRepo, customReleaseFetchPageSize)
	if err != nil {
		return nil, err
	}

	var latest *GitHubRelease
	latestVersion := ""
	for _, release := range releases {
		if release == nil || release.Draft || release.Prerelease {
			continue
		}
		version, ok := parseCustomTag(release.TagName)
		if !ok {
			continue
		}
		if latest == nil || compareCustomVersions(latestVersion, version) < 0 {
			latest = release
			latestVersion = version
		}
	}
	if latest == nil {
		return nil, fmt.Errorf("no custom release found in %s", customGitHubRepo)
	}

	return s.buildChannelInfo(latest, s.currentVersion, latestVersion, compareCustomVersions), nil
}

func (s *UpdateService) buildChannelInfo(
	release *GitHubRelease,
	currentVersion string,
	latestVersion string,
	compare func(string, string) int,
) *UpdateChannelInfo {

	assets := make([]Asset, len(release.Assets))
	for i, a := range release.Assets {
		assets[i] = Asset{
			Name:        a.Name,
			DownloadURL: a.BrowserDownloadURL,
			Size:        a.Size,
		}
	}

	return &UpdateChannelInfo{
		CurrentVersion: currentVersion,
		LatestVersion:  latestVersion,
		HasUpdate:      compare(currentVersion, latestVersion) < 0,
		ReleaseInfo: &ReleaseInfo{
			Name:        release.Name,
			Body:        release.Body,
			PublishedAt: release.PublishedAt,
			HTMLURL:     release.HTMLURL,
			Assets:      assets,
		},
	}
}

func (s *UpdateService) downloadFile(ctx context.Context, downloadURL, dest string) error {
	return s.githubClient.DownloadFile(ctx, downloadURL, dest, maxDownloadSize)
}

func (s *UpdateService) getArchiveName() string {
	osName := runtime.GOOS
	arch := runtime.GOARCH
	return fmt.Sprintf("%s_%s", osName, arch)
}

// validateDownloadURL checks if the URL is from an allowed domain
// SECURITY: This prevents SSRF and ensures downloads only come from trusted GitHub domains
func validateDownloadURL(rawURL string) error {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Must be HTTPS
	if parsedURL.Scheme != "https" {
		return fmt.Errorf("only HTTPS URLs are allowed")
	}

	// Check against allowed hosts
	host := parsedURL.Host
	// GitHub release URLs can be from github.com or objects.githubusercontent.com
	if host != allowedDownloadHost &&
		!strings.HasSuffix(host, "."+allowedDownloadHost) &&
		host != allowedAssetHost &&
		!strings.HasSuffix(host, "."+allowedAssetHost) {
		return fmt.Errorf("download from untrusted host: %s", host)
	}

	return nil
}

func (s *UpdateService) verifyChecksum(ctx context.Context, filePath, checksumURL string) error {
	// Download checksums file
	checksumData, err := s.githubClient.FetchChecksumFile(ctx, checksumURL)
	if err != nil {
		return fmt.Errorf("failed to download checksums: %w", err)
	}

	// Calculate file hash
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actualHash := hex.EncodeToString(h.Sum(nil))

	// Find expected hash in checksums file
	fileName := filepath.Base(filePath)
	scanner := bufio.NewScanner(strings.NewReader(string(checksumData)))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == fileName {
			if parts[0] == actualHash {
				return nil
			}
			return fmt.Errorf("checksum mismatch: expected %s, got %s", parts[0], actualHash)
		}
	}

	return fmt.Errorf("checksum not found for %s", fileName)
}

func (s *UpdateService) extractBinary(archivePath, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	var reader io.Reader = f

	// Handle gzip compression
	if strings.HasSuffix(archivePath, ".gz") || strings.HasSuffix(archivePath, ".tar.gz") || strings.HasSuffix(archivePath, ".tgz") {
		gzr, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer func() { _ = gzr.Close() }()
		reader = gzr
	}

	// Handle tar archive
	if strings.Contains(archivePath, ".tar") {
		tr := tar.NewReader(reader)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}

			// SECURITY: Prevent Zip Slip / Path Traversal attack
			// Only allow files with safe base names, no directory traversal
			baseName := filepath.Base(hdr.Name)

			// Check for path traversal attempts
			if strings.Contains(hdr.Name, "..") {
				return fmt.Errorf("path traversal attempt detected: %s", hdr.Name)
			}

			// Validate the entry is a regular file
			if hdr.Typeflag != tar.TypeReg {
				continue // Skip directories and special files
			}

			// Only extract the specific binary we need
			if baseName == "sub2api" || baseName == "sub2api.exe" {
				// Additional security: limit file size (max 500MB)
				const maxBinarySize = 500 * 1024 * 1024
				if hdr.Size > maxBinarySize {
					return fmt.Errorf("binary too large: %d bytes (max %d)", hdr.Size, maxBinarySize)
				}

				out, err := os.Create(destPath)
				if err != nil {
					return err
				}

				// Use LimitReader to prevent decompression bombs
				limited := io.LimitReader(tr, maxBinarySize)
				if _, err := io.Copy(out, limited); err != nil {
					_ = out.Close()
					return err
				}
				if err := out.Close(); err != nil {
					return err
				}
				return nil
			}
		}
		return fmt.Errorf("binary not found in archive")
	}

	// Direct copy for non-tar files (with size limit)
	const maxBinarySize = 500 * 1024 * 1024
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}

	limited := io.LimitReader(reader, maxBinarySize)
	if _, err := io.Copy(out, limited); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func (s *UpdateService) getFromCache(ctx context.Context) (*UpdateInfo, error) {
	data, err := s.cache.GetUpdateInfo(ctx)
	if err != nil {
		return nil, err
	}

	var cached struct {
		Latest      string            `json:"latest"`
		ReleaseInfo *ReleaseInfo      `json:"release_info"`
		Upstream    UpdateChannelInfo `json:"upstream"`
		Custom      UpdateChannelInfo `json:"custom"`
		Timestamp   int64             `json:"timestamp"`
	}
	if err := json.Unmarshal([]byte(data), &cached); err != nil {
		return nil, err
	}

	if time.Now().Unix()-cached.Timestamp > updateCacheTTL {
		return nil, fmt.Errorf("cache expired")
	}

	info := s.emptyUpdateInfo()
	info.Cached = true
	if cached.Custom.LatestVersion != "" {
		info.Custom = cached.Custom
		info.Custom.CurrentVersion = s.currentVersion
		info.Custom.HasUpdate = compareCustomVersions(s.currentVersion, info.Custom.LatestVersion) < 0
	} else if cached.Latest != "" {
		// Read cache entries written by versions before dual-channel updates.
		info.Custom.LatestVersion = cached.Latest
		info.Custom.ReleaseInfo = cached.ReleaseInfo
		info.Custom.HasUpdate = compareCustomVersions(s.currentVersion, cached.Latest) < 0
	}
	if cached.Upstream.LatestVersion != "" {
		info.Upstream = cached.Upstream
		info.Upstream.CurrentVersion = s.currentUpstreamVersion
		info.Upstream.HasUpdate = compareUpstreamVersions(s.currentUpstreamVersion, info.Upstream.LatestVersion) < 0
	}
	info.syncCustomCompatibilityFields()
	return info, nil
}

func (s *UpdateService) saveToCache(ctx context.Context, info *UpdateInfo) {
	cacheData := struct {
		Upstream  UpdateChannelInfo `json:"upstream"`
		Custom    UpdateChannelInfo `json:"custom"`
		Timestamp int64             `json:"timestamp"`
	}{
		Upstream:  info.Upstream,
		Custom:    info.Custom,
		Timestamp: time.Now().Unix(),
	}

	data, _ := json.Marshal(cacheData)
	_ = s.cache.SetUpdateInfo(ctx, string(data), time.Duration(updateCacheTTL)*time.Second)
}

func (s *UpdateService) emptyUpdateInfo() *UpdateInfo {
	info := &UpdateInfo{
		Cached:    false,
		BuildType: s.buildType,
		Upstream: UpdateChannelInfo{
			CurrentVersion: s.currentUpstreamVersion,
			LatestVersion:  s.currentUpstreamVersion,
		},
		Custom: UpdateChannelInfo{
			CurrentVersion: s.currentVersion,
			LatestVersion:  s.currentVersion,
		},
	}
	info.syncCustomCompatibilityFields()
	return info
}

func (info *UpdateInfo) syncCustomCompatibilityFields() {
	info.CurrentVersion = info.Custom.CurrentVersion
	info.LatestVersion = info.Custom.LatestVersion
	info.HasUpdate = info.Custom.HasUpdate
	info.ReleaseInfo = info.Custom.ReleaseInfo
}

func parseCustomTag(tag string) (string, bool) {
	if !strings.HasPrefix(tag, customTagPrefix) {
		return "", false
	}
	version := strings.TrimPrefix(tag, customTagPrefix)
	if strings.Count(version, ".") != 3 {
		return "", false
	}
	_, ok := parseNumericVersion(version, 4)
	return version, ok
}

func normalizeCustomVersion(version string) string {
	version = strings.TrimPrefix(version, customTagPrefix)
	return strings.TrimPrefix(version, "v")
}

func compareUpstreamVersions(current, latest string) int {
	return compareNumericVersions(current, latest, 3)
}

func compareCustomVersions(current, latest string) int {
	return compareNumericVersions(normalizeCustomVersion(current), normalizeCustomVersion(latest), 4)
}

func compareNumericVersions(current, latest string, segmentCount int) int {
	currentParts, currentOK := parseNumericVersion(current, segmentCount)
	latestParts, latestOK := parseNumericVersion(latest, segmentCount)
	if !currentOK || !latestOK {
		return 0
	}

	for i := 0; i < segmentCount; i++ {
		if currentParts[i] < latestParts[i] {
			return -1
		}
		if currentParts[i] > latestParts[i] {
			return 1
		}
	}
	return 0
}

func parseNumericVersion(v string, segmentCount int) ([]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	if len(parts) < 3 || len(parts) > segmentCount {
		return nil, false
	}
	result := make([]int, segmentCount)
	for i, part := range parts {
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 {
			return nil, false
		}
		result[i] = parsed
	}
	return result, true
}
