import { EventsEmit, EventsOff, EventsOn } from '../../wailsjs/runtime'

export function registerAppRuntimeEvents({ loading, loadingMsg, realtimeProfit }) {
  EventsOn('realtime_profit', (data) => {
    realtimeProfit.value = data
  })

  EventsOn('loadingMsg', (data) => {
    if (data === 'done') {
      loadingMsg.value = '加载完成...'
      EventsEmit('loadingDone', 'app')
      loading.value = false
      return
    }
    loading.value = true
    loadingMsg.value = data
  })

  const previousErrorHandler = window.onerror
  window.onerror = function onRuntimeError(msg, source, lineno, colno, error) {
    EventsEmit('frontendError', {
      page: 'App.vue',
      message: msg,
      source,
      lineno,
      colno,
      error: error ? error.stack : null,
    })
    return true
  }

  return () => {
    EventsOff('realtime_profit')
    EventsOff('loadingMsg')
    window.onerror = previousErrorHandler
  }
}
