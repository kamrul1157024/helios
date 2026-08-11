import { useCallback, useEffect, useState } from 'react'

import { api } from '../bridge.ts'
import type { FileEntry } from '../../shared/models.ts'

interface Props {
  hostId: string
  root: string
  /** The open file, highlighted in the tree. */
  selected: string | null
  /** A file to scroll to and expand the ancestors of, e.g. from quick open. */
  reveal: string | null
  onOpen: (path: string) => void
}

/**
 * A lazily loaded directory tree.
 *
 * Directories are listed the first time they are expanded and then kept — an
 * agent's repository is big enough that reading it all up front would stall the
 * panel, and small enough that holding what was opened costs nothing.
 */
export function FileTree({ hostId, root, selected, reveal, onOpen }: Props): JSX.Element {
  const [children, setChildren] = useState<Record<string, FileEntry[]>>({})
  const [expanded, setExpanded] = useState<Set<string>>(new Set([root]))
  const [busy, setBusy] = useState<Set<string>>(new Set())
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(
    async (dir: string): Promise<FileEntry[]> => {
      setBusy((current) => new Set(current).add(dir))
      try {
        const result = await api(hostId).listFiles(dir)
        setChildren((current) => ({ ...current, [dir]: result.entries }))
        setError(null)
        return result.entries
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err))
        return []
      } finally {
        setBusy((current) => {
          const next = new Set(current)
          next.delete(dir)
          return next
        })
      }
    },
    [hostId],
  )

  // A new root is a different session's directory: start over.
  useEffect(() => {
    setChildren({})
    setExpanded(new Set([root]))
    void load(root)
  }, [hostId, root, load])

  const toggle = (dir: string): void => {
    setExpanded((current) => {
      const next = new Set(current)
      if (next.has(dir)) next.delete(dir)
      else {
        next.add(dir)
        if (!children[dir]) void load(dir)
      }
      return next
    })
  }

  // Opening a file from quick open or the transcript should show where it
  // lives, which means listing every directory between it and the root.
  useEffect(() => {
    if (!reveal || !reveal.startsWith(root)) return
    let cancelled = false
    const expand = async (): Promise<void> => {
      const parts = reveal.slice(root.length).split('/').filter(Boolean)
      parts.pop()
      let dir = root
      for (const part of parts) {
        dir = `${dir}/${part}`
        if (cancelled) return
        setExpanded((current) => new Set(current).add(dir))
        if (!children[dir]) await load(dir)
      }
    }
    void expand()
    return () => {
      cancelled = true
    }
  }, [reveal, root, load])

  const rows: JSX.Element[] = []
  const push = (dir: string, depth: number): void => {
    for (const entry of children[dir] ?? []) {
      const open = expanded.has(entry.path)
      rows.push(
        <button
          key={entry.path}
          className={`tree-row ${selected === entry.path ? 'selected' : ''}`}
          style={{ paddingLeft: 8 + depth * 12 }}
          title={entry.name}
          onClick={() => (entry.is_dir ? toggle(entry.path) : onOpen(entry.path))}
        >
          <span className="tree-twist">{entry.is_dir ? (open ? '▾' : '▸') : ''}</span>
          <span className={`tree-name ${entry.is_dir ? 'dir' : ''}`}>{entry.name}</span>
          {busy.has(entry.path) && <span className="tree-busy">…</span>}
        </button>,
      )
      if (entry.is_dir && open) push(entry.path, depth + 1)
    }
  }
  push(root, 0)

  return (
    <div className="tree">
      {error && <p className="empty-note">{error}</p>}
      {rows}
      {rows.length === 0 && !error && !busy.has(root) && <p className="empty-note">Empty directory.</p>}
    </div>
  )
}
