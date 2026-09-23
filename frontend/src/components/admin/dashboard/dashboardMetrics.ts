import type { AccountHealthStats, DashboardStats } from '@/types'

export type DeltaDirection = 'up' | 'down' | 'flat'
export type DeltaTone = 'positive' | 'negative' | 'neutral'

export interface MetricDelta {
  /** 相对昨日同时段的变化比例；昨日为 0（或为负，比例无意义）时为 null */
  ratio: number | null
  direction: DeltaDirection
  tone: DeltaTone
}

export const toFiniteNumber = (value: unknown): number => {
  const numberValue = Number(value)
  return Number.isFinite(numberValue) ? numberValue : 0
}

export function formatTokens(value: unknown): string {
  const safeValue = toFiniteNumber(value)
  const abs = Math.abs(safeValue)
  if (abs >= 1_000_000_000) return `${(safeValue / 1_000_000_000).toFixed(2)}B`
  if (abs >= 1_000_000) return `${(safeValue / 1_000_000).toFixed(2)}M`
  if (abs >= 1_000) return `${(safeValue / 1_000).toFixed(2)}K`
  return safeValue.toLocaleString()
}

/** 美元金额（不含 $），负数保留符号，精度随量级变化。 */
export function formatCost(value: unknown): string {
  const safeValue = toFiniteNumber(value)
  const abs = Math.abs(safeValue)
  if (abs >= 1000) return `${(safeValue / 1000).toFixed(2)}K`
  if (abs >= 1) return safeValue.toFixed(2)
  if (abs >= 0.01) return safeValue.toFixed(3)
  return safeValue.toFixed(4)
}

export function formatUsd(value: unknown): string {
  const formatted = formatCost(value)
  return formatted.startsWith('-') ? `-$${formatted.slice(1)}` : `$${formatted}`
}

/** 坐标轴刻度用的简洁金额：$0、$2.5、$0.05、$1.2K，不补尾随零。 */
export function formatUsdAxis(value: unknown): string {
  const safeValue = toFiniteNumber(value)
  const abs = Math.abs(safeValue)
  const sign = safeValue < 0 ? '-' : ''
  if (abs === 0) return '$0'
  if (abs >= 1000) return `${sign}$${Number((abs / 1000).toFixed(1))}K`
  if (abs >= 1) return `${sign}$${Number(abs.toFixed(2))}`
  return `${sign}$${Number(abs.toPrecision(2))}`
}

export function formatDuration(ms: unknown): string {
  const safeValue = toFiniteNumber(ms)
  if (safeValue >= 1000) return `${(safeValue / 1000).toFixed(2)}s`
  return `${Math.round(safeValue)}ms`
}

/**
 * 计算今日相对昨日同时段的变化。higherIsBetter 决定颜色语义：
 * 消费、请求量升高是好事；成本、延迟升高是坏事。两边都为 0 时返回 null（不展示）。
 */
export function computeDelta(
  current: unknown,
  previous: unknown,
  options: { higherIsBetter?: boolean } = {}
): MetricDelta | null {
  const higherIsBetter = options.higherIsBetter ?? true
  const cur = toFiniteNumber(current)
  const prev = toFiniteNumber(previous)
  if (cur === 0 && prev === 0) return null
  if (prev <= 0) {
    return { ratio: null, direction: cur > prev ? 'up' : cur < prev ? 'down' : 'flat', tone: 'neutral' }
  }
  const ratio = (cur - prev) / prev
  const direction: DeltaDirection = Math.abs(ratio) < 0.0005 ? 'flat' : ratio > 0 ? 'up' : 'down'
  const tone: DeltaTone =
    direction === 'flat' ? 'neutral' : (direction === 'up') === higherIsBetter ? 'positive' : 'negative'
  return { ratio, direction, tone }
}

/** 变化比例格式化为不带符号的百分比，方向由箭头表达。 */
export function formatDeltaRatio(ratio: number): string {
  const pct = Math.abs(ratio) * 100
  if (pct >= 1000) return '999%+'
  return `${pct < 10 ? pct.toFixed(1) : pct.toFixed(0)}%`
}

export function formatPercent(ratio: number | null): string {
  if (ratio === null) return '—'
  return `${(ratio * 100).toFixed(1)}%`
}

/** 缓存命中率 = 缓存读取 / (输入 + 缓存读取 + 缓存写入)，与 Token 趋势图口径一致。 */
export function cacheHitRate(inputTokens: unknown, cacheReadTokens: unknown, cacheCreationTokens: unknown): number | null {
  const read = toFiniteNumber(cacheReadTokens)
  const prompt = toFiniteNumber(inputTokens) + read + toFiniteNumber(cacheCreationTokens)
  return prompt > 0 ? read / prompt : null
}

/** 毛利率 = (实际扣费 - 账号成本) / 实际扣费；没有扣费时无意义。 */
export function profitMargin(actualCost: unknown, accountCost: unknown): number | null {
  const actual = toFiniteNumber(actualCost)
  if (actual <= 0) return null
  return (actual - toFiniteNumber(accountCost)) / actual
}

export type AccountHealthKey = keyof AccountHealthStats

/** 账号健康分段的展示顺序；status 为账号列表页可直接筛选的状态值。 */
export const ACCOUNT_HEALTH_ORDER: ReadonlyArray<{ key: AccountHealthKey; status: string | null }> = [
  { key: 'available', status: 'active' },
  { key: 'rate_limited', status: 'rate_limited' },
  { key: 'temp_unschedulable', status: 'temp_unschedulable' },
  { key: 'unschedulable', status: 'unschedulable' },
  { key: 'balance_insufficient', status: 'balance_insufficient' },
  { key: 'error', status: 'error' },
  { key: 'inactive', status: 'inactive' },
  { key: 'other', status: null }
]

/** 旧版后端（或旧缓存）没有 account_health 时返回 null，由调用方回退到旧字段展示。 */
export function resolveAccountHealth(stats: Pick<DashboardStats, 'account_health'> | null | undefined): AccountHealthStats | null {
  const health = stats?.account_health
  if (!health) return null
  return {
    available: toFiniteNumber(health.available),
    rate_limited: toFiniteNumber(health.rate_limited),
    temp_unschedulable: toFiniteNumber(health.temp_unschedulable),
    unschedulable: toFiniteNumber(health.unschedulable),
    error: toFiniteNumber(health.error),
    balance_insufficient: toFiniteNumber(health.balance_insufficient),
    inactive: toFiniteNumber(health.inactive),
    other: toFiniteNumber(health.other)
  }
}

/** 预聚合水位为 1970 年（未启用预聚合或从未运行）时视为未知，不据此提示延迟。 */
export function isMeaningfulTimestamp(raw: string | null | undefined): boolean {
  if (!raw) return false
  const time = new Date(raw).getTime()
  return Number.isFinite(time) && time > Date.UTC(2000, 0, 1)
}

export function formatUptime(seconds: unknown, units: { day: string; hour: string; minute: string }): string {
  const total = Math.max(0, Math.floor(toFiniteNumber(seconds)))
  const days = Math.floor(total / 86400)
  const hours = Math.floor((total % 86400) / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  if (days > 0) return `${days}${units.day} ${hours}${units.hour}`
  if (hours > 0) return `${hours}${units.hour} ${minutes}${units.minute}`
  return `${minutes}${units.minute}`
}
