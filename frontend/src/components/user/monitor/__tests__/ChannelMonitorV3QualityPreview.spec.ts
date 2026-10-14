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
  model_id: 'selected-model', checked_at: '2026-10-11T03:00:00Z', has_preview: true, has_artwork: true,
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
  it('opens an HTML output popover with a blue output lamp and no scoring text', async () => {
    const chip = render()
    expect(get).not.toHaveBeenCalled()
    expect(chip.text()).toMatch(/\d{2}:\d{2}/)
    expect(chip.find('time').attributes('datetime')).toBe(latest.checked_at)
    expect(chip.find('span').classes()).toContain('bg-blue-500')
    await chip.trigger('click')
    await flushPromises()
    expect(chip.attributes('aria-expanded')).toBe('true')
    expect(dialog()?.textContent).toContain(en.channelMonitorV3.quality.htmlOutput)
    expect(dialog()?.textContent).toContain(latest.model_id)
    expect(dialog()?.textContent).not.toContain(latest.reason)
    expect(dialog()?.textContent).not.toContain('Score')
    expect(dialog()?.querySelector('img, textarea')).toBeNull()
    await chip.trigger('keydown', { key: 'Escape' })
    await flushPromises()
    expect(dialog()).toBeNull()
  })

  it('does not show a blue lamp when the result has no HTML output', async () => {
    const item = { ...latest, has_artwork: false }
    const chip = render({ history: [item], latest: item })
    expect(chip.find('span').classes()).toContain('bg-gray-400')
    await chip.trigger('focus')
    await flushPromises()
    expect(dialog()?.textContent).toContain(en.channelMonitorV3.quality.noPreview)
  })

  it('does not fetch when history is provided empty, and hides disabled groups', async () => {
    render({ latest: null, history: [] })
    expect(wrapper.text()).toContain(en.channelMonitorV3.quality.status.pending)
    expect(wrapper.text()).toContain('HTML output · 0')
    expect(get).not.toHaveBeenCalled()
    await wrapper.setProps({ enabled: false })
    expect(wrapper.find('button, p').exists()).toBe(false)
    expect(dialog()).toBeNull()
  })
})
