import { API_PATHS } from './api-types.generated'
import { command, requestJSON } from './http-client'

export const GetConfig = () => requestJSON(API_PATHS.getSettings)
export const UpdateConfig = (settings) => command(API_PATHS.updateSettings, { method: 'PUT', body: settings })
export const TestAIConfig = (id) => requestJSON(API_PATHS.testAIConfig, { method: 'POST', body: { id } })
