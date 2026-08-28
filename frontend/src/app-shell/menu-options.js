import { h } from 'vue'
import { RouterLink } from 'vue-router'
import { NIcon, NText } from 'naive-ui'
import {
  AlarmOutline,
  DiamondOutline,
  ExpandOutline,
  FlaskOutline,
  LogoGithub,
  NewspaperOutline,
  SettingsOutline,
  SparklesOutline,
  StarOutline,
} from '@vicons/ionicons5'
import { ReportAnalytics, ReportMoney } from '@vicons/tabler'
import { BrowserOpenURL, EventsEmit } from '../services/browser-runtime.mjs'
import { formatGitHubVersionLabel } from './version-label.js'
import {RESEARCH_CENTER1_LABEL, RESEARCH_CENTER2_LABEL, RESEARCH_TABS} from './research-menu-model.js'
import {MARKET_TABS} from '../market-tabs/market-tab-registry.js'

function renderIcon(icon) {
  return () => h(NIcon, null, { default: () => h(icon) })
}

function createRouterLabel(label, to, onClick) {
  return () =>
    h(
      RouterLink,
      {
        to,
        onClick,
      },
      { default: () => label },
    )
}

function createAnchorLabel(label, onClick, title = '') {
  return () =>
    h(
      'a',
      {
        href: '#',
        title,
        onClick,
      },
      { default: () => (typeof label === 'function' ? label() : label) },
    )
}

function emitLater(eventName, payload) {
  setTimeout(() => {
    EventsEmit(eventName, payload)
  }, 100)
}

function createMarketChildren(activeKey) {
  return MARKET_TABS.map(({key, name, icon}, id) => ({
    label: createRouterLabel(
      name,
      {
        name: 'market',
        query: { name },
      },
      () => {
        activeKey.value = 'market'
        EventsEmit('changeMarketTab', { ID: id, name })
      },
    ),
    key,
    icon: renderIcon(icon),
  }))
}

function createResearchChildren(activeKey) {
  const icons = [DiamondOutline, ReportAnalytics, ReportMoney, SettingsOutline]
  const researchTabs = RESEARCH_TABS.map((name, id) => [`research${id + 1}`, name, icons[id], id])

  return researchTabs.map(([key, name, icon, id]) => ({
    label: createRouterLabel(
      name,
      {
        name: 'research',
        query: { name },
      },
      () => {
        activeKey.value = 'research'
        emitLater('changeResearchTab', { ID: id, name })
      },
    ),
    key,
    icon: renderIcon(icon),
  }))
}

function createResearch2Children(activeKey) {
  const keys = ['recommendations', 'reports', 'yield', 'settings']
  const icons = [DiamondOutline, ReportAnalytics, ReportMoney, SettingsOutline]
  const tabs = RESEARCH_TABS.map((name, id) => [`research2-${keys[id]}`, name, icons[id], id])
  return tabs.map(([key, name, icon, id]) => ({
    label: createRouterLabel(name, {name: 'research2', query: {name}}, () => {
      activeKey.value = 'research2'
      emitLater('changeResearch2Tab', {ID: id, name})
    }),
    key,
    icon: renderIcon(icon),
  }))
}

function createStockRootChildren(router, activeKey) {
  return [
    {
      label: createAnchorLabel('全部', () => {
        activeKey.value = 'stock'
        router.push({
          name: 'stock',
          query: {
            groupName: '全部',
            groupId: 0,
          },
        })
        EventsEmit('changeTab', { ID: 0, name: '全部' })
      }),
      key: 0,
    },
  ]
}

export function createMenuOptions({
  router,
  activeKey,
  enableFund,
  realtimeProfit,
  isFullscreen,
  appVersion,
  toggleFullscreen,
}) {
  return [
    {
      label: createRouterLabel(
        '股票自选',
        {
          name: 'stock',
          query: {
            groupName: '全部',
            groupId: 0,
          },
          params: {},
        },
        () => {
          activeKey.value = 'stock'
        },
      ),
      key: 'stock',
      icon: renderIcon(StarOutline),
      children: createStockRootChildren(router, activeKey),
    },
    {
      label: createRouterLabel(
        '市场行情',
        {
          name: 'market',
          params: {},
        },
        () => {
          activeKey.value = 'market'
          EventsEmit('changeMarketTab', { ID: 0, name: '市场快讯' })
        },
      ),
      key: 'market',
      icon: renderIcon(NewspaperOutline),
      children: createMarketChildren(activeKey),
    },
    {
      label: createRouterLabel(
        '基金自选',
        {
          name: 'fund',
          query: { name: '基金自选' },
        },
        () => {
          activeKey.value = 'fund'
        },
      ),
      show: enableFund.value,
      key: 'fund',
      icon: renderIcon(SparklesOutline),
      children: [
        {
          label: () =>
            h(
              NText,
              { type: realtimeProfit.value > 0 ? 'error' : 'success' },
              { default: () => '功能完善中！' },
            ),
          key: 'realtimeProfit',
          show: realtimeProfit.value,
          icon: renderIcon(AlarmOutline),
        },
      ],
    },
    {
      label: createRouterLabel(
        RESEARCH_CENTER1_LABEL,
        {
          name: 'research',
          query: { name: '股票推荐记录' },
        },
        () => {
          activeKey.value = 'research'
          emitLater('changeResearchTab', { ID: 0, name: '股票推荐记录' })
        },
      ),
      key: 'research',
      icon: renderIcon(FlaskOutline),
      children: createResearchChildren(activeKey),
    },
    {
      label: createRouterLabel(
        RESEARCH_CENTER2_LABEL,
        {
          name: 'research2',
          query: { name: '股票推荐记录' },
        },
        () => {
          activeKey.value = 'research2'
          emitLater('changeResearch2Tab', { ID: 0, name: '股票推荐记录' })
        },
      ),
      key: 'research2',
      icon: renderIcon(FlaskOutline),
      children: createResearch2Children(activeKey),
    },
    {
      label: createAnchorLabel(() => formatGitHubVersionLabel(appVersion.value), () => {
        BrowserOpenURL('https://github.com/yxforever666gh/go-stock')
      }),
      key: 'about',
      icon: renderIcon(LogoGithub),
    },
    {
      show: false,
      label: createAnchorLabel(
        () => (isFullscreen.value ? '取消全屏' : '全屏'),
        toggleFullscreen,
        '全屏 Ctrl+F 退出全屏 Esc',
      ),
      key: 'full',
      icon: renderIcon(ExpandOutline),
    },
  ]
}

export function replaceStockGroupMenuOptions(menuOptions, router, groups) {
  const stockMenu = menuOptions.find((item) => item.key === 'stock')
  if (!stockMenu) {
    return
  }

  const fixedChildren = Array.isArray(stockMenu.children)
    ? stockMenu.children.filter((item) => item.key === 0)
    : []

  const groupChildren = groups.map((item) => ({
    label: createAnchorLabel(item.name, () => {
      router.push({
        name: 'stock',
        query: {
          groupName: item.name,
          groupId: item.ID,
        },
      })
      emitLater('changeTab', item)
    }),
    key: item.ID,
  }))

  stockMenu.children = [...fixedChildren, ...groupChildren]
}

export function applyFeatureMenuVisibility(menuOptions, { enableFund }) {
  menuOptions.forEach((item) => {
    if (item.key === 'fund') {
      item.show = enableFund
    }
  })
}
