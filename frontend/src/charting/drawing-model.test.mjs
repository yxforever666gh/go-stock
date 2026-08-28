import assert from 'node:assert/strict'
import test from 'node:test'
import {
  DRAWING_TOOLS,
  activeDrawings,
  createDrawing,
  drawingPutPayload,
  isDrawingRevisionConflict,
  normalizeDrawingDocument,
  softDeleteDrawing,
} from './drawing-model.js'

const points = [
  {time: '2026-01-01T09:30:00+08:00', value: 10},
  {time: '2026-01-01T10:30:00+08:00', value: 12},
  {time: '2026-01-01T11:00:00+08:00', value: 11},
]

test('all six drawing tools persist time/price anchors', () => {
  assert.deepEqual(DRAWING_TOOLS.map(item => item.value), ['measure', 'trend_line', 'ray', 'horizontal_line', 'wave', 'fibonacci_retracement'])
  for (const tool of DRAWING_TOOLS) {
    const drawing = createDrawing(tool.value, points.slice(0, tool.points), {id: tool.value, now: '2026-01-01T00:00:00Z'})
    assert.equal(drawing.points.length, tool.points)
    assert.equal(typeof drawing.points[0].time, 'string')
    assert.equal(typeof drawing.points[0].value, 'number')
  }
})

test('revision payload is scoped and individual deletion remains a tombstone', () => {
  const drawing = createDrawing('trend_line', points, {id: 'd1', now: '2026-01-01T00:00:00Z'})
  const document = normalizeDrawingDocument({
    instrument: {assetType: 'etf', market: 'sh', code: '510300'},
    period: '5m', adjustment: 'qfq', revision: 7, drawings: [drawing],
  })
  const tombstone = softDeleteDrawing(drawing, '2026-01-02T00:00:00Z')
  assert.equal(activeDrawings([tombstone]).length, 0)
  assert.deepEqual(drawingPutPayload(document, [tombstone]), {
    assetType: 'etf', market: 'SH', period: '5m', adjustment: 'qfq', expectedRevision: 7, drawings: [tombstone],
  })
})

test('revision conflict detection refuses silent last-write-wins', () => {
  assert.equal(isDrawingRevisionConflict(new Error('409 revision conflict')), true)
  assert.equal(isDrawingRevisionConflict(new Error('版本冲突')), true)
  assert.equal(isDrawingRevisionConflict(new Error('timeout')), false)
})
