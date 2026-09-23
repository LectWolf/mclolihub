/**
 * 趋势图 X 轴刻度：水平排列、自动抽稀，并去掉「YYYY-」前缀（2026-09-22 14:00 → 09-22 14:00）。
 * 只影响刻度文字，tooltip 仍使用完整标签。
 */
export const trendAxisTicks = {
  maxRotation: 0,
  autoSkip: true,
  autoSkipPadding: 12,
  maxTicksLimit: 8,
  callback(this: { getLabelForValue: (value: number) => string }, value: string | number): string {
    const label = String(this.getLabelForValue(Number(value)))
    return /^\d{4}-/.test(label) ? label.slice(5) : label
  }
}
