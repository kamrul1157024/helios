import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { api, statusOf } from '../bridge.ts'
import { isMarkdownPath, renderMarkdown } from '../markdown.ts'
import { store, useStore } from '../store.ts'
import { CodeEditor, type Cursor } from './editor.tsx'
import { FileTree } from './file-tree.tsx'
import { FindInFiles } from './find-in-files.tsx'
import { PathLabel } from './path-label.tsx'
import { QuickOpen } from './quick-open.tsx'

/** Past this the editor is read-only: CodeMirror is not a log viewer. */
const MAX_EDIT_BYTES = 1_000_000

interface OpenFile {
  path: string
  /** The content as it stands on disk, for the dirty check and for revert. */
  saved: string
  modTime: string
  dirty: boolean
  /** Markdown opens rendered; everything else opens in the editor. */
  mode: 'edit' | 'preview'
  readOnly: boolean
  binary: boolean
  /** Bumped to remount the editor when the buffer is replaced from disk. */
  version: number
  cursor: Cursor | null
}

/**
 * The workspace: a lazy file tree, editor tabs, quick open and find in files.
 *
 * Everything goes through the session's own daemon, including the local one —
 * the machine Helios runs on is just another host — so a remote session browses
 * exactly like a local one.
 */
export function FilesPanel({
  hostId,
  cwd,
  visible = true,
}: {
  hostId: string
  cwd: string
  /** False while another tab is showing. */
  visible?: boolean
}): JSX.Element {
  const root = useMemo(() => cwd.replace(/\/+$/, '') || '/', [cwd])
  const [files, setFiles] = useState<OpenFile[]>([])
  const [activePath, setActivePath] = useState<string | null>(null)
  const [side, setSide] = useState<'tree' | 'find'>('tree')
  const [findSeq, setFindSeq] = useState(0)
  const [quickOpen, setQuickOpen] = useState(false)
  const [reveal, setReveal] = useState<string | null>(null)
  const target = useStore((s) => s.fileTarget)

  // Drafts live outside React state: a keystroke should not re-render the tree.
  const drafts = useRef<Record<string, string>>({})
  const filesRef = useRef<OpenFile[]>(files)
  filesRef.current = files

  // A different session means a different tree and a different set of buffers.
  useEffect(() => {
    setFiles([])
    setActivePath(null)
    setReveal(null)
    drafts.current = {}
  }, [hostId, root])

  const openFile = useCallback(
    async (path: string, at?: { line: number; column: number }): Promise<void> => {
      setReveal(path)
      const existing = filesRef.current.find((file) => file.path === path)
      if (existing) {
        setActivePath(path)
        if (at) {
          setFiles((current) =>
            current.map((file) =>
              file.path === path ? { ...file, cursor: { ...at, seq: (file.cursor?.seq ?? 0) + 1 } } : file,
            ),
          )
        }
        return
      }

      try {
        const loaded = await api(hostId).readFile(path)
        // A NUL byte means the daemon handed back something that was never text.
        const binary = loaded.content.includes('\u0000')
        setFiles((current) => [
          ...current,
          {
            path,
            saved: loaded.content,
            modTime: loaded.mod_time,
            dirty: false,
            mode: isMarkdownPath(path) ? 'preview' : 'edit',
            readOnly: binary || loaded.content.length > MAX_EDIT_BYTES,
            binary,
            version: 0,
            cursor: at ? { ...at, seq: 1 } : null,
          },
        ])
        setActivePath(path)
      } catch (err) {
        // A path from the transcript is as likely to be a directory as a file;
        // the daemon says which with a 400, and the tree has already revealed it.
        if (statusOf(err) === 400) return
        if (statusOf(err) === 413) store.notify('File is too large to open', 'error')
        else if (statusOf(err) === 404) store.notify(`Not found: ${path}`, 'error')
        else store.fail(err)
      }
    },
    [hostId],
  )

  const save = useCallback(
    async (path: string): Promise<void> => {
      const file = filesRef.current.find((f) => f.path === path)
      if (!file || file.readOnly || !file.dirty) return
      const text = drafts.current[path] ?? file.saved
      try {
        const result = await api(hostId).writeFile(path, text, file.modTime)
        setFiles((current) =>
          current.map((f) =>
            f.path === path ? { ...f, saved: text, modTime: result.mod_time, dirty: false } : f,
          ),
        )
      } catch (err) {
        // The agent edits the same files, so this is a real outcome rather than
        // an edge case: reload, then decide what to keep.
        if (statusOf(err) === 409) store.notify('Changed on disk since it was opened — reload first', 'error')
        else store.fail(err)
      }
    },
    [hostId],
  )

  const reload = useCallback(
    async (path: string): Promise<void> => {
      try {
        const loaded = await api(hostId).readFile(path)
        delete drafts.current[path]
        setFiles((current) =>
          current.map((file) =>
            file.path === path
              ? {
                  ...file,
                  saved: loaded.content,
                  modTime: loaded.mod_time,
                  dirty: false,
                  version: file.version + 1,
                }
              : file,
          ),
        )
      } catch (err) {
        store.fail(err)
      }
    },
    [hostId],
  )

  // The agent edits the tree while the user is elsewhere, so the buffer that
  // was accurate on the way out is stale on the way back. Unsaved edits
  // outrank what is on disk and are left alone; the file keeps its unsaved
  // marker and the user can revert deliberately.
  const wasVisible = useRef(visible)
  useEffect(() => {
    const returning = visible && !wasVisible.current
    wasVisible.current = visible
    if (!returning || !activePath) return
    const file = filesRef.current.find((f) => f.path === activePath)
    if (!file || file.dirty) return
    void reload(activePath)
  }, [visible, activePath, reload])

  const close = (path: string): void => {
    const file = filesRef.current.find((f) => f.path === path)
    if (file?.dirty && !confirm(`Discard unsaved changes to ${basename(path)}?`)) return
    delete drafts.current[path]
    setFiles((current) => {
      const remaining = current.filter((f) => f.path !== path)
      setActivePath((active) =>
        active === path ? (remaining[remaining.length - 1]?.path ?? null) : active,
      )
      return remaining
    })
  }

  const edited = (path: string, text: string): void => {
    drafts.current[path] = text
    const file = filesRef.current.find((f) => f.path === path)
    if (!file) return
    const dirty = text !== file.saved
    if (dirty === file.dirty) return
    setFiles((current) => current.map((f) => (f.path === path ? { ...f, dirty } : f)))
  }

  // A chip in the transcript opens the file here.
  useEffect(() => {
    if (!target || target.hostId !== hostId) return
    void openFile(target.path)
    store.clearFileTarget()
    // seq, not path: reopening the same file has to work.
  }, [target?.seq])

  useEffect(() => {
    const onKey = (event: KeyboardEvent): void => {
      if (!event.metaKey && !event.ctrlKey) return
      const key = event.key.toLowerCase()
      if (key === 'p' && !event.shiftKey) {
        event.preventDefault()
        setQuickOpen(true)
      } else if (key === 'f' && event.shiftKey) {
        event.preventDefault()
        setSide('find')
        setFindSeq((seq) => seq + 1)
      } else if (key === 's' && !event.defaultPrevented) {
        // Not when CodeMirror has already handled it: saving twice would send
        // the second write with a mod time the first one has just replaced.
        event.preventDefault()
        if (activePath) void save(activePath)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [activePath, save])

  const active = files.find((file) => file.path === activePath) ?? null

  return (
    <div className="workspace">
      <aside className="ws-side">
        <div className="ws-side-head">
          <button className={side === 'tree' ? 'ws-view on' : 'ws-view'} onClick={() => setSide('tree')}>
            Explorer
          </button>
          <button
            className={side === 'find' ? 'ws-view on' : 'ws-view'}
            onClick={() => {
              setSide('find')
              setFindSeq((seq) => seq + 1)
            }}
          >
            Search
          </button>
          <button className="icon-btn tiny" title="Go to file (⌘P)" onClick={() => setQuickOpen(true)}>
            ⌕
          </button>
        </div>

        {side === 'tree' ? (
          <FileTree
            hostId={hostId}
            root={root}
            selected={activePath}
            reveal={reveal}
            onOpen={(path) => void openFile(path)}
          />
        ) : (
          <FindInFiles
            hostId={hostId}
            root={root}
            focusSeq={findSeq}
            onOpen={(path, line, column) => void openFile(path, { line, column })}
          />
        )}
      </aside>

      <section className="ws-main">
        <div className="ws-tabs">
          {files.map((file) => (
            <div
              key={file.path}
              className={`ws-tab ${file.path === activePath ? 'active' : ''}`}
              onClick={() => setActivePath(file.path)}
            >
              <span className="ws-tab-name">{basename(file.path)}</span>
              <button
                className="ws-tab-close"
                title={file.dirty ? 'Unsaved changes' : 'Close'}
                onClick={(event) => {
                  event.stopPropagation()
                  close(file.path)
                }}
              >
                {file.dirty ? '●' : '✕'}
              </button>
            </div>
          ))}
        </div>

        {active ? (
          <FileView
            file={active}
            root={root}
            text={drafts.current[active.path] ?? active.saved}
            onChange={(text) => edited(active.path, text)}
            onSave={() => void save(active.path)}
            onReload={() => void reload(active.path)}
            onMode={(mode) =>
              setFiles((current) => current.map((f) => (f.path === active.path ? { ...f, mode } : f)))
            }
          />
        ) : (
          <div className="ws-blank">
            <p className="empty-note">Pick a file, or press ⌘P to go to one.</p>
          </div>
        )}
      </section>

      {quickOpen && (
        <QuickOpen
          hostId={hostId}
          root={root}
          onOpen={(path) => void openFile(path)}
          onClose={() => setQuickOpen(false)}
        />
      )}
    </div>
  )
}

function FileView({
  file,
  root,
  text,
  onChange,
  onSave,
  onReload,
  onMode,
}: {
  file: OpenFile
  root: string
  text: string
  onChange: (text: string) => void
  onSave: () => void
  onReload: () => void
  onMode: (mode: 'edit' | 'preview') => void
}): JSX.Element {
  const markdown = isMarkdownPath(file.path)
  const rendered = markdown && file.mode === 'preview'
  const html = useMemo(() => (rendered ? renderMarkdown(text) : null), [rendered, text])

  return (
    <div className="ws-editor">
      <header className="ws-file-head">
        <PathLabel path={relativeTo(root, file.path)} className="preview-path" />
        {file.dirty && <span className="pill">unsaved</span>}
        {file.readOnly && <span className="pill">{file.binary ? 'binary' : 'read only'}</span>}
        <span className="grow" />
        {markdown && (
          <button
            className="icon-btn tiny"
            title={rendered ? 'Edit source' : 'Show rendered markdown'}
            onClick={() => onMode(rendered ? 'edit' : 'preview')}
          >
            {rendered ? '{}' : '¶'}
          </button>
        )}
        <button className="icon-btn tiny" title="Reload from disk" onClick={onReload}>
          ⟳
        </button>
        <button
          className="icon-btn tiny"
          title="Copy contents"
          onClick={() => void navigator.clipboard.writeText(text)}
        >
          ⧉
        </button>
        {!file.readOnly && (
          <button className="filled tiny" disabled={!file.dirty} onClick={onSave}>
            Save
          </button>
        )}
      </header>

      {file.binary ? (
        <p className="empty-note">Binary file — not shown.</p>
      ) : html !== null ? (
        <div className="md preview-md" dangerouslySetInnerHTML={{ __html: html }} />
      ) : file.readOnly ? (
        <pre className="ws-plain">{text}</pre>
      ) : (
        <CodeEditor
          key={`${file.path}:${file.version}`}
          path={file.path}
          doc={text}
          onChange={onChange}
          onSave={onSave}
          cursor={file.cursor}
        />
      )}
    </div>
  )
}

function basename(path: string): string {
  return path.split('/').pop() || path
}

/** Paths inside the session's directory read better without its prefix. */
function relativeTo(root: string, path: string): string {
  return path.startsWith(`${root}/`) ? path.slice(root.length + 1) : path
}
