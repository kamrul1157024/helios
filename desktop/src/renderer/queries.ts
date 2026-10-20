import { infiniteQueryOptions, queryOptions } from '@tanstack/react-query'

import { api } from './bridge.ts'
import { keys, type SettingsDocument } from './keys.ts'

export { appendDelta, transcriptMessages } from './keys.ts'
import { NEVER_STALE, queryClient } from './query-client.ts'
import { contentFromRaw } from './raw-file.ts'
import type { DiffAt, FileContent, GrepOpts, LogOpts } from '../shared/models.ts'

/**
 * One read per resource, as `queryOptions` rather than as hooks.
 *
 * A factory serves `useQuery` and `queryClient.fetchQuery` from the same
 * definition, and the files panel needs both: it validates a session's saved
 * tabs imperatively and then renders the active one reactively, and those must
 * land on one cache entry rather than fetch twice.
 *
 * The keys themselves live in `keys.ts`, which nothing in the bridge reaches.
 */

// ─── Sessions, groups, notifications ────────────────────────────────────────

/**
 * `grouped` rides the fetch because resolving a session's group costs the
 * daemon a lookup it should not do for a client that will not draw one. It is
 * part of the key for the same reason: the two answers differ.
 */
export function sessionsQuery(hostId: string, grouped: boolean) {
  return queryOptions({
    queryKey: keys.sessions(hostId, grouped),
    queryFn: () => api(hostId).listSessions(grouped ? { grouped: '1' } : {}),
  })
}

export function groupsQuery(hostId: string) {
  return queryOptions({
    queryKey: keys.groups(hostId),
    queryFn: () => api(hostId).listGroups(),
  })
}

export function notificationsQuery(hostId: string) {
  return queryOptions({
    queryKey: keys.notifications(hostId),
    queryFn: () => api(hostId).notifications({ status: 'pending' }),
  })
}

/**
 * One session, fetched by id.
 *
 * The list is not always enough to find it in: sessions a schedule started are
 * kept out of the ordinary list, so opening a run from its schedule would
 * otherwise select something the detail panel cannot see.
 */
export function sessionQuery(hostId: string, sessionId: string) {
  return queryOptions({
    queryKey: keys.session(hostId, sessionId),
    queryFn: async () => (await api(hostId).getSession(sessionId)).session,
    enabled: hostId !== '' && sessionId !== '',
  })
}

/**
 * The sessions a schedule started, on one host.
 *
 * A second list rather than a flag on the first: the ordinary list is what the
 * sidebar is for, and mixing forty automated runs into it is what the fold
 * exists to prevent. Asked for whether or not the section is open, because the
 * header carries the count.
 */
export function jobSessionsQuery(hostId: string) {
  return queryOptions({
    queryKey: keys.jobSessions(hostId),
    queryFn: async () => (await api(hostId).listSessions({ filter: 'jobs' })).sessions,
  })
}

// ─── Schedules ──────────────────────────────────────────────────────────────

export function schedulesQuery(hostId: string) {
  return queryOptions({
    queryKey: keys.schedules(hostId),
    queryFn: () => api(hostId).listSchedules(),
  })
}

/** Every channel on a host, with what this reader has not seen in each. */
export function channelsQuery(hostId: string) {
  return queryOptions({
    queryKey: keys.channels(hostId),
    queryFn: () => api(hostId).listChannels(),
  })
}

/**
 * One channel's conversation.
 *
 * Reading it marks it read on the daemon, which is what the unread count is
 * counted against — so opening a channel is what clears its badge.
 */
/** One thread, read when its panel is open. */
export function channelThreadQuery(hostId: string, channelId: string, root: string) {
  return queryOptions({
    queryKey: keys.channelThread(hostId, channelId, root),
    queryFn: () => api(hostId).channelThread(channelId, root),
    enabled: channelId !== '' && root !== '',
  })
}

export function channelMessagesQuery(hostId: string, channelId: string) {
  return queryOptions({
    queryKey: keys.channelMessages(hostId, channelId),
    queryFn: () => api(hostId).channelMessages(channelId),
    enabled: channelId !== '',
  })
}

/**
 * One schedule's runs.
 *
 * Ordinary sessions, asked for by what started them — which is why there is no
 * second list component anywhere: the runs list is the session list.
 */
export function scheduleRunsQuery(hostId: string, scheduleId: string) {
  return queryOptions({
    queryKey: keys.scheduleRuns(hostId, scheduleId),
    queryFn: async () => (await api(hostId).listSessions({ schedule_id: scheduleId })).sessions,
    enabled: scheduleId !== '',
  })
}

/**
 * The tail of a schedule's own log, refetched while the panel is open.
 *
 * Polled rather than streamed: there is no streaming log in the daemon, and a
 * check that runs every five minutes does not need sub-second delivery.
 */
export function scheduleLogQuery(hostId: string, scheduleId: string) {
  return queryOptions({
    queryKey: keys.scheduleLog(hostId, scheduleId),
    queryFn: () => api(hostId).scheduleLog(scheduleId),
    enabled: scheduleId !== '',
    refetchInterval: 2000,
  })
}

// ─── Settings ───────────────────────────────────────────────────────────────

/**
 * One query for a document three panes write to, none of which owns it: the
 * memory budget, the auto-titler, and the sidebar's sort mode.
 *
 * Never stale, because two of those panes hold a draft while a control is under
 * the pointer. A refetch landing mid-drag would move the thumb.
 */
export function settingsQuery(hostId: string) {
  return queryOptions({
    queryKey: keys.settings(hostId),
    queryFn: async (): Promise<SettingsDocument> => (await api(hostId).settings()) as SettingsDocument,
    staleTime: NEVER_STALE,
  })
}

// ─── Providers, models, directories ─────────────────────────────────────────

export function providersQuery(hostId: string) {
  return queryOptions({
    queryKey: keys.providers(hostId),
    queryFn: () => api(hostId).providers(),
    staleTime: NEVER_STALE,
  })
}

export function modelsQuery(hostId: string, provider: string) {
  return queryOptions({
    queryKey: keys.models(hostId, provider),
    queryFn: () => api(hostId).models(provider),
    enabled: provider !== '',
    staleTime: NEVER_STALE,
  })
}

export function directoriesQuery(hostId: string) {
  return queryOptions({
    queryKey: keys.directories(hostId),
    queryFn: () => api(hostId).listDirectories(),
  })
}

// ─── Git ────────────────────────────────────────────────────────────────────

export function gitStatusQuery(hostId: string, cwd: string) {
  return queryOptions({
    queryKey: keys.gitStatus(hostId, cwd),
    queryFn: () => api(hostId).gitStatus(cwd),
    enabled: cwd !== '',
  })
}

export function gitDiffQuery(hostId: string, cwd: string, file: string, at?: DiffAt) {
  return queryOptions({
    queryKey: keys.gitDiff(hostId, cwd, file, at),
    queryFn: () => api(hostId).gitDiff(cwd, file, at),
    enabled: cwd !== '' && file !== '',
  })
}

export function gitLogQuery(hostId: string, cwd: string, opts?: LogOpts) {
  return queryOptions({
    queryKey: keys.gitLog(hostId, cwd, opts),
    queryFn: () => api(hostId).gitLog(cwd, opts),
    enabled: cwd !== '',
  })
}

/**
 * The scope menu's log, a page at a time.
 *
 * `skip` is the running total rather than the page number, because that is what
 * the daemon takes and what "load more" means when a page comes back short.
 */
export function gitLogPagesQuery(hostId: string, cwd: string, all: boolean) {
  return infiniteQueryOptions({
    queryKey: keys.gitLogPages(hostId, cwd, all),
    queryFn: ({ pageParam }) =>
      api(hostId).gitLog(cwd, { all: all || undefined, limit: LOG_PAGE, skip: pageParam }),
    initialPageParam: 0,
    getNextPageParam: (last, pages) =>
      last.has_more ? pages.reduce((total, page) => total + page.commits.length, 0) : undefined,
    enabled: cwd !== '',
  })
}

export const LOG_PAGE = 50

/** How many transcript messages a page holds. */
export const TRANSCRIPT_PAGE = 50

/**
 * The transcript, paged backwards.
 *
 * `offset` counts back from the newest message, so page 0 is the tail of the
 * conversation and each further page is older. That is the opposite of what
 * `getNextPageParam` usually means, and it is why the rendered list reverses
 * the page order: within a page the messages are already chronological.
 *
 * The live edge does not come through here. `transcriptSince` appends to page 0
 * via setQueryData, driven by the session's own last_event_at — there is no
 * transcript event on the wire to subscribe to.
 */
export function transcriptQuery(hostId: string, sessionId: string) {
  return infiniteQueryOptions({
    queryKey: keys.transcript(hostId, sessionId),
    queryFn: ({ pageParam }) => api(hostId).transcript(sessionId, TRANSCRIPT_PAGE, pageParam),
    initialPageParam: 0,
    getNextPageParam: (last, pages) =>
      last.has_more ? pages.reduce((total, page) => total + page.messages.length, 0) : undefined,
    enabled: sessionId !== '',
    // The reader's place in a long transcript is worth more than freshness: the
    // delta keeps the tail current, and a background refetch would rebuild every
    // page under them.
    staleTime: NEVER_STALE,
  })
}

export function gitChangesQuery(hostId: string, cwd: string, to: string, from?: string, mergeBase = false) {
  return queryOptions({
    queryKey: keys.gitChanges(hostId, cwd, to, from, mergeBase),
    queryFn: () => api(hostId).gitChanges(cwd, to, from, mergeBase),
    enabled: cwd !== '' && to !== '',
  })
}

export function worktreesQuery(hostId: string, cwd: string) {
  return queryOptions({
    queryKey: keys.gitWorktrees(hostId, cwd),
    queryFn: () => api(hostId).gitWorktrees(cwd),
    enabled: cwd !== '',
  })
}

export function reviewedQuery(hostId: string, cwd: string, base: string) {
  return queryOptions({
    queryKey: keys.reviewed(hostId, cwd, base),
    queryFn: () => api(hostId).reviewedFiles(cwd, base),
    enabled: cwd !== '' && base !== '',
  })
}

// ─── Files ──────────────────────────────────────────────────────────────────

export function dirQuery(hostId: string, path: string) {
  return queryOptions({
    queryKey: keys.fileDir(hostId, path),
    queryFn: () => api(hostId).listFiles(path),
    enabled: path !== '',
  })
}

/**
 * Never stale, and deliberately so.
 *
 * The panel copies this into an editable buffer and compares against it to
 * decide whether the file is dirty. A background refetch under an edited buffer
 * would move the thing the comparison is against, marking a dirty file clean.
 * Freshness comes from the reload button and from returning to the panel, both
 * of which refetch explicitly and both of which stand down for a dirty buffer.
 *
 * Nor does mounting refetch. A file event marks the entry stale (spec 65, 2.1)
 * so that a closed file reads fresh when it is next opened — and opening goes
 * through `fetchQuery`, which honours that. Switching to a tab that is already
 * open remounts its view, and a refetch there would move `saved` under a dirty
 * buffer by the back door.
 *
 * The read is raw, and sends the ETag of what is held: an unchanged file is a
 * 304, which hands back the held entry untouched.
 */
export function fileContentQuery(hostId: string, path: string) {
  return queryOptions({
    queryKey: keys.fileContent(hostId, path),
    queryFn: () => readContent(hostId, path),
    enabled: path !== '',
    staleTime: NEVER_STALE,
    refetchOnMount: false,
  })
}

/** One raw read against what the cache holds for the path. */
export async function readContent(hostId: string, path: string): Promise<FileContent> {
  const held = queryClient.getQueryData<FileContent>(keys.fileContent(hostId, path))
  const raw = await api(hostId).readFileRaw(path, held?.etag)
  // A 304 against an entry that has since been dropped has nothing to stand
  // for, so it asks once more for the bytes themselves.
  if (raw.status === 304 && !held) return contentFromRaw(path, await api(hostId).readFileRaw(path))
  return contentFromRaw(path, raw, held)
}

/**
 * The files a session wrote, for the Changes view and the tree's marks.
 *
 * A daemon older than the route answers 404, which reads here as "nothing
 * known" rather than as an error: the view still has git to show.
 */
export function touchedQuery(hostId: string, sessionId: string) {
  return queryOptions({
    queryKey: keys.touched(hostId, sessionId),
    queryFn: async () => {
      try {
        return await api(hostId).touchedFiles(sessionId)
      } catch (err) {
        if ((err as { status?: number }).status === 404) return []
        throw err
      }
    },
    enabled: hostId !== '' && sessionId !== '',
  })
}

/**
 * A file a preview inlines: an image it points at, a stylesheet it links.
 *
 * Its own key, because these are blobs. `fileContent` is `NEVER_STALE` so that
 * a refetch cannot move the disk copy under an edited buffer, and that would
 * pin every asset a preview ever touched for the life of the session. Nothing
 * edits an asset, so it can be let go of.
 */
export function fileAssetQuery(hostId: string, path: string) {
  return queryOptions({
    queryKey: keys.fileAsset(hostId, path),
    queryFn: () => api(hostId).readFile(path),
    staleTime: 60_000,
    gcTime: 5 * 60_000,
  })
}

/** An empty query is not an idle one: ⌘P opens on the whole list. */
export function fileSearchQuery(hostId: string, root: string, q: string, limit = 50) {
  return queryOptions({
    queryKey: keys.fileSearch(hostId, root, q),
    queryFn: () => api(hostId).searchFiles(root, q, limit),
    enabled: root !== '',
  })
}

export function grepQuery(hostId: string, root: string, q: string, opts: GrepOpts) {
  return queryOptions({
    queryKey: keys.fileGrep(hostId, root, q, opts),
    queryFn: () => api(hostId).grepFiles(root, q, opts),
    enabled: root !== '' && q !== '',
  })
}
