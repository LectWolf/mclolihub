package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProjectProbeEventsOntoBucketsCarriesForwardWithinInterval(t *testing.T) {
	start := time.Date(2026, 9, 4, 16, 10, 0, 0, time.UTC)
	end := time.Date(2026, 9, 4, 16, 30, 0, 0, time.UTC)
	bucket := 5 * time.Minute
	interval := 10 * time.Minute
	probeAt := time.Date(2026, 9, 4, 16, 11, 0, 0, time.UTC)

	got := projectProbeEventsOntoBuckets([]ChannelMonitorV2ProbeEvent{{
		ObservedAt: probeAt,
		Success:    true,
		TTFTMs:     800,
	}}, interval, start, end, bucket)

	require.Equal(t, []time.Time{
		time.Date(2026, 9, 4, 16, 10, 0, 0, time.UTC),
		time.Date(2026, 9, 4, 16, 15, 0, 0, time.UTC),
		time.Date(2026, 9, 4, 16, 20, 0, 0, time.UTC),
		time.Date(2026, 9, 4, 16, 25, 0, 0, time.UTC),
	}, probeBucketStarts(got))
	require.Equal(t, 1, got[0].Success)
	require.Equal(t, 800, got[0].TTFTMs)
}

func TestProjectProbeEventsOntoBucketsUsesLookbackProbeForFirstSlots(t *testing.T) {
	start := time.Date(2026, 9, 4, 16, 10, 0, 0, time.UTC)
	end := time.Date(2026, 9, 4, 16, 20, 0, 0, time.UTC)
	// 94 minutes before a 90-minute window ending 17:44 is 16:10-4min = 16:06.
	probeAt := time.Date(2026, 9, 4, 16, 6, 0, 0, time.UTC)

	got := projectProbeEventsOntoBuckets([]ChannelMonitorV2ProbeEvent{{
		ObservedAt: probeAt,
		Success:    true,
		TTFTMs:     400,
	}}, 10*time.Minute, start, end, 5*time.Minute)

	require.Equal(t, []time.Time{
		time.Date(2026, 9, 4, 16, 10, 0, 0, time.UTC),
		time.Date(2026, 9, 4, 16, 15, 0, 0, time.UTC),
	}, probeBucketStarts(got), "85-90m slot inherits a probe still inside the 10-minute interval")
}

func TestProjectProbeEventsOntoBucketsStopsAfterInterval(t *testing.T) {
	start := time.Date(2026, 9, 4, 16, 10, 0, 0, time.UTC)
	end := time.Date(2026, 9, 4, 16, 30, 0, 0, time.UTC)
	probeAt := time.Date(2026, 9, 4, 16, 10, 0, 0, time.UTC)

	got := projectProbeEventsOntoBuckets([]ChannelMonitorV2ProbeEvent{{
		ObservedAt: probeAt,
		Success:    false,
	}}, 10*time.Minute, start, end, 5*time.Minute)

	require.Equal(t, []time.Time{
		time.Date(2026, 9, 4, 16, 10, 0, 0, time.UTC),
		time.Date(2026, 9, 4, 16, 15, 0, 0, time.UTC),
		time.Date(2026, 9, 4, 16, 20, 0, 0, time.UTC),
	}, probeBucketStarts(got), "10-minute probes stay visible for 15 minutes")
	require.Equal(t, 1, got[0].Failure)
}

func probeBucketStarts(buckets []ChannelMonitorV2ProbeBucket) []time.Time {
	out := make([]time.Time, 0, len(buckets))
	for _, bucket := range buckets {
		out = append(out, bucket.BucketStart.UTC())
	}
	return out
}
