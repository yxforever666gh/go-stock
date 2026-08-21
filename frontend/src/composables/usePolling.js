import { onBeforeUnmount } from 'vue'

export function createPollingController(task, interval, options = {}) {
  const setTimer = options.setTimer ?? setInterval
  const clearTimer = options.clearTimer ?? clearInterval
  const documentRef = options.documentRef ?? (typeof document === 'undefined' ? null : document)
  let timer
  let running = false
  let stopped = true

  async function run() {
    if (stopped || running || documentRef?.hidden) return false
    running = true
    try {
      await task()
      return true
    } finally {
      running = false
    }
  }

  function start({ immediate = true } = {}) {
    if (!stopped) return
    stopped = false
    if (immediate) void run()
    timer = setTimer(() => void run(), interval)
  }

  function stop() {
    stopped = true
    if (timer !== undefined) clearTimer(timer)
    timer = undefined
  }

  return { run, start, stop, isRunning: () => running }
}

export function usePolling(task, interval, options) {
  const controller = createPollingController(task, interval, options)
  onBeforeUnmount(controller.stop)
  return controller
}
