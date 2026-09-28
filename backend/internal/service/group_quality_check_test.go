package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQualityWindowDueRequiresBothGates(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	require.False(t, qualityWindowDue(now, time.Time{}, 5))
	require.False(t, qualityWindowDue(now, now.Add(-29*time.Minute), 5))
	require.False(t, qualityWindowDue(now, now.Add(-30*time.Minute), 4.99))
	require.True(t, qualityWindowDue(now, now.Add(-30*time.Minute), 5))
	require.True(t, qualityWindowDue(now, now.Add(-time.Hour), 9))
}

func TestQualityPauseOnlyAfterConsecutiveDegraded(t *testing.T) {
	require.False(t, qualityShouldPause("", "degraded"))
	require.False(t, qualityShouldPause("success", "degraded"))
	require.False(t, qualityShouldPause("degraded", "unknown"))
	require.False(t, qualityShouldPause("degraded", "failed"))
	require.True(t, qualityShouldPause("degraded", "degraded"))
	require.True(t, qualityShouldResume(true, "success"))
	require.False(t, qualityShouldResume(true, "degraded"))
	require.False(t, qualityShouldResume(true, "unknown"))
	require.False(t, qualityShouldResume(false, "success"))
}

func TestQualityAggregateStatus(t *testing.T) {
	require.Equal(t, "unknown", qualityAggregateStatus(false, 4, 4))
	require.Equal(t, "unknown", qualityAggregateStatus(true, 0, 0))
	require.Equal(t, "healthy", qualityAggregateStatus(true, 4, 1))
	require.Equal(t, "suspect", qualityAggregateStatus(true, 4, 2))
}

func TestAssessScheduledTestQualitySkipsCustomPrompt(t *testing.T) {
	status, reason := assessScheduledTestQuality("<html><svg></svg></html>", "say hello")
	require.Equal(t, "unknown", status)
	require.Contains(t, reason, "custom prompt")

	status, reason = assessScheduledTestQuality("   ", DefaultScheduledTestPrompt)
	require.Equal(t, "degraded", status)
	require.Contains(t, reason, "empty")
}

func TestAccountAcceptsQualityPrompt(t *testing.T) {
	require.True(t, accountAcceptsQualityPrompt(&Account{Platform: PlatformOpenAI}))
	require.True(t, accountAcceptsQualityPrompt(&Account{Platform: PlatformGemini}))
	require.False(t, accountAcceptsQualityPrompt(&Account{Platform: PlatformAnthropic}))
	require.False(t, accountAcceptsQualityPrompt(nil))
}
