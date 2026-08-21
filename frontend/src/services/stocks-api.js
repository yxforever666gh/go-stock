import { API_PATHS } from './api-types.generated'
import { command, requestJSON, withPath, withQuery } from './http-client'

export const GetStockList = (key) => requestJSON(withQuery(API_PATHS.searchStockMaster, { key }))
export const SearchStock = (words) => requestJSON(API_PATHS.queryStocks, { method: 'POST', body: { words } })
export const Greet = (code) => requestJSON(withPath(API_PATHS.getStockSnapshot, { code }))
export const GetStockKLine = (code, name, days) => requestJSON(withQuery(withPath(API_PATHS.getStockKLine, { code }), { name, days }))
export const GetStockMinutePriceLineData = (code, name) => requestJSON(withQuery(withPath(API_PATHS.getStockMinuteLine, { code }), { name }))
export const GetFollowList = (groupId) => requestJSON(withQuery(API_PATHS.listFollowedStocks, { groupId }))
export const Follow = (stockCode) => command(API_PATHS.followStock, { method: 'POST', body: { stockCode } })
export const UnFollow = (code) => command(withPath(API_PATHS.unfollowStock, { code }), { method: 'DELETE' })
export const SetCostPriceAndVolume = (code, price, volume) => command(withPath(API_PATHS.updateStockPosition, { code }), { method: 'PUT', body: { price, volume } })
export const SetAlarmChangePercent = (value, alarmPrice, code) => command(withPath(API_PATHS.updateStockAlarm, { code }), { method: 'PUT', body: { value, alarmPrice } })
export const SetStockSort = (sort, code) => command(withPath(API_PATHS.updateStockSort, { code }), { method: 'PUT', body: { sort } })
