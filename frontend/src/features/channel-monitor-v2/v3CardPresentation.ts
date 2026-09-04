import type { MonitorStatus } from '@/api/admin/channelMonitor'
import type { HealthState } from '@/api/channelMonitorV2'
import type { GroupHealthStatus } from '@/api/groupHealth'

export type V3CardStatusKey = MonitorStatus | 'balance_insufficient'
export type V3CardSignalSource = 'traffic' | 'probe' | 'none'

export interface V3CardGroupMeta {
  description?: string | null
  probeEnabled?: boolean
  probeStatus?: GroupHealthStatus | null
  probeTtftMs?: number | null
  probeBucketMs?: number
  probeBuckets?: V3TimelineProbeBucket[]
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
  traffic?: V3TimelineTrafficBucket
  probe?: V3TimelineProbeBucket
}

export function alignTimelineEndMs(nowMs: number, bucketMs: number): number {
  if (bucketMs <= 0) return nowMs
  return Math.floor(nowMs / bucketMs) * bucketMs + bucketMs
}

export function rangesOverlap(aStart: number, aEnd: number, bStart: number, bEnd: number): boolean {
  return aStart < bEnd && bStart < aEnd
}

function probeSlotState(probe: V3TimelineProbeBucket): V3TimelineSlotState {
  if (probe.success <= 0 && probe.failure <= 0) return 'unknown'
  if (probe.success <= 0) return 'critical'
  return probe.ttftMs > 10_000 ? 'warning' : 'healthy'
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
  trafficBuckets?: V3TimelineTrafficBucket[]
  probeBuckets?: V3TimelineProbeBucket[]
}): V3TimelineSlot[] {
  const length = Math.max(0, Math.floor(opts.length))
  const bucketMs = opts.bucketMs > 0 ? opts.bucketMs : 1
  const endMs = alignTimelineEndMs(opts.nowMs, bucketMs)
  const traffic = opts.trafficBuckets ?? []
  const probes = opts.probeBuckets ?? []
  const slots: V3TimelineSlot[] = []

  for (let index = 0; index < length; index += 1) {
    const startMs = endMs - (length - index) * bucketMs
    const slotEndMs = startMs + bucketMs
    const trafficHit = traffic.find(bucket => rangesOverlap(bucket.startMs, bucket.startMs + bucketMs, startMs, slotEndMs))
    if (trafficHit) {
      slots.push({
        startMs,
        source: 'traffic',
        state: trafficSlotState(trafficHit),
        traffic: trafficHit,
      })
      continue
    }

    const probeHit = [...probes]
      .filter(bucket => (
        (bucket.success > 0 || bucket.failure > 0)
        && rangesOverlap(bucket.startMs, bucket.startMs + bucket.durationMs, startMs, slotEndMs)
      ))
      .sort((a, b) => a.startMs - b.startMs)
      .at(-1)
    if (probeHit) {
      slots.push({
        startMs,
        source: 'probe',
        state: probeSlotState(probeHit),
        probe: probeHit,
      })
      continue
    }

    slots.push({ startMs, source: 'none', state: 'unknown' })
  }

  return slots
}
