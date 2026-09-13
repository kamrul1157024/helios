// A colour per author. Several sessions talking in one channel rendered every
// name in the same accent, so telling who said what meant reading each header.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { AUTHOR_USER, authorColour } from '../src/renderer/components/author-colour.ts'

test('the person has no colour of their own', () => {
  // The one author that is not a session. Left in the ordinary text colour,
  // which separates them from every agent more sharply than a hue would.
  assert.equal(authorColour(AUTHOR_USER), undefined)
  assert.equal(authorColour(''), undefined)
})

test('a session keeps the same colour every time it is asked for', () => {
  const first = authorColour('session:s-alpha')
  assert.ok(first)
  assert.equal(authorColour('session:s-alpha'), first)
})

test('different sessions are usually told apart', () => {
  // Not a guarantee — twelve hues collide eventually, and the name beside the
  // colour is what settles it. What must hold is that a handful of sessions in
  // one channel do not all come out the same.
  const ids = ['s-alpha', 's-beta', 's-gamma', 's-delta', 's-epsilon', 's-zeta']
  const colours = new Set(ids.map((id) => authorColour(`session:${id}`)))
  assert.ok(colours.size >= 4, `six sessions produced ${colours.size} colours`)
})

test('the hue is the only thing that varies', () => {
  // Saturation and lightness are fixed so that no session can be dealt a name
  // too dim to read on the dark surface.
  for (const id of ['s-alpha', 's-beta', 's-gamma', 's-delta']) {
    const colour = authorColour(`session:${id}`)
    assert.match(colour ?? '', /^hsl\(\d+(\.\d+)? 62% 66%\)$/)
  }
})

test('the colour follows the id, not the title', () => {
  // Titles are generated and get regenerated. A colour that moved with the
  // title would recolour a conversation already on screen.
  assert.notEqual(authorColour('session:s-alpha'), authorColour('session:s-beta'))
  assert.equal(authorColour('session:s-alpha'), authorColour('session:s-alpha'))
})
