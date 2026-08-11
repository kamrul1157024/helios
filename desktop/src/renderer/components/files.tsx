import { useEffect, useMemo, useState } from 'react'

import { api, statusOf } from '../bridge.ts'
import { highlightCode, languageForPath } from '../markdown.ts'
import { store, useStore } from '../store.ts'
import type { FileEntry } from '../../shared/models.ts'

export function FilesPanel({ hostId, cwd }: { hostId: string; cwd: string }): JSX.Element {
  const [path, setPath] = useState(cwd)
  const [entries, setEntries] = useState<FileEntry[]>([])
  const [error, setError] = useState<string | null>(null)
  const [preview, setPreview] = useState<{ path: string; content: string } | null>(null)
  const target = useStore((s) => s.fileTarget)

  useEffect(() => setPath(cwd), [cwd])

  useEffect(() => {
    let cancelled = false
    setError(null)
    api(hostId)
      .listFiles(path)
      .then((result) => {
        if (!cancelled) setEntries(result.entries)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      })
    return () => {
      cancelled = true
    }
  }, [hostId, path])

  const show = async (filePath: string): Promise<void> => {
    try {
      const file = await api(hostId).readFile(filePath)
      setPreview({ path: filePath, content: file.content })
    } catch (err) {
      // The daemon refuses anything over 10 MB rather than stream it, and a
      // preview pane is not the place to argue with that.
      if (statusOf(err) === 413) store.notify('File is too large to preview', 'error')
      else if (statusOf(err) === 404) store.notify(`Not found: ${filePath}`, 'error')
      else store.fail(err)
    }
  }

  // A chip in the transcript opens the file here: list the directory it lives
  // in, so the surrounding files are one click away, and preview the file.
  useEffect(() => {
    if (!target || target.hostId !== hostId) return
    setPath(dirname(target.path))
    void show(target.path)
    store.clearFileTarget()
    // seq, not path: reopening the same file has to work.
  }, [target?.seq])

  const open = async (entry: FileEntry): Promise<void> => {
    if (entry.is_dir) {
      setPath(entry.path)
      setPreview(null)
      return
    }
    await show(entry.path)
  }

  const parent = dirname(path)

  return (
    <div className="files">
      <header className="files-head">
        <button className="link" disabled={path === '/'} onClick={() => setPath(parent)}>
          ↑
        </button>
        <input
          className="path"
          value={path}
          onChange={(event) => setPath(event.target.value)}
          spellCheck={false}
        />
      </header>

      {error && <p className="empty-note">{error}</p>}

      <div className="files-body">
        <div className="file-list">
          {entries.map((entry) => (
            <button
              key={entry.path}
              className={`file-row ${preview?.path === entry.path ? 'selected' : ''}`}
              onDoubleClick={() => void open(entry)}
              onClick={() => void open(entry)}
            >
              <span className="file-icon">{entry.is_dir ? '▸' : '·'}</span>
              <span className="file-name">{entry.name}</span>
              {!entry.is_dir && <span className="file-size">{humanSize(entry.size)}</span>}
            </button>
          ))}
          {entries.length === 0 && !error && <p className="empty-note">Empty directory.</p>}
        </div>

        {preview && <Preview path={preview.path} content={preview.content} onClose={() => setPreview(null)} />}
      </div>
    </div>
  )
}

function Preview({
  path,
  content,
  onClose,
}: {
  path: string
  content: string
  onClose: () => void
}): JSX.Element {
  // Highlighting a very large file costs more than it is worth, and the daemon
  // will happily hand over a 5 MB log.
  const html = useMemo(
    () => (content.length > 400_000 ? null : highlightCode(content, languageForPath(path))),
    [path, content],
  )
  return (
    <div className="file-preview">
      <header>
        <span className="preview-path" title={path}>
          {path}
        </span>
        <button className="icon-btn tiny" title="Copy contents" onClick={() => void navigator.clipboard.writeText(content)}>
          ⧉
        </button>
        <button className="icon-btn tiny" title="Close preview" onClick={onClose}>
          ✕
        </button>
      </header>
      {html === null ? (
        <pre>{content}</pre>
      ) : (
        <pre className="hljs" dangerouslySetInnerHTML={{ __html: html }} />
      )}
    </div>
  )
}

/** The containing directory, with `/` as its own parent. */
function dirname(path: string): string {
  return path.replace(/\/+$/, '').replace(/\/[^/]*$/, '') || '/'
}

function humanSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`
}
