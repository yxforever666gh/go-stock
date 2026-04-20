import { h } from 'vue'
import { RouterLink } from 'vue-router'
import { NIcon, NText } from 'naive-ui'
import {
  AlarmOutline,
  AnalyticsOutline,
  BarChartSharp,
  DiamondOutline,
  ExpandOutline,
  Flag,
  FlaskOutline,
  LogoGithub,
  NewspaperOutline,
  NewspaperSharp,
  Pulse,
  SettingsOutline,
  SparklesOutline,
  StarOutline,
} from '@vicons/ionicons5'
import { Dragon, FirefoxBrowser, Gripfire, Robot } from '@vicons/fa'
import { ReportAnalytics, ReportMoney, ReportSearch } from '@vicons/tabler'
import { BoxSearch20Regular } from '@vicons/fluent'
import { NotificationFilled, StockOutlined } from '@vicons/antd'
import { BrowserOpenURL, EventsEmit } from '../../wailsjs/runtime'

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
  const marketTabs = [
    ['market1', '市场快讯', NewspaperSharp, 0],
    ['market2', '全球股指', BarChartSharp, 0],
    ['market3', '重大指数', AnalyticsOutline, 0],
    ['market4', '行业排名', Flag, 0],
    ['market5', '个股资金流向', Pulse, 0],
    ['market6', '龙虎榜', Dragon, 0],
    ['market7', '个股研报', StockOutlined, 0],
    ['market8', '公司公告', NotificationFilled, 0],
    ['market9', '行业研究', ReportSearch, 0],
    ['market10', '当前热门', Gripfire, 0],
    ['market11', '指标选股', BoxSearch20Regular, 0],
    ['market12', '名站优选', FirefoxBrowser, 0],
  ]

  return marketTabs.map(([key, name, icon, id]) => ({
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
  const researchTabs = [
    ['research1', '股票收益率', ReportMoney, 0],
    ['research2', '收益率统计', ReportSearch, 1],
    ['research3', 'AI分析报告', ReportAnalytics, 2],
    ['research4', '股票推荐记录', DiamondOutline, 3],
  ]

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
  enableAgent,
  realtimeProfit,
  isFullscreen,
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
        'Ai智能体',
        {
          name: 'agent',
          query: { name: 'Ai智能体' },
        },
        () => {
          activeKey.value = 'agent'
        },
      ),
      key: 'agent',
      show: enableAgent.value,
      icon: renderIcon(Robot),
    },
    {
      label: createRouterLabel(
        '研究中心',
        {
          name: 'research',
          query: { name: '股票收益率' },
        },
        () => {
          activeKey.value = 'research'
          emitLater('changeResearchTab', { ID: 0, name: '股票收益率' })
        },
      ),
      key: 'research',
      icon: renderIcon(FlaskOutline),
      children: createResearchChildren(activeKey),
    },
    {
      label: createRouterLabel(
        '设置',
        {
          name: 'settings',
          query: { name: '设置' },
        },
        () => {
          activeKey.value = 'settings'
        },
      ),
      key: 'settings',
      icon: renderIcon(SettingsOutline),
    },
    {
      label: createAnchorLabel('GitHub', () => {
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

export function applyFeatureMenuVisibility(menuOptions, { enableFund, enableAgent }) {
  menuOptions.forEach((item) => {
    if (item.key === 'fund') {
      item.show = enableFund
    }
    if (item.key === 'agent') {
      item.show = enableAgent
    }
  })
}
