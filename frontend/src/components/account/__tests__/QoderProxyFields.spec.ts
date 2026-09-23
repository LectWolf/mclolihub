import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import QoderProxyFields from '../QoderProxyFields.vue'

const { startQoderOAuth, pollQoderOAuth, copyToClipboard } = vi.hoisted(() => ({
  startQoderOAuth: vi.fn(),
  pollQoderOAuth: vi.fn(),
  copyToClipboard: vi.fn()
}))

vi.mock('@/api/admin/accounts', () => ({
  startQoderOAuth,
  pollQoderOAuth
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copied: ref(false), copyToClipboard })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

type Props = InstanceType<typeof QoderProxyFields>['$props']

const mountFields = (props: Partial<Props> = {}) =>
  mount(QoderProxyFields, {
    props: {
      mode: 'create',
      personalToken: '',
      machineId: '',
      region: 'cn',
      ...props
    } as Props
  })

const summary = (wrapper: ReturnType<typeof mountFields>) =>
  wrapper.get('[data-test="qoder-credential-summary"]').text()

describe('QoderProxyFields', () => {
  beforeEach(() => {
    startQoderOAuth.mockReset()
    pollQoderOAuth.mockReset()
    copyToClipboard.mockReset()
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('summarizes which credential the account will use', async () => {
    const wrapper = mountFields()
    expect(summary(wrapper)).toBe('admin.accounts.qoderProxy.credentialRequired')

    await wrapper.setProps({ personalToken: 'pt-1' })
    expect(summary(wrapper)).toBe('admin.accounts.qoderProxy.credentialSummaryPAT')

    await wrapper.setProps({ deviceLoginUser: 'ada@example.com' })
    expect(summary(wrapper)).toBe('admin.accounts.qoderProxy.credentialSummaryBoth')
    expect(wrapper.get('[data-test="qoder-logged-in"]').exists()).toBe(true)

    await wrapper.setProps({ personalToken: '' })
    expect(summary(wrapper)).toBe('admin.accounts.qoderProxy.credentialSummaryDevice')
  })

  it('counts a saved personal token in edit mode', () => {
    const wrapper = mountFields({ mode: 'edit', hasSavedPersonalToken: true })
    expect(summary(wrapper)).toBe('admin.accounts.qoderProxy.credentialSummaryPAT')
    expect(wrapper.text()).toContain('admin.accounts.qoderProxy.personalTokenSaved')
  })

  it('emits edition changes and keeps the machine id under advanced settings', async () => {
    const wrapper = mountFields({ machineId: 'machine-1' })

    await wrapper.get('[data-test="qoder-region-global"]').trigger('click')
    expect(wrapper.emitted('update:region')?.[0]).toEqual(['global'])
    expect(wrapper.get('[data-test="qoder-region-cn"]').attributes('aria-checked')).toBe('true')

    expect(wrapper.find('#qoder-machine-id-input').exists()).toBe(false)
    await wrapper.get('[data-test="qoder-advanced-toggle"]').trigger('click')
    const input = wrapper.get('#qoder-machine-id-input')
    await input.setValue('machine-2')
    expect(wrapper.emitted('update:machineId')?.at(-1)).toEqual(['machine-2'])
  })

  it('polls the device login and emits the token once Qoder confirms', async () => {
    vi.useFakeTimers()
    vi.spyOn(window, 'open').mockReturnValue(null)
    startQoderOAuth.mockResolvedValue({
      login_url: 'https://qoder.com.cn/device/selectAccounts?nonce=n1',
      nonce: 'n1',
      verifier: 'v1',
      machine_id: 'machine-9',
      region: 'cn'
    })
    pollQoderOAuth
      .mockResolvedValueOnce({ status: 'pending' })
      .mockResolvedValueOnce({
        status: 'ok',
        access_token: 'dt-1',
        refresh_token: 'drt-1',
        user_id: 'u-1',
        email: 'ada@example.com',
        expires_at: '2026-10-01T00:00:00Z',
        region: 'cn'
      })
    const wrapper = mountFields({ proxyId: 3 })

    await wrapper.get('[data-test="qoder-login-start"]').trigger('click')
    await flushPromises()
    expect(startQoderOAuth).toHaveBeenCalledWith('cn', '')
    expect(wrapper.emitted('update:machineId')?.[0]).toEqual(['machine-9'])
    expect(wrapper.get('[data-test="qoder-login-status"]').text()).toBe('admin.accounts.qoderProxy.oauthWaiting')

    await vi.advanceTimersByTimeAsync(2000)
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()

    expect(pollQoderOAuth).toHaveBeenCalledTimes(2)
    expect(pollQoderOAuth).toHaveBeenLastCalledWith('cn', 'n1', 'v1', 3)
    expect(wrapper.emitted('oauth')?.[0]?.[0]).toMatchObject({
      accessToken: 'dt-1',
      refreshToken: 'drt-1',
      email: 'ada@example.com',
      machineId: 'machine-9',
      region: 'cn'
    })
    expect(wrapper.get('[data-test="qoder-login-status"]').text()).toBe('admin.accounts.qoderProxy.oauthSuccess')
  })

  it('stops polling when Qoder rejects the login session', async () => {
    vi.useFakeTimers()
    vi.spyOn(window, 'open').mockReturnValue(null)
    startQoderOAuth.mockResolvedValue({ login_url: 'https://qoder.com/x', nonce: 'n', verifier: 'v', machine_id: '', region: 'global' })
    pollQoderOAuth.mockRejectedValue({ status: 400, message: 'Qoder login was rejected' })
    const wrapper = mountFields({ region: 'global' })

    await wrapper.get('[data-test="qoder-login-start"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    await vi.advanceTimersByTimeAsync(10000)

    expect(pollQoderOAuth).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-test="qoder-login-status"]').text()).toBe('Qoder login was rejected')
    expect(wrapper.emitted('oauth')).toBeUndefined()
  })
})
