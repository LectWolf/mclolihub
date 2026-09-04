import { describe, expect, it } from 'vitest'
import {
  buildV3TimelineSlots,
  resolveV3CardStatus,
  resolveV3CardTtftMs,
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

  it('lets a 10-minute probe occupy two aligned 5-minute collection slots', () => {
    const nowMs = Date.parse('2026-09-04T17:42:00Z')
    const bucketMs = 5 * 60 * 1000
    const probeStart = Date.parse('2026-09-04T17:30:00Z')
    const slots = buildV3TimelineSlots({
      nowMs,
      length: 4,
      bucketMs,
      probeBuckets: [{
        startMs: probeStart,
        durationMs: 10 * 60 * 1000,
        success: 1,
        failure: 0,
        ttftMs: 800,
      }],
    })
    const filled = slots.filter(slot => slot.source === 'probe')
    expect(filled).toHaveLength(2)
    expect(filled.map(slot => new Date(slot.startMs).toISOString())).toEqual([
      '2026-09-04T17:30:00.000Z',
      '2026-09-04T17:35:00.000Z',
    ])
  })

  it('keeps real traffic in a 5-minute slot instead of the overlapping probe', () => {
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
        durationMs: 10 * 60 * 1000,
        success: 1,
        failure: 0,
        ttftMs: 800,
      }],
    })
    const byStart = Object.fromEntries(slots.map(slot => [new Date(slot.startMs).toISOString(), slot.source]))
    expect(byStart['2026-09-04T17:30:00.000Z']).toBe('probe')
    expect(byStart['2026-09-04T17:35:00.000Z']).toBe('traffic')
  })
})
