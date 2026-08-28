import {computed, getCurrentInstance, onBeforeUnmount, ref, unref, watch} from 'vue'
import {createPollingController} from './usePolling.js'
import {markEnvelopeStale, parseDataEnvelope} from '../services/data-envelope.js'
import {isMarketSessionOpen} from '../market-tabs/market-session.js'

export function hasUsableEnvelopeData(envelope) {
  if (!['ok', 'partial', 'stale'].includes(String(envelope?.status || ''))) return false
  const value = envelope?.data
  if (Array.isArray(value)) return value.length > 0
  if (value && typeof value === 'object') {
    if (Array.isArray(value.rows)) return value.rows.length > 0
    if (Array.isArray(value.items)) return value.items.length > 0
    if (Array.isArray(value.list)) return value.list.length > 0
    if (Array.isArray(value.snapshots)) return value.snapshots.length > 0
    return Object.keys(value).length > 0
  }
  return value !== undefined && value !== null && value !== ''
}

export function useMarketDataResource({
  active,
  fallbackData = [],
  intervalMs = 60000,
  loader,
  requestKey = '',
  session = 'cn',
}) {
  const envelope = ref(parseDataEnvelope({data: fallbackData, status: 'unavailable'}))
  const loading = ref(false)
  const error = ref('')
  const hasSuccessfulData = ref(false)
  let requestVersion = 0
  let currentIdentity = resolveRequestIdentity(requestKey)

  function resetSnapshot(nextIdentity = resolveRequestIdentity(requestKey)) {
    requestVersion += 1
    currentIdentity = nextIdentity
    envelope.value = parseDataEnvelope({data: fallbackData, status: 'unavailable'})
    loading.value = false
    error.value = ''
    hasSuccessfulData.value = false
  }

  async function refresh({silent = false} = {}) {
    const identity = resolveRequestIdentity(requestKey)
    if (identity !== currentIdentity) resetSnapshot(identity)
    const version = ++requestVersion
    if (!silent) loading.value = true
    try {
      const result = parseDataEnvelope(await loader(), fallbackData)
      if (version !== requestVersion || identity !== currentIdentity) return false
      if (hasUsableEnvelopeData(result)) {
        envelope.value = result
        error.value = ''
        hasSuccessfulData.value = true
        return true
      }
      if (hasSuccessfulData.value) {
        envelope.value = markEnvelopeStale(envelope.value, result.errors?.[0] || `数据状态：${result.status}`)
      } else {
        envelope.value = result
      }
      error.value = result.errors?.map(item => item?.message || String(item)).join('；') || ''
      return false
    } catch (reason) {
      if (version !== requestVersion || identity !== currentIdentity) return false
      error.value = reason?.message || String(reason)
      envelope.value = hasSuccessfulData.value
        ? markEnvelopeStale(envelope.value, reason)
        : {...markEnvelopeStale({data: fallbackData}, reason), status: 'unavailable', stale: false}
      return false
    } finally {
      if (version === requestVersion && !silent) loading.value = false
    }
  }

  const polling = createPollingController(
    () => refresh({silent: true}),
    intervalMs,
    {
      shouldRun: () => Boolean(unref(active)) && isMarketSessionOpen(session),
    },
  )

  const stopWatch = watch(
    [() => Boolean(unref(active)), () => resolveRequestIdentity(requestKey)],
    ([enabled, identity], previous = []) => {
      const [wasEnabled] = previous
      const identityChanged = identity !== currentIdentity
      if (identityChanged) resetSnapshot(identity)
      if (!enabled) {
        requestVersion += 1
        polling.stop()
        loading.value = false
        return
      }
      if (!wasEnabled || identityChanged) void refresh()
      polling.start({immediate: false})
    },
    {immediate: true},
  )

  function dispose() {
    requestVersion += 1
    polling.stop()
    stopWatch()
  }

  if (getCurrentInstance()) onBeforeUnmount(dispose)

  return {
    data: computed(() => envelope.value.data),
    envelope,
    error,
    loading,
    refresh,
    reset: resetSnapshot,
    dispose,
  }
}

function resolveRequestIdentity(requestKey) {
  const value = typeof requestKey === 'function' ? requestKey() : unref(requestKey)
  if (typeof value === 'string') return value
  if (value === undefined || value === null) return ''
  try {
    return JSON.stringify(value)
  } catch (_) {
    return String(value)
  }
}
