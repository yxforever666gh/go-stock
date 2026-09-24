import { API_PATHS } from './api-types.generated'
import { command, requestJSON } from './http-client'

export const GetConfig = () => requestJSON(API_PATHS.getSettings)
export const UpdateConfig = settings => command(API_PATHS.updateSettings, {method: 'PUT', body: settings})
export const GetPredictionConfig = () => requestJSON(API_PATHS.getPredictionSettings)
export const UpdatePredictionConfig = settings => requestJSON(API_PATHS.updatePredictionSettings, {method: 'PUT', body: settings})
export const TestAIConfig = id => requestJSON(API_PATHS.testPredictionAIConfig, {method: 'POST', body: {id}})
export const TestPredictionEmail = config => command(API_PATHS.testPredictionEmail, {method: 'POST', body: config})
