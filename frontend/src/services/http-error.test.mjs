import assert from 'node:assert/strict'
import test from 'node:test'

import {responseErrorMessage} from './http-error.js'

test('prefers DataEnvelope validation errors for failed HTTP responses', () => {
  assert.equal(responseErrorMessage({errors: [{provider: 'validation', message: '日期无效'}], status: 'unavailable'}, 400), '日期无效')
  assert.equal(responseErrorMessage({error: '未授权'}, 401), '未授权')
  assert.equal(responseErrorMessage(null, 500), '请求失败: 500')
})
