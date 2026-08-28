import assert from 'node:assert/strict'
import test from 'node:test'

import { createPollingController } from './usePolling.js'

test('polling prevents overlapping runs and stops its timer', async () => {
  let tick
  let cleared
  let release
  let calls = 0
  const controller = createPollingController(
    async () => {
      calls += 1
      await new Promise((resolve) => { release = resolve })
    },
    1000,
    {
      setTimer: (callback) => { tick = callback; return 17 },
      clearTimer: (id) => { cleared = id },
      documentRef: { hidden: false },
    },
  )

  controller.start({ immediate: false })
  tick()
  tick()
  assert.equal(calls, 1)
  assert.equal(controller.isRunning(), true)
  release()
  await Promise.resolve()
  controller.stop()
  assert.equal(cleared, 17)
})

test('polling skips work while the page is hidden', async () => {
  let calls = 0
  const controller = createPollingController(async () => { calls += 1 }, 1000, {
    setTimer: () => 1,
    clearTimer: () => {},
    documentRef: { hidden: true },
  })
  controller.start()
  await Promise.resolve()
  assert.equal(calls, 0)
  controller.stop()
})

test('polling honors an active session predicate', async () => {
  let tick
  let active = false
  let calls = 0
  const controller = createPollingController(async () => { calls += 1 }, 1000, {
    setTimer: callback => { tick = callback; return 1 },
    clearTimer: () => {},
    documentRef: {hidden: false},
    shouldRun: () => active,
  })
  controller.start({immediate: false})
  await tick()
  assert.equal(calls, 0)
  active = true
  await tick()
  assert.equal(calls, 1)
  controller.stop()
})
