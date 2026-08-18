export function withQuery(path, values = {}) {
  const query = new URLSearchParams()
  Object.entries(values).forEach(([key, value]) => {
    if (value !== undefined && value !== null && value !== '') query.set(key, String(value))
  })
  const suffix = query.toString()
  return suffix ? `${path}?${suffix}` : path
}

export function withPath(path, values = {}) {
  return Object.entries(values).reduce(
    (result, [key, value]) => result.replace(`{${key}}`, encodeURIComponent(String(value ?? ''))),
    path,
  )
}

export async function requestJSON(path, { method = 'GET', body } = {}) {
  const response = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await response.text()
  let payload = null
  if (text) {
    try {
      payload = JSON.parse(text)
    } catch (_) {
      payload = { error: text }
    }
  }
  if (!response.ok) throw new Error(payload?.error || `请求失败: ${response.status}`)
  return payload
}

export async function command(path, options) {
  const payload = await requestJSON(path, options)
  return payload?.message ?? ''
}
