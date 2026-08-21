/**
 * Admin API Keys API endpoints
 * Handles API key management for administrators
 */

import { apiClient } from '../client'
import type { ApiKey } from '@/types'

export interface UpdateApiKeyGroupResult {
  api_key: ApiKey
  auto_granted_group_access: boolean
  granted_group_id?: number
  granted_group_name?: string
}

/**
 * Update an API key's group binding
 * @param id - API Key ID
 * @param groupId - Group ID (0 to unbind, positive to bind, null/undefined to skip)
 * @returns Updated API key with auto-grant info
 */
export async function updateApiKeyGroup(id: number, groupId: number | null): Promise<UpdateApiKeyGroupResult> {
  const { data } = await apiClient.put<UpdateApiKeyGroupResult>(`/admin/api-keys/${id}`, {
    group_id: groupId === null ? 0 : groupId
  })
  return data
}

export async function updateApiKeyRouting(id: number, input: {
  route_mode?: ApiKey['route_mode']
  route_platform?: ApiKey['route_platform']
  max_rate_multiplier?: number | null
  disabled_group_ids?: number[]
  custom_group_ids?: number[]
}): Promise<ApiKey> {
  const { data } = await apiClient.put<{ api_key: ApiKey }>(`/admin/api-keys/${id}`, input)
  return data.api_key
}

export const apiKeysAPI = {
  updateApiKeyGroup,
  updateApiKeyRouting
}

export default apiKeysAPI
