// Finding the handles in a message. The decoration that hangs off this needs a
// DOM and is covered by e2e; the parsing does not, and is the half that can be
// wrong quietly.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { findMentionTokens } from '../src/renderer/components/mentions.ts'

test('a handle is found once, however often it is written', () => {
  const got = findMentionTokens('@port-client and @split-orders, then @port-client again')
  assert.deepEqual(got.sort(), ['port-client', 'split-orders'])
})

test('punctuation is not part of a handle', () => {
  // "@port-client," names port-client. A handle that swallowed the comma would
  // resolve to nobody and quietly wake no one.
  assert.deepEqual(findMentionTokens('ping @port-client, please'), ['port-client'])
  assert.deepEqual(findMentionTokens('(@port-client)'), ['port-client'])
})

test('an email address is not a mention', () => {
  // The token has to start at an @ that begins a word; "a@b" is not an address
  // of anybody in a channel.
  assert.deepEqual(findMentionTokens('mail me at nobody@example.com'), ['example'])
})

test('handles are matched case-insensitively', () => {
  assert.deepEqual(findMentionTokens('@Port-Client'), ['port-client'])
})

test('a bare @ names nobody', () => {
  assert.deepEqual(findMentionTokens('cost @ £4, and @ the end'), [])
})
