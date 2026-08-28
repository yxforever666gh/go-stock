import {API_PATHS} from './api-types.generated'
import type {
  ETFDetailEnvelope,
  ETFRankingEnvelope,
  ETFRankingPage,
  ETFSearchEnvelope,
  ETFWatchlistItem,
  ETFWatchlistRequest,
  FundRankingEnvelope,
  FundRankingPage,
} from './api-types.generated'
import {command, requestJSON, withPath, withQuery} from './http-client'
import {parseDataEnvelope} from './data-envelope.js'

export type FundRankingQuery = {
  category?: FundRankingPage['category']
  period?: FundRankingPage['period']
  q?: string
  sortDirection?: 'asc' | 'desc'
  page?: number
  pageSize?: number
}

export type ETFRankingQuery = {
  category?: ETFRankingPage['category']
  q?: string
  sort?: ETFRankingPage['sort']
  sortDirection?: 'asc' | 'desc'
  page?: number
  pageSize?: number
}

export const GetFundRankings = async (query: FundRankingQuery = {}) =>
  parseDataEnvelope(await requestJSON<FundRankingEnvelope>(withQuery(API_PATHS.fundRankings, query)), {
    items: [], total: 0, page: query.page || 1, pageSize: query.pageSize || 20,
    category: query.category || 'all', period: query.period || 'day', navDate: '',
  })

export const GetETFRankings = async (query: ETFRankingQuery = {}) =>
  parseDataEnvelope(await requestJSON<ETFRankingEnvelope>(withQuery(API_PATHS.etfRankings, query)), {
    items: [], total: 0, page: query.page || 1, pageSize: query.pageSize || 20, category: query.category || 'all',
  })

export const SearchETFs = async (q: string, limit = 20) =>
  parseDataEnvelope(await requestJSON<ETFSearchEnvelope>(withQuery(API_PATHS.etfSearch, {q: String(q || '').trim(), limit})), [])

export const GetETFDetail = async (code: string) =>
  parseDataEnvelope(await requestJSON<ETFDetailEnvelope>(withPath(API_PATHS.etfDetail, {code})), {})

export const ListFollowedETFs = (): Promise<ETFWatchlistItem[]> =>
  requestJSON<ETFWatchlistItem[]>(API_PATHS.listFollowedETFs)

export const FollowETF = (body: ETFWatchlistRequest): Promise<string> =>
  command(API_PATHS.followETF, {method: 'POST', body})

export const UnfollowETF = (code: string): Promise<string> =>
  command(withPath(API_PATHS.unfollowETF, {code}), {method: 'DELETE'})
