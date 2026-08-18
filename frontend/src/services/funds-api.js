import { API_PATHS } from './api-types.generated'
import { command, requestJSON, withPath, withQuery } from './http-client'

export const GetfundList = (key) => requestJSON(withQuery(API_PATHS.searchFunds, { key }))
export const GetFollowedFund = () => requestJSON(API_PATHS.listFollowedFunds)
export const FollowFund = (fundCode) => command(API_PATHS.followFund, { method: 'POST', body: { fundCode } })
export const UnFollowFund = (code) => command(withPath(API_PATHS.unfollowFund, { code }), { method: 'DELETE' })
