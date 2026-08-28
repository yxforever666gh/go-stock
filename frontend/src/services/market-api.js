import { API_PATHS } from './api-types.generated'
import { requestJSON, withPath, withQuery } from './http-client'
import {parseDataEnvelope} from './data-envelope.js'

const requestDataEnvelope = async (path) => parseDataEnvelope(await requestJSON(path))

export const GetTelegraphList = (source) => requestJSON(withQuery(API_PATHS.listTelegraphs, { source }))
export const ReFleshTelegraphList = (source) => requestJSON(API_PATHS.refreshTelegraphs, { method: 'POST', body: { source } })
export const GlobalStockIndexes = () => requestJSON(API_PATHS.getGlobalIndexes)
export const GetIndustryRank = (sort, count) => requestJSON(withQuery(API_PATHS.getIndustryRank, { sort, count }))
export const GetIndustryMoneyRankSina = (category, sort) => requestJSON(withQuery(API_PATHS.getIndustryMoneyRank, { category, sort }))
export const GetMoneyRankSina = (sort) => requestJSON(withQuery(API_PATHS.getStockMoneyRank, { sort }))
export const GetStockMoneyTrendByDay = (code, days) => requestJSON(withQuery(withPath(API_PATHS.getStockMoneyTrend, { code }), { days }))
export const LongTigerRank = (date) => requestJSON(withQuery(API_PATHS.getLongTigerRank, { date }))
export const StockResearchReport = (code) => requestJSON(code ? withPath(API_PATHS.getStockResearchReports, { code }) : API_PATHS.listStockResearchReports)
export const StockNotice = (code) => requestJSON(code ? withPath(API_PATHS.getStockNotices, { code }) : API_PATHS.listStockNotices)
export const IndustryResearchReport = (code) => requestJSON(code ? withPath(API_PATHS.getIndustryResearchReports, { code }) : API_PATHS.listIndustryResearchReports)
export const EMDictCode = (code) => requestJSON(withQuery(API_PATHS.getMarketDictionary, { code }))
export const AnalyzeSentimentWithFreqWeight = (text) => requestJSON(API_PATHS.analyzeWeightedSentiment, { method: 'POST', body: { text } })
export const HotStock = (marketType) => requestJSON(withQuery(API_PATHS.listHotStocks, { marketType }))
export const HotEvent = (size) => requestJSON(withQuery(API_PATHS.listHotEvents, { size }))
export const HotTopic = (size) => requestJSON(withQuery(API_PATHS.listHotTopics, { size }))
export const InvestCalendarTimeLine = (yearMonth) => requestJSON(withQuery(API_PATHS.getInvestmentCalendar, { yearMonth }))
export const ClsCalendar = () => requestJSON(API_PATHS.getCLSCalendar)

export const GetMarketBreadth = () => requestDataEnvelope(API_PATHS.getMarketBreadth)
export const GetMarketHotWords = ({hours = 24, baselineDays = 7, limit = 30} = {}) =>
  requestDataEnvelope(withQuery(API_PATHS.listMarketHotWords, {hours, baselineDays, limit}))
export const GetMarketFundFlows = ({scope, date, sort = 'netamount', limit = 100} = {}) =>
  requestDataEnvelope(withQuery(API_PATHS.listMarketFundFlows, {scope, date, sort, limit}))
export const GetMarketFuturesPositions = ({symbol = 'IF', date} = {}) =>
  requestDataEnvelope(withQuery(API_PATHS.listFuturesPositions, {symbol, date}))
export const GetMarketMargin = ({scope = 'market', code, date} = {}) =>
  requestDataEnvelope(withQuery(API_PATHS.getMarginTrading, {scope, code, date}))
export const GetInstrumentAuction = (code, {assetType, date} = {}) =>
  requestDataEnvelope(withQuery(withPath(API_PATHS.getInstrumentAuction, {code}), {assetType, date}))
export const GetInstrumentTrades = (code, {assetType, date, cursor, limit = 100} = {}) =>
  requestDataEnvelope(withQuery(withPath(API_PATHS.listInstrumentTrades, {code}), {assetType, date, cursor, limit}))
