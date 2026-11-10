import { useEffect, useState } from 'react'

export interface TimeDisplay {
  now: number
  showUtc: boolean
  setShowUtc: (show: boolean) => void
}

export function useTimeDisplay(): TimeDisplay {
  const [now, setNow] = useState(Date.now)
  const [showUtc, setShowUtc] = useState(false)
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30_000)
    return () => clearInterval(timer)
  }, [])
  return { now, showUtc, setShowUtc }
}
