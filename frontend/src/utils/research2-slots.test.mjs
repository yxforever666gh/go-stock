import test from 'node:test'
import assert from 'node:assert/strict'
import {RESEARCH2_SLOTS, research2SlotBuyLabel, validResearch2Slot} from './research2-slots.js'

test('morning accounts have 24 exclusive five-minute identities', () => {
  assert.equal(RESEARCH2_SLOTS.length, 24)
  assert.equal(new Set(RESEARCH2_SLOTS.map(slot => slot.value)).size, 24)
  assert.deepEqual(RESEARCH2_SLOTS[0], {value: '09:30', label: '09:30–09:35'})
  assert.deepEqual(RESEARCH2_SLOTS.at(-1), {value: '11:25', label: '11:25–11:30'})
  assert.equal(validResearch2Slot('11:30'), false)
  assert.equal(validResearch2Slot('09:50'), true)
})

test('slot labels show only buy execution', () => {
  assert.equal(research2SlotBuyLabel({buyStatus: 'bought_full', boughtCount: 5, buyTargetCount: 5}), '买入：已买入 5/5')
  assert.equal(research2SlotBuyLabel({buyStatus: 'bought_partial', boughtCount: 3, buyTargetCount: 5}), '买入：已买入 3/5，本轮结束')
  assert.equal(research2SlotBuyLabel({buyStatus: 'awaiting_quote', pendingBuyCount: 2}), '买入：等待行情（2 笔）')
  assert.equal(research2SlotBuyLabel({buyStatus: 'cutoff'}), '买入：窗口已截止')
  assert.equal(research2SlotBuyLabel(), '买入：等待报告')
})
