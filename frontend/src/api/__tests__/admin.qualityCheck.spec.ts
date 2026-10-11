import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../client'
import { listQuality, saveQualitySettings, setQualityEnabled } from '../admin/qualityCheck'

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
