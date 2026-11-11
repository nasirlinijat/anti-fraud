import { useCallback, useEffect, useEffectEvent, useRef, useState } from 'react'

type Load<T> = (signal: AbortSignal) => Promise<T>

interface PollingOptions<T> {
  intervalMs?: number
  waitAfterCompletion?: boolean
  onSuccess: (data: T) => void
  onError: (error: unknown) => void
}

// An omitted interval reads once. The page owns its last good result so a
// refresh failure cannot replace it and writes can update it immediately.
export function usePolling<T>(load: Load<T> | null, {
  intervalMs,
  waitAfterCompletion = false,
  onSuccess,
  onError,
}: PollingOptions<T>) {
  const [revision, setRevision] = useState(0)
  const [pending, setPending] = useState(false)
  const busy = useRef(false)
  const stopCurrent = useRef<(() => void) | null>(null)
  const reportSuccess = useEffectEvent(onSuccess)
  const reportError = useEffectEvent(onError)
  const stop = useCallback(() => {
    stopCurrent.current?.()
    busy.current = false
    setPending(false)
  }, [])
  const refresh = useCallback(() => {
    stop()
    setRevision(current => current + 1)
  }, [stop])
  const retry = useCallback(() => {
    if (!load || busy.current) return
    refresh()
    busy.current = true
    setPending(true)
  }, [load, refresh])

  useEffect(() => {
    if (!load) {
      busy.current = false
      return
    }
    const poller = createPoller(load, { intervalMs, waitAfterCompletion }, {
      started: () => {
        busy.current = true
        setPending(true)
      },
      succeeded: (data) => reportSuccess(data),
      failed: (error) => reportError(error),
      settled: () => {
        busy.current = false
        setPending(false)
      },
    })
    stopCurrent.current = poller.cancel
    poller.start()
    return () => {
      poller.cancel()
      stopCurrent.current = null
    }
  }, [load, intervalMs, waitAfterCompletion, revision])

  return { stop, refresh, retry, pending: load !== null && pending }
}

interface Schedule {
  intervalMs?: number
  waitAfterCompletion: boolean
}

// Reports from one poller. After cancel, no report is made.
interface PollReports<T> {
  started: () => void
  succeeded: (data: T) => void
  failed: (error: unknown) => void
  settled: () => void
}

// createPoller reads once on start, then on the schedule until cancel. With
// waitAfterCompletion the interval is counted from the end of each read, so
// reads never overlap; otherwise a read still in flight skips the next tick.
function createPoller<T>(read: Load<T>, { intervalMs, waitAfterCompletion }: Schedule, reports: PollReports<T>) {
  const controller = new AbortController()
  const { signal } = controller
  let inFlight = false
  let timer: ReturnType<typeof setTimeout> | undefined

  async function tick() {
    if (signal.aborted || inFlight) return
    inFlight = true
    reports.started()
    try {
      const data = await read(signal)
      if (!signal.aborted) reports.succeeded(data)
    } catch (error) {
      if (!signal.aborted) reports.failed(error)
    } finally {
      inFlight = false
      if (!signal.aborted) {
        reports.settled()
        if (waitAfterCompletion && intervalMs !== undefined) {
          timer = setTimeout(() => void tick(), intervalMs)
        }
      }
    }
  }

  return {
    start() {
      void tick()
      if (!waitAfterCompletion && intervalMs !== undefined) {
        timer = setInterval(() => void tick(), intervalMs)
      }
    },
    cancel() {
      controller.abort()
      if (waitAfterCompletion) clearTimeout(timer)
      else clearInterval(timer)
    },
  }
}
