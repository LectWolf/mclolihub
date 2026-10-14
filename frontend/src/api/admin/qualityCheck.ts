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
