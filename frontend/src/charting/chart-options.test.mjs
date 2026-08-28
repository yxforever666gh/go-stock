import assert from 'node:assert/strict'
import test from 'node:test'
import {buildChartOption} from './chart-options.js'
import {createDrawing} from './drawing-model.js'

function fixtureModel() {
  return {
    instrument: {assetType: 'stock', market: 'SH', code: '600000'},
    period: 'day', adjustment: 'qfq', source: 'fixture',
    missingIntervals: [{from: '2026-01-10', to: '2026-01-12', reason: 'gap'}],
    bars: Array.from({length: 35}, (_, index) => ({
      time: `2026-01-${String(index + 1).padStart(2, '0')}`,
      open: index + 10, close: index + 10.5, low: index + 9, high: index + 11,
      volume: 1000 + index, amount: 2000 + index, source: 'fixture', raw: {},
    })),
  }
}

test('option builder composes one rendering core for all indicators and drawings', () => {
  const model = fixtureModel()
  const before = JSON.stringify(model.bars)
  const anchors = [{time: model.bars[0].time, value: 10}, {time: model.bars[10].time, value: 20}, {time: model.bars[20].time, value: 15}]
  const drawings = [
    createDrawing('measure', anchors, {id: 'measure'}),
    createDrawing('trend_line', anchors, {id: 'trend'}),
    createDrawing('ray', anchors, {id: 'ray'}),
    createDrawing('horizontal_line', anchors, {id: 'horizontal'}),
    createDrawing('wave', anchors, {id: 'wave'}),
    createDrawing('fibonacci_retracement', anchors, {id: 'fib'}),
  ]
  const option = buildChartOption(model, {mainIndicators: ['MA', 'BOLL'], subIndicator: 'KDJ'}, {}, drawings)
  const ids = option.series.map(item => item.id)
  assert.ok(ids.includes('price:main'))
  assert.ok(ids.includes('indicator:ma:ma5'))
  assert.ok(ids.includes('indicator:boll:upper'))
  assert.ok(ids.includes('indicator:kdj:k'))
  assert.equal(ids.filter(id => id.startsWith('drawing:')).length, 6)
  assert.equal(option.series[0].markArea.data.length, 1)
  assert.equal(JSON.stringify(model.bars), before)
})

test('line mode shares the same axes, zoom and source-aware tooltip', () => {
  const model = fixtureModel()
  const option = buildChartOption(model, {viewMode: 'line', mainIndicators: [], subIndicator: 'RSI'})
  assert.equal(option.series[0].type, 'line')
  assert.equal(option.xAxis.length, 2)
  assert.equal(option.dataZoom.length, 2)
  assert.match(option.tooltip.formatter([{seriesId: 'price:main', dataIndex: 0}]), /来源：fixture/)
})

test('a missing interval between adjacent bars remains visibly marked', () => {
  const model = fixtureModel()
  model.missingIntervals = [{from: '2026-01-10T10:00:00Z', to: '2026-01-10T10:05:00Z', reason: 'no bars'}]
  model.bars = [
    {...model.bars[0], time: '2026-01-10T09:59:00Z'},
    {...model.bars[1], time: '2026-01-10T10:06:00Z'},
  ]
  const option = buildChartOption(model)
  assert.equal(option.series[0].markArea.data.length, 1)
  assert.equal(option.series[0].markArea.data[0][0].xAxis, model.bars[0].time)
  assert.equal(option.series[0].markArea.data[0][1].xAxis, model.bars[1].time)
})
