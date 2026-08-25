import test from 'node:test'
import assert from 'node:assert/strict'

import {
  assertLoopbackHost,
  assertPublishableCandidate,
  isAllowedAction,
} from '../lib/policy.mjs'

test('只允许本机回环地址监听控制台', () => {
  assert.doesNotThrow(() => assertLoopbackHost('127.0.0.1'))
  assert.throws(() => assertLoopbackHost('0.0.0.0'), /127\.0\.0\.1/)
})

test('仅允许已准备且校验通过的候选发布', () => {
  assert.doesNotThrow(() => assertPublishableCandidate({
    status: 'ready',
    sha256: 'a'.repeat(64),
  }))
  assert.throws(() => assertPublishableCandidate({ status: 'building' }), /候选/)
  assert.throws(() => assertPublishableCandidate({ status: 'ready', sha256: 'invalid' }), /SHA256/)
})

test('控制台只接受明确的发布操作', () => {
	assert.equal(isAllowedAction('status'), true)
	assert.equal(isAllowedAction('deploy'), true)
	assert.equal(isAllowedAction('prepare-bluegreen'), true)
	assert.equal(isAllowedAction('return-blue'), true)
	assert.equal(isAllowedAction('cleanup-release'), true)
	assert.equal(isAllowedAction('promote-green'), false)
	assert.equal(isAllowedAction('shell'), false)
})
