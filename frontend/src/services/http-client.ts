import {responseErrorMessage} from './http-error.js'

export function withQuery(path: string, values: Record<string, unknown> = {}): string {
  const query = new URLSearchParams()
  Object.entries(values).forEach(([key, value]) => {
    if (value !== undefined && value !== null && value !== '') query.set(key, String(value))
  })
  const suffix = query.toString()
  return suffix ? `${path}?${suffix}` : path
}

export function withPath(path: string, values: Record<string, unknown> = {}): string {
  return Object.entries(values).reduce(
    (result, [key, value]) => result.replace(`{${key}}`, encodeURIComponent(String(value ?? ''))),
    path,
  )
}

type JSONRequestOptions = {
  method?: string
  body?: unknown
}

export async function requestJSON<T = unknown>(path: string, { method = 'GET', body }: JSONRequestOptions = {}): Promise<T> {
  const response = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await response.text()
  let payload: unknown = null
  if (text) {
    try {
      payload = JSON.parse(text)
    } catch (_) {
      payload = { error: text }
    }
  }
  if (!response.ok) {
    throw new Error(responseErrorMessage(payload, response.status))
  }
  return payload as T
}

export async function command(path: string, options: JSONRequestOptions): Promise<string> {
  const payload = await requestJSON<{message?: string}>(path, options)
  return payload?.message ?? ''
}
