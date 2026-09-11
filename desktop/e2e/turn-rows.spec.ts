// A turn's tool calls drawn as one line.
//
// The thing worth guarding is what a turn may swallow: not the agent's prose,
// and not the call still running.
import type { Page } from '@playwright/test'

import { ALPHA as ALPHA_ID, withToolCalls } from './daemon.ts'
import { expect, test } from './fixtures.ts'

const ALPHA = 'Alpha'

function shell(command: string) {
  return { tool: 'Bash', summary: command, input: { command } }
}

async function open(window: Page): Promise<void> {
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await expect(window.locator('.panel-tabs')).toBeVisible()
}

test('a turn is one row, and opens to the rows it stands for', async ({ window }) => {
  withToolCalls(ALPHA_ID, [shell('git fetch'), shell('npm run typecheck'), shell('go test ./...')])
  await open(window)

  const turn = window.locator('.msg.tool-turn')
  await expect(turn).toHaveCount(1)
  await expect(turn).toContainText('3 steps')
  await expect(turn).toContainText('3 shell')
  // The three rows are inside it, not beside it.
  await expect(window.locator('.msg.tool-call')).toHaveCount(0)

  await turn.locator('.tool-head').click()
  await expect(window.locator('.turn-members .msg.tool-call')).toHaveCount(3)
  await expect(window.locator('.turn-members')).toContainText('npm run typecheck')
})

test('the line names the mix, not just the number', async ({ window }) => {
  withToolCalls(ALPHA_ID, [
    shell('git fetch'),
    { tool: 'Read', summary: 'main.go', input: { file_path: '/repo/main.go' } },
    { tool: 'Edit', summary: 'main.go', input: { file_path: '/repo/main.go', old_string: 'a', new_string: 'b' } },
  ])
  await open(window)

  const turn = window.locator('.msg.tool-turn')
  await expect(turn).toContainText('1 shell, 1 write, 1 read')
})

test('a lone call keeps its own row', async ({ window }) => {
  withToolCalls(ALPHA_ID, [
    { tool: 'Edit', summary: 'main.go', input: { file_path: '/repo/main.go', old_string: 'a', new_string: 'b' } },
  ])
  await open(window)

  await expect(window.locator('.msg.tool-turn')).toHaveCount(0)
  await expect(window.locator('.msg.tool-call')).toHaveCount(1)
  // And the row still carries its patch, which is the point of leaving it.
  await expect(window.locator('.diff-line').first()).toBeVisible()
})

test('a turn carries the verdict of everything in it', async ({ window }) => {
  withToolCalls(ALPHA_ID, [shell('a'), shell('b'), shell('c')])
  await open(window)

  const turn = window.locator('.msg.tool-turn')
  await expect(turn).toHaveCount(1)
  // The stub answers every call as a success, so this is the passing shape;
  // the failing one is covered in test/tool-runs.test.ts against the messages.
  await expect(turn.locator('.tool-verdict')).toHaveText('✓')
})

test('opening every tool call opens the turns too', async ({ window }) => {
  withToolCalls(ALPHA_ID, [shell('a'), shell('b'), shell('c')])
  await open(window)
  await expect(window.locator('.turn-members')).toHaveCount(0)

  // Fold, then open: a press that left the groups shut would have opened
  // nothing the reader can see.
  await window.locator('.tab-fold').click()
  await window.locator('.tab-fold').click()

  await expect(window.locator('.turn-members')).toHaveCount(1)
})
