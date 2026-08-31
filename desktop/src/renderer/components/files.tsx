import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'

import { api, statusOf } from '../bridge.ts'
import { dataUrl, extensionOf, kindOf, type FileKind } from '../filetype.ts'
import { keys } from '../keys.ts'
import { isMarkdownPath, languageForPath, renderMarkdownBlocks } from '../markdown.ts'
import { fileContentQuery, worktreesQuery } from '../queries.ts'
import { store, useStore } from '../store.ts'
import { byLastTouched, type FileContent, type Worktree } from '../../shared/models.ts'
import { CodeEditor, type Cursor, type ReadingPosition } from './editor.tsx'
import { FileTree } from './file-tree.tsx'
import { FindInFiles } from './find-in-files.tsx'
import { HtmlPreview, useInlineImages } from './html-preview.tsx'
import { Chevron } from './icons.tsx'
import { PathLabel } from './path-label.tsx'
import { QuickOpen } from './quick-open.tsx'
import { RootPicker } from './root-picker.tsx'
import { SelectionMenu, useTextSelection, type MenuAction } from './selection-menu.tsx'

/** Past this the editor is read-only: CodeMirror is not a log viewer. */
const MAX_EDIT_BYTES = 1_000_000

const NO_WORKTREES: Worktree[] = []

/**
 * How long scrolling has to stop before the position is written down. Every
 * wheel tick is a scroll event, and none of them is worth a write on its own.
 */
const VIEW_SETTLE = 500

/**
 * An open tab: everything about a file that is this window's rather than the
 * daemon's.
 *
 * The contents are not here. They live in the cache under
 * `keys.fileContent`, which is what lets the same read serve quick open, the
 * restore below, and the editor without three round trips for one file.
 */
interface OpenTab {
  path: string
  dirty: boolean
  /** Markdown opens rendered; everything else opens in the editor. */
  mode: 'edit' | 'preview'
  /** Bumped to remount the editor when the buffer is replaced from disk. Not
   *  derived from the cache: a save writes the answer back too, and remounting
   *  on that would throw away the cursor of whoever pressed ⌘S. */
  version: number
  cursor: Cursor | null
}

/** What the file is, as opposed to what the window is doing with it. */
interface OpenFile extends OpenTab {
  /** The content as it stands on disk, for the dirty check and for revert. */
  saved: string
  modTime: string
  readOnly: boolean
  binary: boolean
  /** How it is shown: as a picture, as a page, or as text. */
  kind: FileKind
  /** Whether `saved` holds base64 rather than the file's own text. */
  base64: boolean
}

/**
 * Whether the daemon handed back something that was never text.
 *
 * The encoding field is the answer when there is one. The NUL check stays
 * behind it for a daemon older than that field, where a file that is not UTF-8
 * has already lost its bytes to the JSON encoder and this is all there is to go
 * on.
 */
function shapeOf(content: FileContent): { binary: boolean; readOnly: boolean } {
  const binary = content.encoding === 'base64' || content.content.includes('\u0000')
  return { binary, readOnly: binary || content.content.length > MAX_EDIT_BYTES }
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
  const client = useQueryClient()
  // The same read the git panel's worktree list makes. Not a repository at all
  // lands as an error, which leaves the session's own directory as the only root.
  const { data: worktrees = NO_WORKTREES } = useQuery({
    ...worktreesQuery(hostId, cwd),
    select: byLastTouched,
  })
  const [tabs, setTabs] = useState<OpenTab[]>([])
  const [activePath, setActivePath] = useState<string | null>(null)
  /**
   * The directories open in the tree, held here rather than in the tree itself.
   *
   * The tree used to keep them and reset on a change of root, which is not the
   * same thing as a change of session: two sessions in one repository share a
   * root, so one silently inherited the other's open folders, and two sessions
   * in different repositories lost them on every switch.
   */
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  // null is collapsed. A narrow Files pane beside a transcript has no room for
  // a tree as well as an editor, and the tree is the half you can do without.
  const [side, setSide] = useState<Side>('tree')
  const [findSeq, setFindSeq] = useState(0)
  const [quickOpen, setQuickOpen] = useState(false)
  const [quickOpenQuery, setQuickOpenQuery] = useState('')
  const [rootPicker, setRootPicker] = useState(false)
  const [reveal, setReveal] = useState<string | null>(null)
  const target = useStore((s) => s.fileTarget)

  // Drafts live outside React state: a keystroke should not re-render the tree.
  const drafts = useRef<Record<string, string>>({})
  /**
   * Where each open file was last being read, by path.
   *
   * A ref for the same reason as the drafts — scrolling should not re-render
   * the panel — with a counter to wake the save below once the reader settles.
   */
  const views = useRef<Record<string, ReadingPosition>>({})
  const [viewTick, setViewTick] = useState(0)
  const viewTimer = useRef<number | null>(null)
  const noteView = useCallback((path: string, at: ReadingPosition): void => {
    views.current[path] = at
    if (viewTimer.current !== null) window.clearTimeout(viewTimer.current)
    viewTimer.current = window.setTimeout(() => setViewTick((tick) => tick + 1), VIEW_SETTLE)
  }, [])
  useEffect(
    () => () => {
      if (viewTimer.current !== null) window.clearTimeout(viewTimer.current)
    },
    [],
  )
  const tabsRef = useRef<OpenTab[]>(tabs)
  tabsRef.current = tabs

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
      const existing = tabsRef.current.find((tab) => tab.path === path)
      if (existing) {
        if (activate) setActivePath(path)
        if (at) {
          setTabs((current) =>
            current.map((tab) =>
              tab.path === path ? { ...tab, cursor: { ...at, seq: (tab.cursor?.seq ?? 0) + 1 } } : tab,
            ),
          )
        }
        return true
      }

      try {
        // Read before the tab exists, because whether the file is there at all
        // is what decides whether it gets one. It lands in the same cache entry
        // the editor then reads, so opening costs one request rather than two.
        await client.fetchQuery(fileContentQuery(hostId, path))
        setTabs((current) => [
          ...current,
          {
            path,
            dirty: false,
            // A picture and a page are things to look at. Text opens in the
            // editor, markdown included only when it is prose.
            mode: kindOf(path) !== 'text' || isMarkdownPath(path) ? 'preview' : 'edit',
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
    [hostId, client],
  )

  // Which files were open, which was in front, and where the tree was pointed.
  // A session is looked away from and come back to all day; losing the tabs
  // every time makes the panel a viewer rather than a place to work.
  const memory = `helios.files.${hostId}.${sessionId}`
  /**
   * Which session the tabs below currently belong to, or null mid-restore.
   *
   * State rather than a ref, because the save effect has to see this change in
   * the same commit as `files`. A ref was visible to the render that still held
   * the outgoing session's tabs: switching to a session with nothing saved left
   * the restore with nothing to await, so it ran to completion synchronously
   * and marked itself done before React had flushed the clear — and the save
   * that followed wrote the previous session's files under the new key.
   */
  const [loadedFor, setLoadedFor] = useState<string | null>(null)
  // Set when something asked for a specific file while the restore below was
  // still running. Opening the panel *by* asking for a file is the common case
  // — an agent's helios_show mounts it — and the restore finishes last, so
  // without this it puts the previous session's file back in front.
  const claimed = useRef(false)

  useEffect(() => {
    setLoadedFor(null)
    claimed.current = false
    setTabs([])
    setActivePath(null)
    setReveal(null)
    drafts.current = {}

    const saved = readWorkspace(memory)
    setRootOverride(saved?.root ?? null)
    setExpanded(new Set(saved?.expanded ?? []))
    setSide(saved?.side === undefined ? 'tree' : saved.side)
    views.current = saved?.view ?? {}

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
      setLoadedFor(memory)
    })()
    return () => {
      cancelled = true
    }
  }, [memory, openFile])

  useEffect(() => {
    // Not before the restore for this session has finished, or the empty state
    // it starts from would overwrite what is on disk.
    if (loadedFor !== memory) return
    writeWorkspace(memory, {
      root: rootOverride,
      open: tabs.map((tab) => tab.path),
      active: activePath,
      expanded: [...expanded],
      side,
      // Pruned to what is open: a closed tab's position is not worth carrying,
      // and the record would otherwise grow for the life of the session.
      view: Object.fromEntries(
        tabs.map((tab) => [tab.path, views.current[tab.path]]).filter(([, at]) => at !== undefined),
      ) as Record<string, ReadingPosition>,
    })
  }, [memory, loadedFor, rootOverride, tabs, activePath, expanded, side, viewTick])

  const save = useCallback(
    async (path: string): Promise<void> => {
      const tab = tabsRef.current.find((t) => t.path === path)
      const onDisk = client.getQueryData(fileContentQuery(hostId, path).queryKey)
      if (!tab || !tab.dirty || !onDisk || shapeOf(onDisk).readOnly) return
      const text = drafts.current[path] ?? onDisk.content
      try {
        const result = await api(hostId).writeFile(path, text, onDisk.mod_time)
        // The server's answer is in hand, mod_time included, so this writes it
        // into the cache rather than invalidating. A refetch here could only
        // race the buffer it is meant to agree with.
        client.setQueryData(fileContentQuery(hostId, path).queryKey, (current) =>
          current ? { ...current, content: text, mod_time: result.mod_time } : current,
        )
        setTabs((current) => current.map((t) => (t.path === path ? { ...t, dirty: false } : t)))
      } catch (err) {
        // The agent edits the same files, so this is a real outcome rather than
        // an edge case: reload, then decide what to keep.
        if (statusOf(err) === 409) store.notify('Changed on disk since it was opened — reload first', 'error')
        else store.fail(err)
      }
    },
    [hostId, client],
  )

  const reload = useCallback(
    async (path: string): Promise<void> => {
      try {
        // staleTime overridden to force the read: the entry never goes stale on
        // its own, precisely so nothing but this and the effect below can move
        // it under an open buffer.
        await client.fetchQuery({ ...fileContentQuery(hostId, path), staleTime: 0 })
        delete drafts.current[path]
        setTabs((current) =>
          current.map((tab) =>
            tab.path === path ? { ...tab, dirty: false, version: tab.version + 1 } : tab,
          ),
        )
      } catch (err) {
        store.fail(err)
      }
    },
    [hostId, client],
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
    const tab = tabsRef.current.find((t) => t.path === activePath)
    if (!tab || tab.dirty) return
    void reload(activePath)
  }, [visible, activePath, reload])

  const close = (path: string): void => {
    const tab = tabsRef.current.find((t) => t.path === path)
    if (tab?.dirty && !confirm(`Discard unsaved changes to ${basename(path)}?`)) return
    delete drafts.current[path]
    setTabs((current) => {
      const remaining = current.filter((t) => t.path !== path)
      setActivePath((active) =>
        active === path ? (remaining[remaining.length - 1]?.path ?? null) : active,
      )
      return remaining
    })
  }

  /** Whether the buffer differs from disk, decided by the tab that can see both. */
  const setDirty = useCallback((path: string, dirty: boolean): void => {
    setTabs((current) => {
      const tab = current.find((t) => t.path === path)
      if (!tab || tab.dirty === dirty) return current
      return current.map((t) => (t.path === path ? { ...t, dirty } : t))
    })
  }, [])

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
      } else if (key === 'b' && !event.shiftKey) {
        event.preventDefault()
        setSide((current) => (current === null ? 'tree' : null))
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

  const active = tabs.find((tab) => tab.path === activePath) ?? null

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
      <aside className="ws-side" hidden={side === null}>
        <div className="ws-side-head">
          {/* Clicking the one already showing puts it away, which is how the
              activity bar this borrows from behaves. */}
          <button
            className={side === 'tree' ? 'ws-view on' : 'ws-view'}
            title="Explorer (⌘B)"
            onClick={() => setSide(side === 'tree' ? null : 'tree')}
          >
            Explorer
          </button>
          <button
            className={side === 'find' ? 'ws-view on' : 'ws-view'}
            title="Search (⇧⌘F)"
            onClick={() => {
              if (side === 'find') {
                setSide(null)
                return
              }
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
            expanded={expanded}
            onExpandedChange={setExpanded}
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
          {/* The way back. Collapsing from inside the explorer would otherwise
              leave nothing on screen saying it is there, and a keyboard
              shortcut is not something you can see. */}
          <button
            className={`ws-side-toggle ${side === null ? 'off' : ''}`}
            title={side === null ? 'Show the explorer (⌘B)' : 'Hide the explorer (⌘B)'}
            aria-label={side === null ? 'Show the explorer' : 'Hide the explorer'}
            aria-expanded={side !== null}
            onClick={() => setSide(side === null ? 'tree' : null)}
          >
            <Chevron dir={side === null ? 'right' : 'left'} />
          </button>
          {tabs.map((tab) => (
            <div
              key={tab.path}
              className={`ws-tab ${tab.path === activePath ? 'active' : ''}`}
              onClick={() => setActivePath(tab.path)}
            >
              <span className="ws-tab-name">{basename(tab.path)}</span>
              <button
                className="ws-tab-close"
                title={tab.dirty ? 'Unsaved changes' : 'Close'}
                onClick={(event) => {
                  event.stopPropagation()
                  close(tab.path)
                }}
              >
                {tab.dirty ? '●' : '✕'}
              </button>
            </div>
          ))}
        </div>

        {active ? (
          <ActiveFile
            key={active.path}
            tab={active}
            root={root}
            hostId={hostId}
            sessionId={sessionId}
            draft={drafts.current[active.path]}
            onChange={(text, dirty) => {
              drafts.current[active.path] = text
              setDirty(active.path, dirty)
            }}
            onSave={() => void save(active.path)}
            onReload={() => void reload(active.path)}
            onMode={(mode) =>
              setTabs((current) => current.map((t) => (t.path === active.path ? { ...t, mode } : t)))
            }
            restore={views.current[active.path] ?? null}
            onPositionChange={(at) => noteView(active.path, at)}
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

/**
 * The tab in front, and the one place a file's contents are read.
 *
 * Only the active tab mounts a query: a dynamic number of tabs cannot mount a
 * dynamic number of hooks, and the panel already renders exactly one file. An
 * inactive tab needs none — its draft is in the panel's ref and its unsaved
 * marker is on its own record.
 *
 * Keyed by path in the parent, so switching tabs remounts this rather than
 * leaving one file's folds and menu over another's.
 */
function ActiveFile({
  tab,
  root,
  hostId,
  sessionId,
  draft,
  onChange,
  onSave,
  onReload,
  onMode,
  restore,
  onPositionChange,
}: {
  tab: OpenTab
  root: string
  hostId: string
  sessionId: string
  /** The unsaved buffer, when this file has one. */
  draft: string | undefined
  onChange: (text: string, dirty: boolean) => void
  onSave: () => void
  onReload: () => void
  onMode: (mode: 'edit' | 'preview') => void
  restore: ReadingPosition | null
  onPositionChange: (at: ReadingPosition) => void
}): JSX.Element {
  const { data, error } = useQuery(fileContentQuery(hostId, tab.path))

  if (error) return <p className="empty-note">{error.message}</p>
  if (!data) return <p className="empty-note">Loading…</p>

  const file: OpenFile = {
    ...tab,
    saved: data.content,
    modTime: data.mod_time,
    kind: kindOf(tab.path),
    base64: data.encoding === 'base64',
    ...shapeOf(data),
  }
  return (
    <FileView
      file={file}
      root={root}
      hostId={hostId}
      sessionId={sessionId}
      text={draft ?? data.content}
      // Compared against what is on disk right here, because this is where both
      // are in hand: editing back to the original clears the unsaved marker.
      onChange={(text) => onChange(text, text !== data.content)}
      onSave={onSave}
      onReload={onReload}
      onMode={onMode}
      restore={restore}
      onPositionChange={onPositionChange}
    />
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
  restore,
  onPositionChange,
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
  /** Where this file was last left, or null if it has not been opened before. */
  restore: ReadingPosition | null
  onPositionChange: (at: ReadingPosition) => void
}): JSX.Element {
  const markdown = isMarkdownPath(file.path)
  const html = file.kind === 'html'
  const rendered = markdown && file.mode === 'preview'
  const renderedHtml = html && file.mode === 'preview'
  const blocks = useMemo(() => (rendered ? renderMarkdownBlocks(text) : null), [rendered, text])
  const [menu, setMenu] = useState<{ x: number; y: number; range: LineRange } | null>(null)
  /** Headings whose section is folded away, by the heading's start line. */
  const [folds, setFolds] = useState<Set<number>>(new Set())
  const preview = useRef<HTMLDivElement | null>(null)
  const [selection, clearSelection] = useTextSelection(preview)
  // Markdown renders its images without a src; this puts the bytes in.
  useInlineImages(preview, { hostId, basePath: file.path, root, revision: blocks })
  const [width, setWidth] = useState(readReadingWidth)
  /** An image's pixel size, once it has loaded, and whether to show it at that. */
  const [size, setSize] = useState<{ width: number; height: number } | null>(null)
  const [natural, setNatural] = useState(false)
  /**
   * Whether this page's scripts may run.
   *
   * Off every time a file is opened, and never remembered. Turning it on is a
   * decision about one page an agent wrote, not a setting to be carried into
   * the next one without being asked again.
   */
  const [runScripts, setRunScripts] = useState(false)
  useEffect(() => setRunScripts(false), [file.path])

  /**
   * What to draw a picture from, or null if there is nothing to draw.
   *
   * Base64 is the ordinary case. An SVG arrives as text, because it is text,
   * and is encoded here. A binary image with neither — a daemon older than the
   * encoding parameter — has already lost its bytes to the JSON encoder, so
   * there is nothing to show and it falls through to the note that says so.
   */
  const source = useMemo(() => {
    if (file.kind !== 'image') return null
    if (file.base64) return dataUrl(file.path, file.saved)
    if (extensionOf(file.path) !== 'svg') return null
    return `data:image/svg+xml;base64,${bytesToBase64(text)}`
  }, [file.kind, file.base64, file.path, file.saved, text])

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
        {/* An image says how big it is, which is the one fact about it a file
            browser is in a position to tell you. "binary" is what is left when
            there is nothing better to say. */}
        {source ? (
          size && <span className="pill">{`${size.width} × ${size.height}`}</span>
        ) : (
          file.readOnly && <span className="pill">{file.binary ? 'binary' : 'read only'}</span>
        )}
        <span className="grow" />
        {html && file.mode === 'preview' && (
          <label
            className="check head-check"
            title="Run this page's scripts. It still cannot reach the network."
          >
            <input
              type="checkbox"
              checked={runScripts}
              onChange={(event) => setRunScripts(event.target.checked)}
            />
            JavaScript
          </label>
        )}
        {(markdown || html) && (
          <button
            className="icon-btn tiny"
            title={
              file.mode === 'preview'
                ? 'Edit source'
                : html
                  ? 'Render the page'
                  : 'Show rendered markdown'
            }
            onClick={() => onMode(file.mode === 'preview' ? 'edit' : 'preview')}
          >
            {file.mode === 'preview' ? '{}' : '¶'}
          </button>
        )}
        <button className="icon-btn tiny" title="Reload from disk" onClick={onReload}>
          ⟳
        </button>
        {!source && (
          <button
            className="icon-btn tiny"
            title="Copy contents"
            onClick={() => void navigator.clipboard.writeText(text)}
          >
            ⧉
          </button>
        )}
        {!file.readOnly && (
          <button className="filled tiny" disabled={!file.dirty} onClick={onSave}>
            Save
          </button>
        )}
      </header>

      {source ? (
        <div className={`ws-image ${natural ? 'natural' : ''}`}>
          <img
            src={source}
            alt={basename(file.path)}
            onLoad={(event) =>
              setSize({
                width: event.currentTarget.naturalWidth,
                height: event.currentTarget.naturalHeight,
              })
            }
            // Two states, which are the two anyone wants: see all of it, or see
            // it properly. A zoom control would be a photo viewer.
            onClick={() => setNatural((on) => !on)}
            title={natural ? 'Fit to the panel' : 'Show at its own size'}
          />
        </div>
      ) : renderedHtml ? (
        <HtmlPreview hostId={hostId} html={text} path={file.path} root={root} scripts={runScripts} />
      ) : file.binary ? (
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
          restore={restore}
          onViewChange={onPositionChange}
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

/** btoa refuses anything above U+00FF, and an SVG is often full of them. */
function bytesToBase64(text: string): string {
  const bytes = new TextEncoder().encode(text)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary)
}

function basename(path: string): string {
  return path.split('/').pop() || path
}

/** Which list the explorer shows, or nothing at all. */
type Side = 'tree' | 'find' | null

/** What the panel remembers about a session between visits. */
interface Workspace {
  root: string | null
  open: string[]
  active: string | null
  /** Directories open in the tree. Absent in records written before this. */
  expanded?: string[]
  /** Where each file was left, by path. Absent in older records. */
  view?: Record<string, ReadingPosition>
  /** The explorer, or null for collapsed. Absent in records written before it
   *  could be, which read as the tree — the panel has always opened that way. */
  side?: Side
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
