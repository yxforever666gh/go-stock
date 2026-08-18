import { API_PATHS } from './api-types.generated'
import { requestJSON } from './http-client'
import { BrowserOpenURL } from './browser-runtime.mjs'

export const GetVersionInfo = () => requestJSON(API_PATHS.getSystemInfo)
export const CheckUpdate = (flag) => requestJSON(API_PATHS.checkForUpdates, { method: 'POST', body: { flag } })
export const Shutdown = () => requestJSON(API_PATHS.shutdownSystem, { method: 'POST' })
export const OpenURL = BrowserOpenURL
