// What a failed file read says to the person who asked for it (spec 65, B1).
//
// In words, from the daemon's `code` when it sent one and from the status when
// it did not: a bare "request failed with 404" tells nobody whether the file
// moved, is locked, or is too big to show.

interface Failure {
  status?: number
  code?: string
  message?: string
}

export function describeFileError(err: unknown): string {
  const { status, code, message = '' } = (err ?? {}) as Failure
  if (code === 'not_found' || status === 404) return 'Not found'
  if (code === 'permission_denied' || status === 403) return 'Permission denied'
  if (code === 'too_large' || status === 413) return message.startsWith('Too large') ? message : 'Too large to open'
  if (code === 'is_directory') return 'This is a folder'
  if (code === 'bad_path' || status === 400) return message || 'Not a path the host will read'
  // No status at all means the request never reached a daemon.
  if (status === undefined) return 'Host unreachable'
  return message || `The host answered ${status}`
}
