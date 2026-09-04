import { API_PATHS } from './api-types.generated'
import { requestJSON } from './http-client'

export const GetVersionInfo = () => requestJSON(API_PATHS.getSystemInfo)
export const Shutdown = () => requestJSON(API_PATHS.shutdownSystem, { method: 'POST' })
