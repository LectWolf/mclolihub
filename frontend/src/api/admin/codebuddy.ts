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
  /** 'zero_only' restricts the account to models the catalog prices at x0. */
  credit_policy?: 'all' | 'zero_only'
  model_mapping?: Record<string, unknown>
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

/**
 * Replaces an existing account's credentials from a fresh login, keeping its
 * groups, priority and usage history. The backend rejects a payload belonging to
 * a different CodeBuddy uid.
 */
export async function reAuthAccount(
  id: number,
  payload: { login_id?: string; credentials?: Record<string, unknown> }
) {
  const { data } = await apiClient.post(`/admin/codebuddy/accounts/${id}/reauth`, payload)
  return data
}

export interface CodeBuddyCreditSegment {
  remaining: number
  total: number
  expires_at?: number
  source?: string
  package_code?: string
}

export interface CodeBuddyCreditsSnapshot {
  credits: number
  count?: number
  segments?: CodeBuddyCreditSegment[]
  soonest_expiry?: number
  intl?: boolean
  fetched_at?: number
  /** The gateway drew this balance down locally since the last billing probe. */
  estimated?: boolean
  /** The upstream rejected a request for lack of credits. */
  exhausted?: boolean
}

/** Gateway-side credit tally; CodeBuddy charges per request, not per token. */
export interface CodeBuddyCreditsUsage {
  day: string
  requests: number
  credits: number
  /** Requests whose model was absent from the catalog, so `credits` is a floor. */
  unpriced?: number
  total_requests: number
  total_credits: number
  updated_at: number
}

/** One calendar day of consumption as CodeBuddy billed it. */
export interface CodeBuddyRequestUsageDay {
  day: string
  credits: number
  requests: number
  by_model?: Record<string, number>
}

/**
 * CodeBuddy's own billing report. Authoritative, and wider than the gateway
 * tally: it also covers the official IDE and any other client on the account.
 */
export interface CodeBuddyRequestUsage {
  days: CodeBuddyRequestUsageDay[]
  total_credits: number
  requests: number
  range_days: number
  fetched_at: number
}

export interface CodeBuddyCreditsReport extends CodeBuddyCreditsSnapshot {
  usage: CodeBuddyCreditsUsage
  official?: CodeBuddyRequestUsage
  /** The balance came from a cached probe older than the refresh window. */
  stale: boolean
}

/**
 * Reads an account's credit standing. `refresh` defaults to true, which probes
 * the billing API; pass false to accept a recent cached balance instead.
 */
export async function queryCredits(id: number, refresh = true): Promise<CodeBuddyCreditsReport> {
  const { data } = await apiClient.get<CodeBuddyCreditsReport>(
    `/admin/codebuddy/accounts/${id}/credits`,
    { params: { refresh } }
  )
  return data
}

/** Upstream silently empties a window wider than 31 days. */
export const REQUEST_USAGE_MAX_DAYS = 30

/** Fetches CodeBuddy's billed consumption, bucketed by day and model. */
export async function queryRequestUsage(
  id: number,
  days = REQUEST_USAGE_MAX_DAYS
): Promise<CodeBuddyRequestUsage> {
  const { data } = await apiClient.get<CodeBuddyRequestUsage>(
    `/admin/codebuddy/accounts/${id}/request-usage`,
    { params: { days } }
  )
  return data
}

const codebuddyAPI = {
  startLogin,
  pollLogin,
  createFromOAuth,
  reAuthAccount,
  queryCredits,
  queryRequestUsage
}
export default codebuddyAPI
