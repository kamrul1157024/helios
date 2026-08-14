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
import { EditorState, type Extension } from '@codemirror/state'
import { EditorView, keymap } from '@codemirror/view'
import { basicSetup } from 'codemirror'
import { useEffect, useRef } from 'react'

import { heliosEditorTheme } from '../codemirror-theme.ts'

/** A place to put the cursor, re-sent with a new seq to jump there again. */
export interface Cursor {
  line: number
  column: number
  seq: number
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
  doc: string
  path: string
  readOnly?: boolean
  onChange: (text: string) => void
  onSave: () => void
  cursor?: Cursor | null
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
  path,
  readOnly,
  onChange,
  onSave,
  cursor,
  onContextMenu,
}: Props): JSX.Element {
  const host = useRef<HTMLDivElement | null>(null)
  const view = useRef<EditorView | null>(null)
  // Held in refs so a new callback identity does not tear down the editor.
  const handlers = useRef({ onChange, onSave, onContextMenu })
  handlers.current = { onChange, onSave, onContextMenu }

  useEffect(() => {
    if (!host.current) return
    const language = editorLanguage(path)
    const state = EditorState.create({
      doc,
      extensions: [
        basicSetup,
        heliosEditorTheme,
        indentUnit.of('  '),
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
          if (update.docChanged) handlers.current.onChange(update.state.doc.toString())
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
    created.focus()
    return () => {
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
      effects: EditorView.scrollIntoView(at, { y: 'center' }),
    })
    editor.focus()
  }, [cursor?.seq])

  return <div className="cm-host" ref={host} />
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
