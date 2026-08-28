import {API_PATHS} from './api-types.generated'
import {requestJSON, withPath, withQuery} from './http-client'
import {parseDataEnvelope} from './data-envelope.js'

const requestEnvelope = async (path, fallbackData) => parseDataEnvelope(await requestJSON(path), fallbackData)

export const ListThemes = ({date, stage, q, sort = 'rank', limit = 100, cursor} = {}) =>
  requestEnvelope(withQuery(API_PATHS.listThemes, {date, stage, q, sort, limit, cursor}), {tradeDate: date || '', items: []})

export const GetTheme = (id, {date} = {}) =>
  requestEnvelope(withQuery(withPath(API_PATHS.getTheme, {id}), {date}), {theme: null, snapshot: null, constituents: [], catalystSummary: {}})

export const ListThemeDailySnapshots = (id, {from, to, stage, limit = 100, cursor} = {}) =>
  requestEnvelope(withQuery(withPath(API_PATHS.listThemeDailySnapshots, {id}), {from, to, stage, limit, cursor}), {themeId: id, items: []})

export const ListThemeCatalysts = (id, {date, status, minCredibility, limit = 100, cursor} = {}) =>
  requestEnvelope(withQuery(withPath(API_PATHS.listThemeCatalysts, {id}), {date, status, minCredibility, limit, cursor}), {themeId: id, tradeDate: date || '', items: []})
