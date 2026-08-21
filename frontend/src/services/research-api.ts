import { API_PATHS } from './api-types.generated'
import type {
  AcceptedResponse,
  AccountCashFlow,
  AccountOverview,
  AccountPerformance,
  AnalysisRun,
  AnalysisRunSummary,
  Recommendation,
  RecommendationChart,
  RecommendationDetail,
} from './api-types.generated'
import { requestJSON, withPath, withQuery } from './http-client'

export const StartAIAnalysis = async (): Promise<boolean> =>
  (await requestJSON<AcceptedResponse>(API_PATHS.startAnalysisRun, { method: 'POST' }))?.accepted ?? false

export const ListAIAnalysisReports = (limit?: number, offset?: number): Promise<AnalysisRunSummary[]> =>
  requestJSON(withQuery(API_PATHS.listAnalysisRuns, { limit, offset }))

export const GetAIAnalysisReport = (id: string): Promise<AnalysisRun> =>
  requestJSON(withPath(API_PATHS.getAnalysisRun, { id }))

export const ListAIRecommendations = (limit?: number, offset?: number): Promise<Recommendation[]> =>
  requestJSON(withQuery(API_PATHS.listRecommendations, { limit, offset }))

export const GetAIRecommendation = (id: string): Promise<RecommendationDetail> =>
  requestJSON(withPath(API_PATHS.getRecommendation, { id }))

export const GetAISimulatedAccount = (): Promise<AccountOverview> =>
  requestJSON(API_PATHS.getSimulatedAccount)

export const ListAISimulatedAccountCashFlows = (): Promise<AccountCashFlow[]> =>
  requestJSON(API_PATHS.listAccountCashFlows)

export const GetAISimulatedAccountPerformance = (): Promise<AccountPerformance> =>
  requestJSON(API_PATHS.getAccountPerformance)

export const GetAIRecommendationChart = (id: string): Promise<RecommendationChart> =>
  requestJSON(withPath(API_PATHS.getRecommendationChart, { id }))

export const RefreshAIRecommendationChart = (id: string): Promise<RecommendationChart> =>
  requestJSON(withPath(API_PATHS.refreshRecommendationChart, { id }), { method: 'POST' })
