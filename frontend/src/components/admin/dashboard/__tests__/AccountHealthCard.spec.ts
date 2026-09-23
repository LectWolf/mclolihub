import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import AccountHealthCard from '../AccountHealthCard.vue'
import type { AccountHealthStats } from '@/types'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key} ${JSON.stringify(params)}` : key
    })
  }
})

const RouterLinkStub = {
  props: ['to'],
  template: '<a :data-to="JSON.stringify(to)"><slot /></a>'
}

const health = (overrides: Partial<AccountHealthStats> = {}): AccountHealthStats => ({
  available: 0,
  rate_limited: 0,
  temp_unschedulable: 0,
  unschedulable: 0,
  error: 0,
  balance_insufficient: 0,
  inactive: 0,
  other: 0,
  ...overrides
})

const mountCard = (props: Record<string, unknown>) =>
  mount(AccountHealthCard, {
    props: {
      health: null,
      total: 0,
      normalFallback: 0,
      errorFallback: 0,
      overloaded: 0,
      ...props
    },
    global: {
      stubs: {
        RouterLink: RouterLinkStub,
        HelpTooltip: true,
        Icon: true
      }
    }
  })

describe('AccountHealthCard', () => {
  it('links each non-empty state to the filtered account list', () => {
    const wrapper = mountCard({
      health: health({ available: 7, rate_limited: 2, error: 1, other: 1 }),
      total: 11
    })

    expect(wrapper.get('[data-testid="account-available"]').text()).toContain('7')
    expect(wrapper.get('[data-testid="account-available"]').text()).toContain('/ 11')

    const rateLimited = wrapper.get('[data-testid="account-health-rate_limited"]')
    expect(JSON.parse(rateLimited.attributes('data-to') || '{}')).toEqual({
      path: '/admin/accounts',
      query: { status: 'rate_limited' }
    })
    expect(rateLimited.text()).toContain('2')

    // 「其他」没有对应的列表筛选，不渲染为链接
    const other = wrapper.get('[data-testid="account-health-other"]')
    expect(other.element.tagName).toBe('SPAN')

    // 为 0 的分类不出现在图例中
    expect(wrapper.find('[data-testid="account-health-temp_unschedulable"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="account-health-inactive"]').exists()).toBe(false)
  })

  it('keeps the schedulable state visible even when it is zero', () => {
    const wrapper = mountCard({ health: health({ error: 3 }), total: 3 })

    expect(wrapper.find('[data-testid="account-health-available"]').exists()).toBe(true)
    const bar = wrapper.get('[data-testid="account-health-bar"]')
    expect(bar.findAll('div')).toHaveLength(1)
  })

  it('shows the overload hint only when accounts are overloaded', () => {
    expect(mountCard({ health: health({ available: 1 }), total: 1 }).find('[data-testid="account-overloaded"]').exists()).toBe(false)
    const wrapper = mountCard({ health: health({ available: 2 }), total: 2, overloaded: 1 })
    expect(wrapper.get('[data-testid="account-overloaded"]').text()).toContain('admin.dashboard.overloadedHint')
  })

  it('falls back to legacy counters when the backend has no health breakdown', () => {
    const wrapper = mountCard({ health: null, total: 9, normalFallback: 6, errorFallback: 2 })

    expect(wrapper.get('[data-testid="account-available"]').text()).toContain('6')
    expect(wrapper.text()).toContain('admin.dashboard.activeOfTotal')
    expect(wrapper.text()).toContain('admin.dashboard.errorAccountsFallback')
    expect(wrapper.find('[data-testid="account-health-bar"]').exists()).toBe(false)
  })
})
