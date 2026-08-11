import { useEffect, useState } from 'react'

import { api } from '../bridge.ts'
import { store } from '../store.ts'
import { PathLabel } from './path-label.tsx'
import type { GitChange, GitDiff, GitStatus } from '../../shared/models.ts'

export function GitPanel({ hostId, cwd, revision }: { hostId: string; cwd: string; revision?: string }): JSX.Element {
  const [status, setStatus] = useState<GitStatus | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [selected, setSelected] = useState<string | null>(null)
  const [diff, setDiff] = useState<GitDiff | null>(null)

  useEffect(() => {
    let cancelled = false
    setError(null)
    api(hostId)
      .gitStatus(cwd)
      .then((result) => {
        if (!cancelled) setStatus(result)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      })
    return () => {
      cancelled = true
    }
    // Re-reads whenever the agent does something: a tool call is the usual way
    // the working tree changes here.
  }, [hostId, cwd, revision])

  useEffect(() => {
    if (!selected) {
      setDiff(null)
      return
    }
    let cancelled = false
    api(hostId)
      .gitDiff(cwd, selected)
      .then((result) => {
        if (!cancelled) setDiff(result)
      })
      .catch((err: unknown) => {
        if (!cancelled) store.fail(err)
      })
    return () => {
      cancelled = true
    }
  }, [hostId, cwd, selected])

  if (error) return <p className="empty-note">{error}</p>
  if (!status) return <p className="empty-note">Loading…</p>

  // An older daemon sends JSON null for an empty list rather than [].
  const groups: [string, GitChange[]][] = [
    ['Staged', status.staged ?? []],
    ['Changed', status.unstaged ?? []],
    ['Untracked', status.untracked ?? []],
  ]

  return (
    <div className="git">
      <header className="git-head">
        <span className="branch">{status.branch}</span>
        {status.ahead > 0 && <span className="pill">↑{status.ahead}</span>}
        {status.behind > 0 && <span className="pill">↓{status.behind}</span>}
        {!status.dirty && <span className="pill clean">clean</span>}
      </header>

      <div className="git-body">
        <div className="git-files">
          {groups.map(([name, files]) =>
            files.length === 0 ? null : (
              <div key={name} className="git-group">
                <span className="git-group-head">{name}</span>
                {files.map((file) => (
                  <button
                    key={`${name}:${file.path}`}
                    className={`git-file ${selected === file.path ? 'selected' : ''}`}
                    onClick={() => setSelected(file.path)}
                  >
                    <span className={`git-status s${file.status.trim() || 'x'}`}>{file.status.trim() || '?'}</span>
                    <PathLabel path={file.path} className="git-path" />
                  </button>
                ))}
              </div>
            ),
          )}
          {!status.dirty && <p className="empty-note">Working tree clean.</p>}
        </div>

        {diff && (
          <div className="git-diff">
            <header>
              <span>{diff.file}</span>
              <span className="muted">{diff.stat}</span>
            </header>
            <pre>
              {diff.diff.split('\n').map((line, index) => (
                <span key={index} className={diffClass(line)}>
                  {line || ' '}
                  {'\n'}
                </span>
              ))}
            </pre>
          </div>
        )}
      </div>
    </div>
  )
}

function diffClass(line: string): string {
  if (line.startsWith('+++') || line.startsWith('---')) return 'd-meta'
  if (line.startsWith('@@')) return 'd-hunk'
  if (line.startsWith('+')) return 'd-add'
  if (line.startsWith('-')) return 'd-del'
  return ''
}
