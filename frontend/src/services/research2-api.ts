import {requestJSON, withPath, withQuery} from './http-client'
import {API_PATHS} from './api-types.generated'
import type {RecommendationChart, Research2AccountOverview, Research2AnalysisRun, Research2AnalysisRunSummary, Research2Performance, Research2Recommendation, Research2RecommendationDetail} from './api-types.generated'

export const ListResearch2Runs = (limit = 100, offset = 0, slot = ""): Promise<Research2AnalysisRunSummary[]> => requestJSON(withQuery(API_PATHS.listResearch2AnalysisRuns, {limit, offset, slot}))
export const GetResearch2Run = (id: string): Promise<Research2AnalysisRun> => requestJSON(withPath(API_PATHS.getResearch2AnalysisRun, {id}))
export const ListResearch2Recommendations = (limit = 100, offset = 0, slot = "09:50"): Promise<Research2Recommendation[]> => requestJSON(withQuery(API_PATHS.listResearch2Recommendations, {limit, offset, slot}))
export const GetResearch2Recommendation = (id: string): Promise<Research2RecommendationDetail> => requestJSON(withPath(API_PATHS.getResearch2Recommendation, {id}))
export const GetResearch2Account = (slot = "09:50"): Promise<Research2AccountOverview> => requestJSON(withQuery(API_PATHS.getResearch2Account, {slot}))
export const GetResearch2Performance = (slot = "09:50"): Promise<Research2Performance> => requestJSON(withQuery(API_PATHS.getResearch2Performance, {slot}))
export const GetResearch2RecommendationChart = (id: string): Promise<RecommendationChart> => requestJSON(withPath(API_PATHS.getResearch2RecommendationChart, {id}))
export const RefreshResearch2RecommendationChart = (id: string): Promise<RecommendationChart> => requestJSON(withPath(API_PATHS.refreshResearch2RecommendationChart, {id}), {method: 'POST'})

export const ListResearch2Slots = () => requestJSON(API_PATHS.listResearch2Slots)
