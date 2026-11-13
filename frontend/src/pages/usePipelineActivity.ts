import { useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { getRecentPipeline, type RecentPipeline } from '../api'
import { usePolling } from '../usePolling'
import { recentFlow, type RecentFlow } from './pipelineActivity'

export const ACTIVITY_INTERVAL_MS = 2000

export interface PipelineActivity extends RecentFlow {
  // Changes with every read, so a bin pulses at most once per read.
  sequence: number
  // The first read only sets the cursor, so its zero counts are not traffic.
  firstRead: boolean
}

type Load = (signal: AbortSignal) => Promise<RecentPipeline>

// Activity is null while motion is reduced, the tab is hidden, the last read
// failed, or no read has finished yet. Each time the tab becomes visible the
// cursor starts again from the newest purchase, so a backlog never plays.
export function usePipelineActivity(): PipelineActivity | null {
  const reducedMotion = useSyncExternalStore(subscribeReducedMotion, prefersReducedMotion)
  const visible = useSyncExternalStore(subscribeVisibility, isDocumentVisible)
  const load = useMemo(() => (visible && !reducedMotion ? recentReader() : null), [visible, reducedMotion])
  const [latest, setLatest] = useState<{ load: Load; activity: PipelineActivity | null } | null>(null)
  const reads = useRef(0)

  usePolling(load, {
    intervalMs: ACTIVITY_INTERVAL_MS,
    waitAfterCompletion: true,
    onSuccess: (recent) => {
      if (!load) return
      reads.current += 1
      setLatest({ load, activity: { ...recentFlow(recent), sequence: reads.current, firstRead: recent.after === null } })
    },
    onError: () => {
      if (load) setLatest({ load, activity: null })
    },
  })

  return latest !== null && latest.load === load ? latest.activity : null
}

// After a failed read the cursor starts again, so the decisions missed during an
// outage are not shown as if they arrived in the last interval.
function recentReader(): Load {
  let after: number | null = null
  return async (signal) => {
    try {
      const recent = await getRecentPipeline(after, signal)
      after = recent.next_after
      return recent
    } catch (error) {
      after = null
      throw error
    }
  }
}

const reducedMotionQuery = '(prefers-reduced-motion: reduce)'

function subscribeReducedMotion(onChange: () => void) {
  const media = window.matchMedia(reducedMotionQuery)
  media.addEventListener('change', onChange)
  return () => media.removeEventListener('change', onChange)
}

function prefersReducedMotion(): boolean {
  return window.matchMedia(reducedMotionQuery).matches
}

function subscribeVisibility(onChange: () => void) {
  document.addEventListener('visibilitychange', onChange)
  return () => document.removeEventListener('visibilitychange', onChange)
}

function isDocumentVisible(): boolean {
  return document.visibilityState === 'visible'
}
