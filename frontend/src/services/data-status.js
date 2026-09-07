export function dataDisplayStatus(value = {}, {loading = false, error = '', chart = false} = {}) {
  value ||= {}
  const data = chart ? value.bars : value.data
  const hasData = Array.isArray(data) ? data.length > 0 : data != null && (typeof data !== 'object' || Object.keys(data).length > 0)
  if (loading && !hasData) return {label: '加载中', type: 'info'}
  if (error) return {label: hasData ? '使用上次数据' : '加载失败', type: hasData ? 'warning' : 'error'}
  if (value.status === 'unavailable') return {label: '不可用', type: 'error'}
  if (!hasData) return {label: chart ? '暂无分钟数据' : '暂无数据', type: 'default'}
  if (value.status === 'after_cutoff') return {label: '截止后数据', type: 'warning'}
  if (value.stale || value.status === 'stale') return {label: '已过期', type: 'warning'}
  if (value.partial || value.status === 'partial' || value.missingIntervals?.length) return {label: chart ? '部分分钟数据' : '部分数据', type: 'warning'}
  if (value.status === 'ok') return {label: chart ? '分钟覆盖完整' : '数据可用', type: 'success'}
  return {label: '状态未确认', type: 'warning'}
}
