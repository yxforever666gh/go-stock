import { API_PATHS } from './api-types.generated'
import type {
  AccountCashFlow,
  AccountOverview,
  AccountPerformance,
  AnalysisRun,
  AnalysisRunSummary,
  BuyOpportunity,
  CapitalDeploymentStatus,
  Recommendation,
  RecommendationChart,
  RecommendationDetail,
} from './api-types.generated'
import { requestJSON, withPath, withQuery } from './http-client'

export const ListAIAnalysisReports = (limit?: number, offset?: number): Promise<AnalysisRunSummary[]> =>
  requestJSON(withQuery(API_PATHS.listAnalysisRuns, { limit, offset }))

export const GetAIAnalysisReport = (id: string): Promise<AnalysisRun> =>
  requestJSON(withPath(API_PATHS.getAnalysisRun, { id }))

export const GetAICapitalDeploymentStatus = (): Promise<CapitalDeploymentStatus> =>
  requestJSON(API_PATHS.getCapitalDeploymentStatus)

export const ListAIBuyOpportunities = (limit?: number, offset?: number): Promise<BuyOpportunity[]> =>
  requestJSON(withQuery(API_PATHS.listBuyOpportunities, { limit, offset }))

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
