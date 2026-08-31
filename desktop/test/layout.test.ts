import assert from 'node:assert/strict'
import test from 'node:test'

import {
  defaultLayout,
  evenSizes,
  groupOf,
  isVisible,
  moveItem,
  panelItem,
  panelOf,
  PANEL_ITEMS,
  parseLayout,
  placedItems,
  reconcile,
  removeItem,
  resize,
  reveal,
  splitInto,
  sweep,
  tabOf,
  termItem,
  totalSize,
  visibleItems,
  type Group,
  type Layout,
} from '../src/renderer/components/layout.ts'

/** Indexing a group, with the check the type checker wants written once. */
function g(layout: Layout, index: number): Group {
  const group = layout.groups[index]
  assert.ok(group, `no group at ${index}`)
  return group
}

function sizes(layout: Layout): number[] {
  return layout.groups.map((group) => group.size)
}

const CHAT = panelItem('chat')
const TERMINAL = panelItem('terminal')
const APPROVALS = panelItem('approvals')
const FILES = panelItem('files')
const GIT = panelItem('git')
const TERM = termItem('h1:s1')
const SHELL = termItem('h1:s1:sh2')

/** The default, with `files` dragged out beside the transcript. */
function split(): Layout {
  return splitInto(defaultLayout(), FILES, 'g1', 'after')
}

/** What is left in the first group once `files` has been split out of it. */
const REST = [CHAT, TERMINAL, APPROVALS, GIT]

test('an item names either a panel or a terminal', () => {
  assert.equal(panelOf(FILES), 'files')
  assert.equal(panelOf(TERM), null)
  assert.equal(tabOf(TERM), 'h1:s1')
  assert.equal(tabOf(FILES), null)
})

test('a session starts as one group listing every panel, reading the transcript', () => {
  const layout = defaultLayout()
  assert.equal(layout.groups.length, 1)
  assert.deepEqual(placedItems(layout), PANEL_ITEMS)
  assert.equal(g(layout, 0).active, CHAT)
  assert.equal(layout.focused, g(layout, 0).id)
})

test('revealing an unplaced item appends it to the focused group', () => {
  const layout = reveal(defaultLayout(), TERM)
  assert.deepEqual(placedItems(layout), [...PANEL_ITEMS, TERM])
  assert.equal(g(layout, 0).active, TERM)
})

test('revealing a panel already in the strip only brings it forward', () => {
  const layout = reveal(defaultLayout(), FILES)
  assert.deepEqual(placedItems(layout), PANEL_ITEMS)
  assert.equal(g(layout, 0).active, FILES)
})

test('revealing a placed item activates it where it is and focuses that group', () => {
  const layout = split()
  const left = g(layout, 0)
  const right = g(layout, 1)
  assert.deepEqual(left.items, REST)
  assert.deepEqual(right.items, [FILES])

  const back = reveal(layout, CHAT)
  assert.equal(back.focused, left.id)
  // The reveal must not drag files out of the group the user put it in.
  assert.equal(groupOf(back, FILES)?.id, right.id)
})

test('a split makes a group beside the one it came from and halves its weight', () => {
  const layout = split()
  assert.equal(layout.groups.length, 2)
  assert.deepEqual(sizes(layout), [0.5, 0.5])
  assert.equal(layout.focused, g(layout, 1).id)
  assert.equal(totalSize(layout), 1)
})

test('splitting before puts the new group on the left', () => {
  const layout = splitInto(defaultLayout(), FILES, 'g1', 'before')
  assert.deepEqual(g(layout, 0).items, [FILES])
  assert.deepEqual(g(layout, 1).items, REST)
})

test('splitting a lone item off its own group does nothing', () => {
  const layout = split()
  const right = g(layout, 1)
  assert.equal(splitInto(layout, FILES, right.id, 'after'), layout)
})

test('both groups are visible at once', () => {
  const layout = split()
  assert.deepEqual(visibleItems(layout).sort(), [CHAT, FILES].sort())
  assert.equal(isVisible(layout, CHAT), true)
  assert.equal(isVisible(layout, FILES), true)
})

test('moving the last item out of a group collapses it and returns its weight', () => {
  const layout = split()
  const left = g(layout, 0)
  const right = g(layout, 1)
  const merged = moveItem(layout, FILES, left.id, 1)

  assert.equal(merged.groups.length, 1)
  // Dropped at slot 1, which is where the pointer was — not appended.
  assert.deepEqual(g(merged, 0).items, [CHAT, FILES, TERMINAL, APPROVALS, GIT])
  assert.equal(g(merged, 0).size, 1)
  assert.equal(groupOf(merged, FILES)?.id, left.id)
  assert.equal(merged.groups.some((group) => group.id === right.id), false)
})

test('moving within a group reorders rather than collapsing', () => {
  const moved = moveItem(defaultLayout(), GIT, 'g1', 0)
  assert.deepEqual(g(moved, 0).items, [GIT, CHAT, TERMINAL, APPROVALS, FILES])
  assert.equal(moved.groups.length, 1)
})

test('closing the middle of three shells activates the one before it', () => {
  let layout = defaultLayout()
  for (const tab of ['a', 'b', 'c']) layout = reveal(layout, termItem(tab))
  layout = reveal(layout, termItem('b'))

  const closed = removeItem(layout, termItem('b'))
  assert.equal(g(closed, 0).active, termItem('a'))
  assert.deepEqual(g(closed, 0).items, [...PANEL_ITEMS, termItem('a'), termItem('c')])
})

test('emptying the only group falls back to the transcript rather than nothing', () => {
  const lone = parseLayout({ groups: [{ id: 'g1', items: [FILES], active: FILES, size: 1 }] })
  const layout = removeItem(lone, FILES)
  assert.deepEqual(g(layout, 0).items, [CHAT])
  assert.equal(g(layout, 0).active, CHAT)
})

test('closing the last item of a split group collapses it', () => {
  const layout = split()
  const closed = removeItem(layout, FILES)
  assert.equal(closed.groups.length, 1)
  assert.deepEqual(g(closed, 0).items, REST)
  assert.equal(g(closed, 0).size, 1)
  assert.equal(closed.focused, g(closed, 0).id)
})

test('reconcile appends live tabs it has not placed', () => {
  const layout = reconcile(defaultLayout(), ['h1:s1', 'h1:s1:sh2'])
  assert.deepEqual(placedItems(layout), [...PANEL_ITEMS, TERM, SHELL])
})

test('reconcile never prunes: a saved terminal survives until its tab re-attaches', () => {
  const saved = parseLayout({
    axis: 'row',
    focused: 'g1',
    groups: [{ id: 'g1', items: [CHAT, SHELL], active: SHELL, size: 1 }],
  })
  // The shells have not been listed yet — this is the window syncShells awaits in.
  const early = reconcile(saved, [])
  assert.deepEqual(placedItems(early), [CHAT, SHELL])
  assert.equal(g(early, 0).active, SHELL)
})

test('reconcile is idempotent for tabs already placed', () => {
  const once = reconcile(defaultLayout(), ['h1:s1'])
  assert.equal(reconcile(once, ['h1:s1']), once)
})

test('sweep spares an item that is visible in a group other than the focused one', () => {
  const layout = split()
  const old = Date.now() - 10 * 60 * 1000
  const dead = sweep(layout, { [CHAT]: old, [FILES]: old, [GIT]: old }, Date.now(), 5 * 60 * 1000)
  // chat and files are each in front of a group; only git is unseen.
  assert.deepEqual(dead, [GIT])
})

test('sweep leaves items inside the window alone', () => {
  const layout = split()
  const now = Date.now()
  assert.deepEqual(sweep(layout, { [GIT]: now - 1000 }, now, 5 * 60 * 1000), [])
})

test('a sash moves weight between its two groups and keeps the total', () => {
  const layout = resize(split(), 0, 0.2)
  assert.deepEqual(sizes(layout), [0.7, 0.3])
  assert.equal(totalSize(layout), 1)
})

test('a sash cannot squeeze a group out of existence', () => {
  const layout = resize(split(), 0, -5)
  assert.ok(g(layout, 0).size >= 0.15)
  assert.equal(totalSize(layout), 1)
})

test('a sash that names no pair is a no-op', () => {
  const layout = split()
  assert.equal(resize(layout, 1, 0.2), layout)
})

test('even sizes reset the weights', () => {
  const layout = evenSizes(resize(split(), 0, 0.3))
  assert.deepEqual(sizes(layout), [1, 1])
})

test('parseLayout falls back to the default for anything it cannot read', () => {
  const chat = PANEL_ITEMS
  for (const raw of [null, undefined, 'nope', 42, [], {}, { groups: [] }, { groups: 'x' }]) {
    assert.deepEqual(g(parseLayout(raw), 0).items, chat, `for ${JSON.stringify(raw)}`)
  }
})

test('parseLayout rejects a group with no id and one whose items are not strings', () => {
  assert.equal(parseLayout({ groups: [{ items: [CHAT], active: CHAT, size: 1 }] }).groups.length, 1)
  assert.deepEqual(g(parseLayout({ groups: [{ id: 'g1', items: [7] }] }), 0).items, PANEL_ITEMS)
})

test('parseLayout repairs a focus and an active that name nothing', () => {
  const layout = parseLayout({
    axis: 'row',
    focused: 'gone',
    groups: [{ id: 'g1', items: [CHAT], active: 'panel:vanished', size: 1 }],
  })
  assert.equal(layout.focused, 'g1')
  assert.equal(g(layout, 0).active, CHAT)
})

test('parseLayout keeps a good layout as it was', () => {
  const saved = split()
  const read = parseLayout(JSON.parse(JSON.stringify(saved)))
  assert.deepEqual(read, saved)
})

test('a group id restored from disk is never handed out twice', () => {
  const saved = parseLayout({
    axis: 'row',
    focused: 'g9',
    groups: [{ id: 'g9', items: [CHAT, FILES], active: CHAT, size: 1 }],
  })
  const after = splitInto(saved, FILES, 'g9', 'after')
  assert.equal(new Set(after.groups.map((group) => group.id)).size, after.groups.length)
})
