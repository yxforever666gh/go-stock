export const THEME_STAGES = Object.freeze(['观察', '发酵', '加速', '分歧', '退潮'])

export const THEME_STAGE_OPTIONS = Object.freeze([
  {label: '全部阶段', value: ''},
  ...THEME_STAGES.map(value => ({label: value, value})),
])

function finite(value, fallback = 0) {
  const number = Number(value)
  return Number.isFinite(number) ? number : fallback
}

function rows(value, keys = ['items']) {
  if (Array.isArray(value)) return value
  for (const key of keys) {
    if (Array.isArray(value?.[key])) return value[key]
  }
  return []
}

function aliases(value) {
  return rows(value || []).map(item => typeof item === 'string' ? item : String(item?.alias || item?.name || '')).filter(Boolean)
}

export function normalizeThemeStage(value) {
  const stage = String(value || '').trim()
  return THEME_STAGES.includes(stage) ? stage : '观察'
}

export function stageType(stage) {
  return ({观察: 'default', 发酵: 'info', 加速: 'error', 分歧: 'warning', 退潮: 'success'})[normalizeThemeStage(stage)]
}

export function normalizeSecurity(value = {}) {
  return {
    assetType: String(value.assetType || 'stock').toLowerCase(),
    market: String(value.market || '').toUpperCase(),
    code: String(value.code || ''),
    name: String(value.name || value.code || '--'),
    role: String(value.role || ''),
    rank: finite(value.rank),
    contributionScore: finite(value.contributionScore),
  }
}

export function normalizeThemeListItem(value = {}) {
  const snapshot = value.snapshot || value
  return {
    themeId: String(value.themeId || value.id || snapshot.themeId || ''),
    name: String(value.name || value.canonicalName || '未命名题材'),
    aliases: aliases(value.aliases),
    snapshotId: String(snapshot.snapshotId || snapshot.id || ''),
    cycleNo: finite(snapshot.cycleNo, 1),
    lifecycleStage: normalizeThemeStage(snapshot.lifecycleStage),
    previousLifecycleStage: value.previousLifecycleStage ? normalizeThemeStage(value.previousLifecycleStage) : '',
    stageChanged: value.stageChanged === true,
    rank: finite(snapshot.rank),
    heatScore: finite(snapshot.heatScore),
    summary: String(snapshot.summary || value.description || ''),
    constituentCount: finite(snapshot.constituentCount),
    catalystCount: finite(snapshot.catalystCount),
    conflictingCatalystCount: finite(snapshot.conflictingCatalystCount),
    representativeSecurities: rows(value.representativeSecurities || []).map(normalizeSecurity),
    observedAt: String(snapshot.observedAt || ''),
    frozenAt: String(snapshot.frozenAt || ''),
  }
}

export function themeListItems(payload) {
  return rows(payload, ['items', 'themes', 'rows']).map(normalizeThemeListItem).filter(item => item.themeId)
}

export function normalizeThemeSnapshot(value = {}) {
  return {
    snapshotId: String(value.snapshotId || value.id || ''),
    tradeDate: String(value.tradeDate || value.date || ''),
    cycleNo: finite(value.cycleNo, 1),
    lifecycleStage: normalizeThemeStage(value.lifecycleStage),
    rank: finite(value.rank),
    heatScore: finite(value.heatScore),
    summary: String(value.summary || ''),
    constituentCount: finite(value.constituentCount),
    catalystCount: finite(value.catalystCount),
    frozenAt: String(value.frozenAt || ''),
  }
}

export function themeSnapshots(payload) {
  return rows(payload, ['items', 'snapshots', 'rows']).map(normalizeThemeSnapshot)
    .filter(item => item.snapshotId || item.tradeDate)
    .sort((left, right) => left.tradeDate.localeCompare(right.tradeDate))
}

function normalizeSourceClaim(value = {}) {
  return {
    sourceClaimId: String(value.sourceClaimId || value.id || ''),
    sourceName: String(value.sourceName || '未标注来源'),
    sourceRef: String(value.sourceRef || ''),
    stance: String(value.stance || value.status || '').toLowerCase(),
    sourceCredibilityScore: finite(value.sourceCredibilityScore ?? value.souceCredibilityScore),
    summary: String(value.summary || ''),
    publishedAt: String(value.publishedAt || ''),
    availableAt: String(value.availableAt || ''),
    collectedAt: String(value.collectedAt || ''),
    evidenceItemIds: rows(value.evidenceItemIds || []).map(String),
  }
}

export function normalizeCatalyst(value = {}) {
  const sources = rows(value.sources || value.claims || [], ['items', 'sources', 'claims']).map(normalizeSourceClaim)
  const stances = new Set(sources.map(item => stanceLabel(item.stance).label))
  return {
    catalystEventId: String(value.catalystEventId || value.id || ''),
    eventType: String(value.eventType || ''),
    title: String(value.title || '未命名催化事件'),
    summary: String(value.summary || ''),
    eventAt: String(value.eventAt || ''),
    firstAvailableAt: String(value.firstAvailableAt || ''),
    credibilityScore: finite(value.credibilityScore),
    status: String(value.status || '').toLowerCase(),
    hasConflict: value.hasConflict === true || (stances.has('支持') && stances.has('反驳')),
    sources,
  }
}

export function themeCatalysts(payload) {
  return rows(payload, ['items', 'catalysts', 'rows']).map(normalizeCatalyst)
    .filter(item => item.catalystEventId || item.title)
    .sort((left, right) => String(left.eventAt).localeCompare(String(right.eventAt)))
}

export function normalizeThemeDetail(payload = {}) {
  const theme = payload.theme || {}
  const snapshot = payload.snapshot || theme.snapshot || null
  return {
    theme: {
      themeId: String(theme.themeId || theme.id || ''),
      name: String(theme.name || theme.canonicalName || '未命名题材'),
      aliases: aliases(theme.aliases),
      description: String(theme.description || ''),
      status: String(theme.status || ''),
    },
    snapshot: snapshot ? normalizeThemeSnapshot(snapshot) : null,
    constituents: rows(payload.constituents || snapshot?.constituents || []).map(value => ({
      constituentId: String(value.constituentId || ''),
      ...normalizeSecurity(value),
    })),
    catalystSummary: {
      total: finite(payload.catalystSummary?.total),
      supports: finite(payload.catalystSummary?.supports),
      contradicts: finite(payload.catalystSummary?.contradicts),
      hasConflict: payload.catalystSummary?.hasConflict === true,
    },
  }
}

export function heatPercent(value) {
  const number = finite(value)
  return Math.max(0, Math.min(100, number <= 1 ? number * 100 : number))
}

export function credibilityPercent(value) {
  return `${heatPercent(value).toFixed(0)}%`
}

export function stanceLabel(value) {
  const stance = String(value || '').toLowerCase()
  if (['supports', 'support', 'positive'].includes(stance)) return {label: '支持', type: 'error'}
  if (['contradicts', 'contradict', 'negative'].includes(stance)) return {label: '反驳', type: 'success'}
  return {label: stance || '中性', type: 'default'}
}
