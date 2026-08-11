/**
 * A path that gives up its directories before its filename.
 *
 * The CSS way to do this is `direction: rtl`, which reorders leading
 * punctuation — a dotfile comes out as "claude." — so the split is done here
 * instead: the directory half truncates, the name half never shrinks.
 */
export function PathLabel({ path, className }: { path: string; className?: string }): JSX.Element {
  const cut = path.lastIndexOf('/')
  const dir = cut < 0 ? '' : path.slice(0, cut + 1)
  const name = cut < 0 ? path : path.slice(cut + 1)
  return (
    <span className={`split-path ${className ?? ''}`} title={path}>
      {dir && <span className="split-dir">{dir}</span>}
      <span className="split-name">{name}</span>
    </span>
  )
}
