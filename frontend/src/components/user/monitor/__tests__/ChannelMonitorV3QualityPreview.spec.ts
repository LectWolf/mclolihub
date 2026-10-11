import { Blob as NodeBlob } from 'node:buffer'
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
  model_id: 'selected-model', checked_at: '2026-10-11T03:00:00Z', has_preview: true,
}
// A real 1x1 PNG, rather than accepting arbitrary bytes as a preview.
const png = () => new NodeBlob([Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jN1sAAAAASUVORK5CYII=', 'base64')], { type: 'image/png' })
let wrapper: VueWrapper
let get = vi.spyOn(apiClient, 'get')
let createUrl = vi.fn()
let revokeUrl = vi.fn()
const adapter = apiClient.defaults.adapter
function render(overrides: Record<string, unknown> = {}) {
  wrapper = mount(ChannelMonitorV3QualityPreview, {
    attachTo: document.body,
    props: { groupId: 7, enabled: true, latest, ...overrides },
    global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: compiledEn } })] },
  })
  return wrapper.find('button')
}
const dialog = () => document.querySelector('[role="dialog"]')!
beforeEach(() => {
  vi.stubGlobal('Blob', NodeBlob)
  get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: png() })
  createUrl = vi.fn().mockReturnValue('blob:latest-image')
  revokeUrl = vi.fn()
  vi.stubGlobal('URL', class extends URL {
    static createObjectURL = createUrl
    static revokeObjectURL = revokeUrl
  })
})
afterEach(() => {
  wrapper?.unmount()
  apiClient.defaults.adapter = adapter
  localStorage.removeItem('auth_token')
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('ChannelMonitorV3QualityPreview', () => {
  it('loads only on hover and includes the permission group query, blob response and abort signal', async () => {
    render()
    expect(get).not.toHaveBeenCalled()
    await wrapper.trigger('mouseenter')
    await flushPromises()
    expect(get).toHaveBeenCalledWith(`/channel-monitor-v2/quality-preview/${latest.result_id}`, expect.objectContaining({
      params: { group_id: 7 }, responseType: 'blob', signal: expect.any(AbortSignal),
    }))
    expect(dialog().querySelector('img')?.getAttribute('src')).toBe('blob:latest-image')
    expect(dialog().querySelector('img')?.getAttribute('alt')).toContain('selected-model')
    expect(dialog().textContent).toContain(latest.model_id)
    expect(dialog().textContent).toContain(latest.reason)
    expect(dialog().querySelector('time')?.getAttribute('datetime')).toBe(latest.checked_at)
    wrapper.unmount()
    expect(revokeUrl).toHaveBeenCalledWith('blob:latest-image')
  })

  it('runs the shared Axios auth interceptor for keyboard-focus preview loading', async () => {
    get.mockRestore()
    localStorage.setItem('auth_token', 'test-bearer')
    const request = vi.fn(async config => ({ data: png(), status: 200, statusText: 'OK', headers: {}, config }))
    apiClient.defaults.adapter = request
    const button = render()
    await button.trigger('focus')
    await flushPromises()
    expect(request).toHaveBeenCalledOnce()
    const config = request.mock.calls[0][0]
    expect(config.headers.Authorization).toBe('Bearer test-bearer')
    expect(config.params.group_id).toBe(7)
    expect(config.responseType).toBe('blob')
    expect(button.attributes('aria-expanded')).toBe('true')
    expect(dialog().querySelector('img')).not.toBeNull()
    await button.trigger('keydown', { key: 'Escape' })
    expect(document.querySelector('[role="dialog"]')).toBeNull()
  })

  it('supports mobile click opening and closing', async () => {
    const button = render()
    await button.trigger('click')
    await flushPromises()
    expect(button.attributes('aria-expanded')).toBe('true')
    await button.trigger('click')
    expect(button.attributes('aria-expanded')).toBe('false')
    expect(revokeUrl).toHaveBeenCalledOnce()
  })

  it.each(['failed', 'unknown'] as const)('clears the old image immediately and renders the latest %s result, not the last pass', async status => {
    const button = render()
    await button.trigger('focus')
    await flushPromises()
    await wrapper.setProps({ latest: { ...latest, result_id: 'new-result', status, reason: 'Request failed', has_preview: false } })
    expect(revokeUrl).toHaveBeenCalledWith('blob:latest-image')
    expect(dialog().querySelector('img')).toBeNull()
    expect(button.classes().join(' ')).toContain(status === 'failed' ? 'amber' : 'gray')
    expect(button.text()).toContain(en.channelMonitorV3.quality.status[status])
    expect(dialog().textContent).toContain(en.channelMonitorV3.quality.noPreview)
    expect(dialog().textContent).toContain('Request failed')
    expect(get).toHaveBeenCalledOnce()
  })

  it('aborts old requests and ignores late resolution when latest result changes', async () => {
    let resolveOld!: (response: { data: NodeBlob }) => void
    get.mockReturnValueOnce(new Promise(resolve => { resolveOld = resolve }))
    const button = render()
    await button.trigger('focus')
    const oldSignal = get.mock.calls[0][1]?.signal
    await wrapper.setProps({ latest: { ...latest, result_id: 'new-result', status: 'degraded' } })
    await flushPromises()
    expect(oldSignal?.aborted).toBe(true)
    expect(get).toHaveBeenLastCalledWith('/channel-monitor-v2/quality-preview/new-result', expect.objectContaining({ params: { group_id: 7 } }))
    expect(createUrl).toHaveBeenCalledOnce()
    resolveOld({ data: png() })
    await flushPromises()
    expect(createUrl).toHaveBeenCalledOnce()
    expect(dialog().querySelector('img')?.getAttribute('src')).toBe('blob:latest-image')
    expect(button.classes().join(' ')).toContain('red')
  })

  it('aborts a pending request on unmount without creating a late blob URL', async () => {
    let resolve!: (response: { data: NodeBlob }) => void
    get.mockReturnValueOnce(new Promise(done => { resolve = done }))
    await render().trigger('focus')
    const signal = get.mock.calls[0][1]?.signal
    wrapper.unmount()
    expect(signal?.aborted).toBe(true)
    resolve({ data: png() })
    await flushPromises()
    expect(createUrl).not.toHaveBeenCalled()
  })

  it.each(['failed', 'unknown', 'skipped'] as const)('renders %s independently without a healthy color or fake preview', async status => {
    const button = render({ latest: { ...latest, status, has_preview: false } })
    await button.trigger('focus')
    expect(button.text()).toContain(en.channelMonitorV3.quality.status[status])
    expect(button.classes().join(' ')).not.toMatch(/emerald|green/)
    expect(dialog().textContent).toContain(en.channelMonitorV3.quality.noPreview)
    expect(get).not.toHaveBeenCalled()
  })

  it('renders enabled groups awaiting their first result and hides disabled groups', async () => {
    const button = render({ latest: null })
    expect(button.text()).toContain(en.channelMonitorV3.quality.status.pending)
    await button.trigger('focus')
    expect(get).not.toHaveBeenCalled()
    await wrapper.setProps({ enabled: false })
    expect(wrapper.find('button').exists()).toBe(false)
    expect(document.querySelector('[role="dialog"]')).toBeNull()
  })

  it.each([
    ['HTML', () => new NodeBlob(['<html>Not a PNG</html>'], { type: 'text/html' })],
    ['JSON', () => new NodeBlob(['{"error":"private"}'], { type: 'application/json' })],
    ['empty', () => new NodeBlob([], { type: 'image/png' })],
    ['wrong signature', () => new NodeBlob(['not-a-real-png'], { type: 'image/png' })],
  ])('rejects %s responses, shows a translated error and supports retry', async (_, invalidBlob) => {
    get.mockResolvedValueOnce({ data: invalidBlob() })
    await render().trigger('focus')
    await flushPromises()
    expect(dialog().querySelector('[role="alert"]')?.textContent).toContain(en.channelMonitorV3.quality.previewFailed)
    expect(createUrl).not.toHaveBeenCalled()
    dialog().querySelector<HTMLButtonElement>('[role="alert"] button')!.click()
    await flushPromises()
    expect(get).toHaveBeenCalledTimes(2)
    expect(dialog().querySelector('img')).not.toBeNull()
  })

  it('offers retry if the browser cannot decode the PNG', async () => {
    await render().trigger('focus')
    await flushPromises()
    dialog().querySelector('img')!.dispatchEvent(new Event('error'))
    await flushPromises()
    expect(revokeUrl).toHaveBeenCalledOnce()
    expect(dialog().querySelector('img')).toBeNull()
    expect(dialog().querySelector('[role="alert"] button')).not.toBeNull()
  })
})

it('fits the preview to a narrow viewport without covering its trigger', async () => {
  vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(320)
  vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(600)
  const button = render()
  vi.spyOn(button.element, 'getBoundingClientRect').mockReturnValue({
    top: 300, bottom: 324, left: 250, right: 310, width: 60, height: 24, x: 250, y: 300, toJSON: () => ({}),
  })
  await button.trigger('focus')
  await flushPromises()
  const preview = dialog() as HTMLElement
  expect(Number.parseFloat(preview.style.width)).toBe(288)
  expect(Number.parseFloat(preview.style.left)).toBe(16)
  expect(Number.parseFloat(preview.style.top) + Number.parseFloat(preview.style.maxHeight)).toBeLessThan(300)
})

it('shows retry on an HTTP failure without displaying internal error text', async () => {
  get.mockRejectedValueOnce(new Error('internal account id 555'))
  await render().trigger('focus')
  await flushPromises()
  expect(dialog().querySelector('[role="alert"] button')).not.toBeNull()
  expect(dialog().textContent).not.toContain('555')
  expect(createUrl).not.toHaveBeenCalled()
})

it('keeps the image when polling returns a new object for the same result', async () => {
  await render().trigger('focus')
  await flushPromises()
  await wrapper.setProps({ latest: { ...latest } })
  await flushPromises()
  expect(get).toHaveBeenCalledOnce()
  expect(revokeUrl).not.toHaveBeenCalled()
  expect(dialog().querySelector('img')?.getAttribute('src')).toBe('blob:latest-image')
})
