import {requestJSON, withPath, withQuery} from './http-client'
import {API_PATHS} from './api-types.generated'
import type {Research2AccountOverview, Research2AnalysisRun, Research2AnalysisRunSummary, Research2Performance, Research2Recommendation, Research2RecommendationDetail} from './api-types.generated'

export const ListResearch2Runs = (limit = 100, offset = 0): Promise<Research2AnalysisRunSummary[]> => requestJSON(withQuery(API_PATHS.listResearch2AnalysisRuns, {limit, offset}))
export const GetResearch2Run = (id: string): Promise<Research2AnalysisRun> => requestJSON(withPath(API_PATHS.getResearch2AnalysisRun, {id}))
export const ListResearch2Recommendations = (limit = 100, offset = 0): Promise<Research2Recommendation[]> => requestJSON(withQuery(API_PATHS.listResearch2Recommendations, {limit, offset}))
export const GetResearch2Recommendation = (id: string): Promise<Research2RecommendationDetail> => requestJSON(withPath(API_PATHS.getResearch2Recommendation, {id}))
export const GetResearch2Account = (): Promise<Research2AccountOverview> => requestJSON(API_PATHS.getResearch2Account)
export const GetResearch2Performance = (): Promise<Research2Performance> => requestJSON(API_PATHS.getResearch2Performance)
