package service

import (
	"context"
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

func TestCompletedMonitorWindowStartLeadsParseFilterByTrailingBuckets(t *testing.T) {
	filter := ChannelMonitorV2Filter{
		Start:  time.Date(2026, 9, 4, 16, 20, 0, 0, time.UTC),
		End:    time.Date(2026, 9, 4, 17, 50, 0, 0, time.UTC),
		Bucket: 5 * time.Minute,
	}
	// V3 counts 18 completed 5-minute bars back from floor(data_through).
	// ParseFilter ends at the next wall-clock bucket (17:50), so 17:42:30
	// leaves the oldest two V3 slots before filter.Start.
	got := completedMonitorWindowStart(filter, time.Date(2026, 9, 4, 17, 42, 30, 0, time.UTC))
	require.Equal(t, time.Date(2026, 9, 4, 16, 10, 0, 0, time.UTC), got)
	require.Equal(t, time.Date(2026, 9, 4, 16, 15, 0, 0, time.UTC), completedMonitorWindowStart(filter, time.Date(2026, 9, 4, 17, 47, 0, 0, time.UTC)))
}

func TestAttachProbeBucketsFillsCompletedWindowLead(t *testing.T) {
	gid := int64(4)
	filter := ChannelMonitorV2Filter{
		Start:  time.Date(2026, 9, 4, 16, 20, 0, 0, time.UTC),
		End:    time.Date(2026, 9, 4, 17, 50, 0, 0, time.UTC),
		Bucket: 5 * time.Minute,
	}
	loader := &probeTrendStub{events: map[int64][]ChannelMonitorV2ProbeEvent{
		gid: {{ObservedAt: time.Date(2026, 9, 4, 16, 11, 0, 0, time.UTC), Success: true, TTFTMs: 400}},
	}}
	svc := &ChannelMonitorV2Service{repo: loader}
	matrix := &ChannelMonitorV2Matrix{
		Coverage: ChannelMonitorV2Coverage{DataThrough: time.Date(2026, 9, 4, 17, 42, 30, 0, time.UTC)},
		Items:    []ChannelMonitorV2MatrixRow{{GroupID: &gid}},
	}

	svc.attachProbeBuckets(context.Background(), matrix, filter)

	got := probeBucketStarts(matrix.Items[0].ProbeBuckets)
	require.GreaterOrEqual(t, len(got), 3)
	require.Equal(t, []time.Time{
		time.Date(2026, 9, 4, 16, 10, 0, 0, time.UTC),
		time.Date(2026, 9, 4, 16, 15, 0, 0, time.UTC),
		time.Date(2026, 9, 4, 16, 20, 0, 0, time.UTC),
	}, got[:3])
}

type probeTrendStub struct {
	channelMonitorV2RepoStub
	events map[int64][]ChannelMonitorV2ProbeEvent
}

func (s *probeTrendStub) LoadGroupProbeEvents(_ context.Context, _ []int64, _, _ time.Time) (map[int64][]ChannelMonitorV2ProbeEvent, error) {
	return s.events, nil
}

func (s *probeTrendStub) LoadGroupProbeIntervals(_ context.Context, _ []int64) (map[int64]time.Duration, error) {
	return map[int64]time.Duration{}, nil
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
