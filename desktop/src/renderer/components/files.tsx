import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import type { Text } from '@codemirror/state'

import { api, bridge, statusOf } from '../bridge.ts'
import { changedLines, fileDiff, gutterMarks } from '../diff.ts'
import { dataUrl, extensionOf, kindOf, mimeForPath, type FileKind } from '../filetype.ts'
import { changedPaths, fileEffectFor } from '../keys.ts'
import { isMarkdownPath, languageForPath, renderMarkdownBlocks } from '../markdown.ts'
import { useFenceHighlight } from '../highlighter.ts'
import { useMermaid } from '../mermaid.ts'
import {
  fileContentQuery,
  gitChangesQuery,
  gitDiffQuery,
  gitStatusQuery,
  readContent,
  touchedQuery,
  worktreesQuery,
} from '../queries.ts'
import { comparable } from '../raw-file.ts'
import { store, useStore } from '../store.ts'
import { byLastTouched, type FileContent, type TouchedFile, type Worktree } from '../../shared/models.ts'
import { ChangesView } from './changes-view.tsx'
import { useScope, type Scope } from './commits.tsx'
import { DiffView, type DiffLayout } from './diff-view.tsx'
import { firstLineOf } from './edit-offsets.ts'
import { CodeEditor, type Cursor, type Flash, type ReadingPosition } from './editor.tsx'
import { FileDiff } from './file-diff.tsx'
import { describeFileError } from './file-errors.ts'
import { FileTree } from './file-tree.tsx'
import { FindInFiles } from './find-in-files.tsx'
import { HtmlPreview, useInlineImages } from './html-preview.tsx'
import { Chevron, Close, Copy, Reload, Search, Wrap } from './icons.tsx'
import { Modal } from './newsession.tsx'
import { PathLabel } from './path-label.tsx'
import { QuickOpen } from './quick-open.tsx'
import { RootPicker } from './root-picker.tsx'
import { SelectionMenu, useTextSelection, type MenuAction } from './selection-menu.tsx'
import { scopeDiffAt, scopeFiles, scopeRange, type ScopeFile } from './scope-files.ts'
import { CLOSED_LIMIT, RECENT_LIMIT, baseName, nextActive, pushRecent, tabLabels } from './tab-strip.ts'
import { agentMarks, gitMarks } from './tree-rows.ts'

/** Past this the editor is read-only: CodeMirror is not a log viewer. */
const MAX_EDIT_BYTES = 1_000_000

const NO_WORKTREES: Worktree[] = []
const NO_TOUCHED: TouchedFile[] = []

/**
 * How a tab shows its file: in the editor, rendered, or as its patch in the
 * Changes mode's scope (spec 65, B8). One file has one tab, whichever it is.
 */
type TabMode = 'edit' | 'preview' | 'diff'

/** What a file opens as when nobody asked for its diff. */
function defaultMode(path: string): Exclude<TabMode, 'diff'> {
  // A picture and a page are things to look at. Text opens in the editor,
  // markdown included only when it is prose.
  return kindOf(path) !== 'text' || isMarkdownPath(path) ? 'preview' : 'edit'
}

/**
 * How long scrolling has to stop before the position is written down. Every
 * wheel tick is a scroll event, and none of them is worth a write on its own.
 */
const VIEW_SETTLE = 500

/** At most this many files an agent just wrote are fetched ahead (spec 65, 2.3). */
const AGENT_PREFETCH = 5

/** How long after an event the agent-write prefetch waits, to stay out of the way. */
const AGENT_PREFETCH_DELAY = 300

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
  /** Markdown opens rendered, a file opened from Changes opens as its diff, and
   *  everything else opens in the editor. */
  mode: TabMode
  /** A line of the new file an agent asked the diff to show. */
  diffLine: number | null
  /** Bumped to remount the editor when the buffer is replaced from disk. Not
   *  derived from the cache: a save writes the answer back too, and remounting
   *  on that would throw away the cursor of whoever pressed ⌘S. */
  version: number
  cursor: Cursor | null
  /** The file changed under an unsaved buffer. Raises the bar; changes nothing
   *  else. Cleared by a reload or by a save that lands. */
  staleOnDisk: boolean
  /** This version came from an event rather than from a person, so the editor
   *  must not take focus when it remounts — the user may be typing somewhere
   *  else entirely. Cleared by the effect that follows the render using it. */
  quiet: boolean
  /** Lines an agent's write just changed, tinted for a moment (spec 65, B6). */
  flash: Flash | null
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
  /** The file's own bytes, when it was read raw and is not text. */
  bytes?: ArrayBuffer
}

/** A tab that was closed, kept for ⌥⇧T with where it was being read. */
interface ClosedTab {
  path: string
  view?: ReadingPosition
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
  const binary =
    content.encoding === 'base64' || content.encoding === 'binary' || content.content.includes('\u0000')
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
  // The same read the root picker's worktree list makes. Not a repository at all
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
  const lastSide = useRef<Exclude<Side, null>>('tree')
  if (side !== null) lastSide.current = side
  // The search is mounted the first time it is shown and kept from then on, so
  // a session that never searched has no search field lying in wait.
  const [findUsed, setFindUsed] = useState(false)
  if (side === 'find' && !findUsed) setFindUsed(true)
  const [findSeq, setFindSeq] = useState(0)
  const [quickOpen, setQuickOpen] = useState(false)
  const [quickOpenQuery, setQuickOpenQuery] = useState('')
  const [rootPicker, setRootPicker] = useState(false)
  const [reveal, setReveal] = useState<{ path: string; seq: number } | null>(null)
  const [recent, setRecent] = useState<string[]>([])
  const [confirm, setConfirm] = useState<string[] | null>(null)
  const [tabMenu, setTabMenu] = useState<{ x: number; y: number; path: string } | null>(null)
  const [hoveredTab, setHoveredTab] = useState<string | null>(null)
  const [wrapChoice, setWrapChoice] = useState<boolean | null>(() => readWrap(hostId))
  const target = useStore((s) => s.fileTarget)
  const termSize = useStore((s) => s.termSize)
  const local = useStore((s) => s.hosts.find((host) => host.id === hostId)?.local ?? false)
  const panel = useRef<HTMLDivElement | null>(null)
  const tabStrip = useRef<HTMLDivElement | null>(null)

  // Drafts live outside React state: a keystroke should not re-render the tree.
  // Held as CodeMirror documents, not strings: the string is built when it is
  // needed — a save, a preview — rather than on every keystroke.
  const drafts = useRef<Record<string, Text>>({})
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
  /** Closed tabs, newest last, for ⌥⇧T. Per session, and gone with the window. */
  const closed = useRef<ClosedTab[]>([])

  const revealPath = useCallback((path: string) => {
    setReveal((current) => ({ path, seq: (current?.seq ?? 0) + 1 }))
  }, [])

  /** Puts the caret somewhere in a tab that is already open. */
  const jumpTo = useCallback((path: string, at: { line: number; column: number; flash?: boolean }) => {
    setTabs((current) =>
      current.map((tab) => (tab.path === path ? { ...tab, cursor: { ...at, seq: (tab.cursor?.seq ?? 0) + 1 } } : tab)),
    )
  }, [])

  const dropTab = useCallback((path: string) => {
    setTabs((current) => {
      const remaining = current.filter((t) => t.path !== path)
      setActivePath((active) =>
        nextActive(
          current.map((t) => t.path),
          path,
          active,
        ),
      )
      return remaining
    })
  }, [])

  const openFile = useCallback(
    async (
      path: string,
      at?: { line: number; column: number; flash?: boolean },
      /** True when the caller has another path to try if this one is missing. */
      quiet = false,
      /**
       * False while reopening the tabs a session had last time. Those must not
       * take the front, or a file asked for as the panel mounts loses it to
       * whatever was open before.
       */
      activate = true,
      /** Open on the file's diff, as a click in Changes does. */
      diff?: { line?: number },
    ): Promise<boolean> => {
      if (activate) revealPath(path)
      const existing = tabsRef.current.find((tab) => tab.path === path)
      if (existing) {
        if (activate) setActivePath(path)
        if (at) jumpTo(path, at)
        if (diff) {
          setTabs((current) =>
            current.map((tab) => (tab.path === path ? { ...tab, mode: 'diff', diffLine: diff.line ?? null } : tab)),
          )
        }
        return true
      }

      const add = (): void => {
        setTabs((current) =>
          current.some((tab) => tab.path === path)
            ? current
            : [
                ...current,
                {
                  path,
                  dirty: false,
                  mode: diff ? 'diff' : defaultMode(path),
                  diffLine: diff?.line ?? null,
                  version: 0,
                  cursor: at ? { ...at, seq: 1 } : null,
                  staleOnDisk: false,
                  quiet: false,
                  flash: null,
                },
              ],
        )
        if (activate) setActivePath(path)
      }

      // A person asked for this file: the tab opens now, with its header, and
      // the body follows (spec 65, B1). With the hover prefetch it is usually
      // already there. A failure says what failed in the tab itself.
      if (!quiet) {
        add()
        client.fetchQuery(fileContentQuery(hostId, path)).catch((err: unknown) => {
          // A path from the transcript is as likely to be a directory as a
          // file; the daemon says which with a 400, and the tree has already
          // revealed it, so the tab has nothing to show.
          if (statusOf(err) === 400) dropTab(path)
        })
        return true
      }

      try {
        // Quiet callers need the answer before the tab exists: a restore skips
        // files the agent has since deleted, and a chip tries another path.
        await client.fetchQuery(fileContentQuery(hostId, path))
        add()
        return true
      } catch (err) {
        return statusOf(err) === 400
      }
    },
    [hostId, client, revealPath, jumpTo, dropTab],
  )

  // Which files were open, which was in front, and where the tree was pointed.
  // A session is looked away from and come back to all day; losing the tabs
  // every time makes the panel a viewer rather than a place to work.
  const memory = `helios.files.${hostId}.${sessionId}`
  const recentKey = `helios.files.recent.${hostId}.${sessionId}`
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
    closed.current = []
    setTabs([])
    setActivePath(null)
    setReveal(null)
    setFindUsed(false)
    drafts.current = {}
    setRecent(readList(recentKey))

    const saved = readWorkspace(memory)
    setRootOverride(saved?.root ?? null)
    setExpanded(new Set(saved?.expanded ?? []))
    // A layout saved with the Git panel in front opens here on Changes, which is
    // where that panel's job went (spec 65, B8).
    setSide(store.takeChangesRequest(hostId, sessionId) ? 'changes' : saved?.side === undefined ? 'tree' : saved.side)
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
  }, [memory, recentKey, openFile, hostId, sessionId])

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

  // What was looked at lately, for quick open's empty query. A file that comes
  // to the front is a file being worked with, however it got there.
  useEffect(() => {
    if (loadedFor !== memory || !activePath) return
    setRecent((current) => {
      const next = pushRecent(current, activePath, RECENT_LIMIT)
      writeList(recentKey, next)
      return next
    })
  }, [activePath, loadedFor, memory, recentKey])

  const save = useCallback(
    async (path: string): Promise<void> => {
      const tab = tabsRef.current.find((t) => t.path === path)
      const onDisk = client.getQueryData(fileContentQuery(hostId, path).queryKey)
      if (!tab || !tab.dirty || !onDisk || shapeOf(onDisk).readOnly) return
      // The one place the buffer becomes a string.
      const text = drafts.current[path]?.toString() ?? onDisk.content
      try {
        const result = await api(hostId).writeFile(path, text, onDisk.mod_time, onDisk.etag)
        // The server's answer is in hand, mod_time included, so this writes it
        // into the cache rather than invalidating. A refetch here could only
        // race the buffer it is meant to agree with. The ETag goes with it —
        // or goes, from a daemon that sends none, so the next read is a full
        // one rather than a 304 against bytes that are no longer there.
        client.setQueryData(fileContentQuery(hostId, path).queryKey, (current) =>
          current
            ? { ...current, content: text, mod_time: result.mod_time, etag: result.etag, size: result.size }
            : current,
        )
        // The bar goes with the save: what is on disk is now what is in the
        // buffer, whoever else had written to it in between.
        setTabs((current) =>
          current.map((t) => (t.path === path ? { ...t, dirty: false, staleOnDisk: false } : t)),
        )
      } catch (err) {
        // The agent edits the same files, so this is a real outcome rather than
        // an edge case: reload, then decide what to keep.
        if (statusOf(err) === 409) store.notify('Changed on disk since it was opened — reload first', 'error')
        else store.fail(err)
      }
    },
    [hostId, client],
  )

  /**
   * Replaces the buffer from disk. `quiet` keeps the editor from taking focus;
   * `flash` tints the lines that moved.
   */
  const adopt = useCallback((path: string, quiet: boolean, flash: number[] = []): void => {
    delete drafts.current[path]
    setTabs((current) =>
      current.map((tab) =>
        tab.path === path
          ? {
              ...tab,
              dirty: false,
              staleOnDisk: false,
              quiet,
              version: tab.version + 1,
              flash: flash.length > 0 ? { lines: flash, seq: (tab.flash?.seq ?? 0) + 1 } : null,
            }
          : tab,
      ),
    )
  }, [])

  const reload = useCallback(
    async (path: string): Promise<void> => {
      try {
        // staleTime overridden to force the read: the entry never goes stale on
        // its own, precisely so nothing but this and the effect below can move
        // it under an open buffer.
        await client.fetchQuery({ ...fileContentQuery(hostId, path), staleTime: 0 })
        adopt(path, false)
      } catch (err) {
        store.fail(err)
      }
    },
    [hostId, client, adopt],
  )

  /**
   * Brings one tab in line with the disk after something said the file moved.
   *
   * Reads around the cache on purpose. `fetchQuery` would write the answer into
   * the entry `ActiveFile` derives `saved` from, and under a dirty tab that
   * moves two things at once: the text the unsaved marker is decided against,
   * and the `mod_time` a later save sends as its base — which would turn the
   * 409 that protects the other writer into a silent overwrite.
   *
   * The read sends the held ETag, so an event about bytes already on screen —
   * the echo of this window's own save — costs a 304 and changes nothing.
   */
  const syncFromDisk = useCallback(
    async (path: string, quiet = true): Promise<void> => {
      const tab = tabsRef.current.find((t) => t.path === path)
      const held = client.getQueryData(fileContentQuery(hostId, path).queryKey)
      if (!tab || !held) return

      let fresh: FileContent | null = null
      try {
        fresh = await readContent(hostId, path)
      } catch (err) {
        // Gone is a real answer and the bar reports it. Anything else is a
        // transient, and the next event or the next arrival tries again.
        if (statusOf(err) !== 404) return
      }

      switch (fileEffectFor(tab, comparable(held), fresh ? comparable(fresh) : null)) {
        case 'ignore':
          return
        case 'mark':
          setTabs((current) =>
            current.map((t) => (t.path === path ? { ...t, staleOnDisk: true } : t)),
          )
          return
        case 'reload':
          // The answer is already in hand, so this writes it rather than
          // fetching the same bytes a second time.
          if (fresh) client.setQueryData(fileContentQuery(hostId, path).queryKey, fresh)
          adopt(
            path,
            quiet,
            fresh && fresh.encoding !== 'binary' ? changedLines(held.content, fresh.content) : [],
          )
      }
    },
    [hostId, client, adopt],
  )

  /** Fetches a file ahead of the click. Never for an open tab: see the query. */
  const prefetch = useCallback(
    (path: string): void => {
      if (tabsRef.current.some((tab) => tab.path === path)) return
      void client.prefetchQuery(fileContentQuery(hostId, path))
    },
    [hostId, client],
  )

  /**
   * The daemon says a path it was asked to watch has changed content.
   *
   * Every open tab, not just the one in front: a background tab holds no query,
   * but it holds a draft and an unsaved marker, and its cache entry is what the
   * user sees the moment they click it.
   *
   * And a few files that are not open, fetched ahead at low priority: a person
   * often opens the file the agent has just changed.
   */
  const rootRef = useRef(root)
  rootRef.current = root
  const visibleRef = useRef(visible)
  visibleRef.current = visible
  useEffect(() => {
    return bridge.hosts.onEvent(({ hostId: from, event }) => {
      if (from !== hostId || event.type !== 'file_changed') return
      const named = changedPaths(event.data.paths)
      const moved = new Set(named.map((entry) => entry.path))
      for (const tab of tabsRef.current) {
        if (moved.has(tab.path)) void syncFromDisk(tab.path)
      }
      if (!visibleRef.current) return
      const gone = new Set(
        (Array.isArray(event.data.paths) ? event.data.paths : [])
          .filter((entry) => (entry as { gone?: unknown }).gone === true)
          .map((entry) => (entry as { path?: unknown }).path),
      )
      const ahead = named
        .filter(({ path, kind }) => kind === 'file' && !gone.has(path) && path.startsWith(`${rootRef.current}/`))
        .slice(0, AGENT_PREFETCH)
      if (ahead.length === 0) return
      window.setTimeout(() => ahead.forEach(({ path }) => prefetch(path)), AGENT_PREFETCH_DELAY)
    })
  }, [hostId, syncFromDisk, prefetch])

  /**
   * Clears the quiet flag once the render that used it has happened.
   *
   * It is read on mount only, so leaving it set would suppress the focus a
   * later tab switch is right to take. Clearing does not bump the version, so
   * nothing remounts.
   */
  const activeTab = tabs.find((tab) => tab.path === activePath) ?? null
  useEffect(() => {
    if (!activeTab?.quiet) return
    setTabs((current) => current.map((t) => (t.quiet ? { ...t, quiet: false } : t)))
  }, [activeTab?.path, activeTab?.version, activeTab?.quiet])

  // The agent edits the tree while the user is elsewhere, so the buffer that
  // was accurate on the way out is stale on the way back. Every clean tab, not
  // just the one in front — and a read is also what re-registers the path with
  // the daemon's watcher after its TTL expired. Unsaved edits outrank what is
  // on disk and are left alone; the file keeps its unsaved marker, and the bar
  // appears if what is on disk really did move.
  const wasVisible = useRef(visible)
  useEffect(() => {
    const returning = visible && !wasVisible.current
    wasVisible.current = visible
    if (!returning) return
    for (const tab of tabsRef.current) void syncFromDisk(tab.path, tab.path !== activePath)
  }, [visible, activePath, syncFromDisk])

  /** Closes tabs, no questions asked. Their places go on the reopen list. */
  const closeNow = useCallback((paths: string[]): void => {
    const going = new Set(paths)
    for (const path of paths) {
      delete drafts.current[path]
      closed.current = [...closed.current.filter((tab) => tab.path !== path), { path, view: views.current[path] }].slice(
        -CLOSED_LIMIT,
      )
    }
    setTabs((current) => {
      let active: string | null = null
      setActivePath((was) => {
        let order = current.map((t) => t.path)
        active = was
        for (const path of paths) {
          active = nextActive(order, path, active)
          order = order.filter((p) => p !== path)
        }
        return active
      })
      return current.filter((t) => !going.has(t.path))
    })
  }, [])

  /** Closes tabs, asking first — in the app's own dialog — when any is unsaved. */
  const close = useCallback(
    (paths: string[]): void => {
      if (paths.length === 0) return
      const dirty = tabsRef.current.some((t) => paths.includes(t.path) && t.dirty)
      if (dirty) setConfirm(paths)
      else closeNow(paths)
    },
    [closeNow],
  )

  /** ⌥⇧T: the last tab closed comes back, where it was being read. */
  const reopen = useCallback((): void => {
    const last = closed.current.pop()
    if (!last) return
    if (last.view) views.current[last.path] = last.view
    void openFile(last.path)
  }, [openFile])

  /** ⌃Tab and ⌃⇧Tab. */
  const cycle = useCallback(
    (by: number): void => {
      const order = tabsRef.current.map((t) => t.path)
      if (order.length < 2) return
      const at = activePath ? order.indexOf(activePath) : -1
      setActivePath(order[(at + by + order.length) % order.length] ?? null)
    },
    [activePath],
  )

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
  const checkouts = useRef({ root, owners: [] as string[] })
  checkouts.current = { root, owners: [sessionRoot, ...worktrees.map((worktree) => worktree.path)] }

  useEffect(() => {
    if (!target || target.hostId !== hostId) return
    claimed.current = true
    const path = target.path
    if (target.mode === 'find') {
      // The whole path, not its last segment. The chip already knows the
      // directory, and the search re-roots there rather than hunting the name
      // across a tree it was never in.
      setQuickOpenQuery(path)
      setQuickOpen(true)
      store.clearFileTarget()
      return
    }
    const mapped = inRoot(path, checkouts.current.root, checkouts.current.owners)
    // An agent pointing at a file usually means one line of it.
    const at = target.line ? { line: target.line, column: 1 } : undefined
    const needle = target.needle
    void (async () => {
      let opened = path
      if (mapped !== path && (await openFile(mapped, at, true))) opened = mapped
      else await openFile(path, at)
      // A tool row knows what its edit wrote but not where. The file does.
      if (!needle || at) return
      try {
        const content = await client.fetchQuery(fileContentQuery(hostId, opened))
        const line = firstLineOf(content.content, needle)
        if (line) jumpTo(opened, { line, column: 1 })
      } catch {
        // The tab says what went wrong with the read; there is no line to find.
      }
    })()
    store.clearFileTarget()
    // seq, not path: reopening the same file has to work.
  }, [target?.seq])

  const wrapFor = useCallback((path: string): boolean => wrapChoice ?? wrapsByDefault(path), [wrapChoice])
  const toggleWrap = useCallback(() => {
    if (!activePath) return
    const next = !wrapFor(activePath)
    setWrapChoice(next)
    writeWrap(hostId, next)
  }, [activePath, wrapFor, hostId])

  /**
   * Keeps the keyboard in the panel when a key changes the tab in front.
   *
   * The editor that had it is unmounted with its tab, and focus that falls to
   * the document is focus the next ⌃Tab or ⌘W no longer counts as the panel's.
   * Moved to the panel itself before the change, not after it: the next key
   * can arrive before any frame does. An editor that mounts in front takes it
   * from there.
   */
  const holdFocus = useCallback((): void => {
    panel.current?.focus({ preventScroll: true })
  }, [])

  useEffect(() => {
    const onKey = (event: KeyboardEvent): void => {
      // A hidden panel is still mounted (detail.tsx keeps every panel), and it
      // must not save a file or open a dialog the user cannot see (F2).
      if (!visible) return
      const inside = panel.current?.contains(document.activeElement) ?? false
      const mod = event.metaKey || event.ctrlKey
      const key = event.key.toLowerCase()
      // A key the panel acts on is the panel's alone. This listens in the
      // capture phase so it hears the key before the editor does, and stops it
      // there: CodeMirror reads ⌘⇧F as its own ⌘F and opened its find panel
      // under Find in Files, and a key left to travel on can type into the
      // buffer it was never meant for.
      const take = (): void => {
        event.preventDefault()
        event.stopPropagation()
      }

      // Tab keys belong to the panel only while it has the keyboard: ⌃Tab in
      // a composer beside it is not a request to change files.
      if (event.ctrlKey && event.key === 'Tab') {
        if (!inside) return
        take()
        holdFocus()
        cycle(event.shiftKey ? -1 : 1)
        return
      }
      if (event.altKey && event.shiftKey && !mod && event.code === 'KeyT') {
        if (!inside) return
        take()
        holdFocus()
        reopen()
        return
      }
      if (event.altKey && !event.shiftKey && !mod && event.code === 'KeyZ') {
        if (!inside) return
        take()
        toggleWrap()
        return
      }
      if (!mod) return
      if (key === 'p' && !event.shiftKey) {
        take()
        setQuickOpen(true)
      } else if (key === 'e' && event.shiftKey) {
        // ⌘⇧E, not ⌘B: ⌘B is the app sidebar's, and one press used to fold
        // both (F3).
        take()
        setSide((current) => (current === null ? lastSide.current : null))
      } else if (key === 'f' && event.shiftKey) {
        take()
        setSide('find')
        setFindSeq((seq) => seq + 1)
      } else if (key === 'w' && !event.shiftKey) {
        // The app's ⌘W closes the session's terminal; it steps aside for an
        // event from in here, and this closes the tab instead.
        if (!inside || !activePath) return
        take()
        holdFocus()
        close([activePath])
      } else if (key === 's') {
        // Taken before CodeMirror's own ⌘S can run: saving twice would send the
        // second write with a mod time the first one has just replaced.
        take()
        if (activePath) void save(activePath)
      }
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [visible, activePath, save, close, cycle, reopen, toggleWrap, holdFocus])

  // The tab in front is always in view, however many are open.
  useEffect(() => {
    tabStrip.current?.querySelector('.ws-tab.active')?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  }, [activePath, tabs.length])

  const active = tabs.find((tab) => tab.path === activePath) ?? null
  const labels = useMemo(() => tabLabels(tabs.map((tab) => tab.path)), [tabs])

  // Git state and the agent's edits, for the tree's marks and the Changes view.
  // Observed only while something on screen draws them, so a session in the
  // background costs nothing when a file moves.
  // Git status also decides which tabs have a Diff, so it is read whenever the
  // panel is on screen, not only while the tree or Changes is.
  const marksWanted = visible && (side === 'tree' || side === 'changes')
  const statusQuery = useQuery({ ...gitStatusQuery(hostId, root), enabled: visible && root !== '' })
  const touchedQ = useQuery({ ...touchedQuery(hostId, sessionId), enabled: marksWanted && sessionId !== '' })
  const git = useMemo(() => gitMarks(statusQuery.data), [statusQuery.data])
  const touched = touchedQ.data ?? NO_TOUCHED
  const agent = useMemo(() => agentMarks(touched, root), [touched, root])

  // Which changes the Changes mode shows, and so which files have a Diff.
  const { scope, pick: pickScope, adopt: adoptScope } = useScope(hostId, root, sessionId, visible)
  const range = scopeRange(scope)
  const changesQuery = useQuery({
    ...gitChangesQuery(hostId, root, range?.to ?? '', range?.from, range?.mergeBase),
    enabled: visible && range !== null,
  })
  const inScope = useMemo(
    () => scopeFiles(scope, statusQuery.data, range ? changesQuery.data : null, root),
    [scope, statusQuery.data, range, changesQuery.data, root],
  )
  // Whether the list above is the answer yet, or only the absence of one: a
  // tab opened on its diff waits for it rather than falling back to Source.
  const scopeKnown = scope !== null && (range ? changesQuery.data !== undefined : statusQuery.data !== undefined)
  const [diffLayout, setDiffLayout] = useState<DiffLayout>(readDiffLayout)

  // An agent asked, through helios_show, for a diff: Changes in front, in the
  // scope it named, with the file open on its diff when it named one.
  const diffTarget = useStore((s) => s.diffTarget)
  useEffect(() => {
    if (!diffTarget || diffTarget.hostId !== hostId) return
    if (diffTarget.sessionId && diffTarget.sessionId !== sessionId) return
    setSide('changes')
    if (diffTarget.base) {
      adoptScope({ kind: 'review', base: diffTarget.base, span: 0, label: `${diffTarget.base}...HEAD` })
    } else if (diffTarget.commit) {
      adoptScope({ kind: 'commit', to: diffTarget.commit, from: null, span: 1, label: diffTarget.commit.slice(0, 7) })
    } else {
      adoptScope({ kind: 'working' })
    }
    if (diffTarget.layout) setDiffLayout(diffTarget.layout)
    if (diffTarget.path) void openFile(diffTarget.path, undefined, false, true, { line: diffTarget.line })
    store.clearDiffTarget()
  }, [diffTarget?.seq])

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

  const pickSide = (next: Exclude<Side, null>): void => {
    // Clicking the one already showing puts it away, which is how the
    // activity bar this borrows from behaves.
    if (side === next) {
      setSide(null)
      return
    }
    setSide(next)
    if (next === 'find') setFindSeq((seq) => seq + 1)
  }

  const tabActions = (path: string): MenuAction[] => {
    const order = tabs.map((t) => t.path)
    const at = order.indexOf(path)
    return [
      { label: 'Close', run: () => close([path]) },
      { label: 'Close others', run: () => close(order.filter((p) => p !== path)) },
      { label: 'Close to the right', run: () => close(order.slice(at + 1)) },
      { label: 'Close saved', run: () => close(tabs.filter((t) => !t.dirty).map((t) => t.path)) },
      { label: 'Copy path', run: () => void navigator.clipboard.writeText(path) },
      {
        label: 'Reveal in tree',
        run: () => {
          setSide('tree')
          revealPath(path)
        },
      },
    ]
  }

  const revealInOS = local
    ? (path: string) => {
        bridge.shell.reveal(hostId, path).catch((err: unknown) => store.fail(err))
      }
    : undefined

  return (
    <div className="workspace" ref={panel} tabIndex={-1}>
      <aside className="ws-side" hidden={side === null}>
        <div className="ws-side-head">
          <button className={side === 'tree' ? 'ws-view on' : 'ws-view'} title="Explorer (⇧⌘E)" onClick={() => pickSide('tree')}>
            Explorer
          </button>
          <button className={side === 'find' ? 'ws-view on' : 'ws-view'} title="Search (⇧⌘F)" onClick={() => pickSide('find')}>
            Search
          </button>
          <button
            className={side === 'changes' ? 'ws-view on' : 'ws-view'}
            title="What changed, and what this session wrote"
            onClick={() => pickSide('changes')}
          >
            Changes
          </button>
          <button
            className="icon-btn tiny"
            title="Go to file (⌘P)"
            aria-label="Go to file"
            onClick={() => setQuickOpen(true)}
          >
            <Search />
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

        {/* All three stay mounted and hide, so a search typed, a scroll
            position in the tree, or a folded group are where they were left
            on the way back (F5). */}
        <FileTree
          hostId={hostId}
          root={root}
          selected={activePath}
          reveal={reveal}
          expanded={expanded}
          onExpandedChange={setExpanded}
          onOpen={(path) => void openFile(path)}
          onPrefetch={prefetch}
          git={git}
          agent={agent}
          onRevealInOS={revealInOS}
          visible={side === 'tree'}
        />
        {findUsed && (
          <div className="find-host" hidden={side !== 'find'}>
            {/* Keyed by session: what one session searched for is not the
                next one's search. */}
            <FindInFiles
              key={memory}
              hostId={hostId}
              root={root}
              focusSeq={findSeq}
              worktrees={worktrees}
              onOpen={(path, line, column) => void openFile(path, { line, column, flash: true })}
              onPickRoot={setRootOverride}
            />
          </div>
        )}
        {side === 'changes' && (
          <ChangesView
            hostId={hostId}
            root={root}
            scope={scope}
            onPickScope={(next: Scope) => pickScope(next)}
            status={statusQuery.data}
            touched={touched}
            files={inScope}
            changes={range ? changesQuery.data : undefined}
            agent={agent}
            loading={range ? changesQuery.isLoading : statusQuery.isLoading && touchedQ.isLoading}
            onOpen={(path, line) => void openFile(path, line ? { line, column: 1 } : undefined, false, true, {})}
          />
        )}
      </aside>

      <section className="ws-main">
        <div className="ws-tabs" ref={tabStrip}>
          {/* The way back. Collapsing from inside the explorer would otherwise
              leave nothing on screen saying it is there, and a keyboard
              shortcut is not something you can see. */}
          <button
            className={`ws-side-toggle ${side === null ? 'off' : ''}`}
            title={side === null ? 'Show the explorer (⇧⌘E)' : 'Hide the explorer (⇧⌘E)'}
            aria-label={side === null ? 'Show the explorer' : 'Hide the explorer'}
            aria-expanded={side !== null}
            onClick={() => setSide(side === null ? lastSide.current : null)}
          >
            <Chevron dir={side === null ? 'right' : 'left'} />
          </button>
          {tabs.map((tab) => {
            const label = labels[tab.path] ?? { name: baseName(tab.path), suffix: '' }
            const hovered = hoveredTab === tab.path
            return (
              <div
                key={tab.path}
                className={`ws-tab ${tab.path === activePath ? 'active' : ''} ${tab.dirty ? 'dirty' : ''}`}
                title={tab.path}
                onClick={() => setActivePath(tab.path)}
                onMouseEnter={() => setHoveredTab(tab.path)}
                onMouseLeave={() => setHoveredTab((current) => (current === tab.path ? null : current))}
                // Middle-click closes, as in every browser and editor. Down is
                // swallowed so the middle button does not start autoscroll.
                onMouseDown={(event) => {
                  if (event.button === 1) event.preventDefault()
                }}
                onAuxClick={(event) => {
                  if (event.button !== 1) return
                  event.preventDefault()
                  close([tab.path])
                }}
                onContextMenu={(event) => {
                  event.preventDefault()
                  setTabMenu({ x: event.clientX, y: event.clientY, path: tab.path })
                }}
              >
                <span className="ws-tab-name">{label.name}</span>
                {label.suffix && <span className="ws-tab-suffix">{label.suffix}</span>}
                <button
                  className="ws-tab-close"
                  title={tab.dirty ? 'Unsaved changes — close' : 'Close'}
                  aria-label={`Close ${label.name}`}
                  onClick={(event) => {
                    event.stopPropagation()
                    close([tab.path])
                  }}
                >
                  {/* The unsaved dot turns into the cross under the pointer, so
                      the close is where the reader expects it either way. */}
                  {tab.dirty && !hovered ? '●' : '✕'}
                </button>
              </div>
            )
          })}
        </div>

        {active ? (
          <ActiveFile
            key={active.path}
            tab={active}
            root={root}
            hostId={hostId}
            sessionId={sessionId}
            draft={drafts.current[active.path]}
            wrap={wrapFor(active.path)}
            fontSize={termSize}
            gitLetter={git.files.get(active.path)}
            gitRoot={statusQuery.data?.root ?? ''}
            scope={scope}
            scopeFile={inScope.get(active.path)}
            scopeKnown={scopeKnown}
            diffLayout={diffLayout}
            onDiffLayout={(next) => {
              setDiffLayout(next)
              writeDiffLayout(next)
            }}
            active={visible}
            onToggleWrap={toggleWrap}
            onChange={(doc, dirty) => {
              drafts.current[active.path] = doc
              setDirty(active.path, dirty)
            }}
            // The editor's own ⌘S lands here too, and a hidden panel's editor
            // can still hold the keyboard: the same rule as the window's (F2).
            onSave={() => {
              if (visibleRef.current) void save(active.path)
            }}
            onReload={() => void reload(active.path)}
            onDismissStale={() =>
              setTabs((current) =>
                current.map((t) => (t.path === active.path ? { ...t, staleOnDisk: false } : t)),
              )
            }
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
          openTabs={tabs.map((tab) => tab.path)}
          recent={recent}
          onOpen={(path, at) => void openFile(path, at)}
          onPrefetch={prefetch}
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

      {tabMenu && (
        <SelectionMenu x={tabMenu.x} y={tabMenu.y} actions={tabActions(tabMenu.path)} onClose={() => setTabMenu(null)} />
      )}

      {confirm && (
        <DiscardDialog
          names={tabs.filter((t) => confirm.includes(t.path) && t.dirty).map((t) => baseName(t.path))}
          onCancel={() => setConfirm(null)}
          onDiscard={() => {
            closeNow(confirm)
            setConfirm(null)
          }}
        />
      )}
    </div>
  )
}

/** Closing unsaved work asks first, in the app's own dialog rather than `confirm()`. */
function DiscardDialog({
  names,
  onCancel,
  onDiscard,
}: {
  names: string[]
  onCancel: () => void
  onDiscard: () => void
}): JSX.Element {
  return (
    <Modal title="Discard unsaved changes?" onClose={onCancel}>
      <p className="discard-text">
        {names.length === 1
          ? `${names[0]} has changes that are not saved. Closing it throws them away.`
          : `${names.join(', ')} have changes that are not saved. Closing them throws them away.`}
      </p>
      <div className="modal-actions">
        <button autoFocus onClick={onCancel}>
          Cancel
        </button>
        <button className="danger" onClick={onDiscard}>
          Discard
        </button>
      </div>
    </Modal>
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
 *
 * The header is drawn whatever state the read is in: the path and Reload are
 * what a reader reaches for exactly when the body is slow or has failed.
 */
function ActiveFile({
  tab,
  root,
  hostId,
  sessionId,
  draft,
  wrap,
  fontSize,
  gitLetter,
  gitRoot,
  onToggleWrap,
  onChange,
  onSave,
  onReload,
  onDismissStale,
  onMode,
  restore,
  onPositionChange,
  scope,
  scopeFile,
  scopeKnown,
  diffLayout,
  onDiffLayout,
  active,
}: {
  tab: OpenTab
  root: string
  hostId: string
  sessionId: string
  /** The unsaved buffer, when this file has one. */
  draft: Text | undefined
  wrap: boolean
  fontSize: number
  /** The file's git state, which decides whether there is a change bar to fetch. */
  gitLetter: string | undefined
  /** The repository the status came from; git names files relative to it. */
  gitRoot: string
  onToggleWrap: () => void
  onChange: (doc: Text, dirty: boolean) => void
  onSave: () => void
  onReload: () => void
  onDismissStale: () => void
  onMode: (mode: TabMode) => void
  restore: ReadingPosition | null
  onPositionChange: (at: ReadingPosition) => void
  /** The Changes mode's scope, which the Diff view shows the file in. */
  scope: Scope | null
  /** The file as the scope changed it, or undefined when it did not. */
  scopeFile: ScopeFile | undefined
  /** False while the scope's file list is still on its way. */
  scopeKnown: boolean
  diffLayout: DiffLayout
  onDiffLayout: (layout: DiffLayout) => void
  /** The panel is on screen, so the diff may take F7. */
  active: boolean
}): JSX.Element {
  const deleted = scopeFile?.letter === 'D'
  // A file the scope did not change has no diff to show, so a tab left on its
  // diff falls back to the file — once the scope has answered. A deleted file
  // has nothing but its diff.
  const mode: TabMode = deleted
    ? 'diff'
    : tab.mode === 'diff' && !scopeFile && scopeKnown
      ? defaultMode(tab.path)
      : tab.mode
  const modes: TabMode[] = deleted
    ? ['diff']
    : ['edit', ...(scopeFile ? (['diff'] as const) : []), ...(hasPreview(tab.path) ? (['preview'] as const) : [])]
  const { data, error, refetch } = useQuery({ ...fileContentQuery(hostId, tab.path), enabled: !deleted })

  // The change bar: the file's own patch against HEAD, asked for only when git
  // says the file has one. Refreshed with every other git read when a file
  // event invalidates them.
  const rel = gitRoot && tab.path.startsWith(`${gitRoot}/`) ? tab.path.slice(gitRoot.length + 1) : ''
  const patch = useQuery({
    ...gitDiffQuery(hostId, gitRoot, gitLetter && gitLetter !== 'D' ? rel : '', { untracked: gitLetter === 'U' }),
  })
  const changes = useMemo(() => (patch.data?.diff ? gutterMarks(patch.data.diff) : null), [patch.data])

  if (mode === 'diff') {
    return (
      <div className="ws-editor">
        <header className="ws-file-head">
          <PathLabel path={relativeTo(root, tab.path)} className="preview-path" />
          {deleted && <span className="pill">deleted</span>}
          <span className="grow" />
          <ViewToggle modes={modes} mode={mode} onMode={onMode} />
          <div className="seg-toggle" role="group" aria-label="Diff layout">
            {(['split', 'unified'] as const).map((layout) => (
              <button
                key={layout}
                className={diffLayout === layout ? 'on' : ''}
                aria-pressed={diffLayout === layout}
                onClick={() => onDiffLayout(layout)}
              >
                {layout === 'split' ? 'Split' : 'Unified'}
              </button>
            ))}
          </div>
          <HeadButton title="Copy path" onClick={() => void navigator.clipboard.writeText(tab.path)}>
            <Copy />
          </HeadButton>
        </header>
        {scope && scopeFile ? (
          <FileDiff
            hostId={hostId}
            cwd={root}
            path={tab.path}
            rel={scopeFile.rel}
            at={scopeDiffAt(scope, scopeFile.letter)}
            layout={diffLayout}
            line={tab.diffLine ?? undefined}
            sessionId={sessionId}
            active={active}
          />
        ) : (
          <div className="tree-loading">
            <span className="spinner tiny" />
          </div>
        )}
      </div>
    )
  }

  if (!data) {
    return (
      <div className="ws-editor">
        <header className="ws-file-head">
          <PathLabel path={relativeTo(root, tab.path)} className="preview-path" />
          <span className="grow" />
          <ViewToggle modes={modes} mode={mode} onMode={onMode} />
          <HeadButton title="Reload from disk" onClick={() => void refetch()}>
            <Reload />
          </HeadButton>
          <HeadButton title="Copy path" onClick={() => void navigator.clipboard.writeText(tab.path)}>
            <Copy />
          </HeadButton>
        </header>
        {error ? (
          <div className="ws-error" role="alert">
            <p>{describeFileError(error)}</p>
            <button className="tiny" onClick={() => void refetch()}>
              Retry
            </button>
          </div>
        ) : (
          <Skeleton />
        )}
      </div>
    )
  }

  const file: OpenFile = {
    ...tab,
    mode,
    saved: data.content,
    modTime: data.mod_time,
    kind: kindOf(tab.path),
    base64: data.encoding === 'base64',
    bytes: data.bytes,
    ...shapeOf(data),
  }
  return (
    <FileView
      file={file}
      root={root}
      hostId={hostId}
      sessionId={sessionId}
      draft={draft}
      wrap={wrap}
      fontSize={fontSize}
      changes={changes}
      onToggleWrap={onToggleWrap}
      onChange={onChange}
      onSave={onSave}
      onReload={onReload}
      onDismissStale={onDismissStale}
      onMode={onMode}
      modes={modes}
      restore={restore}
      onPositionChange={onPositionChange}
    />
  )
}

/** Markdown and HTML have a rendered view beside their source. */
function hasPreview(path: string): boolean {
  return isMarkdownPath(path) || kindOf(path) === 'html'
}

const MODE_LABELS: Record<TabMode, { label: string; title: string }> = {
  edit: { label: 'Source', title: 'The file itself' },
  diff: { label: 'Diff', title: 'What changed, in the Changes scope (F7 for the next change)' },
  preview: { label: 'Preview', title: 'Rendered' },
}

/**
 * Source, Diff and Preview as one labelled control (spec 65, B8). Which view is
 * showing is read off the control, not remembered. Absent when there is only
 * one view to have.
 */
function ViewToggle({
  modes,
  mode,
  onMode,
}: {
  modes: TabMode[]
  mode: TabMode
  onMode: (mode: TabMode) => void
}): JSX.Element | null {
  if (modes.length < 2) return null
  return (
    <div className="seg-toggle" role="group" aria-label="View">
      {modes.map((each) => (
        <button
          key={each}
          className={mode === each ? 'on' : ''}
          aria-pressed={mode === each}
          title={MODE_LABELS[each].title}
          onClick={() => onMode(each)}
        >
          {MODE_LABELS[each].label}
        </button>
      ))}
    </div>
  )
}

/** About twenty grey lines where the text will be. Rarely seen, with the prefetch. */
function Skeleton(): JSX.Element {
  return (
    <div className="ws-skeleton" aria-busy="true" aria-label="Loading">
      {SKELETON_WIDTHS.map((width, index) => (
        <span key={index} className="skeleton-line" style={{ width: `${width}%` }} />
      ))}
    </div>
  )
}

const SKELETON_WIDTHS = [38, 62, 54, 0, 71, 45, 66, 29, 0, 58, 74, 49, 33, 0, 64, 52, 41, 69, 36, 57]

/** One of the file header's icon buttons, labelled for a tooltip and a reader. */
function HeadButton({
  title,
  onClick,
  pressed,
  children,
}: {
  title: string
  onClick: () => void
  pressed?: boolean
  children: React.ReactNode
}): JSX.Element {
  return (
    <button
      className={`icon-btn tiny head-btn ${pressed ? 'on' : ''}`}
      title={title}
      aria-label={title}
      aria-pressed={pressed}
      onClick={onClick}
    >
      {children}
    </button>
  )
}

function FileView({
  file,
  root,
  hostId,
  sessionId,
  draft,
  wrap,
  fontSize,
  changes,
  onToggleWrap,
  onChange,
  onSave,
  onReload,
  onDismissStale,
  onMode,
  modes,
  restore,
  onPositionChange,
}: {
  file: OpenFile
  root: string
  hostId: string
  sessionId: string
  draft: Text | undefined
  wrap: boolean
  fontSize: number
  changes: ReturnType<typeof gutterMarks> | null
  onToggleWrap: () => void
  onChange: (doc: Text, dirty: boolean) => void
  onSave: () => void
  onReload: () => void
  /** Keep the buffer and put the bar away. The save still 409s, which is right:
   *  the other writer's change is still on disk and the user chose theirs. */
  onDismissStale: () => void
  onMode: (mode: TabMode) => void
  /** The views this file offers. */
  modes: TabMode[]
  /** Where this file was last left, or null if it has not been opened before. */
  restore: ReadingPosition | null
  onPositionChange: (at: ReadingPosition) => void
}): JSX.Element {
  // The buffer as text. Built here, on a render, rather than per keystroke:
  // this component renders when the unsaved marker flips or the mode changes,
  // not while somebody types.
  const text = useMemo(() => draft?.toString() ?? file.saved, [draft, file.saved])
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
  useMermaid(preview, blocks)
  useFenceHighlight(preview, blocks)
  const [width, setWidth] = useState(readReadingWidth)
  /** An image's pixel size, once it has loaded, and whether to show it at that. */
  const [size, setSize] = useState<{ width: number; height: number } | null>(null)
  const [natural, setNatural] = useState(false)
  const [diskDiff, setDiskDiff] = useState<string | null>(null)
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
   * Raw bytes are the ordinary case now, and become a Blob URL with no base64
   * in between. Base64 is what an older daemon sends. An SVG arrives as text,
   * because it is text, and is encoded here. A binary image with none of these
   * — a daemon older than the encoding parameter — has already lost its bytes
   * to the JSON encoder, so there is nothing to show and it falls through to
   * the note that says so.
   */
  const source = useMemo(() => {
    if (file.kind !== 'image') return null
    if (file.bytes) return URL.createObjectURL(new Blob([file.bytes], { type: mimeForPath(file.path) }))
    if (file.base64) return dataUrl(file.path, file.saved)
    if (extensionOf(file.path) !== 'svg') return null
    return `data:image/svg+xml;base64,${bytesToBase64(text)}`
  }, [file.kind, file.bytes, file.base64, file.path, file.saved, text])
  useEffect(
    () => () => {
      if (source?.startsWith('blob:')) URL.revokeObjectURL(source)
    },
    [source],
  )

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

  /** The disk copy against the buffer, for a reader deciding which to keep. */
  const showDiskDiff = async (): Promise<void> => {
    try {
      // Around the cache, for the same reason as the panel's sync: the cached
      // entry is what the unsaved marker is decided against.
      const disk = await readContent(hostId, file.path)
      setDiskDiff(fileDiff(text, disk.content))
    } catch (err) {
      store.notify(describeFileError(err), 'error')
    }
  }

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

  const editable = !source && !renderedHtml && !file.binary && blocks === null

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
        <ViewToggle modes={modes} mode={file.mode} onMode={onMode} />
        {editable && (
          <HeadButton title={wrap ? 'Stop wrapping long lines (⌥Z)' : 'Wrap long lines (⌥Z)'} pressed={wrap} onClick={onToggleWrap}>
            <Wrap />
          </HeadButton>
        )}
        <HeadButton title="Reload from disk" onClick={onReload}>
          <Reload />
        </HeadButton>
        {!source && (
          <HeadButton title="Copy contents" onClick={() => void navigator.clipboard.writeText(text)}>
            <Copy />
          </HeadButton>
        )}
        {!file.readOnly && (
          <button className="filled tiny" disabled={!file.dirty} onClick={onSave}>
            Save
          </button>
        )}
      </header>

      {/* The file moved under an unsaved buffer. Nothing has been touched: the
          reader decides whether to take what is on disk or keep what they
          typed. No merge, and no dialog in the way of either answer. */}
      {file.staleOnDisk && (
        <div className="ws-stale-bar" role="status">
          <span>Changed on disk. Your unsaved edits are still here.</span>
          <span className="grow" />
          <button className="tiny" onClick={() => void showDiskDiff()}>
            Show diff
          </button>
          <button className="filled tiny" onClick={onReload}>
            Reload
          </button>
          <button className="icon-btn tiny" title="Keep my version" aria-label="Keep my version" onClick={onDismissStale}>
            <Close />
          </button>
        </div>
      )}

      {source ? (
        <div className={`ws-image ${natural ? 'natural' : ''}`}>
          <img
            src={source}
            alt={baseName(file.path)}
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
      ) : (
        // Past the edit limit the same editor reads the file, read-only: line
        // numbers and search still work, which a <pre> never had.
        <CodeEditor
          key={`${file.path}:${file.version}`}
          path={file.path}
          doc={draft ?? file.saved}
          saved={file.saved}
          readOnly={file.readOnly}
          wrap={wrap}
          changes={changes}
          flash={file.flash}
          fontSize={fontSize}
          onChange={onChange}
          onSave={onSave}
          cursor={file.cursor}
          autoFocus={!file.quiet}
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

      {diskDiff !== null && (
        <Modal title={`${baseName(file.path)}: your buffer → on disk`} onClose={() => setDiskDiff(null)}>
          <div className="disk-diff">
            <DiffView
              diff={diskDiff}
              empty="The disk copy matches your buffer."
              language={languageForPath(file.path)}
              layout="unified"
            />
          </div>
        </Modal>
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

/** Which list the explorer shows, or nothing at all. */
type Side = 'tree' | 'find' | 'changes' | null

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

/** A most-recent-first list of paths, kept per session. */
function readList(key: string): string[] {
  try {
    const raw = JSON.parse(localStorage.getItem(key) ?? '[]') as unknown
    return Array.isArray(raw) ? raw.filter((item): item is string => typeof item === 'string') : []
  } catch {
    return []
  }
}

function writeList(key: string, list: string[]): void {
  try {
    localStorage.setItem(key, JSON.stringify(list))
  } catch {
    // A full or unavailable store costs the list, not the panel.
  }
}

/**
 * Word wrap, as chosen with ⌥Z — per host, because a host is where a person's
 * habits about one codebase live. Null until chosen, which means each file's
 * own default.
 */
function readWrap(hostId: string): boolean | null {
  try {
    const saved = localStorage.getItem(`helios.files.wrap.${hostId}`)
    return saved === null ? null : saved === 'on'
  } catch {
    return null
  }
}

function writeWrap(hostId: string, wrap: boolean): void {
  try {
    localStorage.setItem(`helios.files.wrap.${hostId}`, wrap ? 'on' : 'off')
  } catch {
    // As above: the preference, not the panel.
  }
}

/** Split or unified, as last chosen: a reading preference, so one for every file. */
const DIFF_LAYOUT_KEY = 'helios.files.diff-layout'

function readDiffLayout(): DiffLayout {
  try {
    return localStorage.getItem(DIFF_LAYOUT_KEY) === 'unified' ? 'unified' : 'split'
  } catch {
    return 'split'
  }
}

function writeDiffLayout(layout: DiffLayout): void {
  try {
    localStorage.setItem(DIFF_LAYOUT_KEY, layout)
  } catch {
    // The preference, not the panel.
  }
}

/** Prose wraps until told otherwise; code scrolls sideways. */
function wrapsByDefault(path: string): boolean {
  if (isMarkdownPath(path)) return true
  const language = languageForPath(path)
  return language === null || language === 'plaintext'
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
