import { API_PATHS } from './api-types.generated'
import { command, requestJSON, withPath, withQuery } from './http-client'

export const GetConfig = () => requestJSON(API_PATHS.getSettings)
export const UpdateConfig = (settings) => command(API_PATHS.updateSettings, { method: 'PUT', body: settings })
export const GetAiConfigs = () => requestJSON(API_PATHS.listAIConfigs)
export const TestAIConfig = (id) => requestJSON(API_PATHS.testAIConfig, { method: 'POST', body: { id } })
export const GetPromptTemplates = (name, type) => requestJSON(withQuery(API_PATHS.listPrompts, { name, type }))
export const AddPrompt = (prompt) => command(API_PATHS.createPrompt, { method: 'POST', body: prompt })
export const DelPrompt = (id) => command(withPath(API_PATHS.deletePrompt, { id }), { method: 'DELETE' })
