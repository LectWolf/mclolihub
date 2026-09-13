import { apiClient } from '../client'

export interface CodeBuddyStartRequest {
  site?: 'cn' | 'intl'
  proxy_id?: number
}

export interface CodeBuddyStartResponse {
  login_id: string
  verification_uri: string
  expires_in: number
}

export interface CodeBuddyPollResponse {
  done: boolean
  error?: string
  uid?: string
  nickname?: string
}

export interface CodeBuddyCreateRequest {
  login_id?: string
  credentials?: Record<string, unknown>
  proxy_id?: number | null
  name?: string
  concurrency?: number
  priority?: number
  group_ids?: number[]
}

export async function startLogin(payload: CodeBuddyStartRequest): Promise<CodeBuddyStartResponse> {
  const { data } = await apiClient.post<CodeBuddyStartResponse>('/admin/codebuddy/oauth/start', payload)
  return data
}

export async function pollLogin(loginId: string): Promise<CodeBuddyPollResponse> {
  const { data } = await apiClient.post<CodeBuddyPollResponse>('/admin/codebuddy/oauth/poll', {
    login_id: loginId
  })
  return data
}

export async function createFromOAuth(payload: CodeBuddyCreateRequest) {
  const { data } = await apiClient.post('/admin/codebuddy/oauth/create-from-oauth', payload)
  return data
}

const codebuddyAPI = { startLogin, pollLogin, createFromOAuth }
export default codebuddyAPI
