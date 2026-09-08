import { API_PATHS } from './api-types.generated'
import { command, requestJSON, withPath, withQuery } from './http-client'

export const GetConfig = () => requestJSON(API_PATHS.getSettings)
export const UpdateConfig = (settings) => command(API_PATHS.updateSettings, { method: 'PUT', body: settings })
export const GetResearchConfig = center => requestJSON(withPath(API_PATHS.getResearchSettings, {center}))
export const UpdateResearchConfig = (center, settings) => requestJSON(withPath(API_PATHS.updateResearchSettings, {center}), {method: 'PUT', body: settings})
export const TestAIConfig = (id, center) => requestJSON(withQuery(API_PATHS.testAIConfig, {center}), { method: 'POST', body: { id } })
export const TestResearch2Email = () => command(API_PATHS.testResearch2Email, {method: 'POST'})
