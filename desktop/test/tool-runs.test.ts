// What a turn may swallow. A group that eats a sentence, or the call still
// running, is hiding the thing the reader is following.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { groupRuns, runSucceeded, summariseTurn } from '../src/renderer/components/tool-runs.ts'
import type { TranscriptMessage } from '../src/shared/models.ts'

const stamp = '2026-01-01T00:00:00Z'

function call(tool = 'Bash'): TranscriptMessage {
  return { seq: 0, role: 'tool_use', tool, timestamp: stamp }
}

function result(success = true): TranscriptMessage {
  return { seq: 0, role: 'tool_result', tool: 'Bash', success, timestamp: stamp }
}

function said(content = 'a sentence'): TranscriptMessage {
  return { seq: 0, role: 'assistant', content, timestamp: stamp }
}

function kinds(items: ReturnType<typeof groupRuns>): string[] {
  return items.map((item) => item.kind)
}

/** Three calls with their results, then a line of prose. */
const ranThree = [call(), result(), call(), result(), call(), result(), said()]

test('the calls between two things the agent said become one turn', () => {
  assert.deepEqual(groupRuns(ranThree, false), [
    { kind: 'turn', indices: [0, 2, 4] },
    { kind: 'message', index: 6 },
  ])
})

test('a turn groups whatever kinds it holds', () => {
  const messages = [call('Read'), result(), call('Edit'), result(), call('Bash'), result()]
  assert.deepEqual(groupRuns(messages, false), [{ kind: 'turn', indices: [0, 2, 4] }])
})

test('a lone call keeps its row: the row says more than a count would', () => {
  const messages = [call('Edit'), result(), said()]
  assert.deepEqual(kinds(groupRuns(messages, false)), ['message', 'message', 'message'])
})

test('anything the agent says breaks a turn', () => {
  const messages = [call(), result(), said('why'), call(), result(), call(), result()]
  const items = groupRuns(messages, false)
  // The single call before the sentence stays a row; the pair after it groups.
  assert.deepEqual(kinds(items), ['message', 'message', 'message', 'turn'])
})

test('the turn in flight is left alone while the agent is working', () => {
  // No result on the last one yet: this is what the reader is watching.
  const messages = [call(), result(), call()]
  assert.equal(kinds(groupRuns(messages, true)).filter((kind) => kind === 'turn').length, 0)
  // Once the agent stops, the same messages group.
  assert.equal(kinds(groupRuns(messages, false)).filter((kind) => kind === 'turn').length, 1)
})

test('an earlier turn still groups while a later one is live', () => {
  const messages = [...ranThree, call(), result(), call()]
  const items = groupRuns(messages, true)
  assert.deepEqual(items[0], { kind: 'turn', indices: [0, 2, 4] })
  assert.equal(kinds(items).filter((kind) => kind === 'turn').length, 1)
})

test('the summary counts kinds, in one order however they were called', () => {
  const messages = [
    call('Read'),
    result(),
    call('Bash'),
    result(),
    call('Edit'),
    result(),
    call('Read'),
    result(),
  ]
  assert.equal(summariseTurn(messages, [0, 2, 4, 6]), '1 shell, 1 write, 2 reads')
})

test('a tool of no particular kind is counted as a call', () => {
  const messages = [call('WebFetch'), result(), call('WebFetch'), result()]
  assert.equal(summariseTurn(messages, [0, 2]), '2 calls')
})

test('one of a kind reads as one, not as none', () => {
  const messages = [call('Grep'), result(), call('Bash'), result()]
  assert.equal(summariseTurn(messages, [0, 2]), '1 shell, 1 search')
})

test('a turn reports a failure inside it', () => {
  const messages = [call(), result(true), call(), result(false), call(), result(true)]
  assert.equal(runSucceeded(messages, [0, 2, 4]), false)
  assert.equal(runSucceeded(messages, [0, 4]), true)
})

test('a call with no result yet is not a failure', () => {
  assert.equal(runSucceeded([call()], [0]), true)
})

test('an empty transcript groups into nothing', () => {
  assert.deepEqual(groupRuns([], false), [])
})
