import { css } from '@codemirror/lang-css'
import { go } from '@codemirror/lang-go'
import { html } from '@codemirror/lang-html'
import { javascript } from '@codemirror/lang-javascript'
import { json } from '@codemirror/lang-json'
import { markdown } from '@codemirror/lang-markdown'
import { python } from '@codemirror/lang-python'
import { rust } from '@codemirror/lang-rust'
import { yaml } from '@codemirror/lang-yaml'
import { indentUnit } from '@codemirror/language'
import { Compartment, EditorState, Text, type Extension } from '@codemirror/state'
import { EditorView, keymap } from '@codemirror/view'
import { basicSetup } from 'codemirror'
import { useEffect, useRef, useState } from 'react'

import { heliosEditorTheme } from '../codemirror-theme.ts'
import type { GutterMarks } from '../diff.ts'
import { languageForPath } from '../markdown.ts'
import { changeGutter, flashLines, setChanges, setFlash } from './editor-marks.ts'

/** A place to put the cursor, re-sent with a new seq to jump there again. */
export interface Cursor {
  line: number
  column: number
  seq: number
  /** Tint the line on arrival, for a jump from a search result. */
  flash?: boolean
}

/** Lines to tint for a moment, re-sent with a new seq to tint again. */
export interface Flash {
  lines: number[]
  seq: number
}

/** How long a tint lasts. Matches the fade in the stylesheet. */
const FLASH_MS = { agent: 1500, jump: 1200 }

/**
 * Where a file was last being read.
 *
 * The scroll offset is kept as well as the caret because the two are not the
 * same answer: a caret scrolled back into view lands wherever the editor
 * chooses to put it, which is rarely where the reader had it.
 */
export interface ReadingPosition {
  line: number
  column: number
  /** Pixels down the scroller. */
  top: number
}

/** Where the pointer was, and the lines it was pointing at. */
export interface ContextTarget {
  x: number
  y: number
  startLine: number
  endLine: number
}

interface Props {
  /** The buffer's starting text. Mount one editor per file — key by path. */
  doc: string | Text
  /**
   * The text as it stands on disk, which the buffer is compared against to
   * decide whether it is dirty.
   */
  saved: string
  path: string
  readOnly?: boolean
  /** Said in the status line: what the bytes were read as. */
  encoding?: string
  /** Long lines wrap rather than scroll. */
  wrap?: boolean
  /** The change bar against HEAD, or null for none. */
  changes?: GutterMarks | null
  /** Lines an agent just wrote, to tint. */
  flash?: Flash | null
  /** Text size, in px. */
  fontSize?: number
  /**
   * The buffer moved. It hands over the document itself rather than a string:
   * building the string is O(file) and this fires on every keystroke. The save
   * builds it once, when it is needed.
   */
  onChange: (doc: Text, dirty: boolean) => void
  onSave: () => void
  cursor?: Cursor | null
  /**
   * Whether to take the keyboard when the buffer is built. Default true.
   *
   * False when the remount was not asked for by a person: a file event
   * replacing the text of a tab that happens to be in front must not pull the
   * caret out of whatever the user is typing into elsewhere.
   */
  autoFocus?: boolean
  /** Where the file was left last time. Applied once, as the buffer is built. */
  restore?: ReadingPosition | null
  onViewChange?: (at: ReadingPosition) => void
  onContextMenu?: (target: ContextTarget) => void
}

/**
 * A CodeMirror buffer.
 *
 * The text lives in CodeMirror rather than in React state: routing every
 * keystroke through the panel would re-render the tree and the tab strip for
 * each character typed. The parent hears about edits through onChange and keeps
 * the draft in a ref.
 */
export function CodeEditor({
  doc,
  saved,
  path,
  readOnly,
  encoding = 'UTF-8',
  wrap = false,
  changes = null,
  flash = null,
  fontSize,
  onChange,
  onSave,
  cursor,
  autoFocus = true,
  restore,
  onViewChange,
  onContextMenu,
}: Props): JSX.Element {
  const host = useRef<HTMLDivElement | null>(null)
  const view = useRef<EditorView | null>(null)
  const wrapping = useRef(new Compartment())
  const [status, setStatus] = useState<{ line: number; column: number; selected: number }>({
    line: 1,
    column: 1,
    selected: 0,
  })
  /**
   * The text on disk as a CodeMirror document, for the dirty check (spec 65,
   * 2.4). Comparing documents walks the rope and stops at the first difference;
   * a length that differs settles it without walking at all. Rebuilt only when
   * the disk copy changes, which is a save or a reload, not a keystroke.
   */
  const savedDoc = useRef<{ text: string; doc: Text } | null>(null)
  if (savedDoc.current?.text !== saved) savedDoc.current = { text: saved, doc: Text.of(saved.split(/\r\n?|\n/)) }
  const wrapRef = useRef(wrap)
  wrapRef.current = wrap
  // Held in refs so a new callback identity does not tear down the editor.
  const handlers = useRef({ onChange, onSave, onContextMenu, onViewChange })
  handlers.current = { onChange, onSave, onContextMenu, onViewChange }
  // Read once, when the buffer is built. As a prop it would rebuild the editor
  // every time the position it describes changed, which is every keystroke.
  const restoreRef = useRef(restore)
  restoreRef.current = restore
  // Read at mount for the same reason as restore: as a dependency it would
  // rebuild the editor whenever the panel changed its mind about focus.
  const focusRef = useRef(autoFocus)
  focusRef.current = autoFocus

  const report = (editor: EditorView): void => {
    const { main } = editor.state.selection
    const line = editor.state.doc.lineAt(main.head)
    const column = main.head - line.from + 1
    setStatus((current) =>
      current.line === line.number && current.column === column && current.selected === main.to - main.from
        ? current
        : { line: line.number, column, selected: main.to - main.from },
    )
    handlers.current.onViewChange?.({ line: line.number, column, top: editor.scrollDOM.scrollTop })
  }

  useEffect(() => {
    if (!host.current) return
    // A read-only view is a file past the edit limit, and a grammar over
    // megabytes is the one thing here that would hold the window. It is shown
    // plain, with line numbers and search, which is what reading one needs.
    const language = readOnly ? null : editorLanguage(path)
    const state = EditorState.create({
      doc,
      extensions: [
        basicSetup,
        heliosEditorTheme,
        indentUnit.of('  '),
        changeGutter,
        flashLines,
        wrapping.current.of(wrapRef.current ? EditorView.lineWrapping : []),
        ...(language ? [language] : []),
        keymap.of([
          {
            key: 'Mod-s',
            preventDefault: true,
            run: () => {
              handlers.current.onSave()
              return true
            },
          },
        ]),
        EditorState.readOnly.of(Boolean(readOnly)),
        EditorView.editable.of(!readOnly),
        EditorView.updateListener.of((update) => {
          if (update.docChanged) {
            const now = update.state.doc
            const disk = savedDoc.current?.doc
            handlers.current.onChange(now, !disk || now.length !== disk.length || !now.eq(disk))
          }
          if (update.docChanged || update.selectionSet) report(update.view)
        }),
        EditorView.domEventHandlers({
          contextmenu: (event, editor) => {
            const handle = handlers.current.onContextMenu
            if (!handle) return false
            const { main } = editor.state.selection
            // Right-clicking outside the selection asks about the line under
            // the pointer, which is what the click just pointed at.
            const at = editor.posAtCoords({ x: event.clientX, y: event.clientY })
            const inside = at !== null && at >= main.from && at <= main.to
            const from = inside || at === null ? main.from : at
            const to = inside || at === null ? main.to : at
            event.preventDefault()
            handle({
              x: event.clientX,
              y: event.clientY,
              startLine: editor.state.doc.lineAt(from).number,
              endLine: editor.state.doc.lineAt(to).number,
            })
            return true
          },
        }),
      ],
    })
    const created = new EditorView({ state, parent: host.current })
    view.current = created
    if (focusRef.current) created.focus()

    const start = restoreRef.current
    if (start) {
      const line = created.state.doc.line(Math.min(Math.max(start.line, 1), created.state.doc.lines))
      created.dispatch({ selection: { anchor: Math.min(line.from + Math.max(start.column - 1, 0), line.to) } })
      // After a frame: the scroller has no height until the buffer is laid out,
      // and a scrollTop set against a zero height is silently clamped to nought.
      requestAnimationFrame(() => {
        if (view.current === created) created.scrollDOM.scrollTop = start.top
      })
    }

    const onScroll = (): void => report(created)
    created.scrollDOM.addEventListener('scroll', onScroll, { passive: true })

    return () => {
      created.scrollDOM.removeEventListener('scroll', onScroll)
      created.destroy()
      view.current = null
    }
    // doc is the buffer's starting text only: later edits belong to CodeMirror,
    // and reloading a file remounts this component through its key.
  }, [path, readOnly])

  useEffect(() => {
    const editor = view.current
    if (!editor || !cursor) return
    const line = editor.state.doc.line(Math.min(Math.max(cursor.line, 1), editor.state.doc.lines))
    const at = Math.min(line.from + Math.max(cursor.column - 1, 0), line.to)
    editor.dispatch({
      selection: { anchor: at },
      effects: [
        EditorView.scrollIntoView(at, { y: 'center' }),
        ...(cursor.flash ? [setFlash.of([line.number])] : []),
      ],
    })
    editor.focus()
    if (!cursor.flash) return
    const timer = window.setTimeout(() => view.current?.dispatch({ effects: setFlash.of([]) }), FLASH_MS.jump)
    return () => window.clearTimeout(timer)
  }, [cursor?.seq])

  useEffect(() => {
    view.current?.dispatch({ effects: wrapping.current.reconfigure(wrap ? EditorView.lineWrapping : []) })
  }, [wrap])

  useEffect(() => {
    view.current?.dispatch({ effects: setChanges.of(changes) })
  }, [changes])

  useEffect(() => {
    const editor = view.current
    if (!editor || !flash || flash.lines.length === 0) return
    editor.dispatch({ effects: setFlash.of(flash.lines) })
    const timer = window.setTimeout(() => view.current?.dispatch({ effects: setFlash.of([]) }), FLASH_MS.agent)
    return () => window.clearTimeout(timer)
  }, [flash?.seq])

  return (
    <div className="cm-wrap" style={fontSize ? { fontSize } : undefined}>
      <div className="cm-host" ref={host} />
      <div className="cm-status" aria-label="Editor status">
        <span>
          Ln {status.line}, Col {status.column}
        </span>
        {status.selected > 0 && <span>({status.selected} selected)</span>}
        <span className="grow" />
        <span>{languageName(path)}</span>
        <span>{encoding}</span>
        {wrap && <span title="Long lines wrap (⌥Z)">Wrap</span>}
        {readOnly && <span className="cm-status-flag">Read-only</span>}
      </div>
    </div>
  )
}

/** What the status line calls a file's language. */
function languageName(path: string): string {
  const name = languageForPath(path)
  if (!name) return 'Plain text'
  return LANGUAGE_NAMES[name] ?? name.charAt(0).toUpperCase() + name.slice(1)
}

const LANGUAGE_NAMES: Record<string, string> = {
  typescript: 'TypeScript',
  javascript: 'JavaScript',
  csharp: 'C#',
  cpp: 'C++',
  css: 'CSS',
  scss: 'SCSS',
  sql: 'SQL',
  json: 'JSON',
  yaml: 'YAML',
  xml: 'HTML/XML',
  ini: 'INI',
  php: 'PHP',
  plaintext: 'Plain text',
}

/** CodeMirror grammar for a path, or nothing for a language it does not know. */
export function editorLanguage(path: string): Extension | null {
  const base = (path.split('/').pop() ?? path).toLowerCase()
  const ext = base.includes('.') ? base.slice(base.lastIndexOf('.') + 1) : base
  switch (ext) {
    case 'ts':
      return javascript({ typescript: true })
    case 'tsx':
      return javascript({ typescript: true, jsx: true })
    case 'js':
    case 'mjs':
    case 'cjs':
      return javascript({})
    case 'jsx':
      return javascript({ jsx: true })
    case 'go':
      return go()
    case 'py':
      return python()
    case 'md':
    case 'markdown':
      return markdown()
    case 'json':
      return json()
    case 'html':
    case 'htm':
      return html()
    case 'css':
    case 'scss':
      return css()
    case 'yaml':
    case 'yml':
      return yaml()
    case 'rs':
      return rust()
    default:
      return null
  }
}
