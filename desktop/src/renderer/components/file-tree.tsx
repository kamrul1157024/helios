import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { useQueries, type UseQueryResult } from '@tanstack/react-query'

import { iconKindOf } from '../filetype.ts'
import { dirQuery } from '../queries.ts'
import { timeAgo, type FileEntry } from '../../shared/models.ts'
import { describeFileError } from './file-errors.ts'
import { Chevron, FileIcon, FolderOpen, Folder } from './icons.tsx'
import { useHoverPrefetch } from './prefetch.ts'
import { SelectionMenu, type MenuAction } from './selection-menu.tsx'
import {
  GIT_LETTER_TITLES,
  flattenTree,
  typeAhead,
  type AgentMarks,
  type DirState,
  type GitMarks,
  type TreeRow,
} from './tree-rows.ts'
import { useScrollWindow } from './windowed.tsx'

interface Props {
  hostId: string
  root: string
  /** The open file, highlighted in the tree. */
  selected: string | null
  /**
   * A file to scroll to and expand the ancestors of, e.g. from quick open. The
   * seq asks again for a path already revealed once.
   */
  reveal: { path: string; seq: number } | null
  /**
   * Directories to show open. Owned by the panel because it is remembered per
   * session, and a root is not a session: two sessions in one repository share
   * one.
   */
  expanded: Set<string>
  onExpandedChange: (next: Set<string>) => void
  onOpen: (path: string) => void
  /** Called once the pointer has rested on a file, to fetch it before the click. */
  onPrefetch?: (path: string) => void
  /** Git state for the rows, from the status read the Changes mode makes. */
  git: GitMarks
  /** What this session's agent wrote. */
  agent: AgentMarks
  /** Shows a path in the system file manager. Absent for a remote host. */
  onRevealInOS?: (path: string) => void
  /** False while another view of the explorer is in front. It stays mounted. */
  visible?: boolean
}

/** Every row is this tall, which is what lets the list draw only what shows. */
export const TREE_ROW_HEIGHT = 22

/** Rows drawn beyond each edge of the view, so a quick scroll finds them ready. */
const OVERSCAN = 20

/** How long typed letters keep adding to one jump before starting a new one. */
const TYPE_AHEAD_MS = 700

const IGNORED_TITLE = 'Ignored by .gitignore. Search skips it.'

/**
 * A lazily loaded directory tree.
 *
 * Directories are listed the first time they are expanded and then kept — an
 * agent's repository is big enough that reading it all up front would stall the
 * panel, and small enough that holding what was opened costs nothing.
 *
 * The open folders are flattened into one list and only the rows near the view
 * are drawn (spec 65, 2.5), so a folder of ten thousand files costs what a
 * screenful does. It is one focus stop with a moving active row, as a tree in
 * any editor is.
 */
export function FileTree({
  hostId,
  root,
  selected,
  reveal,
  expanded,
  onExpandedChange,
  onOpen,
  onPrefetch,
  git,
  agent,
  onRevealInOS,
  visible = true,
}: Props): JSX.Element {
  // Read by the reveal effect, which adds to what is already open without
  // wanting to re-run every time that changes.
  const expandedRef = useRef(expanded)
  expandedRef.current = expanded
  const scroller = useRef<HTMLDivElement | null>(null)
  const hover = useHoverPrefetch(onPrefetch)
  const [cursor, setCursor] = useState<string | null>(null)
  const [menu, setMenu] = useState<{ x: number; y: number; entry: FileEntry } | null>(null)
  const typed = useRef({ text: '', at: 0 })
  // Row ids for aria-activedescendant, unique per tree: every session's panel
  // stays mounted, so two trees share the document.
  const idBase = useId()
  const rowId = (index: number): string => `${idBase}-row-${index}`

  /**
   * Every directory that has to be listed: the root, and whatever is open
   * under it. Wherever the instruction came from — a restore for this session,
   * a reveal, or a click — it lands here, and one query per directory is what
   * keeps a slow one from being asked for twice.
   */
  const dirs = useMemo(() => {
    const wanted = new Set<string>([root])
    for (const dir of expanded) if (dir.startsWith(root)) wanted.add(dir)
    return [...wanted]
  }, [root, expanded])

  // Combined so the result is stable while no listing changed: a fresh object
  // every render would flatten the whole tree again for every hover. The
  // combiner is memoised on the folder list for the same reason — a new one is
  // what makes React Query run it again.
  const combine = useCallback(
    (results: UseQueryResult<{ path: string; entries: FileEntry[] }>[]) => {
      const children: Record<string, FileEntry[]> = {}
      const states: Record<string, DirState> = {}
      const retry: Record<string, () => void> = {}
      results.forEach((result, index) => {
        const dir = dirs[index] as string
        if (result.data) children[dir] = result.data.entries
        // "…" for a folder with nothing to show yet, never for a refetch behind
        // rows already on screen: an agent writing a file is not news here.
        states[dir] = {
          loading: result.isLoading,
          ...(result.error && !result.data ? { error: describeFileError(result.error) } : {}),
        }
        retry[dir] = () => void result.refetch()
      })
      return { children, states, retry }
    },
    [dirs],
  )
  const listed = useQueries({ queries: dirs.map((dir) => dirQuery(hostId, dir)), combine })
  const { children, states, retry } = listed

  const rows = useMemo(() => flattenTree(root, children, expanded, states), [root, children, expanded, states])
  const indexOf = useMemo(() => {
    const map = new Map<string, number>()
    rows.forEach((row, index) => {
      if (row.kind === 'entry') map.set(row.entry.path, index)
    })
    return map
  }, [rows])

  const { slice, scrollToIndex } = useScrollWindow(scroller, rows.length, TREE_ROW_HEIGHT, OVERSCAN)

  const toggle = (dir: string, open?: boolean): void => {
    const next = new Set(expanded)
    const opening = open ?? !next.has(dir)
    if (opening === next.has(dir)) return
    if (opening) next.add(dir)
    else next.delete(dir)
    onExpandedChange(next)
  }

  // Opening a file from quick open or the transcript should show where it
  // lives, which means listing every directory between it and the root.
  const revealPath = reveal?.path ?? null
  useEffect(() => {
    if (!revealPath || !revealPath.startsWith(root)) return
    const parts = revealPath.slice(root.length).split('/').filter(Boolean)
    parts.pop()
    const next = new Set(expandedRef.current)
    let dir = root
    for (const part of parts) {
      dir = `${dir}/${part}`
      next.add(dir)
    }
    onExpandedChange(next)
  }, [revealPath, reveal?.seq, root, onExpandedChange])

  // And then the middle of the view, once the listings have put its row there.
  // Once per reveal: the listings refetch, and dragging the reader back to the
  // revealed file every time they did would make scrolling away impossible.
  const revealed = useRef<number | null>(null)
  useEffect(() => {
    // A hidden list has no height to centre in. The reveal waits until it is
    // shown, which is why `visible` is a dependency.
    if (!reveal || !visible || revealed.current === reveal.seq) return
    const index = indexOf.get(reveal.path)
    if (index === undefined) return
    revealed.current = reveal.seq
    setCursor(reveal.path)
    scrollToIndex(index, 'center')
  }, [reveal, visible, indexOf, scrollToIndex])

  const active = cursor !== null && indexOf.has(cursor) ? cursor : selected
  const activeIndex = active !== null ? (indexOf.get(active) ?? -1) : -1

  const moveTo = (index: number): void => {
    const row = rows[Math.max(0, Math.min(index, rows.length - 1))]
    if (row?.kind !== 'entry') return
    setCursor(row.entry.path)
    scrollToIndex(indexOf.get(row.entry.path) ?? 0, 'nearest')
  }

  const onKey = (event: React.KeyboardEvent): void => {
    if (event.metaKey || event.ctrlKey || event.altKey) return
    const row = rows[activeIndex]
    const entry = row?.kind === 'entry' ? row.entry : null
    switch (event.key) {
      case 'ArrowDown':
        moveTo(activeIndex + 1)
        break
      case 'ArrowUp':
        moveTo(Math.max(activeIndex - 1, 0))
        break
      case 'Home':
        moveTo(0)
        break
      case 'End':
        moveTo(rows.length - 1)
        break
      case 'ArrowRight':
        if (!entry?.is_dir) return
        if (!expanded.has(entry.path)) toggle(entry.path, true)
        else moveTo(activeIndex + 1)
        break
      case 'ArrowLeft': {
        if (entry?.is_dir && expanded.has(entry.path)) {
          toggle(entry.path, false)
          break
        }
        const parent = entry ? entry.path.slice(0, entry.path.lastIndexOf('/')) : ''
        const at = indexOf.get(parent)
        if (at !== undefined) moveTo(at)
        break
      }
      case 'Enter':
        if (!entry) return
        if (entry.is_dir) toggle(entry.path)
        else onOpen(entry.path)
        break
      default: {
        if (event.key.length !== 1 || event.key === ' ') return
        const now = Date.now()
        const text = now - typed.current.at > TYPE_AHEAD_MS ? event.key : typed.current.text + event.key
        typed.current = { text, at: now }
        moveTo(typeAhead(rows, Math.max(activeIndex, 0), text))
      }
    }
    event.preventDefault()
  }

  const menuActions = (entry: FileEntry): MenuAction[] => {
    const relative = entry.path.startsWith(`${root}/`) ? entry.path.slice(root.length + 1) : entry.path
    return [
      ...(entry.is_dir ? [] : [{ label: 'Open', run: () => onOpen(entry.path) }]),
      { label: 'Copy path', run: () => void navigator.clipboard.writeText(entry.path) },
      { label: 'Copy relative path', run: () => void navigator.clipboard.writeText(relative) },
      ...(onRevealInOS ? [{ label: 'Reveal in file manager', run: () => onRevealInOS(entry.path) }] : []),
    ]
  }

  const rootState = states[root]
  const shown = rows.slice(slice.start, slice.end)

  return (
    <div
      className="tree"
      hidden={!visible}
      ref={scroller}
      role="tree"
      aria-label="Files"
      tabIndex={0}
      aria-activedescendant={activeIndex >= 0 ? rowId(activeIndex) : undefined}
      onKeyDown={onKey}
      onMouseLeave={hover.leave}
    >
      {rootState?.loading && rows.length === 0 && (
        <div className="tree-loading">
          <span className="spinner tiny" />
        </div>
      )}
      <div style={{ height: slice.padTop }} />
      {shown.map((row, offset) => {
        const index = slice.start + offset
        return row.kind === 'error' ? (
          <TreeError key={`error:${row.dir}`} row={row} onRetry={retry[row.dir]} />
        ) : (
          <TreeEntry
            key={row.entry.path}
            id={rowId(index)}
            entry={row.entry}
            depth={row.depth}
            open={expanded.has(row.entry.path)}
            busy={Boolean(states[row.entry.path]?.loading)}
            selected={selected === row.entry.path}
            active={index === activeIndex}
            git={git}
            agent={agent}
            onClick={() => {
              setCursor(row.entry.path)
              if (row.entry.is_dir) toggle(row.entry.path)
              else onOpen(row.entry.path)
            }}
            onHover={() => !row.entry.is_dir && hover.enter(row.entry.path)}
            onMenu={(x, y) => {
              setCursor(row.entry.path)
              setMenu({ x, y, entry: row.entry })
            }}
          />
        )
      })}
      <div style={{ height: slice.padBottom }} />
      {rows.length === 0 && !rootState?.loading && !rootState?.error && (
        <p className="empty-note">Empty directory.</p>
      )}
      {menu && (
        <SelectionMenu x={menu.x} y={menu.y} actions={menuActions(menu.entry)} onClose={() => setMenu(null)} />
      )}
    </div>
  )
}

/** Where a row's indent guides and label start, per level of depth. */
const INDENT = 12

function TreeEntry({
  id,
  entry,
  depth,
  open,
  busy,
  selected,
  active,
  git,
  agent,
  onClick,
  onHover,
  onMenu,
}: {
  id: string
  entry: FileEntry
  depth: number
  open: boolean
  busy: boolean
  selected: boolean
  active: boolean
  git: GitMarks
  agent: AgentMarks
  onClick: () => void
  onHover: () => void
  onMenu: (x: number, y: number) => void
}): JSX.Element {
  const letter = entry.is_dir ? undefined : git.files.get(entry.path)
  const changedInside = entry.is_dir && git.dirs.has(entry.path)
  const touched = entry.is_dir ? undefined : agent.files.get(entry.path)
  const touchedInside = entry.is_dir && agent.dirs.has(entry.path)
  const classes = ['tree-row', selected ? 'selected' : '', active ? 'active' : '', entry.ignored ? 'ignored' : '']
  return (
    <div
      id={id}
      role="treeitem"
      aria-selected={selected}
      aria-expanded={entry.is_dir ? open : undefined}
      className={classes.filter(Boolean).join(' ')}
      style={{ paddingLeft: 8 + depth * INDENT }}
      title={entry.ignored ? `${entry.name}\n${IGNORED_TITLE}` : entry.name}
      onClick={onClick}
      onMouseEnter={onHover}
      onContextMenu={(event) => {
        event.preventDefault()
        onMenu(event.clientX, event.clientY)
      }}
    >
      {Array.from({ length: depth }, (_, level) => (
        <span key={level} className="tree-guide" style={{ left: 8 + level * INDENT + 6 }} />
      ))}
      <span className="tree-twist">{entry.is_dir && <Chevron open={open} />}</span>
      {entry.is_dir ? (
        open ? (
          <FolderOpen className="tree-icon folder" />
        ) : (
          <Folder className="tree-icon folder" />
        )
      ) : (
        <FileIcon kind={iconKindOf(entry.path)} className="tree-icon" />
      )}
      <span className={`tree-name ${entry.is_dir ? 'dir' : ''} ${letter ? `g-${letter}` : ''}`}>{entry.name}</span>
      {busy && <span className="tree-busy">…</span>}
      <span className="grow" />
      {touched && (
        <span className="agent-mark" title={`Edited by this session, ${timeAgo(touched.last_at)} ago`} />
      )}
      {touchedInside && <span className="agent-mark hollow" title="Holds files this session edited" />}
      {letter && (
        <span className={`tree-badge g-${letter}`} title={GIT_LETTER_TITLES[letter]}>
          {letter}
        </span>
      )}
      {changedInside && <span className="tree-dot" title="Holds uncommitted changes" />}
    </div>
  )
}

function TreeError({ row, onRetry }: { row: Extract<TreeRow, { kind: 'error' }>; onRetry?: () => void }): JSX.Element {
  return (
    <div className="tree-row tree-error" style={{ paddingLeft: 8 + row.depth * INDENT + 20 }} role="alert">
      <span className="tree-error-text" title={row.message}>
        {row.message}
      </span>
      {onRetry && (
        <button className="tree-retry" onClick={onRetry}>
          Retry
        </button>
      )}
    </div>
  )
}
