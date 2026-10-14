import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { baseCompile } from '@intlify/message-compiler'
import ChannelMonitorV3QualityPreview from '../ChannelMonitorV3QualityPreview.vue'
import { apiClient } from '@/api/client'
import type { MonitorLatestQuality } from '@/api/channelMonitorV2'
import en from '@/i18n/locales/en/channelMonitorV3'

const compiledEn = JSON.parse(JSON.stringify(en), (_key, value) => typeof value === 'string' ? new Function(`return ${baseCompile(value, { mode: 'arrow' }).code}`)() : value)
const latest: MonitorLatestQuality = {
  result_id: '123e4567-e89b-12d3-a456-426614174000', status: 'success', reason: 'Animation meets rubric',
  model_id: 'selected-model', checked_at: '2026-10-11T03:00:00Z', has_preview: true, score: 86,
}
let wrapper: VueWrapper
let get = vi.spyOn(apiClient, 'get')

function render(overrides: Record<string, unknown> = {}) {
  wrapper = mount(ChannelMonitorV3QualityPreview, {
    attachTo: document.body,
    props: { groupId: 7, enabled: true, latest, history: [latest], ...overrides },
    global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: compiledEn } })] },
  })
  return wrapper.find('button')
}
const dialog = () => document.querySelector('[role="dialog"]')

beforeEach(() => {
  get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: { items: [] } })
})
afterEach(() => {
  wrapper?.unmount()
  vi.restoreAllMocks()
})

describe('ChannelMonitorV3QualityPreview', () => {
  it('opens a score popover from a history chip and does not show diagnostics, reasons, or PNG', async () => {
    const chip = render()
    expect(get).not.toHaveBeenCalled()
    expect(chip.text()).toMatch(/\d{2}:\d{2}/)
    expect(chip.find('time').attributes('datetime')).toBe(latest.checked_at)
    await chip.trigger('click')
    await flushPromises()
    expect(chip.attributes('aria-expanded')).toBe('true')
    expect(dialog()?.textContent).toContain(en.channelMonitorV3.quality.verdict.pass)
    expect(dialog()?.textContent).toContain('86')
    expect(dialog()?.textContent).toContain(latest.model_id)
    expect(dialog()?.textContent).not.toContain(latest.reason)
    expect(dialog()?.textContent).not.toContain(en.channelMonitorV3.quality.copyDiagnostics)
    expect(dialog()?.querySelector('img, textarea')).toBeNull()
    await chip.trigger('keydown', { key: 'Escape' })
    await flushPromises()
    expect(dialog()).toBeNull()
  })

  it('keeps a missing score neutral instead of inventing a pass or a numeric score', async () => {
    const chip = render({ history: [{ ...latest, status: 'success', score: null }] })
    await chip.trigger('focus')
    await flushPromises()
    expect(dialog()?.textContent).toContain(en.channelMonitorV3.quality.verdict.unscored)
    expect(dialog()?.querySelector('dd')?.textContent).toBe('—')
    expect(dialog()?.textContent).not.toContain(en.channelMonitorV3.quality.verdict.pass)
  })

  it('does not fetch when history is provided empty, and hides disabled groups', async () => {
    render({ latest: null, history: [] })
    expect(wrapper.text()).toContain(en.channelMonitorV3.quality.status.pending)
    expect(wrapper.text()).toContain('Check history · 0')
    expect(get).not.toHaveBeenCalled()
    await wrapper.setProps({ enabled: false })
    expect(wrapper.find('button, p').exists()).toBe(false)
    expect(dialog()).toBeNull()
  })

  it('fits the preview to a narrow viewport without covering its trigger', async () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(320)
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(600)
    const chip = render()
    vi.spyOn(chip.element, 'getBoundingClientRect').mockReturnValue({
      top: 300, bottom: 324, left: 250, right: 310, width: 60, height: 24, x: 250, y: 300, toJSON: () => ({}),
    })
    await chip.trigger('focus')
    await flushPromises()
    const preview = dialog() as HTMLElement
    expect(Number.parseFloat(preview.style.width)).toBe(288)
    expect(Number.parseFloat(preview.style.left)).toBe(16)
    expect(Number.parseFloat(preview.style.top) + Number.parseFloat(preview.style.maxHeight)).toBeLessThan(300)
  })
})
