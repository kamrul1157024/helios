/** A unified patch, coloured. Shared by the working-tree and commit views. */
export function DiffView({ diff, empty }: { diff: string; empty?: string }): JSX.Element {
  if (!diff.trim()) return <p className="empty-note">{empty ?? 'No changes.'}</p>
  return (
    <pre>
      {diff.split('\n').map((line, index) => (
        <span key={index} className={diffClass(line)}>
          {line || ' '}
          {'\n'}
        </span>
      ))}
    </pre>
  )
}

export function diffClass(line: string): string {
  if (line.startsWith('+++') || line.startsWith('---')) return 'd-meta'
  if (line.startsWith('@@')) return 'd-hunk'
  if (line.startsWith('+')) return 'd-add'
  if (line.startsWith('-')) return 'd-del'
  if (line.startsWith('diff --git') || line.startsWith('index ')) return 'd-meta'
  return ''
}
