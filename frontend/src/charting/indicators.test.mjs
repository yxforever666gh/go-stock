import assert from 'node:assert/strict'
import test from 'node:test'
import {calculateBOLL, calculateIndicators, calculateKDJ, calculateMA, calculateMACD, calculateRSI} from './indicators.js'

test('MA and BOLL use a complete rolling window', () => {
  assert.deepEqual(calculateMA([1, 2, 3, 4], 2), [null, 1.5, 2.5, 3.5])
  const boll = calculateBOLL([1, 2, 3], 2, 2)
  assert.deepEqual(boll.middle, [null, 1.5, 2.5])
  assert.deepEqual(boll.upper, [null, 2.5, 3.5])
  assert.deepEqual(boll.lower, [null, 0.5, 1.5])
})

test('MACD, KDJ and RSI fixtures are deterministic at flat and trending boundaries', () => {
  const macd = calculateMACD([10, 10, 10, 10])
  assert.deepEqual(macd.dif, [0, 0, 0, 0])
  assert.deepEqual(macd.dea, [0, 0, 0, 0])
  assert.deepEqual(macd.histogram, [0, 0, 0, 0])

  const flatBars = Array.from({length: 4}, () => ({open: 10, close: 10, low: 10, high: 10, volume: 1}))
  assert.deepEqual(calculateKDJ(flatBars).k, [50, 50, 50, 50])
  assert.equal(calculateRSI([1, 2, 3, 4, 5, 6, 7], [6]).rsi6[6], 100)
  assert.equal(calculateRSI([5, 5, 5, 5, 5, 5, 5], [6]).rsi6[6], 50)
})

test('full indicator bundle never emits NaN', () => {
  const bars = Array.from({length: 40}, (_, index) => ({
    open: index + 1,
    close: index + 1.5,
    low: index + 0.5,
    high: index + 2,
    volume: (index + 1) * 100,
  }))
  const indicators = calculateIndicators(bars)
  const visit = value => {
    if (Array.isArray(value)) value.forEach(visit)
    else if (value && typeof value === 'object') Object.values(value).forEach(visit)
    else if (value !== null) assert.equal(Number.isNaN(value), false)
  }
  visit(indicators)
  assert.equal(indicators.vol.averages.ma5[4], 300)
})
