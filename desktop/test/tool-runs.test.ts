// What a run may swallow. A group that eats a sentence, a filename, or the
// command still running is hiding the thing the reader is following.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { groupRuns, runSucceeded } from '../src/renderer/components/tool-runs.ts'
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

/** Three calls with their results, then a line of prose. */
const ranThree = [call(), result(), call(), result(), call(), result(), said()]

test('three consecutive commands become one run', () => {
  assert.deepEqual(groupRuns(ranThree, false), [
    { kind: 'run', indices: [0, 2, 4] },
    { kind: 'message', index: 6 },
  ])
})

test('two are left as two rows: a group of two saves nothing', () => {
  const messages = [call(), result(), call(), result()]
  assert.deepEqual(
    groupRuns(messages, false).map((item) => item.kind),
    ['message', 'message', 'message', 'message'],
  )
})

test('anything the agent says breaks a run', () => {
  const messages = [call(), result(), said('why'), call(), result(), call(), result()]
  // Two either side of the sentence, so neither group reaches three.
  assert.equal(groupRuns(messages, false).filter((item) => item.kind === 'run').length, 0)
})

test('a read or a write breaks a run, because those rows name a file', () => {
  const messages = [call(), result(), call(), result(), call('Edit'), result(), call(), result()]
  assert.deepEqual(
    groupRuns(messages, false).map((item) => item.kind),
    ['message', 'message', 'message', 'message', 'message', 'message', 'message', 'message'],
  )
})

test('a longer run groups across its own results', () => {
  const messages = [call(), result(), call(), result(), call(), result(), call(), result()]
  assert.deepEqual(groupRuns(messages, false), [{ kind: 'run', indices: [0, 2, 4, 6] }])
})

test('the run in flight is left alone while the agent is working', () => {
  // No result on the last one yet: this is what the reader is watching.
  const messages = [call(), result(), call(), result(), call()]
  assert.equal(groupRuns(messages, true).filter((item) => item.kind === 'run').length, 0)
  // Once the agent stops, the same messages group.
  assert.equal(groupRuns(messages, false).filter((item) => item.kind === 'run').length, 1)
})

test('an earlier run still groups while a later one is live', () => {
  const messages = [...ranThree, call(), result(), call(), result(), call()]
  const items = groupRuns(messages, true)
  assert.deepEqual(items[0], { kind: 'run', indices: [0, 2, 4] })
  assert.equal(items.filter((item) => item.kind === 'run').length, 1)
})

test('a run reports a failure inside it', () => {
  const messages = [call(), result(true), call(), result(false), call(), result(true)]
  assert.equal(runSucceeded(messages, [0, 2, 4]), false)
  assert.equal(runSucceeded(messages, [0, 4]), true)
})

test('a call with no result yet is not a failure', () => {
  const messages = [call()]
  assert.equal(runSucceeded(messages, [0]), true)
})

test('an empty transcript groups into nothing', () => {
  assert.deepEqual(groupRuns([], false), [])
})
