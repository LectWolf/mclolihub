import { apiClient } from '../client'

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

export async function listQuality(): Promise<GroupQualityStatus[]> {
  const { data } = await apiClient.get<GroupQualityStatus[]>('/admin/groups/quality-check')
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
