import { API_PATHS } from './api-types.generated'
import { command, requestJSON, withPath, withQuery } from './http-client'

export const GetGroupList = () => requestJSON(API_PATHS.listGroups)
export const AddGroup = (group) => command(API_PATHS.createGroup, { method: 'POST', body: group })
export const RemoveGroup = (id) => command(withPath(API_PATHS.deleteGroup, { id }), { method: 'DELETE' })
export const UpdateGroupSort = async (id, sort) => (await requestJSON(withPath(API_PATHS.updateGroupSort, { id }), { method: 'PUT', body: { sort } }))?.ok ?? false
export const InitializeGroupSort = async () => (await requestJSON(API_PATHS.initializeGroupSort, { method: 'POST' }))?.ok ?? false
export const AddStockGroup = (id, stockCode) => command(withPath(API_PATHS.addGroupStock, { id }), { method: 'POST', body: { stockCode } })
export const RemoveStockGroup = (code, name, id) => command(withQuery(withPath(API_PATHS.removeGroupStock, { id, code }), { name }), { method: 'DELETE' })
