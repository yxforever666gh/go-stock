import test from 'node:test'
import assert from 'node:assert/strict'
import {formatDrawdown, formatInteger, formatMoney, formatNumber, formatPercent, formatPrice} from './number-format.js'

test('formats research numbers with international thousands separators', () => {
  assert.equal(formatInteger(1234567), '1,234,567')
  assert.equal(formatNumber(1234567.8, 2), '1,234,567.80')
  assert.equal(formatPrice(12345.6), '12,345.600')
  assert.equal(formatMoney(1234567.8), '¥1,234,567.80')
  assert.equal(formatMoney(-1234567.8), '-¥1,234,567.80')
  assert.equal(formatPercent(12.34567), '+1,234.57%')
  assert.equal(formatDrawdown(0.034), '-3.40%')
  assert.equal(formatDrawdown(-0.034), '-3.40%')
})
