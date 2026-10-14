package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV2CacheRateUsesTotalInput(t *testing.T) {
	acc := newMetricAccumulator()
	// Two different traffic weights: a 90% bucket and a 10% bucket.
	acc.addFact(channelMonitorV2Fact{Input: 1000, Output: 100, CacheRead: 900, CacheCreation: 50, Success: 1})
	acc.addFact(channelMonitorV2Fact{Input: 9000, Output: 400, CacheRead: 900, CacheCreation: 50, Success: 9})
	metric := acc.metric(90, true)
	require.Equal(t, int64(1800), metric.CacheRateNumerator)
	require.Equal(t, int64(10000), metric.CacheRateDenominator)
	require.InDelta(t, 0.18, metric.CacheRate, 1e-12)
	require.LessOrEqual(t, metric.CacheRate, 1.0)
	require.Equal(t, int64(10500), metric.TokenCount, "cache buckets are included in total input")
	require.Equal(t, int64(100), metric.CacheCreationTokens)
	require.Equal(t, int64(1800), metric.CacheReadTokens)
}

func TestChannelMonitorV2CacheRateWithoutInput(t *testing.T) {
	metric := newMetricAccumulator().metric(90, true)
	require.Zero(t, metric.CacheRateDenominator)
	require.Zero(t, metric.CacheRate)
}
