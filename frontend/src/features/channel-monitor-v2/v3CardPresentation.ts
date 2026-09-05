import type { MonitorStatus } from '@/api/admin/channelMonitor'
import type { HealthState } from '@/api/channelMonitorV2'
import type { GroupHealthStatus } from '@/api/groupHealth'

export type V3CardStatusKey = MonitorStatus | 'balance_insufficient'
export type V3CardSignalSource = 'traffic' | 'probe' | 'mixed' | 'none'

export interface V3CardGroupMeta {
  description?: string | null
  probeEnabled?: boolean
  probeStatus?: GroupHealthStatus | null
  probeTtftMs?: number | null
  sortOrder?: number
}

export function trafficMonitorStatus(overall?: HealthState | null): MonitorStatus | null {
  if (overall === 'healthy') return 'operational'
  if (overall === 'warning') return 'degraded'
  if (overall === 'critical') return 'failed'
  return null
}

export function probeMonitorStatus(
  status?: GroupHealthStatus | null,
  probeEnabled?: boolean,
): V3CardStatusKey | null {
  if (!probeEnabled) return null
  if (status === 'healthy') return 'operational'
  if (status === 'unavailable') return 'failed'
  if (status === 'balance_insufficient') return 'balance_insufficient'
  return null
}

export function resolveV3CardStatus(opts: {
  trafficOverall?: HealthState | null
  probeEnabled?: boolean
  probeStatus?: GroupHealthStatus | null
}): { status: V3CardStatusKey | null; source: V3CardSignalSource } {
  const traffic = trafficMonitorStatus(opts.trafficOverall)
  if (traffic) return { status: traffic, source: 'traffic' }
  const probe = probeMonitorStatus(opts.probeStatus, opts.probeEnabled)
  if (probe) return { status: probe, source: 'probe' }
  return { status: null, source: 'none' }
}

export function resolveV3CardTtftMs(opts: {
  trafficTtftMs?: number | null
  probeEnabled?: boolean
  probeTtftMs?: number | null
}): { ms: number | null; source: V3CardSignalSource } {
  if (opts.trafficTtftMs != null && Number.isFinite(opts.trafficTtftMs)) {
    return { ms: opts.trafficTtftMs, source: 'traffic' }
  }
  if (opts.probeEnabled && opts.probeTtftMs != null && opts.probeTtftMs > 0) {
    return { ms: opts.probeTtftMs, source: 'probe' }
  }
  return { ms: null, source: 'none' }
}

export type V3TimelineSlotState = 'healthy' | 'warning' | 'critical' | 'unknown'

export interface V3TimelineTrafficBucket {
  startMs: number
  requestCount: number
  errorRate: number
  cacheRate: number
  ttftP50Ms: number | null
  overall: HealthState
}

export interface V3TimelineProbeBucket {
  startMs: number
  durationMs: number
  success: number
  failure: number
  ttftMs: number
}

export interface V3TimelineSlot {
  startMs: number
  source: V3CardSignalSource
  state: V3TimelineSlotState
  availabilityRate: number | null
  traffic?: V3TimelineTrafficBucket
  probe?: V3TimelineProbeBucket
}

export function effectiveProbeOutcome(success = 0, failure = 0): { success: number; failure: number } {
  const ok = Math.max(0, success)
  const fail = Math.max(0, failure)
  // A group probe round stops on first success, so mixed success+failure counts as up.
  if (ok > 0) return { success: 1, failure: 0 }
  if (fail > 0) return { success: 0, failure: 1 }
  return { success: 0, failure: 0 }
}

export function mixAvailabilityRate(opts: {
  hasTraffic?: boolean
  trafficErrorRate?: number | null
  trafficRequestCount?: number
  probeSuccess?: number
  probeFailure?: number
}): { rate: number | null; source: V3CardSignalSource } {
  const probe = effectiveProbeOutcome(opts.probeSuccess, opts.probeFailure)
  const probeSuccess = probe.success
  const probeFailure = probe.failure
  const probeTotal = probeSuccess + probeFailure
  const hasTraffic = Boolean(opts.hasTraffic) || opts.trafficErrorRate != null
  const trafficRate = hasTraffic ? 1 - (opts.trafficErrorRate ?? 0) : null
  const probeRate = probeTotal > 0 ? probeSuccess / probeTotal : null

  if (trafficRate == null && probeRate == null) return { rate: null, source: 'none' }
  if (trafficRate == null) return { rate: probeRate, source: 'probe' }
  if (probeRate == null) return { rate: trafficRate, source: 'traffic' }

  const countedTraffic = opts.trafficRequestCount ?? 0
  const trafficWeight = countedTraffic > 0 ? countedTraffic : probeTotal
  return {
    rate: (trafficRate * trafficWeight + probeSuccess) / (trafficWeight + probeTotal),
    source: 'mixed',
  }
}

export function statusFromAvailabilityRate(rate: number | null): MonitorStatus | null {
  if (rate == null || !Number.isFinite(rate)) return null
  if (rate >= 0.8) return 'operational'
  if (rate >= 0.5) return 'degraded'
  return 'failed'
}

function stateFromAvailabilityRate(rate: number | null): V3TimelineSlotState {
  if (rate == null || !Number.isFinite(rate)) return 'unknown'
  if (rate >= 0.8) return 'healthy'
  if (rate >= 0.5) return 'warning'
  return 'critical'
}

export function alignTimelineEndMs(nowMs: number, bucketMs: number, dataThroughMs?: number): number {
  if (bucketMs <= 0) return nowMs
  // Exclusive end is the start of the in-progress bucket so the rightmost bar
  // is the last completed collection slot, not a trailing empty gray "now".
  let endMs = Math.floor(nowMs / bucketMs) * bucketMs
  if (dataThroughMs != null && Number.isFinite(dataThroughMs)) {
    const throughEnd = Math.floor(dataThroughMs / bucketMs) * bucketMs
    if (throughEnd > 0 && throughEnd < endMs) endMs = throughEnd
  }
  return endMs
}

export function rangesOverlap(aStart: number, aEnd: number, bStart: number, bEnd: number): boolean {
  return aStart < bEnd && bStart < aEnd
}

function trafficSlotState(traffic: V3TimelineTrafficBucket): V3TimelineSlotState {
  if (traffic.overall === 'healthy' || traffic.overall === 'warning' || traffic.overall === 'critical') {
    return traffic.overall
  }
  return 'unknown'
}

/** A 10-minute probe overlaps two aligned 5-minute collection slots. */
export function buildV3TimelineSlots(opts: {
  nowMs: number
  length: number
  bucketMs: number
  dataThroughMs?: number
  trafficBuckets?: V3TimelineTrafficBucket[]
  probeBuckets?: V3TimelineProbeBucket[]
}): V3TimelineSlot[] {
  const length = Math.max(0, Math.floor(opts.length))
  const bucketMs = opts.bucketMs > 0 ? opts.bucketMs : 1
  const endMs = alignTimelineEndMs(opts.nowMs, bucketMs, opts.dataThroughMs)
  const traffic = opts.trafficBuckets ?? []
  const probes = opts.probeBuckets ?? []
  const slots: V3TimelineSlot[] = []

  for (let index = 0; index < length; index += 1) {
    const startMs = endMs - (length - index) * bucketMs
    const slotEndMs = startMs + bucketMs
    const trafficHit = traffic.find(bucket => rangesOverlap(bucket.startMs, bucket.startMs + bucketMs, startMs, slotEndMs))
    const probeHit = [...probes]
      .filter(bucket => (
        (bucket.success > 0 || bucket.failure > 0)
        && rangesOverlap(bucket.startMs, bucket.startMs + bucket.durationMs, startMs, slotEndMs)
      ))
      .sort((a, b) => a.startMs - b.startMs)
      .at(-1)

    if (trafficHit) {
      const trafficRate = 1 - trafficHit.errorRate
      slots.push({
        startMs,
        source: 'traffic',
        state: trafficSlotState(trafficHit),
        availabilityRate: trafficRate,
        traffic: trafficHit,
      })
      continue
    }
    if (!probeHit) {
      slots.push({ startMs, source: 'none', state: 'unknown', availabilityRate: null })
      continue
    }
    const mixed = mixAvailabilityRate({
      probeSuccess: probeHit.success,
      probeFailure: probeHit.failure,
    })
    slots.push({
      startMs,
      source: 'probe',
      state: stateFromAvailabilityRate(mixed.rate),
      availabilityRate: mixed.rate,
      probe: probeHit,
    })
  }

  return slots
}

export function latestActiveV3Slot(slots: V3TimelineSlot[]): V3TimelineSlot | null {
  return [...slots].reverse().find(slot => slot.source !== 'none') ?? null
}


