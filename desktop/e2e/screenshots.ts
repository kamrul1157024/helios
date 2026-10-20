// Pictures of the files panel for a reviewer (spec 65, "Testing").
//
// Saved beside the specs rather than in test-results, which Playwright clears
// on every run and keeps only for failures. The folder is ignored by git: the
// pictures are for looking at, not for diffing.
import path from 'node:path'

import type { Page } from '@playwright/test'

export async function snap(window: Page, name: string): Promise<void> {
  await window.screenshot({ path: path.join(__dirname, 'screenshots', `${name}.png`) })
}
