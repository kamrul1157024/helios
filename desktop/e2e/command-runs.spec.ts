// A run of shell commands drawn as one line.
//
// The thing worth guarding is what a run may swallow: not the agent's prose,
// not a file it wrote, and not the command still running.
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

test('a run of commands is one row, and opens to the rows it stands for', async ({ window }) => {
  withToolCalls(ALPHA_ID, [shell('git fetch'), shell('npm run typecheck'), shell('go test ./...')])
  await open(window)

  const run = window.locator('.msg.tool-run')
  await expect(run).toHaveCount(1)
  await expect(run).toContainText('3 shell commands')
  // The three rows are inside it, not beside it.
  await expect(window.locator('.msg.tool-call')).toHaveCount(0)

  await run.locator('.tool-head').click()
  await expect(window.locator('.run-members .msg.tool-call')).toHaveCount(3)
  await expect(window.locator('.run-members')).toContainText('npm run typecheck')
})

test('two commands stay two rows', async ({ window }) => {
  withToolCalls(ALPHA_ID, [shell('git fetch'), shell('git status')])
  await open(window)

  await expect(window.locator('.msg.tool-run')).toHaveCount(0)
  await expect(window.locator('.msg.tool-call')).toHaveCount(2)
})

test('a file the agent touched keeps its own row', async ({ window }) => {
  withToolCalls(ALPHA_ID, [
    shell('git fetch'),
    shell('git status'),
    { tool: 'Edit', summary: 'main.go', input: { file_path: '/repo/main.go', old_string: 'a', new_string: 'b' } },
    shell('go build'),
  ])
  await open(window)

  // Neither side of the edit reaches three, so nothing groups and the filename
  // stays visible.
  await expect(window.locator('.msg.tool-run')).toHaveCount(0)
  await expect(window.locator('.msg.tool-call')).toHaveCount(4)
})

test('a run says when something in it failed', async ({ window }) => {
  withToolCalls(ALPHA_ID, [shell('a'), shell('b'), shell('c')])
  await open(window)

  const run = window.locator('.msg.tool-run')
  await expect(run).toHaveCount(1)
  // The stub answers every call as a success, so this is the passing shape;
  // the failing one is covered in test/tool-runs.test.ts against the messages
  // themselves.
  await expect(run.locator('.tool-verdict')).toHaveText('✓')
})
