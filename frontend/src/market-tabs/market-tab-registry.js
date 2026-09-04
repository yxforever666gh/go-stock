import AnalyticsOutlineModule from '@vicons/ionicons5/AnalyticsOutline.js'
import BarChartSharpModule from '@vicons/ionicons5/BarChartSharp.js'
import FlagModule from '@vicons/ionicons5/Flag.js'
import NewspaperSharpModule from '@vicons/ionicons5/NewspaperSharp.js'
import PulseModule from '@vicons/ionicons5/Pulse.js'
import ScaleOutlineModule from '@vicons/ionicons5/ScaleOutline.js'
import WalletOutlineModule from '@vicons/ionicons5/WalletOutline.js'
import DragonModule from '@vicons/fa/Dragon.js'
import FirefoxBrowserModule from '@vicons/fa/FirefoxBrowser.js'
import GripfireModule from '@vicons/fa/Gripfire.js'
import ReportMoneyModule from '@vicons/tabler/ReportMoney.js'
import ReportSearchModule from '@vicons/tabler/ReportSearch.js'
import TrendingUpModule from '@vicons/tabler/TrendingUp.js'
import BoxSearch20RegularModule from '@vicons/fluent/BoxSearch20Regular.js'
import NotificationFilledModule from '@vicons/antd/NotificationFilled.js'
import StockOutlinedModule from '@vicons/antd/StockOutlined.js'

const iconComponent = module => module.default ?? module
const AnalyticsOutline = iconComponent(AnalyticsOutlineModule)
const BarChartSharp = iconComponent(BarChartSharpModule)
const Flag = iconComponent(FlagModule)
const NewspaperSharp = iconComponent(NewspaperSharpModule)
const Pulse = iconComponent(PulseModule)
const ScaleOutline = iconComponent(ScaleOutlineModule)
const WalletOutline = iconComponent(WalletOutlineModule)
const Dragon = iconComponent(DragonModule)
const FirefoxBrowser = iconComponent(FirefoxBrowserModule)
const Gripfire = iconComponent(GripfireModule)
const ReportMoney = iconComponent(ReportMoneyModule)
const ReportSearch = iconComponent(ReportSearchModule)
const TrendingUp = iconComponent(TrendingUpModule)
const BoxSearch20Regular = iconComponent(BoxSearch20RegularModule)
const NotificationFilled = iconComponent(NotificationFilledModule)
const StockOutlined = iconComponent(StockOutlinedModule)

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
  {key: 'market10', name: '当前热门', icon: Gripfire, load: () => import('./HotTopicsTab.vue'), activeAware: true},
  {key: 'market11', name: '指标选股', icon: BoxSearch20Regular, load: () => import('./SelectStockTab.vue')},
  {key: 'market12', name: '名站优选', icon: FirefoxBrowser, load: () => import('./FavoriteSitesTab.vue')},
  {key: 'market13-margin', name: '融资融券', icon: WalletOutline, load: () => import('./MarginTradingTab.vue'), activeAware: true},
])

export const DEFAULT_MARKET_TAB = MARKET_TABS[0].name

export function findMarketTab(name) {
  return MARKET_TABS.find(tab => tab.name === name)
}
