import { API_PATHS } from './api-types.generated'
import { command, requestJSON, withPath } from './http-client'

export const NewChatStream = (stock, stockCode, question, aiConfigId, sysPromptId, enableTools, think) => requestJSON(API_PATHS.createChatRun, {
  method: 'POST', body: { stock, stockCode, question, aiConfigId, sysPromptId, enableTools, think },
})
export const GetAIResponseResult = (stockCode) => requestJSON(withPath(API_PATHS.getAIResponse, { stockCode }))
export const SaveAIResponseResult = (stockCode, stockName, result, chatId, question, aiConfigId) => command(API_PATHS.saveAIResponse, {
  method: 'POST', body: { stockCode, stockName, result, chatId, question, aiConfigId },
})
export const ShareAnalysis = (stockCode, stockName) => command(withPath(API_PATHS.shareAIResponse, { stockCode }), { method: 'POST', body: { stockName } })
