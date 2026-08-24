function numberOr(value, fallback = 0) {
  const number = Number(value)
  return Number.isFinite(number) ? number : fallback
}

function nullableNumber(value) {
  if (value === null || value === undefined || value === '') return null
  const number = Number(value)
  return Number.isFinite(number) ? number : null
}

export function sampleAssessment(closedTrades) {
  const count = Math.max(0, Math.trunc(numberOr(closedTrades)))
  if (count < 30) return {key: 'insufficient', label: '样本不足', type: 'warning'}
  if (count < 100) return {key: 'preliminary', label: '初步观察', type: 'info'}
  return {key: 'stage_ready', label: '可进行阶段性评价', type: 'success'}
}

export function normalizeAccountOverview(account = {}) {
	const cumulativeNetContribution = numberOr(account.cumulativeNetContribution, numberOr(account.initialCash, 500000))
	const currentPositions = Math.max(0, Math.trunc(numberOr(account.currentPositions, account.positions?.length || 0)))
	const pendingBuys = Math.max(0, Math.trunc(numberOr(account.pendingBuys)))
	const timeWeightedReturn = numberOr(account.timeWeightedReturn, account.netYieldRate)

  return {
		...account,
		cumulativeNetContribution,
		currentPositions,
		pendingBuys,
		timeWeightedReturn,
		cumulativeCapitalReturn: numberOr(account.cumulativeCapitalReturn,
			cumulativeNetContribution > 0 ? numberOr(account.netProfit) / cumulativeNetContribution : 0),
	}
}

export function normalizePerformance(payload = {}, account = {}) {
  const metrics = payload.metrics || {}
  const closedTrades = Math.max(0, Math.trunc(numberOr(metrics.closedTrades)))
  const assessment = sampleAssessment(closedTrades)
  const backendAssessment = String(metrics.sampleLevel || metrics.sampleAssessment || '').trim()

  return {
    valuedAt: payload.valuedAt || account.valuedAt || '',
    unitValue: numberOr(payload.unitValue, 1),
    timeWeightedReturn: numberOr(payload.timeWeightedReturn, account.timeWeightedReturn ?? account.netYieldRate),
    cumulativeCapitalReturn: numberOr(payload.cumulativeCapitalReturn, account.cumulativeCapitalReturn),
    netProfit: numberOr(payload.netProfit, account.netProfit),
    netAssetValue: numberOr(payload.netAssetValue, account.netAssetValue),
    cumulativeNetContribution: numberOr(payload.cumulativeNetContribution, account.cumulativeNetContribution),
    curve: Array.isArray(payload.curve) ? payload.curve : [],
    metrics: {
      closedTrades,
      sampleAssessment: backendAssessment || assessment.label,
      sampleAssessmentType: assessment.type,
      winRate: nullableNumber(metrics.winRate),
      averageGainRate: nullableNumber(metrics.averageGainRate ?? metrics.averageProfit),
      averageLossRate: nullableNumber(metrics.averageLossRate ?? metrics.averageLoss),
      payoffRatio: nullableNumber(metrics.payoffRatio),
      maxDrawdown: nullableNumber(metrics.maxDrawdown),
      totalFees: nullableNumber(metrics.totalFees),
      totalTurnover: nullableNumber(metrics.totalTurnover),
      turnoverRate: nullableNumber(metrics.turnoverRate),
      capitalUtilization: nullableNumber(metrics.capitalUtilization),
      averageHoldingMinutes: nullableNumber(metrics.averageHoldingMinutes ?? (
        metrics.averageHoldingHours === null || metrics.averageHoldingHours === undefined
          ? null
          : numberOr(metrics.averageHoldingHours) * 60
      )),
      missedExecutionRate: nullableNumber(metrics.missedExecutionRate),
      industryConcentration: nullableNumber(metrics.industryConcentration),
      industryConcentrationStatus: String(metrics.industryConcentrationStatus || ''),
    },
  }
}

export function normalizeCashFlows(payload) {
  const rows = Array.isArray(payload) ? payload : (Array.isArray(payload?.items) ? payload.items : [])
  return rows.map((item, index) => ({
    ...item,
    flowId: item.flowId || `cash-flow-${index}`,
    amount: numberOr(item.amount),
    netAssetValueBefore: nullableNumber(item.netAssetValueBefore),
    netAssetValueAfter: nullableNumber(item.netAssetValueAfter),
    unitValueBefore: nullableNumber(item.unitValueBefore),
    unitsIssued: nullableNumber(item.unitsIssued),
  }))
}

export function formatHoldingMinutes(value) {
  const minutes = nullableNumber(value)
  if (minutes === null) return '--'
  const hours = minutes / 60
  if (hours < 24) return `${numberOr(hours).toFixed(1)} 小时`
  const days = Math.floor(hours / 24)
  const remainder = hours - days * 24
  return remainder >= 0.05 ? `${days} 天 ${remainder.toFixed(1)} 小时` : `${days} 天`
}
