export const AUDIT_TABS = Object.freeze([
  {name: 'final', label: '最终结果'},
  {name: 'prompt', label: '提示词与输入'},
  {name: 'evidence', label: '证据快照'},
  {name: 'calls', label: '模型调用'},
  {name: 'response', label: '原始响应与修复'},
])

export const TERMINAL_REPLAY_STATUSES = Object.freeze(['completed', 'failed'])

function record(value) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value : {}
}

export function normalizeResearchAudit(value, fallback = {}) {
  const source = record(value)
  const unwrapped = source.availability || source.ownerId ? source : record(source.data)
  const state = record(unwrapped.state)
  return {
    availability: String(unwrapped.availability || 'unavailable'),
    ownerType: String(unwrapped.ownerType || fallback.ownerType || ''),
    ownerId: String(unwrapped.ownerId || fallback.ownerId || ''),
    cutoffAt: unwrapped.cutoffAt || '',
    state: {
      status: String(state.status || ''),
      payloadCount: Number(state.payloadCount || 0),
      lastError: String(state.lastError || ''),
      createdAt: state.createdAt || '',
      updatedAt: state.updatedAt || '',
    },
    payloads: Array.isArray(unwrapped.payloads) ? unwrapped.payloads.map((payload, index) => ({
      ...record(payload),
      payloadId: String(payload?.payloadId || `payload-${index + 1}`),
      phase: String(payload?.phase || 'unknown'),
      callSequence: Number(payload?.callSequence || index + 1),
      attempt: Number(payload?.attempt || 1),
      redactionCount: Number(payload?.redactionCount || 0),
    })) : [],
  }
}

export function auditIsAvailable(audit) {
  return audit?.availability === 'available'
}

export function auditIsLegacy(audit) {
  return audit?.availability === 'legacy_unavailable'
}

export function prettyAuditValue(value, empty = '--') {
  if (value === undefined || value === null || value === '') return empty
  if (typeof value === 'string') {
    const trimmed = value.trim()
    if (!trimmed) return empty
    try {
      return JSON.stringify(JSON.parse(trimmed), null, 2)
    } catch (_) {
      return value
    }
  }
  try {
    return JSON.stringify(value, null, 2)
  } catch (_) {
    return String(value)
  }
}

export function auditPayloadLabel(payload, index = 0) {
  const phase = payload?.phase || 'unknown'
  const sequence = Number(payload?.callSequence || index + 1)
  const attempt = Number(payload?.attempt || 1)
  const model = [payload?.providerName, payload?.modelName].filter(Boolean).join(' / ')
  return `${phase} · 调用 ${sequence} · 尝试 ${attempt}${model ? ` · ${model}` : ''}`
}

export function replayStatus(value) {
  return String(value?.status || value?.state?.status || 'pending').toLowerCase()
}

export function replayIsTerminal(value) {
  return TERMINAL_REPLAY_STATUSES.includes(replayStatus(value))
}

export function replayDifference(value) {
  const source = record(value)
  return source.diffSummary ?? source.difference ?? source.diff ?? source.differences ?? source.comparison ?? source.resultDiff ?? source.result ?? null
}

export function replayError(value) {
  const source = record(value)
  const state = record(source.state)
  return String(source.lastError || source.error || state.lastError || '')
}

export function modelConfigOptions(values) {
  if (!Array.isArray(values)) return []
  return values
    .filter(value => value && value.disabled !== true && Number(value.ID ?? value.id) > 0)
    .map(value => {
      const id = Number(value.ID ?? value.id)
      const name = String(value.name || '').trim()
      const model = String(value.modelName || '').trim()
      return {value: id, label: name && model ? `${name}（${model}）` : name || model || `模型配置 ${id}`}
    })
}
