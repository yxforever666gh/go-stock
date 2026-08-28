import {computed, ref, shallowRef, unref, watch} from 'vue'
import {GetInstrumentChart, GetLegacyInstrumentChart} from '../services/instruments-api.js'
import {markEnvelopeStale, parseDataEnvelope} from '../services/data-envelope.js'

function identityOf(query = {}) {
  return [query.code, query.assetType, query.market, query.period, query.adjustment, query.from, query.to, query.limit, query.legacy, query.name].join('|')
}

export function useInstrumentChart(queryRef) {
  const envelope = shallowRef(parseDataEnvelope({data: {bars: [], missingIntervals: []}, status: 'unavailable'}))
  const loading = ref(false)
  const error = ref('')
  let requestVersion = 0
  let succeeded = false

  const identity = computed(() => identityOf(unref(queryRef)))

  async function refresh() {
    const query = unref(queryRef) || {}
    if (!query.code) return
    const version = ++requestVersion
    loading.value = true
    error.value = ''
    try {
      const result = query.legacy
        ? await GetLegacyInstrumentChart(query.code, query)
        : await GetInstrumentChart(query.code, query)
      if (version !== requestVersion) return
      if (result.status === 'unavailable' && succeeded) {
        envelope.value = markEnvelopeStale(envelope.value, result.errors?.[0] || '最新图表数据不可用')
      } else {
        envelope.value = result
        succeeded = result.status !== 'unavailable'
      }
    } catch (reason) {
      if (version !== requestVersion) return
      error.value = reason?.message || String(reason)
      envelope.value = succeeded
        ? markEnvelopeStale(envelope.value, reason)
        : parseDataEnvelope({data: {bars: [], missingIntervals: []}, status: 'unavailable', errors: [error.value]})
    } finally {
      if (version === requestVersion) loading.value = false
    }
  }

  watch(identity, () => {
    requestVersion += 1
    succeeded = false
    envelope.value = parseDataEnvelope({data: {bars: [], missingIntervals: []}, status: 'unavailable'})
    void refresh()
  }, {immediate: true})

  return {envelope, loading, error, refresh}
}
