function normalizedErrors(value) {
  if (!value) return []
  if (Array.isArray(value)) return value.filter(Boolean)
  return [value]
}

export function parseDataEnvelope(payload, fallbackData = []) {
  const objectPayload = payload && typeof payload === 'object' && !Array.isArray(payload) ? payload : null
  const isEnvelope = objectPayload && Object.prototype.hasOwnProperty.call(objectPayload, 'data')
  const data = isEnvelope ? objectPayload.data : (payload ?? fallbackData)
  const errors = normalizedErrors(objectPayload?.errors ?? objectPayload?.error)
  const rawStatus = String(objectPayload?.status || '').trim().toLowerCase()
  const partial = objectPayload?.partial === true || rawStatus === 'partial' || (!rawStatus && errors.length > 0)
  const stale = objectPayload?.stale === true || rawStatus === 'stale'
  const status = stale ? 'stale' : (rawStatus || (partial ? 'partial' : 'ok'))
  const sources = Array.isArray(objectPayload?.sources) ? objectPayload.sources : []
  const warnings = normalizedErrors(objectPayload?.warnings).map(item => item?.message || String(item))

  return {
    data: data ?? fallbackData,
    source: objectPayload?.source ?? objectPayload?.sources ?? '',
    asOf: objectPayload?.asOf ?? objectPayload?.as_of ?? '',
    fetchedAt: objectPayload?.fetchedAt ?? objectPayload?.fetched_at ?? '',
    status,
    errors,
    sources,
    warnings,
    evidenceProfile: String(objectPayload?.evidenceProfile || ''),
    evidenceSetId: String(objectPayload?.evidenceSetId || ''),
    partial,
    stale,
    meta: objectPayload?.meta && typeof objectPayload.meta === 'object' ? objectPayload.meta : {},
  }
}

export function markEnvelopeStale(envelope, error) {
  const errors = normalizedErrors(error?.message || error)
  return {
    ...parseDataEnvelope(envelope),
    status: 'stale',
    stale: true,
    errors: [...(envelope?.errors || []), ...errors],
  }
}
