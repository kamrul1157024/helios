// The two things the editor draws over a file that are not the file: the
// change bar against HEAD, and the tint on lines an agent has just written.
//
// Both are state fields fed by an effect, so the panel can hand them new data
// without rebuilding the editor — which would cost the reader's undo history
// and their place in the file.
import { RangeSet, RangeSetBuilder, StateEffect, StateField, type EditorState, type Extension } from '@codemirror/state'
import { Decoration, EditorView, GutterMarker, gutter, type DecorationSet } from '@codemirror/view'

import type { GutterMarks } from '../diff.ts'

class ChangeMarker extends GutterMarker {
  constructor(readonly kind: 'added' | 'changed' | 'removed') {
    super()
  }

  override eq(other: GutterMarker): boolean {
    return other instanceof ChangeMarker && other.kind === this.kind
  }

  override toDOM(): Node {
    const bar = document.createElement('span')
    bar.className = `cm-change cm-change-${this.kind}`
    bar.title = this.kind === 'added' ? 'Added since HEAD' : this.kind === 'changed' ? 'Changed since HEAD' : 'Lines removed below'
    return bar
  }
}

const MARKERS = {
  added: new ChangeMarker('added'),
  changed: new ChangeMarker('changed'),
  removed: new ChangeMarker('removed'),
}

/** Replaces the change marks. Null clears them. */
export const setChanges = StateEffect.define<GutterMarks | null>()

function changeSet(state: EditorState, marks: GutterMarks | null): RangeSet<GutterMarker> {
  if (!marks) return RangeSet.empty
  const byLine = new Map<number, ChangeMarker>()
  for (const line of marks.removed) byLine.set(Math.max(line, 1), MARKERS.removed)
  for (const line of marks.added) byLine.set(line, MARKERS.added)
  for (const line of marks.changed) byLine.set(line, MARKERS.changed)
  const builder = new RangeSetBuilder<GutterMarker>()
  for (const line of [...byLine.keys()].sort((a, b) => a - b)) {
    if (line > state.doc.lines) break
    builder.add(state.doc.line(line).from, state.doc.line(line).from, byLine.get(line) as ChangeMarker)
  }
  return builder.finish()
}

const changeField = StateField.define<RangeSet<GutterMarker>>({
  create: () => RangeSet.empty,
  update(value, transaction) {
    for (const effect of transaction.effects) {
      if (effect.is(setChanges)) return changeSet(transaction.state, effect.value)
    }
    return value.map(transaction.changes)
  },
})

/** The change bar: a thin gutter left of the numbers, as editors draw it. */
export const changeGutter: Extension = [
  changeField,
  gutter({ class: 'cm-changes-gutter', markers: (view) => view.state.field(changeField) }),
]

/** Tints these lines, for as long as the stylesheet's fade takes. Empty clears. */
export const setFlash = StateEffect.define<number[]>()

const flashLine = Decoration.line({ class: 'cm-flash' })

const flashField = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(value, transaction) {
    for (const effect of transaction.effects) {
      if (!effect.is(setFlash)) continue
      const builder = new RangeSetBuilder<Decoration>()
      const doc = transaction.state.doc
      for (const line of [...new Set(effect.value)].sort((a, b) => a - b)) {
        if (line >= 1 && line <= doc.lines) builder.add(doc.line(line).from, doc.line(line).from, flashLine)
      }
      return builder.finish()
    }
    return value.map(transaction.changes)
  },
  provide: (field) => EditorView.decorations.from(field),
})

export const flashLines: Extension = flashField
