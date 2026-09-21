export const RESEARCH2_SLOTS = Array.from({length: 24}, (_, index) => {
  const minute = 570 + index * 5
  const clock = value => `${String(Math.floor(value / 60)).padStart(2, '0')}:${String(value % 60).padStart(2, '0')}`
  return {value: clock(minute), label: `${clock(minute)}–${clock(minute + 5)}`}
})

export const validResearch2Slot = value => RESEARCH2_SLOTS.some(slot => slot.value === value)

const nonNegativeInteger = value => Math.max(0, Number.parseInt(value, 10) || 0)

export function research2SlotBuyLabel(state = {}) {
  const bought = nonNegativeInteger(state?.boughtCount)
  const target = nonNegativeInteger(state?.buyTargetCount)
  const pending = nonNegativeInteger(state?.pendingBuyCount)
  switch (state?.buyStatus) {
    case 'bought_full':
      return `买入：已买入 ${bought}/${target}`
    case 'bought_partial':
      return `买入：已买入 ${bought}/${target}，本轮结束`
    case 'awaiting_quote':
      return `买入：等待行情${pending > 0 ? `（${pending} 笔）` : ''}`
    case 'no_recommendation':
      return '买入：本轮无标的'
    case 'cutoff':
      return '买入：窗口已截止'
    case 'disabled':
      return '买入：新增买入已关闭'
    case 'failed':
      return '买入：执行失败'
    case 'processing':
      return '买入：处理中'
    case 'no_purchase':
      return '买入：本轮未成交'
    default:
      return '买入：等待报告'
  }
}
