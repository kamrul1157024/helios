// A result drawn twice, or a running call drawn as a failure, are both worse
// than the line this saves — so the pairing is checked rather than eyeballed.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  foldedCommand,
  followsItsCall,
  headline,
  resultOf,
} from '../src/renderer/components/tool-calls.ts'
import type { TranscriptMessage } from '../src/shared/models.ts'

function call(tool = 'Bash'): TranscriptMessage {
  return { seq: 0, role: 'tool_use', tool, timestamp: '2026-01-01T00:00:00Z' }
}

function result(success: boolean): TranscriptMessage {
  return { seq: 0, role: 'tool_result', tool: 'Bash', success, timestamp: '2026-01-01T00:00:00Z' }
}

test('a result after its call is folded into it', () => {
  const messages = [call(), result(true)]
  assert.equal(followsItsCall(messages, 1), true)
  assert.equal(resultOf(messages, 0), true)
})

test('a failure reads as one', () => {
  const messages = [call(), result(false)]
  assert.equal(resultOf(messages, 0), false)
})

test('a call still running has no verdict, which is not a failure', () => {
  const messages = [call()]
  assert.equal(resultOf(messages, 0), undefined)
})

test('a result with no call above it keeps its own row', () => {
  const messages = [
    { seq: 0, role: 'assistant', content: 'thinking', timestamp: '2026-01-01T00:00:00Z' },
    result(true),
  ] as TranscriptMessage[]
  assert.equal(followsItsCall(messages, 1), false)
})

test('the verdict belongs to the call it followed, not the one before that', () => {
  const messages = [call('Read'), call('Bash'), result(false)]
  assert.equal(resultOf(messages, 0), undefined)
  assert.equal(resultOf(messages, 1), false)
})

test('a described Bash row says what the agent meant, not how it did it', () => {
  const input = { command: 'gh pr create --body "$(cat <<\'EOF\'\nlong\nEOF\n)"', description: 'Open the PR' }
  assert.equal(headline('Bash', input), 'Open the PR')
  assert.equal(foldedCommand('Bash', input), input.command)
})

test('without a description the row is the command itself, and folds nothing', () => {
  const input = { command: 'npm run typecheck' }
  assert.equal(headline('Bash', input), 'npm run typecheck')
  // Nothing to hold back: the row already shows it, and opening unclamps it.
  assert.equal(foldedCommand('Bash', input), '')
})

test('a blank description does not count as one', () => {
  const input = { command: 'ls', description: '   ' }
  assert.equal(headline('Bash', input), 'ls')
  assert.equal(foldedCommand('Bash', input), '')
})

test('a description on another tool is left to that tool', () => {
  const input = { file_path: '/tmp/x.go', description: 'not a bash row' }
  assert.equal(headline('Edit', input, 'x.go'), 'x.go')
  assert.equal(foldedCommand('Edit', input), '')
})

test('a multi-line summary is flattened, so the row stays a row', () => {
  assert.equal(headline('Grep', {}, 'first\n  second'), 'first second')
})
