import {API_PATHS} from './api-types.generated'
import {requestJSON, withPath, withQuery} from './http-client'
import {parseDataEnvelope} from './data-envelope.js'

function instrumentPath(operation, code) {
  return withPath(API_PATHS[operation], {code})
}

function drawingQuery({assetType, market, period, adjustment, expectedRevision} = {}) {
  return {assetType, market, period, adjustment, expectedRevision}
}

export const GetInstrumentChart = async (code, {assetType, market, period, adjustment, from, to, limit} = {}) =>
  parseDataEnvelope(await requestJSON(withQuery(instrumentPath('getInstrumentChart', code), {
    assetType, market, period, adjustment, from, to, limit,
  })), {bars: [], missingIntervals: []})

export const GetLegacyInstrumentChart = async (code, {name, days = 365, assetType = 'index', market = ''} = {}) => {
  const rows = await requestJSON(withQuery(withPath(API_PATHS.getStockKLine, {code}), {name, days}))
  const bars = (Array.isArray(rows) ? rows : []).map(item => ({
    time: item.time || item.day || item.date,
    open: item.open,
    close: item.close,
    low: item.low,
    high: item.high,
    volume: item.volume,
    amount: item.amount || 0,
    source: 'legacy:getStockKLine',
  }))
  const asOf = String(bars.at(-1)?.time || '')
  return parseDataEnvelope({
    data: {instrument: {assetType, market, code}, name, period: 'day', adjustment: 'none', bars, missingIntervals: []},
    source: 'legacy:getStockKLine',
    asOf,
    fetchedAt: new Date().toISOString(),
    status: bars.length ? 'ok' : 'unavailable',
    warnings: ['境外行情沿用兼容接口，仅提供旧接口可取得的日线数据。'],
  }, {bars: [], missingIntervals: []})
}

export const GetInstrumentDrawings = (code, scope = {}) =>
  requestJSON(withQuery(instrumentPath('getInstrumentDrawings', code), drawingQuery(scope)))

export const PutInstrumentDrawings = (code, payload) =>
  requestJSON(instrumentPath('putInstrumentDrawings', code), {method: 'PUT', body: payload})

export const DeleteInstrumentDrawings = (code, scope = {}) =>
  requestJSON(withQuery(instrumentPath('deleteInstrumentDrawings', code), drawingQuery(scope)), {method: 'DELETE'})
