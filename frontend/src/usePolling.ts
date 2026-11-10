import { useCallback, useEffect, useEffectEvent, useRef, useState } from 'react'

interface PollingOptions<T> {
  intervalMs?: number
  waitAfterCompletion?: boolean
  onSuccess: (data: T) => void
  onError: (error: unknown) => void
}

// An omitted interval reads once. The page owns its last good result so a
// refresh failure cannot replace it and writes can update it immediately.
export function usePolling<T>(load: ((signal: AbortSignal) => Promise<T>) | null, {
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
    const controller = new AbortController()
    const { signal } = controller
    let inFlight = false
    let timer: ReturnType<typeof setTimeout> | undefined

    async function tick(read: (signal: AbortSignal) => Promise<T>) {
      if (signal.aborted || inFlight) return
      inFlight = true
      busy.current = true
      setPending(true)
      try {
        const data = await read(signal)
        if (!signal.aborted) reportSuccess(data)
      } catch (error) {
        if (!signal.aborted) reportError(error)
      } finally {
        inFlight = false
        if (!signal.aborted) {
          busy.current = false
          setPending(false)
          if (waitAfterCompletion && intervalMs !== undefined) {
            timer = setTimeout(() => void tick(read), intervalMs)
          }
        }
      }
    }

    const cancel = () => {
      controller.abort()
      if (waitAfterCompletion) clearTimeout(timer)
      else clearInterval(timer)
    }
    stopCurrent.current = cancel
    void tick(load)
    if (!waitAfterCompletion && intervalMs !== undefined) {
      timer = setInterval(() => void tick(load), intervalMs)
    }
    return () => {
      cancel()
      stopCurrent.current = null
    }
  }, [load, intervalMs, waitAfterCompletion, revision])

  return { stop, refresh, retry, pending: load !== null && pending }
}
