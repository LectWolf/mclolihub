import { describe, expect, it } from 'vitest'
import {
  alignTimelineEndMs,
  buildV3TimelineSlots,
  mixAvailabilityRate,
  resolveV3CardStatus,
  resolveV3CardTtftMs,
  statusFromAvailabilityRate,
  trafficMonitorStatus,
} from '../v3CardPresentation'

describe('v3CardPresentation', () => {
  it('maps traffic health to the card badge and ignores probe when traffic scored', () => {
    expect(trafficMonitorStatus('healthy')).toBe('operational')
    expect(trafficMonitorStatus('warning')).toBe('degraded')
    expect(trafficMonitorStatus('critical')).toBe('failed')
    expect(trafficMonitorStatus('unknown')).toBeNull()
    expect(resolveV3CardStatus({
      trafficOverall: 'critical',
      probeEnabled: true,
      probeStatus: 'healthy',
    })).toEqual({ status: 'failed', source: 'traffic' })
  })

  it('falls back to the 10-minute group probe when traffic samples are insufficient', () => {
    expect(resolveV3CardStatus({
      trafficOverall: 'unknown',
      probeEnabled: true,
      probeStatus: 'healthy',
    })).toEqual({ status: 'operational', source: 'probe' })
    expect(resolveV3CardStatus({
      trafficOverall: 'unknown',
      probeEnabled: true,
      probeStatus: 'unavailable',
    })).toEqual({ status: 'failed', source: 'probe' })
    expect(resolveV3CardStatus({
      trafficOverall: 'unknown',
      probeEnabled: true,
      probeStatus: 'balance_insufficient',
    })).toEqual({ status: 'balance_insufficient', source: 'probe' })
    expect(resolveV3CardStatus({
      trafficOverall: 'unknown',
      probeEnabled: false,
      probeStatus: 'healthy',
    })).toEqual({ status: null, source: 'none' })
  })

  it('uses probe TTFT only when traffic has no first-token sample', () => {
    expect(resolveV3CardTtftMs({
      trafficTtftMs: 800,
      probeEnabled: true,
      probeTtftMs: 1200,
    })).toEqual({ ms: 800, source: 'traffic' })
    expect(resolveV3CardTtftMs({
      trafficTtftMs: null,
      probeEnabled: true,
      probeTtftMs: 1200,
    })).toEqual({ ms: 1200, source: 'probe' })
    expect(resolveV3CardTtftMs({
      trafficTtftMs: null,
      probeEnabled: true,
      probeTtftMs: 0,
    })).toEqual({ ms: null, source: 'none' })
  })

  it('ends the timeline at aggregated data_through instead of the in-progress wall-clock bucket', () => {
    const bucketMs = 5 * 60 * 1000
    const nowMs = Date.parse('2026-09-04T17:47:00Z')
    const dataThroughMs = Date.parse('2026-09-04T17:42:30Z')
    expect(alignTimelineEndMs(nowMs, bucketMs)).toBe(Date.parse('2026-09-04T17:45:00Z'))
    expect(alignTimelineEndMs(nowMs, bucketMs, dataThroughMs)).toBe(Date.parse('2026-09-04T17:40:00Z'))
  })

  it('paints each collection slot that already inherited a probe sample', () => {
    const nowMs = Date.parse('2026-09-04T17:42:00Z')
    const bucketMs = 5 * 60 * 1000
    const slots = buildV3TimelineSlots({
      nowMs,
      length: 4,
      bucketMs,
      probeBuckets: [
        { startMs: Date.parse('2026-09-04T17:30:00Z'), durationMs: bucketMs, success: 1, failure: 0, ttftMs: 800 },
        { startMs: Date.parse('2026-09-04T17:35:00Z'), durationMs: bucketMs, success: 1, failure: 0, ttftMs: 800 },
      ],
    })
    const filled = slots.filter(slot => slot.source === 'probe')
    expect(filled).toHaveLength(2)
    expect(filled.map(slot => new Date(slot.startMs).toISOString())).toEqual([
      '2026-09-04T17:30:00.000Z',
      '2026-09-04T17:35:00.000Z',
    ])
    expect(slots.map(slot => new Date(slot.startMs).toISOString()).at(-1)).toBe('2026-09-04T17:35:00.000Z')
  })

  it('uses probe only in collection slots that have no user traffic', () => {
    const nowMs = Date.parse('2026-09-04T17:42:00Z')
    const bucketMs = 5 * 60 * 1000
    const slots = buildV3TimelineSlots({
      nowMs,
      length: 4,
      bucketMs,
      trafficBuckets: [{
        startMs: Date.parse('2026-09-04T17:35:00Z'),
        requestCount: 3,
        errorRate: 0,
        cacheRate: 0.4,
        ttftP50Ms: 900,
        overall: 'healthy',
      }],
      probeBuckets: [{
        startMs: Date.parse('2026-09-04T17:30:00Z'),
        durationMs: bucketMs,
        success: 0,
        failure: 1,
        ttftMs: 0,
      }],
    })
    const byStart = Object.fromEntries(slots.map(slot => [new Date(slot.startMs).toISOString(), slot]))
    expect(byStart['2026-09-04T17:30:00.000Z'].source).toBe('probe')
    expect(byStart['2026-09-04T17:30:00.000Z'].availabilityRate).toBe(0)
    expect(byStart['2026-09-04T17:35:00.000Z'].source).toBe('traffic')
    expect(byStart['2026-09-04T17:35:00.000Z'].availabilityRate).toBe(1)
  })

  it('lets probe samples change displayed availability', () => {
    expect(mixAvailabilityRate({
      probeSuccess: 1,
      probeFailure: 0,
    })).toEqual({ rate: 1, source: 'probe' })
    expect(mixAvailabilityRate({
      probeSuccess: 0,
      probeFailure: 1,
    })).toEqual({ rate: 0, source: 'probe' })
    expect(mixAvailabilityRate({
      probeSuccess: 1,
      probeFailure: 2,
    })).toEqual({ rate: 1, source: 'probe' })
    expect(mixAvailabilityRate({
      hasTraffic: true,
      trafficErrorRate: 0,
      trafficRequestCount: 3,
      probeSuccess: 0,
      probeFailure: 1,
    })).toEqual({ rate: 0.75, source: 'mixed' })
    expect(mixAvailabilityRate({
      hasTraffic: true,
      trafficErrorRate: 0,
      trafficRequestCount: 0,
      probeSuccess: 0,
      probeFailure: 1,
    })).toEqual({ rate: 0.5, source: 'mixed' })
    expect(statusFromAvailabilityRate(0.75)).toBe('degraded')
    expect(statusFromAvailabilityRate(0)).toBe('failed')
    expect(statusFromAvailabilityRate(1)).toBe('operational')
  })

})
