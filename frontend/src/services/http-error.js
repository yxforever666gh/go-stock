function errorMessage(value) {
  if (typeof value === 'string') return value.trim()
  if (value && typeof value === 'object' && value.message) return String(value.message).trim()
  return ''
}

export function responseErrorMessage(payload, status) {
  if (payload && typeof payload === 'object') {
    const errors = Array.isArray(payload.errors) ? payload.errors.map(errorMessage).filter(Boolean) : []
    if (errors.length) return errors.join('；')
    const direct = errorMessage(payload.error) || errorMessage(payload.message)
    if (direct) return direct
  }
  return `请求失败: ${status}`
}
