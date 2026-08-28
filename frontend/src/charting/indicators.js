function finite(value) {
  const number = Number(value)
  return Number.isFinite(number) ? number : 0
}

function nullableRound(value, digits = 6) {
  if (!Number.isFinite(value)) return null
  const factor = 10 ** digits
  return Math.round(value * factor) / factor
}

export function calculateMA(values, period) {
  const window = Math.max(1, Math.trunc(Number(period) || 1))
  const result = Array(values.length).fill(null)
  let sum = 0
  for (let index = 0; index < values.length; index += 1) {
    sum += finite(values[index])
    if (index >= window) sum -= finite(values[index - window])
    if (index >= window - 1) result[index] = nullableRound(sum / window)
  }
  return result
}

export function calculateBOLL(values, period = 20, multiplier = 2) {
  const window = Math.max(1, Math.trunc(Number(period) || 20))
  const middle = Array(values.length).fill(null)
  const upper = Array(values.length).fill(null)
  const lower = Array(values.length).fill(null)
  let sum = 0
  let squareSum = 0
  for (let index = 0; index < values.length; index += 1) {
    const current = finite(values[index])
    sum += current
    squareSum += current * current
    if (index >= window) {
      const removed = finite(values[index - window])
      sum -= removed
      squareSum -= removed * removed
    }
    if (index < window - 1) continue
    const mean = sum / window
    const variance = Math.max(0, squareSum / window - mean * mean)
    const deviation = Math.sqrt(variance) * Number(multiplier || 2)
    middle[index] = nullableRound(mean)
    upper[index] = nullableRound(mean + deviation)
    lower[index] = nullableRound(mean - deviation)
  }
  return {middle, upper, lower}
}

export function calculateEMA(values, period) {
  const result = Array(values.length).fill(null)
  if (!values.length) return result
  const alpha = 2 / (Math.max(1, Number(period) || 1) + 1)
  let previous = finite(values[0])
  result[0] = nullableRound(previous)
  for (let index = 1; index < values.length; index += 1) {
    previous = finite(values[index]) * alpha + previous * (1 - alpha)
    result[index] = nullableRound(previous)
  }
  return result
}

export function calculateMACD(values, shortPeriod = 12, longPeriod = 26, signalPeriod = 9) {
  const shortEMA = calculateEMA(values, shortPeriod)
  const longEMA = calculateEMA(values, longPeriod)
  const dif = values.map((_, index) => nullableRound(finite(shortEMA[index]) - finite(longEMA[index])))
  const dea = calculateEMA(dif, signalPeriod)
  const histogram = dif.map((value, index) => nullableRound((finite(value) - finite(dea[index])) * 2))
  return {dif, dea, histogram}
}

function rollingExtrema(values, period, compare) {
  const result = Array(values.length).fill(null)
  const deque = []
  for (let index = 0; index < values.length; index += 1) {
    while (deque.length && deque[0] <= index - period) deque.shift()
    while (deque.length && compare(finite(values[index]), finite(values[deque.at(-1)]))) deque.pop()
    deque.push(index)
    result[index] = finite(values[deque[0]])
  }
  return result
}

export function calculateKDJ(bars, period = 9, smoothK = 3, smoothD = 3) {
  const highs = bars.map(item => finite(item.high))
  const lows = bars.map(item => finite(item.low))
  const highest = rollingExtrema(highs, period, (left, right) => left >= right)
  const lowest = rollingExtrema(lows, period, (left, right) => left <= right)
  const k = Array(bars.length).fill(null)
  const d = Array(bars.length).fill(null)
  const j = Array(bars.length).fill(null)
  let previousK = 50
  let previousD = 50
  for (let index = 0; index < bars.length; index += 1) {
    const range = highest[index] - lowest[index]
    const rsv = range === 0 ? 50 : (finite(bars[index].close) - lowest[index]) / range * 100
    previousK = ((smoothK - 1) * previousK + rsv) / smoothK
    previousD = ((smoothD - 1) * previousD + previousK) / smoothD
    k[index] = nullableRound(previousK)
    d[index] = nullableRound(previousD)
    j[index] = nullableRound(3 * previousK - 2 * previousD)
  }
  return {k, d, j}
}

function oneRSI(values, period) {
  const result = Array(values.length).fill(null)
  if (values.length <= period) return result
  let averageGain = 0
  let averageLoss = 0
  for (let index = 1; index <= period; index += 1) {
    const delta = finite(values[index]) - finite(values[index - 1])
    averageGain += Math.max(delta, 0)
    averageLoss += Math.max(-delta, 0)
  }
  averageGain /= period
  averageLoss /= period
  const rsi = () => {
    if (averageGain === 0 && averageLoss === 0) return 50
    if (averageLoss === 0) return 100
    return 100 - 100 / (1 + averageGain / averageLoss)
  }
  result[period] = nullableRound(rsi())
  for (let index = period + 1; index < values.length; index += 1) {
    const delta = finite(values[index]) - finite(values[index - 1])
    averageGain = (averageGain * (period - 1) + Math.max(delta, 0)) / period
    averageLoss = (averageLoss * (period - 1) + Math.max(-delta, 0)) / period
    result[index] = nullableRound(rsi())
  }
  return result
}

export function calculateRSI(values, periods = [6, 12, 24]) {
  return Object.fromEntries(periods.map(period => [`rsi${period}`, oneRSI(values, period)]))
}

export function calculateVOL(bars, periods = [5, 10]) {
  const volume = bars.map(item => finite(item.volume))
  return {
    volume,
    averages: Object.fromEntries(periods.map(period => [`ma${period}`, calculateMA(volume, period)])),
  }
}

export function calculateIndicators(bars, {maPeriods = [5, 10, 20, 30], bollPeriod = 20} = {}) {
  const closes = bars.map(item => finite(item.close))
  return {
    ma: Object.fromEntries(maPeriods.map(period => [`ma${period}`, calculateMA(closes, period)])),
    boll: calculateBOLL(closes, bollPeriod),
    vol: calculateVOL(bars),
    macd: calculateMACD(closes),
    kdj: calculateKDJ(bars),
    rsi: calculateRSI(closes),
  }
}
