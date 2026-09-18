import test from 'node:test'
import assert from 'node:assert/strict'
import {RESEARCH2_SLOTS, validResearch2Slot} from './research2-slots.js'

test('morning accounts have 24 exclusive five-minute identities', () => {
  assert.equal(RESEARCH2_SLOTS.length, 24)
  assert.equal(new Set(RESEARCH2_SLOTS.map(slot => slot.value)).size, 24)
  assert.deepEqual(RESEARCH2_SLOTS[0], {value: '09:30', label: '09:30–09:35'})
  assert.deepEqual(RESEARCH2_SLOTS.at(-1), {value: '11:25', label: '11:25–11:30'})
  assert.equal(validResearch2Slot('11:30'), false)
  assert.equal(validResearch2Slot('09:50'), true)
})
