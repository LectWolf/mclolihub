import { apiClient } from './client'

export type GroupHealthStatus = 'healthy' | 'unavailable' | 'balance_insufficient' | 'not_enabled'

export interface GroupHealthTrendBucket {
  started_at: string
  probe_success: number
  probe_failure: number
  real_success: number
  real_failure: number
  probe_ttft_ms: number
  real_ttft_ms: number
}

export interface GroupHealthItem {
  group_id: number
  name: string
  platform: string
  rate_multiplier: number
  probe_enabled: boolean
  probe_model: string
  probe_interval_seconds: number
  status: GroupHealthStatus
  reason: string
  last_probe_at: string | null
  last_success_at: string | null
  next_probe_at: string | null
  probe_ttft_ms: number
  probe_ttft_avg_ms: number
  probe_ttft_p95_ms: number
  probe_samples: number
  probe_availability_6h: number
  real_ttft_p50_ms: number
  real_ttft_avg_ms: number
  real_ttft_p95_ms: number
  real_ttft_samples: number
  real_total_avg_ms: number
  real_availability_6h: number
  cache_rate_overall: number
  cache_rate_6h: number
  trend: GroupHealthTrendBucket[]
}

export interface GroupHealthResponse {
  items: GroupHealthItem[]
  window_hours: number
  bucket_minutes: number
}

export async function listGroupHealth(
  signal?: AbortSignal,
  query?: { trendHours?: number; trend?: 'probe' | 'full' | 'none' },
): Promise<GroupHealthResponse> {
  const params: Record<string, string | number> = {}
  if (query?.trendHours && query.trendHours > 0) params.trend_hours = query.trendHours
  if (query?.trend && query.trend !== 'full') params.trend = query.trend
  const { data } = await apiClient.get<GroupHealthResponse>('/group-health', {
    signal,
    params: Object.keys(params).length ? params : undefined,
  })
  return data
}

export async function listAdminGroupHealth(signal?: AbortSignal): Promise<GroupHealthResponse> {
  const { data } = await apiClient.get<GroupHealthResponse>('/admin/groups/health', { signal })
  return data
}

export async function refreshAdminGroupHealth(groupId: number): Promise<GroupHealthResponse> {
  const { data } = await apiClient.post<GroupHealthResponse>(`/admin/groups/${groupId}/health/refresh`)
  return data
}
