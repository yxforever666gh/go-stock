import assert from 'node:assert/strict'
import test from 'node:test'

import {isChinaTradingSession, shanghaiDate} from './market-session.js'

test('detects mainland trading windows in Asia/Shanghai', () => {
  assert.equal(isChinaTradingSession(new Date('2026-08-28T01:14:00Z')), false)
  assert.equal(isChinaTradingSession(new Date('2026-08-28T01:15:00Z')), true)
  assert.equal(isChinaTradingSession(new Date('2026-08-28T04:00:00Z')), false)
  assert.equal(isChinaTradingSession(new Date('2026-08-28T05:00:00Z')), true)
  assert.equal(isChinaTradingSession(new Date('2026-08-29T02:00:00Z')), false)
  assert.equal(shanghaiDate(new Date('2026-08-27T16:30:00Z')), '2026-08-28')
})
