import { describe, expect, it } from 'vitest'

import {
  cacheHitRate,
  computeDelta,
  formatDeltaRatio,
  formatPercent,
  formatUptime,
  formatUsd,
  formatUsdAxis,
  isMeaningfulTimestamp,
  profitMargin,
  resolveAccountHealth
} from '../dashboardMetrics'

describe('computeDelta', () => {
  it('marks growth as positive when higher is better', () => {
    expect(computeDelta(125, 100)).toEqual({ ratio: 0.25, direction: 'up', tone: 'positive' })
    expect(computeDelta(80, 100)).toEqual({ ratio: -0.2, direction: 'down', tone: 'negative' })
  })

  it('inverts the tone when lower is better', () => {
    expect(computeDelta(120, 100, { higherIsBetter: false })?.tone).toBe('negative')
    expect(computeDelta(90, 100, { higherIsBetter: false })?.tone).toBe('positive')
  })

  it('treats tiny changes as flat', () => {
    const delta = computeDelta(100.01, 100)
    expect(delta?.direction).toBe('flat')
    expect(delta?.tone).toBe('neutral')
    expect(delta?.ratio).toBeCloseTo(0.0001)
  })

  it('has no ratio when there is no positive baseline', () => {
    expect(computeDelta(5, 0)).toEqual({ ratio: null, direction: 'up', tone: 'neutral' })
    expect(computeDelta(3, -2)).toEqual({ ratio: null, direction: 'up', tone: 'neutral' })
    expect(computeDelta(0, 0)).toBeNull()
  })

  it('tolerates missing values', () => {
    expect(computeDelta(undefined, undefined)).toBeNull()
    expect(computeDelta('12', null)).toEqual({ ratio: null, direction: 'up', tone: 'neutral' })
  })
})

describe('formatting helpers', () => {
  it('formats delta ratios without a sign', () => {
    expect(formatDeltaRatio(0.0345)).toBe('3.5%')
    expect(formatDeltaRatio(-0.25)).toBe('25%')
    expect(formatDeltaRatio(42)).toBe('999%+')
  })

  it('formats usd amounts including negatives', () => {
    expect(formatUsd(12.5)).toBe('$12.50')
    expect(formatUsd(-3.456)).toBe('-$3.46')
    expect(formatUsd(1530)).toBe('$1.53K')
    expect(formatUsd(0)).toBe('$0.0000')
  })

  it('keeps axis ticks short', () => {
    expect(formatUsdAxis(0)).toBe('$0')
    expect(formatUsdAxis(2.5)).toBe('$2.5')
    expect(formatUsdAxis(0.05)).toBe('$0.05')
    expect(formatUsdAxis(1500)).toBe('$1.5K')
    expect(formatUsdAxis(-4)).toBe('-$4')
  })

  it('formats percentages and uptime', () => {
    expect(formatPercent(0.6214)).toBe('62.1%')
    expect(formatPercent(null)).toBe('—')
    const units = { day: 'd', hour: 'h', minute: 'm' }
    expect(formatUptime(3 * 86400 + 4 * 3600 + 5, units)).toBe('3d 4h')
    expect(formatUptime(2 * 3600 + 15 * 60, units)).toBe('2h 15m')
    expect(formatUptime(59, units)).toBe('0m')
  })
})

describe('ratios', () => {
  it('computes cache hit rate over all prompt tokens', () => {
    expect(cacheHitRate(100, 300, 100)).toBeCloseTo(0.6)
    expect(cacheHitRate(0, 0, 0)).toBeNull()
  })

  it('computes profit margin only when there is revenue', () => {
    expect(profitMargin(10, 6)).toBeCloseTo(0.4)
    expect(profitMargin(10, 12)).toBeCloseTo(-0.2)
    expect(profitMargin(0, 5)).toBeNull()
  })
})

describe('resolveAccountHealth', () => {
  it('returns null for payloads from older backends', () => {
    expect(resolveAccountHealth(null)).toBeNull()
    expect(resolveAccountHealth({})).toBeNull()
  })

  it('normalizes every category to a finite number', () => {
    const health = resolveAccountHealth({
      account_health: {
        available: 5,
        rate_limited: 1,
        temp_unschedulable: 0,
        unschedulable: 2,
        error: 1,
        balance_insufficient: 0,
        inactive: 3,
        other: Number.NaN
      }
    })
    expect(health).toEqual({
      available: 5,
      rate_limited: 1,
      temp_unschedulable: 0,
      unschedulable: 2,
      error: 1,
      balance_insufficient: 0,
      inactive: 3,
      other: 0
    })
  })
})

describe('isMeaningfulTimestamp', () => {
  it('ignores the epoch placeholder used when aggregation is disabled', () => {
    expect(isMeaningfulTimestamp('1970-01-01T00:00:00Z')).toBe(false)
    expect(isMeaningfulTimestamp('')).toBe(false)
    expect(isMeaningfulTimestamp('not-a-date')).toBe(false)
    expect(isMeaningfulTimestamp('2026-09-23T08:00:00Z')).toBe(true)
  })
})
