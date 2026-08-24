import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { api, statusOf } from '../bridge.ts'
import { isMarkdownPath, languageForPath, renderMarkdownBlocks } from '../markdown.ts'
import { store, useStore } from '../store.ts'
import { byLastTouched, type Worktree } from '../../shared/models.ts'
import { CodeEditor, type Cursor } from './editor.tsx'
import { FileTree } from './file-tree.tsx'
import { FindInFiles } from './find-in-files.tsx'
import { Chevron } from './icons.tsx'
import { PathLabel } from './path-label.tsx'
import { QuickOpen } from './quick-open.tsx'
import { RootPicker } from './root-picker.tsx'
import { SelectionMenu, useTextSelection, type MenuAction } from './selection-menu.tsx'

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
  sessionId,
  cwd,
  visible = true,
}: {
  hostId: string
  sessionId: string
  cwd: string
  /** False while another tab is showing. */
  visible?: boolean
}): JSX.Element {
  const [rootOverride, setRootOverride] = useState<string | null>(null)
  const sessionRoot = useMemo(() => cwd.replace(/\/+$/, '') || '/', [cwd])
  const root = useMemo(() => (rootOverride ?? sessionRoot).replace(/\/+$/, '') || '/', [sessionRoot, rootOverride])
  const [worktrees, setWorktrees] = useState<Worktree[]>([])
  const [files, setFiles] = useState<OpenFile[]>([])
  const [activePath, setActivePath] = useState<string | null>(null)
  const [side, setSide] = useState<'tree' | 'find'>('tree')
  const [findSeq, setFindSeq] = useState(0)
  const [quickOpen, setQuickOpen] = useState(false)
  const [quickOpenQuery, setQuickOpenQuery] = useState('')
  const [rootPicker, setRootPicker] = useState(false)
  const [reveal, setReveal] = useState<string | null>(null)
  const target = useStore((s) => s.fileTarget)

  // Drafts live outside React state: a keystroke should not re-render the tree.
  const drafts = useRef<Record<string, string>>({})
  const filesRef = useRef<OpenFile[]>(files)
  filesRef.current = files

  // Another session is another repository until proven otherwise, so the root
  // it was re-pointed at means nothing here.
  useEffect(() => {
    let cancelled = false
    api(hostId)
      .gitWorktrees(cwd)
      .then((result) => {
        if (!cancelled) setWorktrees(byLastTouched(result))
      })
      .catch(() => {
        // Not a repository: the session's own directory is the only root.
        if (!cancelled) setWorktrees([])
      })
    return () => {
      cancelled = true
    }
  }, [hostId, cwd])

  const openFile = useCallback(
    async (
      path: string,
      at?: { line: number; column: number },
      /** True when the caller has another path to try if this one is missing. */
      quiet = false,
      /**
       * False while reopening the tabs a session had last time. Those must not
       * take the front, or a file asked for as the panel mounts loses it to
       * whatever was open before.
       */
      activate = true,
    ): Promise<boolean> => {
      if (activate) setReveal(path)
      const existing = filesRef.current.find((file) => file.path === path)
      if (existing) {
        if (activate) setActivePath(path)
        if (at) {
          setFiles((current) =>
            current.map((file) =>
              file.path === path ? { ...file, cursor: { ...at, seq: (file.cursor?.seq ?? 0) + 1 } } : file,
            ),
          )
        }
        return true
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
        if (activate) setActivePath(path)
        return true
      } catch (err) {
        // A path from the transcript is as likely to be a directory as a file;
        // the daemon says which with a 400, and the tree has already revealed it.
        if (statusOf(err) === 400) return true
        if (quiet) return false
        if (statusOf(err) === 413) store.notify('File is too large to open', 'error')
        else if (statusOf(err) === 404) store.notify(`Not found: ${path}`, 'error')
        else store.fail(err)
        return false
      }
    },
    [hostId],
  )

  // Which files were open, which was in front, and where the tree was pointed.
  // A session is looked away from and come back to all day; losing the tabs
  // every time makes the panel a viewer rather than a place to work.
  const memory = `helios.files.${hostId}.${sessionId}`
  const restored = useRef<string | null>(null)
  // Set when something asked for a specific file while the restore below was
  // still running. Opening the panel *by* asking for a file is the common case
  // — an agent's helios_show mounts it — and the restore finishes last, so
  // without this it puts the previous session's file back in front.
  const claimed = useRef(false)

  useEffect(() => {
    restored.current = null
    claimed.current = false
    setFiles([])
    setActivePath(null)
    setReveal(null)
    drafts.current = {}

    const saved = readWorkspace(memory)
    setRootOverride(saved?.root ?? null)

    let cancelled = false
    void (async () => {
      for (const path of saved?.open ?? []) {
        if (cancelled) return
        // Quiet: the agent may have deleted a file since it was last open, and
        // a toast per missing tab is not news the user asked for.
        await openFile(path, undefined, true, false)
      }
      if (cancelled) return
      if (saved?.active && !claimed.current) setActivePath(saved.active)
      restored.current = memory
    })()
    return () => {
      cancelled = true
    }
  }, [memory, openFile])

  useEffect(() => {
    // Not before the restore for this session has finished, or the empty state
    // it starts from would overwrite what is on disk.
    if (restored.current !== memory) return
    writeWorkspace(memory, {
      root: rootOverride,
      open: files.map((file) => file.path),
      active: activePath,
    })
  }, [memory, rootOverride, files, activePath])

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

  // A chip in the transcript names a path in the session's own checkout, which
  // is not where the panel is pointed once another root has been picked. Open
  // the same file under the current root, and fall back to the literal path
  // when this root does not have it — a file the agent has only just created.
  const scope = useRef({ root, owners: [] as string[] })
  scope.current = { root, owners: [sessionRoot, ...worktrees.map((worktree) => worktree.path)] }

  useEffect(() => {
    if (!target || target.hostId !== hostId) return
    claimed.current = true
    const path = target.path
    if (target.mode === 'find') {
      setQuickOpenQuery(basename(path))
      setQuickOpen(true)
      store.clearFileTarget()
      return
    }
    const mapped = inRoot(path, scope.current.root, scope.current.owners)
    // An agent pointing at a file usually means one line of it.
    const at = target.line ? { line: target.line, column: 1 } : undefined
    void (async () => {
      if (mapped !== path && (await openFile(mapped, at, true))) return
      await openFile(path, at)
    })()
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

  // The root as a path of its own: every segment above it is a folder the user
  // can drop the tree onto, which is how you get out of a directory that turned
  // out to be one level too deep.
  const crumbs = useMemo(() => {
    const segments = root.split('/').filter(Boolean)
    const out = [{ label: '/', path: '/' }]
    let accumulated = ''
    for (const segment of segments) {
      accumulated += `/${segment}`
      out.push({ label: segment, path: accumulated })
    }
    return out
  }, [root])

  // A deep root overflows the strip, and the segment that matters is the last.
  const crumbStrip = useRef<HTMLDivElement | null>(null)
  useEffect(() => {
    const strip = crumbStrip.current
    if (strip) strip.scrollLeft = strip.scrollWidth
  }, [root])

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

        <div className="ws-root">
          <div className="ws-crumbs" ref={crumbStrip}>
            {crumbs.map((crumb, index) => (
              <button
                key={crumb.path}
                className={`ws-crumb${index === crumbs.length - 1 ? ' here' : ''}`}
                title={crumb.path}
                onClick={() => setRootOverride(crumb.path)}
              >
                {crumb.label}
              </button>
            ))}
          </div>
          <button
            className="ws-root-more"
            title="Choose a worktree or another folder"
            onClick={() => setRootPicker(true)}
          >
            <Chevron dir="down" />
          </button>
          {root !== sessionRoot && (
            <button className="pill" title="Back to this session's own folder" onClick={() => setRootOverride(null)}>
              ✕
            </button>
          )}
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
            worktrees={worktrees}
            onOpen={(path, line, column) => void openFile(path, { line, column })}
            onPickRoot={setRootOverride}
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
            hostId={hostId}
            sessionId={sessionId}
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
          initialQuery={quickOpenQuery}
          worktrees={worktrees}
          onOpen={(path) => void openFile(path)}
          onPickRoot={setRootOverride}
          onClose={() => {
            setQuickOpen(false)
            setQuickOpenQuery('')
          }}
        />
      )}

      {rootPicker && (
        <RootPicker
          hostId={hostId}
          root={root}
          worktrees={worktrees}
          onPick={(path) => setRootOverride(path)}
          onClose={() => setRootPicker(false)}
        />
      )}
    </div>
  )
}

function FileView({
  file,
  root,
  hostId,
  sessionId,
  text,
  onChange,
  onSave,
  onReload,
  onMode,
}: {
  file: OpenFile
  root: string
  hostId: string
  sessionId: string
  text: string
  onChange: (text: string) => void
  onSave: () => void
  onReload: () => void
  onMode: (mode: 'edit' | 'preview') => void
}): JSX.Element {
  const markdown = isMarkdownPath(file.path)
  const rendered = markdown && file.mode === 'preview'
  const blocks = useMemo(() => (rendered ? renderMarkdownBlocks(text) : null), [rendered, text])
  const [menu, setMenu] = useState<{ x: number; y: number; range: LineRange } | null>(null)
  /** Headings whose section is folded away, by the heading's start line. */
  const [folds, setFolds] = useState<Set<number>>(new Set())
  const preview = useRef<HTMLDivElement | null>(null)
  const [selection, clearSelection] = useTextSelection(preview)
  const [width, setWidth] = useState(readReadingWidth)

  // The rendered prose still has lines underneath it, and what the reader
  // highlighted names them: the blocks its two ends fall in.
  const picked = useMemo(() => (selection ? linesOf(selection.range) : null), [selection])

  // Everything under a folded heading, down to the next heading that is not
  // deeper than it — folding "## Build" takes its subsections with it.
  const hidden = useMemo(() => {
    const out = new Set<number>()
    if (!blocks || folds.size === 0) return out
    let under: number | null = null
    for (const block of blocks) {
      if (block.depth !== undefined && under !== null && block.depth <= under) under = null
      if (under !== null) {
        out.add(block.startLine)
        continue
      }
      if (block.depth !== undefined && folds.has(block.startLine)) under = block.depth
    }
    return out
  }, [blocks, folds])

  // A menu names lines, and a fold names a heading, of the file that was open
  // when they were made.
  useEffect(() => {
    setMenu(null)
    setFolds(new Set())
  }, [file.path, rendered])

  const act = (action: 'copy' | 'prompt', range: LineRange): void => {
    const lines = text.split('\n').slice(range.start - 1, range.end)
    if (action === 'copy') {
      void navigator.clipboard.writeText(lines.join('\n'))
      store.notify(`Copied ${label(range)}`)
      return
    }
    store.appendPrompt(hostId, sessionId, promptFor(file.path, range, lines))
  }

  // Whole lines either way, as the editor's own menu does: the source is what
  // the agent will be asked about, not the prose it renders as.
  const actions = (range: LineRange): MenuAction[] => [
    { label: `Copy ${label(range)}`, run: () => act('copy', range) },
    { label: `Send ${label(range)} as prompt`, run: () => act('prompt', range) },
  ]

  // Dragging the right edge moves both: the column stays centred, so it grows
  // by twice what the pointer travelled. The drag starts from the width on
  // screen rather than from state, so the first pixel of a drag always moves
  // the edge, and stops at the room the panel has — a column wider than that
  // is a number the reader cannot see the effect of.
  const resize = (event: React.PointerEvent<HTMLDivElement>): void => {
    event.preventDefault()
    const fromX = event.clientX
    const doc = event.currentTarget.parentElement
    const fromWidth = doc?.getBoundingClientRect().width ?? MIN_WIDTH
    const room = roomFor(doc)
    let latest = fromWidth
    const move = (moved: PointerEvent): void => {
      latest = clampWidth(fromWidth + (moved.clientX - fromX) * 2, room)
      setWidth(latest)
    }
    const done = (): void => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', done)
      // Widened to the edge means "fill the panel", not "999 pixels": a number
      // taken from today's window would leave gutters in a wider one.
      const filled = latest >= room
      if (filled) setWidth(null)
      writeReadingWidth(filled ? null : latest)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', done)
  }

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
      ) : blocks !== null ? (
        <div className="md preview-md" ref={preview}>
          <div className="md-doc" style={{ width: width ?? '100%' }}>
            {blocks.map((block) => {
              if (hidden.has(block.startLine)) return null
              const range = { start: block.startLine, end: block.endLine }
              const folded = folds.has(block.startLine)
              return (
                <div
                  key={block.startLine}
                  className={`md-block${block.depth ? ' md-heading' : ''}`}
                  data-line-start={block.startLine}
                  data-line-end={block.endLine}
                  title={label(range)}
                  onContextMenu={(event) => {
                    event.preventDefault()
                    // Right-clicking outside the selection asks about the block
                    // under the pointer, which is what the click pointed at.
                    const covered =
                      picked !== null && block.startLine >= picked.start && block.endLine <= picked.end
                    setMenu({ x: event.clientX, y: event.clientY, range: covered ? picked : range })
                  }}
                >
                  {block.depth !== undefined && (
                    <button
                      className="md-fold"
                      title={folded ? 'Expand section' : 'Collapse section'}
                      onClick={() =>
                        setFolds((current) => {
                          const next = new Set(current)
                          if (!next.delete(block.startLine)) next.add(block.startLine)
                          return next
                        })
                      }
                    >
                      <Chevron open={!folded} />
                    </button>
                  )}
                  <div className="md-block-body" dangerouslySetInnerHTML={{ __html: block.html }} />
                </div>
              )
            })}
            <div
              className="md-grip"
              title="Drag to narrow the text — double-click to fill the panel"
              onPointerDown={resize}
              onDoubleClick={() => {
                setWidth(null)
                writeReadingWidth(null)
              }}
            />
          </div>
        </div>
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
          onContextMenu={(at) => setMenu({ x: at.x, y: at.y, range: { start: at.startLine, end: at.endLine } })}
        />
      )}

      {menu ? (
        <SelectionMenu x={menu.x} y={menu.y} actions={actions(menu.range)} onClose={() => setMenu(null)} />
      ) : (
        selection &&
        picked && (
          <SelectionMenu
            anchor="above"
            x={selection.x}
            y={selection.y}
            actions={actions(picked)}
            onClose={clearSelection}
          />
        )
      )}
    </div>
  )
}

/** A range of file lines, 1-based and inclusive. */
interface LineRange {
  start: number
  end: number
}

function label(range: LineRange): string {
  return range.start === range.end ? `L${range.start}` : `L${range.start}-${range.end}`
}

/**
 * Past this the lines go to the agent as a reference instead of a quote: it can
 * read the file itself, and a prompt is a worse place to put it than the disk.
 */
const INLINE_LINE_LIMIT = 40

function promptFor(path: string, range: LineRange, lines: string[]): string {
  const header = `\`${path}\` ${label(range)}`
  if (lines.length > INLINE_LINE_LIMIT) return `${header}\n`
  return `${header}:\n\`\`\`${languageForPath(path) ?? ''}\n${lines.join('\n')}\n\`\`\`\n`
}

/** The file lines a selection covers, from the blocks its two ends fall in. */
function linesOf(range: Range): LineRange | null {
  const from = blockLines(range.startContainer)
  const to = blockLines(range.endContainer)
  if (!from || !to) return null
  return { start: Math.min(from.start, to.start), end: Math.max(from.end, to.end) }
}

function blockLines(node: Node): LineRange | null {
  const element = node instanceof Element ? node : node.parentElement
  const block = element?.closest<HTMLElement>('[data-line-start]')
  if (!block) return null
  return { start: Number(block.dataset.lineStart), end: Number(block.dataset.lineEnd) }
}

/**
 * How wide the rendered column is, in pixels. A reading preference rather than
 * a property of any one file, so every preview shares it — and null until the
 * reader says otherwise, which means the whole panel. A document that arrives
 * pre-narrowed just looks like a panel that will not fill.
 */
const MIN_WIDTH = 420
const WIDTH_KEY = 'helios.md-width'

function clampWidth(width: number, room: number): number {
  return Math.round(Math.min(Math.max(width, MIN_WIDTH), Math.max(room, MIN_WIDTH)))
}

/** What the scroller has to give, inside its padding. */
function roomFor(doc: Element | null): number {
  const scroller = doc?.closest('.preview-md')
  if (!scroller) return Number.POSITIVE_INFINITY
  const style = getComputedStyle(scroller)
  return scroller.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight)
}

function readReadingWidth(): number | null {
  try {
    const saved = Number(localStorage.getItem(WIDTH_KEY))
    return saved > 0 ? Math.max(saved, MIN_WIDTH) : null
  } catch {
    return null
  }
}

function writeReadingWidth(width: number | null): void {
  try {
    if (width === null) localStorage.removeItem(WIDTH_KEY)
    else localStorage.setItem(WIDTH_KEY, String(width))
  } catch {
    // A full or unavailable store costs the preference, not the panel.
  }
}

function basename(path: string): string {
  return path.split('/').pop() || path
}

/** What the panel remembers about a session between visits. */
interface Workspace {
  root: string | null
  open: string[]
  active: string | null
}

function readWorkspace(key: string): Workspace | null {
  try {
    const raw = localStorage.getItem(key)
    return raw ? (JSON.parse(raw) as Workspace) : null
  } catch {
    return null
  }
}

function writeWorkspace(key: string, workspace: Workspace): void {
  try {
    localStorage.setItem(key, JSON.stringify(workspace))
  } catch {
    // A full or unavailable store costs the memory, not the panel.
  }
}

/**
 * Rewrites a path from whichever checkout it names into the one on screen. The
 * longest owner wins: a worktree nested under another repository's directory
 * would otherwise be read as a path inside it.
 */
function inRoot(path: string, root: string, owners: string[]): string {
  if (path === root || path.startsWith(`${root}/`)) return path
  const owner = owners
    .filter((candidate) => candidate && path.startsWith(`${candidate}/`))
    .sort((a, b) => b.length - a.length)[0]
  return owner ? `${root}/${path.slice(owner.length + 1)}` : path
}

/** Paths inside the session's directory read better without its prefix. */
function relativeTo(root: string, path: string): string {
  return path.startsWith(`${root}/`) ? path.slice(root.length + 1) : path
}
