import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { baseCompile } from '@intlify/message-compiler'
import ChannelMonitorV3QualityPreview from '../ChannelMonitorV3QualityPreview.vue'
import { apiClient } from '@/api/client'
import type { MonitorLatestQuality } from '@/api/channelMonitorV2'
import type { GroupQualityDiagnosticResult } from '@/api/admin/qualityCheck'
import en from '@/i18n/locales/en/channelMonitorV3'

const { copyToClipboard } = vi.hoisted(() => ({ copyToClipboard: vi.fn().mockResolvedValue(true) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard }) }))
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
  vi.stubGlobal('innerHeight', 768)
  vi.stubGlobal('innerWidth', 1024)
  get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: { items: [] } })
  copyToClipboard.mockClear()
})
afterEach(() => {
  wrapper?.unmount()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
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

  it('shows and copies the exact raw text for a result without HTML', async () => {
    const output = '  <svg><script>alert(1)</script></svg>\nunfinished reply  '
    get.mockResolvedValue({ data: { output } })
    const item = { ...latest, status: 'failed' as const, has_artwork: false, has_raw_output: true, reason: 'Artwork generation response was incomplete or truncated' }
    const chip = render({ history: [item] })
    expect(get).not.toHaveBeenCalled()
    await chip.trigger('click')
    await flushPromises()
    expect(get).toHaveBeenCalledWith(expect.stringContaining('/quality-raw-output/'), expect.objectContaining({ params: { group_id: 7 } }))
    expect(dialog()?.textContent).toContain(item.reason)
    expect(dialog()?.querySelector('pre')?.textContent).toBe(output)
    expect(dialog()?.querySelector('script, iframe')).toBeNull()
    expect(dialog()?.querySelector('pre')?.children.length).toBe(0)
    const copy = Array.from(dialog()!.querySelectorAll('button')).find(button => button.textContent === 'Copy raw output')!
    copy.click()
    await flushPromises()
    expect(copyToClipboard).toHaveBeenCalledWith(output)
    expect(dialog()).not.toBeNull()
  })

  it('places a short no-HTML panel below the pointer using its actual height', async () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      return new DOMRect(0, 0, 520, this.getAttribute('role') === 'dialog' ? 160 : 22)
    })
    const chip = render({ history: [{ ...latest, has_artwork: false }] })
    await chip.trigger('mouseenter', { clientX: 200, clientY: 400 })
    await flushPromises()
    expect((dialog() as HTMLElement).style.top).toBe('412px')
  })

  it('does not fetch unavailable raw output for old records', async () => {
    const chip = render({ history: [{ ...latest, has_artwork: false, has_raw_output: false }] })
    await chip.trigger('click')
    await flushPromises()
    expect(get).not.toHaveBeenCalled()
    expect(dialog()?.textContent).toContain('No saved raw output')
    expect(dialog()?.querySelector('pre')).toBeNull()
  })

  it('ignores stale raw replies when switching records', async () => {
    let resolveFirst!: (value: { data: { output: string } }) => void
    get.mockImplementationOnce(() => new Promise(resolve => { resolveFirst = resolve })).mockResolvedValueOnce({ data: { output: 'second reply' } })
    const first = { ...latest, has_artwork: false, has_raw_output: true }
    const second = { ...first, result_id: 'second-id' }
    render({ history: [first, second] })
    await wrapper.findAll('button')[0]!.trigger('mouseenter')
    await wrapper.findAll('button')[1]!.trigger('mouseenter')
    await flushPromises()
    expect(dialog()?.querySelector('pre')?.textContent).toBe('second reply')
    resolveFirst({ data: { output: 'stale reply' } })
    await flushPromises()
    expect(dialog()?.querySelector('pre')?.textContent).toBe('second reply')
  })

  it('offers retry after a raw-output read fails and handles an empty reply', async () => {
    get.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({ data: { output: '' } })
    const chip = render({ history: [{ ...latest, has_artwork: false, has_raw_output: true }] })
    await chip.trigger('click')
    await flushPromises()
    expect(dialog()?.querySelector('[role="alert"]')).not.toBeNull()
    Array.from(dialog()!.querySelectorAll('button')).find(button => button.textContent === 'Retry')!.click()
    await flushPromises()
    expect(dialog()?.querySelector('[role="alert"]')).toBeNull()
    expect(dialog()?.textContent).toContain('No saved raw output')
    expect(dialog()?.querySelector('pre')).toBeNull()
  })

  it('repositions above the pointer after asynchronous raw content grows near the viewport edge', async () => {
    let resolveOutput!: (value: { data: { output: string } }) => void
    let measuredHeight = 120
    get.mockImplementationOnce(() => new Promise(resolve => { resolveOutput = resolve }))
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      return new DOMRect(0, 0, 520, this.getAttribute('role') === 'dialog' ? measuredHeight : 22)
    })
    const chip = render({ history: [{ ...latest, has_artwork: false, has_raw_output: true }] })
    await chip.trigger('mouseenter', { clientX: 900, clientY: 600 })
    await flushPromises()
    expect((dialog() as HTMLElement).style.top).toBe('612px')
    measuredHeight = 350
    resolveOutput({ data: { output: 'received text' } })
    await flushPromises()
    const style = (dialog() as HTMLElement).style
    expect(style.top).toBe('238px')
    expect(Number.parseInt(style.left) + 520).toBeLessThanOrEqual(1008)
  })
})

const noArtwork = { ...latest, has_artwork: false }
const diagnosticResult: GroupQualityDiagnosticResult = {
  result_id: latest.result_id,
  reason: 'Upstream failure',
  diagnostics: {
    stage: 'generation', configured_protocol: 'responses', total_duration_ms: 320,
    generation_duration_ms: 300, generation_timeout_seconds: 180, received_text_bytes: 0,
    received_events: 1, completion_received: false, attempt_count: 2, failure_code: 'upstream_failure',
  },
  attempts: [
    { number: 1, http_status: 429, error_type: 'rate_limit', message: '<script>alert(1)</script>', duration_ms: 120 },
    { number: 2, http_status: 503, failure_code: 'upstream_failure', message: 'Unavailable', duration_ms: 180 },
  ],
  route: { account_id: 23, platform: 'openai', endpoint: '/v1/responses', model: 'chosen-model', proxy_enabled: false },
}
const diagnosticsSection = () => dialog()!.querySelector('section')!
async function requestDiagnostics() {
  (diagnosticsSection().querySelector('button') as HTMLButtonElement).click()
  await flushPromises()
}
function deferredDiagnostics() {
  let resolve!: (value: { data: GroupQualityDiagnosticResult }) => void
  const promise = new Promise<{ data: GroupQualityDiagnosticResult }>(done => { resolve = done })
  return { promise, resolve }
}

describe('admin quality diagnostics', () => {
  it.each([undefined, false])('keeps diagnostics unavailable without opt-in (%s)', async adminDiagnostics => {
    const chip = render({ history: [noArtwork], ...(adminDiagnostics === undefined ? {} : { adminDiagnostics }) })
    await chip.trigger('click')
    await flushPromises()
    expect(diagnosticsSection()).toBeNull()
    expect(get).not.toHaveBeenCalled()
    expect(wrapper.props('adminDiagnostics')).toBe(false)
  })

  it('requires a group even when diagnostics are enabled', async () => {
    await render({ history: [noArtwork], adminDiagnostics: true, groupId: undefined }).trigger('click')
    expect(diagnosticsSection()).toBeNull()
    expect(get).not.toHaveBeenCalled()
  })

  it('loads only on demand, prevents duplicate loads, and renders attempts and route as plain text', async () => {
    const pending = deferredDiagnostics()
    get.mockReturnValueOnce(pending.promise)
    await render({ history: [noArtwork], adminDiagnostics: true }).trigger('mouseenter')
    expect(get).not.toHaveBeenCalled()
    expect(diagnosticsSection().textContent).toContain('View diagnostics')
    await requestDiagnostics()
    expect(diagnosticsSection().querySelector('[role="status"]')?.textContent).toBe('Loading diagnostics')
    expect(diagnosticsSection().querySelector('button')?.disabled).toBe(true)
    await requestDiagnostics()
    expect(get).toHaveBeenCalledTimes(1)
    expect(get).toHaveBeenCalledWith(`/admin/groups/7/quality-check/diagnostics/${latest.result_id}`, { signal: expect.any(AbortSignal) })
    pending.resolve({ data: diagnosticResult })
    await flushPromises()
    const pre = diagnosticsSection().querySelector('pre')!
    expect(pre.textContent).toContain('Attempt count: 2')
    expect(pre.textContent).toContain('Attempt 1\nHTTP status: 429\nError type: rate_limit')
    expect(pre.textContent).toContain('Message: <script>alert(1)</script>')
    expect(pre.textContent).toContain('Attempt 2\nHTTP status: 503')
    expect(pre.textContent).toContain('Failure code: upstream_failure')
    expect(pre.textContent).toContain('Duration (ms): 180')
    expect(pre.textContent).toContain('Completion received: No')
    expect(pre.textContent).toContain('Request route\nAccount ID: 23\nPlatform: openai\nEndpoint: /v1/responses\nModel: chosen-model\nProxy enabled: No')
    expect(pre.children.length).toBe(0)
    expect(diagnosticsSection().querySelector('script, iframe, a')).toBeNull()
    expect(diagnosticsSection().textContent).not.toContain(en.channelMonitorV3.quality.diagnosticsLegacy)
  })

  it('keeps the HTML sandbox while allowing diagnostics on artwork results', async () => {
    get.mockImplementation(async url => ({ data: String(url).includes('/diagnostics/') ? diagnosticResult : { html: '<html><body><script>alert(1)</script><p>Safe artwork</p></body></html>' } }))
    await render({ adminDiagnostics: true }).trigger('click')
    await flushPromises()
    const frame = dialog()!.querySelector('iframe')!
    expect(frame.getAttribute('sandbox')).toBe('')
    expect(frame.getAttribute('srcdoc')).not.toContain('<script>')
    await requestDiagnostics()
    expect(diagnosticsSection().textContent).toContain('Attempt count: 2')
  })

  it('supports retry after a failure without displaying the API error', async () => {
    get.mockRejectedValueOnce(new Error('private network details')).mockResolvedValueOnce({ data: diagnosticResult })
    await render({ history: [noArtwork], adminDiagnostics: true }).trigger('click')
    await requestDiagnostics()
    expect(diagnosticsSection().querySelector('[role="alert"]')?.textContent).toBe(en.channelMonitorV3.quality.diagnosticsFailed)
    expect(dialog()?.textContent).not.toContain('private network details')
    expect(diagnosticsSection().querySelector('button')?.textContent?.trim()).toBe('Retry')
    await requestDiagnostics()
    expect(diagnosticsSection().querySelector('[role="alert"]')).toBeNull()
    expect(diagnosticsSection().textContent).toContain('Attempt count: 2')
  })

  it.each([{ attempts: undefined }, { attempts: null }, { attempts: [] }])('explains missing upstream details for old records ($attempts)', async ({ attempts }) => {
    get.mockResolvedValueOnce({ data: { ...diagnosticResult, diagnostics: null, attempts, route: undefined } })
    await render({ history: [noArtwork], adminDiagnostics: true }).trigger('click')
    await requestDiagnostics()
    expect(diagnosticsSection().textContent).toContain(en.channelMonitorV3.quality.diagnosticsLegacy)
    expect(diagnosticsSection().textContent).toContain('Attempt count: —')
    expect(diagnosticsSection().textContent).not.toContain('Request route')
  })

  it('aborts switched results and ignores stale replies after the next result has loaded', async () => {
    const pending = deferredDiagnostics()
    get.mockReturnValueOnce(pending.promise).mockResolvedValueOnce({ data: { ...diagnosticResult, result_id: 'second-id', reason: 'Current result' } })
    render({ history: [noArtwork, { ...noArtwork, result_id: 'second-id' }], adminDiagnostics: true })
    await wrapper.findAll('button')[0]!.trigger('mouseenter')
    await requestDiagnostics()
    const signal = get.mock.calls[0]?.[1]?.signal
    await wrapper.findAll('button')[1]!.trigger('mouseenter')
    expect(signal?.aborted).toBe(true)
    expect(get).toHaveBeenCalledTimes(1)
    expect(diagnosticsSection().querySelector('pre')).toBeNull()
    await requestDiagnostics()
    expect(diagnosticsSection().textContent).toContain('Current result')
    pending.resolve({ data: { ...diagnosticResult, reason: 'Stale result' } })
    await flushPromises()
    expect(diagnosticsSection().textContent).toContain('Current result')
    expect(diagnosticsSection().textContent).not.toContain('Stale result')
  })

  it.each(['group', 'close', 'unmount', 'permission'] as const)('invalidates pending diagnostics on %s', async action => {
    const pending = deferredDiagnostics()
    get.mockReturnValueOnce(pending.promise)
    await render({ history: [noArtwork], adminDiagnostics: true }).trigger('click')
    await requestDiagnostics()
    const signal = get.mock.calls[0]?.[1]?.signal
    if (action === 'group') await wrapper.setProps({ groupId: 8 })
    if (action === 'close') (dialog()!.querySelector('button[aria-label="Close preview"]') as HTMLButtonElement).click()
    if (action === 'unmount') wrapper.unmount()
    if (action === 'permission') await wrapper.setProps({ adminDiagnostics: false })
    await flushPromises()
    expect(signal?.aborted).toBe(true)
    pending.resolve({ data: diagnosticResult })
    await flushPromises()
    expect(document.body.textContent).not.toContain('Account ID: 23')
    if (action === 'group' || action === 'close') {
      await wrapper.find('button').trigger('click')
      expect(diagnosticsSection().querySelector('pre')).toBeNull()
      expect(get).toHaveBeenCalledTimes(1)
    }
  })
})
