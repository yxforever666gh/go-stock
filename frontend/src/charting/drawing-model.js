import {drawingScopeKey, normalizeAdjustment, normalizeInstrument, normalizePeriod} from './chart-contract.js'

export const DRAWING_TOOLS = Object.freeze([
  {value: 'measure', label: '测距', points: 2},
  {value: 'trend_line', label: '趋势线', points: 2},
  {value: 'ray', label: '射线', points: 2},
  {value: 'horizontal_line', label: '水平线', points: 1},
  {value: 'wave', label: '波段', points: 3},
  {value: 'fibonacci_retracement', label: '斐波那契', points: 2},
])

const toolMap = new Map(DRAWING_TOOLS.map(item => [item.value, item]))

export function drawingPointCount(type) {
  return toolMap.get(type)?.points || 0
}

export function isDrawingRevisionConflict(reason) {
  return /(?:revision|版本|冲突|conflict|409)/i.test(reason?.message || String(reason))
}

export function normalizeDrawingPoint(value = {}) {
  const time = String(value.time || '').trim()
  const number = Number(value.value)
  return time && Number.isFinite(number) ? {time, value: number} : null
}

export function normalizeDrawing(value = {}) {
  const type = String(value.type || '').trim()
  if (!toolMap.has(type)) return null
  const points = (Array.isArray(value.points) ? value.points : []).map(normalizeDrawingPoint).filter(Boolean)
  if (points.length < drawingPointCount(type)) return null
  return {
    id: String(value.id || ''),
    type,
    points,
    style: value.style && typeof value.style === 'object' ? value.style : {},
    createdAt: String(value.createdAt || ''),
    updatedAt: String(value.updatedAt || ''),
    deletedAt: value.deletedAt ? String(value.deletedAt) : null,
  }
}

function drawingID() {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID()
  return `drawing-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

export function createDrawing(type, points, {id, style, now = new Date().toISOString()} = {}) {
  const normalizedPoints = (points || []).map(normalizeDrawingPoint).filter(Boolean)
  if (!toolMap.has(type)) throw new Error(`不支持的绘图类型：${type}`)
  if (normalizedPoints.length < drawingPointCount(type)) throw new Error(`${toolMap.get(type).label}的锚点不足`)
  return {
    id: id || drawingID(),
    type,
    points: normalizedPoints,
    style: style || {},
    createdAt: now,
    updatedAt: now,
    deletedAt: null,
  }
}

export function softDeleteDrawing(drawing, now = new Date().toISOString()) {
  return {...drawing, updatedAt: now, deletedAt: now}
}

export function activeDrawings(drawings = []) {
  return drawings.map(normalizeDrawing).filter(item => item && !item.deletedAt)
}

export function normalizeDrawingDocument(value = {}, fallback = {}) {
  const instrument = normalizeInstrument(value.instrument || fallback.instrument)
  const period = normalizePeriod(value.period || fallback.period)
  const adjustment = normalizeAdjustment(value.adjustment || fallback.adjustment, instrument.assetType)
  return {
    instrument,
    period,
    adjustment,
    revision: Math.max(0, Number(value.revision) || 0),
    drawings: (Array.isArray(value.drawings) ? value.drawings : []).map(normalizeDrawing).filter(Boolean),
    deletedAt: value.deletedAt ? String(value.deletedAt) : null,
    updatedAt: String(value.updatedAt || ''),
    scopeKey: drawingScopeKey({instrument, period, adjustment}),
  }
}

export function drawingPutPayload(document, drawings = document.drawings) {
  return {
    assetType: document.instrument.assetType,
    market: document.instrument.market,
    period: document.period,
    adjustment: document.adjustment,
    expectedRevision: document.revision,
    drawings,
  }
}
