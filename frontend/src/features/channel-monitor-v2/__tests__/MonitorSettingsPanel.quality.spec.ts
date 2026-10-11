import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { baseCompile } from '@intlify/message-compiler'
import MonitorSettingsPanel from '../MonitorSettingsPanel.vue'
import { listQuality, saveQualitySettings, type GroupQualityStatus } from '@/api/admin/qualityCheck'
import { getConfig, updateConfig } from '@/api/channelMonitorV2'
import en from '@/i18n/locales/en/channelMonitorV2'
const compiledEn = JSON.parse(JSON.stringify(en), (_key, value) => typeof value === 'string' ? new Function(`return ${baseCompile(value, { mode: 'arrow' }).code}`)() : value)

vi.mock('@/api/admin/qualityCheck', () => ({ listQuality: vi.fn(), saveQualitySettings: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: { getAllIncludingInactive: vi.fn(async () => [
  { id: 7, name: 'Group A', platform: 'openai' }, { id: 8, name: 'Group B', platform: 'openai' },
]) } } }))
vi.mock('@/api/channelMonitorV2', async importOriginal => ({
  ...await importOriginal<typeof import('@/api/channelMonitorV2')>(),
  getConfig: vi.fn(), updateConfig: vi.fn(),
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
  vi.mocked(saveQualitySettings).mockImplementation(async (id, payload) => ({ ...status, ...payload, group_id: id }))
  vi.mocked(getConfig).mockResolvedValue({
    version: 1, enabled: true, refresh_interval_seconds: 60, platforms: [], group_ids: [],
    health_thresholds: { minimum_sample: 50, warning_error_rate: 0.05, critical_error_rate: 0.2, target_ttft_ms: 3000,
      warning_ttft_ms: 3000, critical_ttft_ms: 10000, warning_cache_rate: 0.85, critical_cache_rate: 0.6,
      error_weight: 0.6, ttft_weight: 0.2, cache_weight: 0.2 },
  })
})
afterEach(() => wrapper?.unmount())

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
