import { API_PATHS } from './api-types.generated'
import { requestJSON, withPath, withQuery } from './http-client'

export const StartAIAnalysis = async () => (await requestJSON(API_PATHS.startAnalysisRun, { method: 'POST' }))?.accepted ?? false
export const ListAIAnalysisReports = (limit, offset) => requestJSON(withQuery(API_PATHS.listAnalysisRuns, { limit, offset }))
export const GetAIAnalysisReport = (id) => requestJSON(withPath(API_PATHS.getAnalysisRun, { id }))
export const ListAIRecommendations = (limit, offset) => requestJSON(withQuery(API_PATHS.listRecommendations, { limit, offset }))
export const GetAIRecommendation = (id) => requestJSON(withPath(API_PATHS.getRecommendation, { id }))
export const GetAISimulatedAccount = () => requestJSON(API_PATHS.getSimulatedAccount)
