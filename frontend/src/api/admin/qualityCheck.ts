import { apiClient } from '../client'
import type { MonitorLatestQuality } from '../channelMonitorV2'

export interface GroupQualityStatus {
  group_id: number
  enabled: boolean
  status: 'unknown' | 'healthy' | 'suspect' | string
  checked_accounts: number
  degraded_accounts: number
  last_run_at?: string | null
  min_spend_usd: number
  min_interval_minutes: number
  model_id: string
  default_prompt: string
}

export async function listQuality(signal?: AbortSignal): Promise<GroupQualityStatus[]> {
  const { data } = await apiClient.get<GroupQualityStatus[]>('/admin/groups/quality-check', ...(signal ? [{ signal }] : []))
  return data ?? []
}

export async function setQualityEnabled(groupId: number, enabled: boolean): Promise<GroupQualityStatus> {
  const { data } = await apiClient.put<GroupQualityStatus>(`/admin/groups/${groupId}/quality-check`, { enabled })
  return data
}

export interface GroupQualitySettings {
  enabled: boolean
  min_spend_usd?: number
  min_interval_minutes?: number
  model_id?: string
}

export async function saveQualitySettings(groupId: number, settings: GroupQualitySettings): Promise<GroupQualityStatus> {
  const { data } = await apiClient.put<GroupQualityStatus>(`/admin/groups/${groupId}/quality-check`, settings)
  return data
}

export interface GroupQualityRun {
  run_id: string
  group_id: number
  source?: 'manual' | 'automatic'
  status: 'running' | 'completed' | 'failed'
  reason: string
  checked_accounts: number
  total_accounts: number
  started_at: string
  finished_at?: string | null
  latest_quality?: MonitorLatestQuality | null
}

export async function runQualityCheck(groupId: number, signal?: AbortSignal): Promise<GroupQualityRun> {
  const { data } = await apiClient.post<GroupQualityRun>(`/admin/groups/${groupId}/quality-check/run`, undefined, { signal })
  return data
}

export async function getQualityRun(groupId: number, signal?: AbortSignal): Promise<GroupQualityRun | null> {
  const { data } = await apiClient.get<GroupQualityRun | null>(`/admin/groups/${groupId}/quality-check/run`, { signal })
  return data ?? null
}

export interface GroupQualityDiagnostics {
  stage: string
  configured_protocol: string
  total_duration_ms: number
  generation_duration_ms: number
  generation_timeout_seconds: number
  received_text_bytes: number
  received_events: number
  completion_received: boolean
  attempt_count?: number
  last_event_type?: string
  capture_overflow?: boolean
  failure_code?: string
  deadline_source?: string
  artwork_available?: boolean
  render_duration_ms?: number
  render_failure_code?: string
}

export interface QualityAttemptDiagnostic {
  number: number
  http_status?: number
  error_type?: string
  message?: string
  failure_code?: string
  duration_ms: number
}

export interface GroupQualityDiagnosticResult {
  result_id: string
  group_id?: number
  group_name?: string
  reason: string
  diagnostics: GroupQualityDiagnostics | null
  attempts?: QualityAttemptDiagnostic[] | null
  details_truncated?: boolean
  route?: {
    account_id: number
    account_name?: string
    platform: string
    endpoint: string
    model: string
    proxy_enabled: boolean
  } | null
}

export async function getQualityDiagnostics(groupId: number, resultId: string, signal?: AbortSignal): Promise<GroupQualityDiagnosticResult> {
  const { data } = await apiClient.get<GroupQualityDiagnosticResult>(`/admin/groups/${groupId}/quality-check/diagnostics/${encodeURIComponent(resultId)}`, { signal })
  if (!data || data.result_id !== resultId || (data.group_id != null && data.group_id !== groupId)) throw new Error('Invalid quality diagnostics')
  return data
}
