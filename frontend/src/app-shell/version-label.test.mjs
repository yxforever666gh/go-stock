import assert from 'node:assert/strict'
import test from 'node:test'

import { formatGitHubVersionLabel } from './version-label.js'

test('GitHub label displays the runtime application version', () => {
  assert.equal(formatGitHubVersionLabel('1.7.9'), 'GitHub · v1.7.9')
  assert.equal(formatGitHubVersionLabel(' 1.8.0 '), 'GitHub · v1.8.0')
  assert.equal(formatGitHubVersionLabel(''), 'GitHub · vdev')
})
