import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const source = await readFile(new URL('./themes-api.js', import.meta.url), 'utf8')
const generated = await readFile(new URL('./api-types.generated.ts', import.meta.url), 'utf8')

test('theme services use only generated canonical API paths', () => {
  const operations = ['listThemes', 'getTheme', 'listThemeDailySnapshots', 'listThemeCatalysts']
  for (const operation of operations) {
    assert.match(source, new RegExp(`API_PATHS\\.${operation}\\b`))
    assert.match(generated, new RegExp(`${operation}: "/api/v1/themes`))
  }
  assert.equal(source.includes('/api/v1/'), false)
})

test('theme services forward the locked query dimensions', () => {
  assert.match(source, /date, stage, q, sort, limit, cursor/)
  assert.match(source, /from, to, stage, limit, cursor/)
  assert.match(source, /date, status, minCredibility, limit, cursor/)
  assert.match(source, /requestEnvelope/)
})
