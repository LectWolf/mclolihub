import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../client'
import { getQualityRun, listQuality, runQualityCheck, saveQualitySettings, setQualityEnabled } from '../admin/qualityCheck'

afterEach(() => vi.restoreAllMocks())

describe('admin quality configuration API', () => {
  it('keeps the legacy enabled-only payload without resetting omitted thresholds', async () => {
    const status = { group_id: 7, enabled: false, min_spend_usd: 12.5, min_interval_minutes: 60, model_id: 'chosen-model', default_prompt: 'Fixed prompt' }
    const put = vi.spyOn(apiClient, 'put').mockResolvedValue({ data: status })
    expect(await setQualityEnabled(7, false)).toEqual(status)
    expect(put).toHaveBeenCalledWith('/admin/groups/7/quality-check', { enabled: false })
  })

  it('saves settings and returns complete status without sending the read-only prompt', async () => {
    const payload = { enabled: true, min_spend_usd: 15, min_interval_minutes: 90, model_id: '' }
    const status = { group_id: 7, ...payload, status: 'healthy', default_prompt: 'Fixed prompt' }
    const put = vi.spyOn(apiClient, 'put').mockResolvedValue({ data: status })
    expect(await saveQualitySettings(7, payload)).toEqual(status)
    expect(put).toHaveBeenCalledWith('/admin/groups/7/quality-check', payload)
  })

  it('preserves omitted settings and reads the configured thresholds and local prompt', async () => {
    const status = { group_id: 7, enabled: true, min_spend_usd: 9, min_interval_minutes: 60, model_id: '', default_prompt: 'Fixed prompt' }
    const put = vi.spyOn(apiClient, 'put').mockResolvedValue({ data: status })
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: [status] })
    await saveQualitySettings(7, { enabled: true, model_id: '' })
    expect(put).toHaveBeenCalledWith('/admin/groups/7/quality-check', { enabled: true, model_id: '' })
    expect(await listQuality()).toEqual([status])
    expect(get).toHaveBeenCalledWith('/admin/groups/quality-check')
  })
})

describe('admin manual quality run API', () => {
  const run = { run_id: 'run-7', group_id: 7, status: 'running', reason: '', checked_accounts: 0, total_accounts: 3, started_at: '2026-08-08T10:00:00Z' }

  it('submits a run without a body and passes the abort signal', async () => {
    const signal = new AbortController().signal
    const post = vi.spyOn(apiClient, 'post').mockResolvedValue({ data: run })
    expect(await runQualityCheck(7, signal)).toEqual(run)
    expect(post).toHaveBeenCalledWith('/admin/groups/7/quality-check/run', undefined, { signal })
  })

  it('reads the current run including latest quality for the group preview', async () => {
    const signal = new AbortController().signal
    const result = { ...run, status: 'completed', latest_quality: { result_id: 'result-7', status: 'success', reason: 'Passed', model_id: 'saved-model', checked_at: run.started_at, has_preview: true } }
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: result })
    expect(await getQualityRun(7, signal)).toEqual(result)
    expect(get).toHaveBeenCalledWith('/admin/groups/7/quality-check/run', { signal })
  })

  it('preserves a null response when no manual run exists', async () => {
    vi.spyOn(apiClient, 'get').mockResolvedValue({ data: null })
    expect(await getQualityRun(7)).toBeNull()
  })

  it.each([400, 409])('leaves HTTP %s rejection available to the panel', async status => {
    const error = { response: { status } }
    vi.spyOn(apiClient, 'post').mockRejectedValue(error)
    await expect(runQualityCheck(7)).rejects.toBe(error)
  })

  it('passes cancellation through quality status refresh', async () => {
    const signal = new AbortController().signal
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: [] })
    expect(await listQuality(signal)).toEqual([])
    expect(get).toHaveBeenCalledWith('/admin/groups/quality-check', { signal })
  })
})
