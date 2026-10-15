import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { baseCompile } from '@intlify/message-compiler'
import MonitorSettingsPanel from '../MonitorSettingsPanel.vue'
import { getQualityRun, listQuality, runQualityCheck, saveQualitySettings, type GroupQualityRun, type GroupQualityStatus } from '@/api/admin/qualityCheck'
import { getConfig, updateConfig } from '@/api/channelMonitorV2'
import en from '@/i18n/locales/en/channelMonitorV2'
import enV3 from '@/i18n/locales/en/channelMonitorV3'
const compiledEn = JSON.parse(JSON.stringify({ ...en, ...enV3 }), (_key, value) => typeof value === 'string' ? new Function(`return ${baseCompile(value, { mode: 'arrow' }).code}`)() : value)

vi.mock('@/api/admin/qualityCheck', () => ({ listQuality: vi.fn(), saveQualitySettings: vi.fn(), getQualityRun: vi.fn(), runQualityCheck: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: { getAllIncludingInactive: vi.fn(async () => [
  { id: 7, name: 'Group A', platform: 'openai' }, { id: 8, name: 'Group B', platform: 'openai' },
]) } } }))
vi.mock('@/api/channelMonitorV2', async importOriginal => ({
  ...await importOriginal<typeof import('@/api/channelMonitorV2')>(),
  getConfig: vi.fn(), updateConfig: vi.fn(), getQualityHistory: vi.fn(async () => []),
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn(), cachedPublicSettings: {} }) }))
vi.mock('@/utils/featureFlags', () => ({ getChannelMonitorMode: () => 'v2', isChannelMonitorV2Mode: () => true }))

const status: GroupQualityStatus = {
  group_id: 7, enabled: true, status: 'healthy', checked_accounts: 1, degraded_accounts: 0,
  min_spend_usd: 7.5, min_interval_minutes: 45, model_id: 'test-model', default_prompt: '<html>Fixed local prompt</html>',
}
let wrapper: VueWrapper
async function render() {
  wrapper = mount(MonitorSettingsPanel, {
    global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: compiledEn } })], stubs: { RouterLink: true } },
  })
  await flushPromises()
  return wrapper.find('[data-quality-group="7"]')
}
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(listQuality).mockResolvedValue([status, { ...status, group_id: 8 }])
  vi.mocked(getQualityRun).mockReset().mockResolvedValue(null)
  vi.mocked(runQualityCheck).mockReset().mockResolvedValue(running)
  vi.mocked(saveQualitySettings).mockImplementation(async (id, payload) => ({ ...status, ...payload, group_id: id }))
  vi.mocked(getConfig).mockResolvedValue({
    version: 1, enabled: true, refresh_interval_seconds: 60, platforms: [], group_ids: [],
    health_thresholds: { minimum_sample: 50, warning_error_rate: 0.05, critical_error_rate: 0.2, target_ttft_ms: 3000,
      warning_ttft_ms: 3000, critical_ttft_ms: 10000, warning_cache_rate: 0.85, critical_cache_rate: 0.6,
      error_weight: 0.6, ttft_weight: 0.2, cache_weight: 0.2 },
  })
})
afterEach(() => { wrapper?.unmount(); vi.useRealTimers() })

describe('MonitorSettingsPanel quality configuration', () => {
  it('keeps group edits local until save and sends all thresholds and the selected model', async () => {
    const group = await render()
    expect((group.find('[data-quality-spend]').element as HTMLInputElement).value).toBe('7.5')
    await group.find('[data-quality-spend]').setValue('12.25')
    await group.find('[data-quality-interval]').setValue('60')
    await group.find('[data-quality-model]').setValue(' selected-model ')
    await group.find('[role="switch"]').trigger('click')
    expect(saveQualitySettings).not.toHaveBeenCalled()
    expect(group.find('[role="switch"]').attributes('aria-checked')).toBe('false')
    await group.find('[data-quality-save]').trigger('click')
    await flushPromises()
    expect(saveQualitySettings).toHaveBeenCalledWith(7, { enabled: false, min_spend_usd: 12.25, min_interval_minutes: 60, model_id: 'selected-model' })
    expect(group.find('[data-quality-save]').attributes('disabled')).toBeDefined()
    expect((wrapper.find('[data-quality-group="8"] [data-quality-spend]').element as HTMLInputElement).value).toBe('7.5')
  })

  it.each(['', '0', '0.009', '-1', '1000001', '1e999', 'abc'])('rejects invalid spend %s without making a request', async value => {
    const group = await render()
    await group.find('[data-quality-spend]').setValue(value)
    await group.find('[data-quality-save]').trigger('click')
    expect(saveQualitySettings).not.toHaveBeenCalled()
    expect(group.find('[role="alert"]').text()).toBe(en.channelMonitorV2.settings.qualitySpendInvalid)
  })

  it.each(['', '0', '-1', '1.5', '10081', 'abc'])('rejects invalid interval %s without making a request', async value => {
    const group = await render()
    await group.find('[data-quality-interval]').setValue(value)
    await group.find('[data-quality-save]').trigger('click')
    expect(saveQualitySettings).not.toHaveBeenCalled()
    expect(group.find('[role="alert"]').text()).toBe(en.channelMonitorV2.settings.qualityIntervalInvalid)
  })

  it.each([[0.01, 1], [1000000, 10080]])('accepts boundary values %s USD / %s minutes and blank default model', async (spend, interval) => {
    const group = await render()
    await group.find('[data-quality-spend]').setValue(String(spend))
    await group.find('[data-quality-interval]').setValue(String(interval))
    await group.find('[data-quality-model]').setValue('')
    await group.find('[data-quality-save]').trigger('click')
    await flushPromises()
    expect(saveQualitySettings).toHaveBeenCalledWith(7, { enabled: true, min_spend_usd: spend, min_interval_minutes: interval, model_id: '' })
  })

  it('retains all drafts on save failure and disables inputs while saving', async () => {
    let reject!: (error: Error) => void
    vi.mocked(saveQualitySettings).mockReturnValue(new Promise((_, fail) => { reject = fail }))
    const group = await render()
    await group.find('[data-quality-spend]').setValue('18')
    await wrapper.find('[data-quality-group="8"] [data-quality-model]').setValue('other-draft')
    await group.find('[data-quality-save]').trigger('click')
    expect(group.find('[data-quality-spend]').attributes('disabled')).toBeDefined()
    reject(new Error('private upstream detail'))
    await flushPromises()
    expect((group.find('[data-quality-spend]').element as HTMLInputElement).value).toBe('18')
    expect((wrapper.find('[data-quality-group="8"] [data-quality-model]').element as HTMLInputElement).value).toBe('other-draft')
    expect(group.find('[role="alert"]').text()).toBe(en.channelMonitorV2.settings.qualitySaveFailed)
    expect(group.find('[data-quality-save]').attributes('disabled')).toBeUndefined()
    expect(listQuality).toHaveBeenCalledTimes(1)
  })

  it('preserves quality inputs when unrelated monitor settings fail to save', async () => {
    vi.mocked(updateConfig).mockRejectedValue(new Error('save failed'))
    const group = await render()
    await group.find('[data-quality-spend]').setValue('19')
    await wrapper.find('[role="switch"]').trigger('click')
    await wrapper.find('header button').trigger('click')
    await flushPromises()
    expect(updateConfig).toHaveBeenCalledOnce()
    expect((group.find('[data-quality-spend]').element as HTMLInputElement).value).toBe('19')
    expect(listQuality).toHaveBeenCalledTimes(1)
  })

  it('renders the fixed prompt once as expandable plain text, never editable HTML', async () => {
    await render()
    expect(wrapper.findAll('details')).toHaveLength(1)
    expect(wrapper.find('details pre').text()).toBe(status.default_prompt)
    expect(wrapper.find('details textarea').exists()).toBe(false)
    expect(wrapper.find('details pre html').exists()).toBe(false)
  })

  it('shows load error instead of editable defaults and supports retry', async () => {
    vi.mocked(listQuality).mockRejectedValueOnce(new Error('unavailable'))
    await render()
    expect(wrapper.find('[data-quality-spend]').exists()).toBe(false)
    const alert = wrapper.find('[role="alert"]')
    expect(alert.text()).toContain(en.channelMonitorV2.settings.qualityLoadFailed)
    await alert.find('button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-quality-spend]').exists()).toBe(true)
    expect(listQuality).toHaveBeenCalledTimes(2)
  })
})

it('parses the entire numeric spend input rather than a parseFloat prefix', async () => {
  const group = await render()
  await group.find('[data-quality-spend]').setValue('1e2')
  await group.find('[data-quality-save]').trigger('click')
  await flushPromises()
  expect(saveQualitySettings).toHaveBeenCalledWith(7, expect.objectContaining({ min_spend_usd: 100 }))
})

const running: GroupQualityRun = {
  run_id: 'run-7', group_id: 7, status: 'running', reason: '', checked_accounts: 1, total_accounts: 3, started_at: '2026-08-08T10:00:00Z',
}
const latest = { result_id: 'result-7', status: 'success' as const, reason: 'Quality passed', model_id: 'test-model', checked_at: '2026-08-08T10:01:00Z', has_preview: true }

describe('MonitorSettingsPanel manual group quality check', () => {
  it('requires saved enabled settings and disables the run button for drafts and saves', async () => {
    const group = await render()
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeUndefined()
    expect(group.text()).toContain(en.channelMonitorV2.settings.qualityRunHint)
    await group.find('[data-quality-model]').setValue('unsaved-model')
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeDefined()
    expect(group.find('[data-quality-run-hint]').text()).toBe(en.channelMonitorV2.settings.qualityRunUnsaved)
    let resolve!: (value: GroupQualityStatus) => void
    vi.mocked(saveQualitySettings).mockReturnValueOnce(new Promise(done => { resolve = done }))
    await group.find('[data-quality-save]').trigger('click')
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeDefined()
    resolve({ ...status, model_id: 'unsaved-model' })
    await flushPromises()
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeUndefined()
    await group.find('[role="switch"]').trigger('click')
    await group.find('[data-quality-save]').trigger('click')
    await flushPromises()
    expect(group.find('[data-quality-run-hint]').text()).toBe(en.channelMonitorV2.settings.qualityRunDisabled)
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeDefined()
    expect(runQualityCheck).not.toHaveBeenCalled()
  })

  it('runs immediately, polls every five seconds without overlap, and refreshes only its group', async () => {
    vi.useFakeTimers()
    const group = await render()
    let accept!: (value: GroupQualityRun) => void
    vi.mocked(runQualityCheck).mockReturnValueOnce(new Promise(done => { accept = done }))
    await wrapper.find('[data-quality-group="8"] [data-quality-model]').setValue('keep-other-draft')
    await group.find('[data-quality-run]').trigger('click')
    expect(group.find('[data-quality-run]').text()).toBe(en.channelMonitorV2.settings.qualityRun)
    expect(group.find('[data-quality-run-result]').exists()).toBe(false)
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeDefined()
    expect(runQualityCheck).toHaveBeenCalledWith(7, expect.any(AbortSignal))
    accept(running)
    await flushPromises()
    expect(group.find('[data-quality-run-result]').text()).toContain('1 / 3')
    let finish!: (value: GroupQualityRun) => void
    vi.mocked(getQualityRun).mockReturnValueOnce(new Promise(done => { finish = done }))
    await vi.advanceTimersByTimeAsync(4999)
    expect(getQualityRun).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(getQualityRun).toHaveBeenCalledTimes(3)
    await vi.advanceTimersByTimeAsync(15000)
    expect(getQualityRun).toHaveBeenCalledTimes(3)
    vi.mocked(listQuality).mockResolvedValueOnce([{ ...status, status: 'suspect' }, { ...status, group_id: 8, model_id: 'server-other' }])
    finish({ ...running, status: 'completed', checked_accounts: 3, latest_quality: latest })
    await flushPromises()
    expect(group.find('[data-quality-run-result]').text()).toContain('Completed')
    expect(group.find('[data-quality-run-result]').text()).toContain('3 / 3')
    expect(group.text()).toContain(en.channelMonitorV2.settings.qualityStatus.suspect)
    expect(group.findComponent({ name: 'ChannelMonitorV3QualityPreview' }).props()).toMatchObject({ groupId: 7, groupName: 'Group A', enabled: true, latest, adminDiagnostics: true })
    expect((wrapper.find('[data-quality-group="8"] [data-quality-model]').element as HTMLInputElement).value).toBe('keep-other-draft')
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeUndefined()
    await vi.advanceTimersByTimeAsync(10000)
    expect(getQualityRun).toHaveBeenCalledTimes(3)
  })

  it('restores an existing run after configuration load and displays failed results safely', async () => {
    vi.useFakeTimers()
    vi.mocked(getQualityRun).mockImplementation(async id => id === 7 ? running : null)
    const group = await render()
    expect(group.find('[data-quality-run]').text()).toBe(en.channelMonitorV2.settings.qualityRunning)
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeDefined()
    vi.mocked(getQualityRun).mockResolvedValueOnce({ ...running, status: 'failed', reason: 'private raw upstream error', latest_quality: latest })
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(group.find('[data-quality-run-result]').text()).toContain(en.channelMonitorV2.settings.qualityRunFailed)
    expect(wrapper.text()).not.toContain('private raw upstream error')
    expect(group.findComponent({ name: 'ChannelMonitorV3QualityPreview' }).props('latest')).toEqual(latest)
    expect(listQuality).toHaveBeenCalledTimes(2)
    expect(runQualityCheck).not.toHaveBeenCalled()
  })

  it('keeps an unknown status locked after polling failure and supports checking status', async () => {
    vi.useFakeTimers()
    const group = await render()
    await group.find('[data-quality-run]').trigger('click')
    await flushPromises()
    vi.mocked(getQualityRun).mockRejectedValueOnce(new Error('private network details'))
    await vi.advanceTimersByTimeAsync(5000)
    expect(group.find('[data-quality-run-error]').text()).toContain(en.channelMonitorV2.settings.qualityRunPollFailed)
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).not.toContain('private network details')
    await vi.advanceTimersByTimeAsync(10000)
    expect(getQualityRun).toHaveBeenCalledTimes(3)
    vi.mocked(getQualityRun).mockResolvedValueOnce({ ...running, status: 'completed' })
    await group.find('[data-quality-run-retry]').trigger('click')
    await flushPromises()
    expect(group.find('[data-quality-run-error]').exists()).toBe(false)
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeUndefined()
    expect(runQualityCheck).toHaveBeenCalledOnce()
  })

  it('requires initial status recovery to succeed before permitting a run', async () => {
    vi.mocked(getQualityRun).mockRejectedValueOnce(new Error('initial failure'))
    const group = await render()
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeDefined()
    await group.find('[data-quality-run-retry]').trigger('click')
    await flushPromises()
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeUndefined()
  })

  it.each([[400, true], [409, true], [500, true], [400, false], [409, false]])('handles POST %s (normalized: %s) using fixed messages and safe retry behavior', async (statusCode, normalized) => {
    vi.useFakeTimers()
    vi.mocked(runQualityCheck).mockRejectedValueOnce(normalized
      ? { status: statusCode, error: 'private response' }
      : { response: { status: statusCode, data: { error: 'private response' } } })
    const group = await render()
    await group.find('[data-quality-run]').trigger('click')
    await flushPromises()
    const message = statusCode === 400 ? 'qualityRunUnavailable' : statusCode === 409 ? 'qualityRunBusyUnavailable' : 'qualityRunSubmitFailed'
    expect(group.find('[data-quality-run-error]').text()).toContain(en.channelMonitorV2.settings[message])
    expect(wrapper.text()).not.toContain('private response')
    expect(group.find('[data-quality-run]').attributes('disabled') !== undefined).toBe(statusCode !== 400)
    if (statusCode === 409) {
      expect(getQualityRun).toHaveBeenCalledTimes(3)
      expect(group.find('[data-quality-run-result]').exists()).toBe(false)
      vi.mocked(getQualityRun).mockResolvedValueOnce(running)
      await group.find('[data-quality-run-retry]').trigger('click')
      await flushPromises()
      expect(group.find('[data-quality-run]').attributes('disabled')).toBeDefined()
      expect(getQualityRun).toHaveBeenCalledTimes(4)
    }
  })

  it.each([
    ['save and enable quality checks before running manually', 'qualityRunDisabled'],
    ['no eligible text and vision accounts in this group', 'qualityRunNoEligibleAccounts'],
    ['no available accounts in this group', 'qualityRunNoEligibleAccounts'],
    ['no available accounts allow the configured quality model', 'qualityRunModelNotAllowed'],
  ] as const)('distinguishes HTTP 400 reason %s in both error shapes', async (message, key) => {
    const group = await render()
    for (const error of [{ status: 400, message }, { message: 'Request failed with status code 400', response: { status: 400, data: { message } } }]) {
      vi.mocked(runQualityCheck).mockRejectedValueOnce(error)
      await group.find('[data-quality-run]').trigger('click')
      await flushPromises()
      expect(group.find('[data-quality-run-error]').text()).toContain(en.channelMonitorV2.settings[key])
      expect(group.find('[data-quality-run]').attributes('disabled')).toBeUndefined()
    }
  })

  it('retries terminal quality refresh while retaining the run result and group drafts', async () => {
    const group = await render()
    vi.mocked(runQualityCheck).mockResolvedValueOnce({ ...running, status: 'completed', latest_quality: latest })
    vi.mocked(listQuality).mockRejectedValueOnce(new Error('private refresh failure'))
    await group.find('[data-quality-run]').trigger('click')
    await flushPromises()
    expect(group.find('[data-quality-run-error]').text()).toContain(en.channelMonitorV2.settings.qualityRunRefreshFailed)
    await group.find('[data-quality-model]').setValue('new-draft')
    vi.mocked(getQualityRun).mockResolvedValueOnce({ ...running, status: 'completed', latest_quality: latest })
    await group.find('[data-quality-run-retry]').trigger('click')
    await flushPromises()
    expect(group.find('[data-quality-run-error]').exists()).toBe(false)
    expect((group.find('[data-quality-model]').element as HTMLInputElement).value).toBe('new-draft')
    expect(wrapper.text()).not.toContain('private refresh failure')
  })

  it('clears scheduled polling when unmounted', async () => {
    vi.useFakeTimers()
    vi.mocked(getQualityRun).mockImplementation(async id => id === 7 ? running : null)
    await render()
    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(15000)
    expect(getQualityRun).toHaveBeenCalledTimes(2)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('aborts in-flight polling and ignores a late response after unmount', async () => {
    vi.useFakeTimers()
    const group = await render()
    await group.find('[data-quality-run]').trigger('click')
    await flushPromises()
    let finish!: (value: GroupQualityRun) => void
    vi.mocked(getQualityRun).mockReturnValueOnce(new Promise(done => { finish = done }))
    await vi.advanceTimersByTimeAsync(5000)
    const signal = vi.mocked(getQualityRun).mock.calls.at(-1)![1]!
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    finish({ ...running, status: 'completed', latest_quality: latest })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(10000)
    expect(listQuality).toHaveBeenCalledTimes(1)
    expect(getQualityRun).toHaveBeenCalledTimes(3)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('aborts an in-flight submission and ignores its late accepted response', async () => {
    vi.useFakeTimers()
    const group = await render()
    let accept!: (value: GroupQualityRun) => void
    vi.mocked(runQualityCheck).mockReturnValueOnce(new Promise(done => { accept = done }))
    await group.find('[data-quality-run]').trigger('click')
    const signal = vi.mocked(runQualityCheck).mock.calls[0][1]!
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    accept(running)
    await flushPromises()
    await vi.advanceTimersByTimeAsync(10000)
    expect(getQualityRun).toHaveBeenCalledTimes(2)
    expect(vi.getTimerCount()).toBe(0)
  })
})

describe('MonitorSettingsPanel manual and automatic status recovery', () => {
  it('immediately recovers a conflicting automatic run and polls until terminal state', async () => {
    vi.useFakeTimers()
    const group = await render()
    vi.mocked(runQualityCheck).mockRejectedValueOnce({ status: 409, error: 'private upstream error' })
    vi.mocked(getQualityRun).mockResolvedValueOnce({ ...running, source: 'automatic', checked_accounts: 2 })
    await group.find('[data-quality-run]').trigger('click')
    await flushPromises()
    expect(getQualityRun).toHaveBeenCalledTimes(3)
    expect(group.find('[data-quality-run-source]').text()).toBe(en.channelMonitorV2.settings.qualityRunSource.automatic)
    expect(group.find('[data-quality-run-result]').text()).toContain('2 / 3')
    expect(group.find('[data-quality-run-error]').exists()).toBe(false)
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeDefined()
    vi.mocked(getQualityRun).mockResolvedValueOnce({ ...running, source: 'automatic', status: 'completed', checked_accounts: 3 })
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(group.find('[data-quality-run-result]').text()).toContain('Completed')
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).not.toContain('private upstream error')
    expect(runQualityCheck).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('keeps a conflicting null state unavailable across repeated status checks', async () => {
    vi.useFakeTimers()
    const group = await render()
    vi.mocked(runQualityCheck).mockRejectedValueOnce({ response: { status: 409 } })
    await group.find('[data-quality-run]').trigger('click')
    await flushPromises()
    expect(getQualityRun).toHaveBeenCalledTimes(3)
    expect(group.find('[data-quality-run-error]').text()).toContain(en.channelMonitorV2.settings.qualityRunBusyUnavailable)
    expect(group.find('[data-quality-run-result]').exists()).toBe(false)
    expect(group.find('[data-quality-run]').text()).toBe(en.channelMonitorV2.settings.qualityRun)
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeDefined()
    await group.find('[data-quality-run-retry]').trigger('click')
    await flushPromises()
    expect(group.find('[data-quality-run-error]').text()).toContain(en.channelMonitorV2.settings.qualityRunBusyUnavailable)
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeDefined()
    expect(runQualityCheck).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('does not treat an old terminal run as an active task after a conflict', async () => {
    const group = await render()
    vi.mocked(runQualityCheck).mockRejectedValueOnce({ status: 409 })
    vi.mocked(getQualityRun).mockResolvedValueOnce({ ...running, status: 'completed', source: 'manual' })
    await group.find('[data-quality-run]').trigger('click')
    await flushPromises()
    expect(group.find('[data-quality-run-result]').text()).toContain('Completed')
    expect(group.find('[data-quality-run-error]').text()).toContain(en.channelMonitorV2.settings.qualityRunBusyUnavailable)
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeDefined()
  })

  it('locks unknown state after conflict recovery and polling failures until a terminal task is confirmed', async () => {
    vi.useFakeTimers()
    const group = await render()
    vi.mocked(runQualityCheck).mockRejectedValueOnce({ status: 409 })
    vi.mocked(getQualityRun).mockRejectedValueOnce(new Error('private database detail'))
    await group.find('[data-quality-run]').trigger('click')
    await flushPromises()
    expect(group.find('[data-quality-run-result]').exists()).toBe(false)
    expect(group.find('[data-quality-run-error]').text()).toContain(en.channelMonitorV2.settings.qualityRunPollFailed)
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeDefined()
    vi.mocked(getQualityRun).mockResolvedValueOnce({ ...running, source: 'automatic' })
    await group.find('[data-quality-run-retry]').trigger('click')
    await flushPromises()
    vi.mocked(getQualityRun).mockRejectedValueOnce(new Error('private network detail'))
    await vi.advanceTimersByTimeAsync(5000)
    expect(group.find('[data-quality-run-error]').text()).toContain(en.channelMonitorV2.settings.qualityRunPollFailed)
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeDefined()
    expect(vi.getTimerCount()).toBe(0)
    vi.mocked(getQualityRun).mockResolvedValueOnce({ ...running, source: 'automatic', status: 'failed' })
    await group.find('[data-quality-run-retry]').trigger('click')
    await flushPromises()
    expect(group.find('[data-quality-run]').attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).not.toContain('private')
  })

  it('aborts immediate conflict recovery and ignores its response after unmount', async () => {
    vi.useFakeTimers()
    const group = await render()
    vi.mocked(runQualityCheck).mockRejectedValueOnce({ status: 409 })
    let resolve!: (run: GroupQualityRun) => void
    vi.mocked(getQualityRun).mockReturnValueOnce(new Promise(done => { resolve = done }))
    await group.find('[data-quality-run]').trigger('click')
    await flushPromises()
    const signal = vi.mocked(getQualityRun).mock.calls.at(-1)![1]!
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    resolve({ ...running, source: 'automatic' })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(10000)
    expect(getQualityRun).toHaveBeenCalledTimes(3)
    expect(listQuality).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('labels legacy runs without a source as manual', async () => {
    vi.useFakeTimers()
    vi.mocked(getQualityRun).mockImplementation(async id => id === 7 ? running : null)
    const group = await render()
    expect(group.find('[data-quality-run-source]').text()).toBe(en.channelMonitorV2.settings.qualityRunSource.manual)
  })
})
