function objectValue(value) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value : {}
}

function stringValue(value) {
  return value === undefined || value === null ? '' : String(value)
}

export function nullableNumber(value) {
  if (value === undefined || value === null || value === '') return null
  const number = Number(value)
  return Number.isFinite(number) ? number : null
}

function nonNegativeNumber(value, fallback = 0) {
  return Math.max(0, nullableNumber(value) ?? fallback)
}

function normalizeRepresentativeNews(value, index) {
  const item = objectValue(value)
  return {
    ...item,
    publishedAt: stringValue(item.publishedAt),
    source: stringValue(item.source || '未标注来源'),
    excerpt: stringValue(item.excerpt),
    url: stringValue(item.url),
    _key: stringValue(item.id || item.url || `${item.publishedAt || 'news'}-${index}`),
  }
}

function normalizeHotWordItem(value, index) {
  const item = objectValue(value)
  const sources = Array.isArray(item.sources) ? [...new Set(item.sources.map(stringValue).filter(Boolean))] : []
  return {
    ...item,
    rank: Math.max(1, nullableNumber(item.rank) ?? index + 1),
    word: stringValue(item.word),
    score: nullableNumber(item.score),
    documentCount: nonNegativeNumber(item.documentCount),
    occurrenceCount: nonNegativeNumber(item.occurrenceCount),
    documentShare: nullableNumber(item.documentShare),
    baselineDocumentCount: nonNegativeNumber(item.baselineDocumentCount),
    burstRatio: nullableNumber(item.burstRatio),
    growthPct: nullableNumber(item.growthPct),
    sourceCount: nonNegativeNumber(item.sourceCount, sources.length),
    sources,
    latestAt: stringValue(item.latestAt),
    confidence: stringValue(item.confidence || 'low').toLowerCase(),
    representativeNews: (Array.isArray(item.representativeNews) ? item.representativeNews : [])
      .slice(0, 3)
      .map(normalizeRepresentativeNews),
    _key: stringValue(item.word || `hot-word-${index}`),
  }
}

export function normalizeHotWordsPayload(value = {}) {
  const source = objectValue(value)
  const baseline = objectValue(source.baseline)
  const sentiment = objectValue(source.sentiment)
  return {
    window: objectValue(source.window),
    baseline: {
      ...baseline,
      available: baseline.available === true,
    },
    currentDocumentCount: nonNegativeNumber(source.currentDocumentCount),
    sentiment: {
      ...sentiment,
      score: nullableNumber(sentiment.score) ?? 0,
      label: stringValue(sentiment.label || sentiment.description || '中性'),
    },
    items: (Array.isArray(source.items) ? source.items : []).map(normalizeHotWordItem),
  }
}

export function formatDocumentShare(value) {
  const number = nullableNumber(value)
  return number === null ? '--' : `${(number * 100).toFixed(number < 0.001 ? 2 : 1)}%`
}

export function hotWordTrend(item, baselineAvailable) {
  if (!baselineAvailable) return {label: '暂无可靠基线', type: 'warning'}
  if (nonNegativeNumber(item?.baselineDocumentCount) === 0) return {label: '新出现', type: 'error'}
  const growthPct = nullableNumber(item?.growthPct)
  if (growthPct !== null) {
    return {
      label: `${growthPct > 0 ? '+' : ''}${growthPct.toFixed(1)}%`,
      type: growthPct > 0 ? 'error' : (growthPct < 0 ? 'success' : 'default'),
    }
  }
  const burstRatio = nullableNumber(item?.burstRatio)
  if (burstRatio !== null) {
    return {
      label: `${burstRatio.toFixed(2)}×`,
      type: burstRatio > 1 ? 'error' : (burstRatio < 1 ? 'success' : 'default'),
    }
  }
  return {label: '--', type: 'default'}
}

export function confidencePresentation(value) {
  const confidence = stringValue(value).toLowerCase()
  if (confidence === 'high') return {label: '高', type: 'success'}
  if (confidence === 'medium') return {label: '中', type: 'info'}
  return {label: '低', type: 'warning'}
}
