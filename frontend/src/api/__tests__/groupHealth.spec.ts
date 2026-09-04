import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../client'
import { listAdminGroupHealth, listGroupHealth } from '../groupHealth'

afterEach(() => vi.restoreAllMocks())

describe('group health API', () => {
  it('loads the user-visible health list', async () => {
    const payload = { items: [], window_hours: 6, bucket_minutes: 5 }
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: payload })
    const controller = new AbortController()

    await expect(listGroupHealth(controller.signal)).resolves.toEqual(payload)
    expect(get).toHaveBeenCalledWith('/group-health', { signal: controller.signal, params: undefined })
  })

  it('requests a compact probe trend window', async () => {
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: { items: [], window_hours: 2, bucket_minutes: 10 } })
    await listGroupHealth(undefined, { trendHours: 2, trend: 'probe' })
    expect(get).toHaveBeenCalledWith('/group-health', {
      signal: undefined,
      params: { trend_hours: 2, trend: 'probe' },
    })
  })

  it('loads the administrator health list', async () => {
    const payload = { items: [], window_hours: 6, bucket_minutes: 5 }
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: payload })

    await expect(listAdminGroupHealth()).resolves.toEqual(payload)
    expect(get).toHaveBeenCalledWith('/admin/groups/health', { signal: undefined })
  })
})
