import { API_PATHS } from './api-types.generated'
import { requestJSON } from './http-client'

function downloadBase64(filename, mime, contentBase64) {
  const binary = atob(contentBase64)
  const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0))
  const url = URL.createObjectURL(new Blob([bytes], { type: mime || 'application/octet-stream' }))
  const link = document.createElement('a')
  link.href = url
  link.download = filename || 'download'
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}

async function exportFile(path, body) {
  const result = await requestJSON(path, { method: 'POST', body: { mode: 'download', ...body } })
  if (result?.contentBase64) {
    downloadBase64(result.filename, result.mime, result.contentBase64)
    return result.filename ? `已下载：${result.filename}` : '下载成功'
  }
  return result?.path || ''
}

export const ExportConfig = () => exportFile(API_PATHS.exportConfig, {})
