import {
  AnalyticsOutline,
  BarChartSharp,
  Flag,
  NewspaperSharp,
  Pulse,
  ScaleOutline,
  WalletOutline,
} from '@vicons/ionicons5'
import { Dragon, FirefoxBrowser, Gripfire } from '@vicons/fa'
import { ReportMoney, ReportSearch, TrendingUp } from '@vicons/tabler'
import { BoxSearch20Regular } from '@vicons/fluent'
import { NotificationFilled, StockOutlined } from '@vicons/antd'

export const MARKET_TABS = Object.freeze([
  {key: 'market1', name: '市场快讯', icon: NewspaperSharp, load: () => import('./MarketNewsTab.vue'), activeAware: true},
  {key: 'market2', name: '全球股指', icon: BarChartSharp, load: () => import('./GlobalIndexesTab.vue')},
  {key: 'market3', name: '重大指数', icon: AnalyticsOutline, load: () => import('./MajorIndexesTab.vue')},
  {key: 'market3-futures', name: '期指多空', icon: ScaleOutline, load: () => import('./FuturesPositionsTab.vue'), activeAware: true},
  {key: 'market4', name: '行业排名', icon: Flag, load: () => import('./IndustryRankTab.vue')},
  {key: 'market5', name: '个股资金流向', icon: Pulse, load: () => import('./MoneyFlowTab.vue')},
  {key: 'market5-sector', name: '板块资金流向', icon: ReportMoney, load: () => import('./SectorFundFlowTab.vue'), activeAware: true},
  {key: 'market5-concept', name: '概念资金流向', icon: TrendingUp, load: () => import('./ConceptFundFlowTab.vue'), activeAware: true},
  {key: 'market6', name: '龙虎榜', icon: Dragon, load: () => import('./LongTigerTab.vue')},
  {key: 'market7', name: '个股研报', icon: StockOutlined, load: () => import('./StockResearchTab.vue')},
  {key: 'market8', name: '公司公告', icon: NotificationFilled, load: () => import('./StockNoticeTab.vue')},
  {key: 'market9', name: '行业研究', icon: ReportSearch, load: () => import('./IndustryResearchTab.vue')},
  {key: 'market10', name: '当前热门', icon: Gripfire, load: () => import('./HotTopicsTab.vue')},
  {key: 'market11', name: '指标选股', icon: BoxSearch20Regular, load: () => import('./SelectStockTab.vue')},
  {key: 'market12', name: '名站优选', icon: FirefoxBrowser, load: () => import('./FavoriteSitesTab.vue')},
  {key: 'market13-margin', name: '融资融券', icon: WalletOutline, load: () => import('./MarginTradingTab.vue'), activeAware: true},
])

export const DEFAULT_MARKET_TAB = MARKET_TABS[0].name

export function findMarketTab(name) {
  return MARKET_TABS.find(tab => tab.name === name)
}
