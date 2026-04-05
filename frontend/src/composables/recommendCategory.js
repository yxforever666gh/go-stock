export function categoryLabel(label, category) {
  const normalizedLabel = normalizeCategory(label)
  if (normalizedLabel) {
    return normalizedCategoryLabel(normalizedLabel)
  }

  const normalizedCategory = normalizeCategory(category)
  if (normalizedCategory) {
    return normalizedCategoryLabel(normalizedCategory)
  }

  const rawText = String(label || category || '').trim()
  if (!rawText) {
    return '未标注'
  }

  return rawText
}

export function categoryTagType(category) {
  switch (normalizeCategory(category)) {
    case 'immediate':
      return 'danger'
    case 'conditional':
      return 'warning'
    case 'avoid':
      return 'default'
    default:
      return 'default'
  }
}

export function categoryDescription(label, category) {
  const raw = String(label || category || '').trim()
  if (!raw) {
    return '这条历史记录没有写入结构化分类标签，只保留了当时的原始推荐内容。'
  }

  switch (normalizeCategory(raw)) {
    case 'immediate':
      return '历史记录里曾出现过“立刻买入”语义；当前系统已统一收敛到等待激活，后续新报告不再使用该标签。'
    case 'conditional':
      return '表示这条推荐需要等待激活条件成立后才算有效；如果报告没有给出可机械执行的触发信号，就不会纳入严格回测。'
    case 'avoid':
      return '表示当前阶段风险收益比不合适，或者存在明显不确定性，策略上应以规避和等待为主。'
    default:
      return '这条记录使用的是非标准分类文本，页面按原文展示，不再自动改写成别的结构化标签。'
  }
}

function normalizeCategory(category) {
  const text = String(category || '').trim().toLowerCase()
  if (!text) {
    return ''
  }
  if (text === 'avoid' || text.includes('回避')) {
    return 'avoid'
  }
  if (
    text === 'immediate' ||
    text === 'immediate_buy' ||
    text === 'immediatebuy' ||
    text.includes('立刻买入') ||
    text.includes('立即买入') ||
    text === 'low_absorb' ||
    text === 'lowabsorb' ||
    text.includes('低吸')
  ) {
    return 'immediate'
  }
  if (
    text === 'conditional' ||
    text === 'activated_buy' ||
    text === 'activatedbuy' ||
    text.includes('激活买入') ||
    text.includes('条件触发') ||
    text.includes('触发买入') ||
    text === 'right_confirm' ||
    text === 'rightconfirm' ||
    text.includes('右侧确认') ||
    text === 'observe' ||
    text.includes('观察')
  ) {
    return 'conditional'
  }
  return text
}

function normalizedCategoryLabel(category) {
  switch (category) {
    case 'immediate':
      return '等待激活'
    case 'conditional':
      return '等待激活'
    case 'avoid':
      return '回避标的'
    default:
      return ''
  }
}
