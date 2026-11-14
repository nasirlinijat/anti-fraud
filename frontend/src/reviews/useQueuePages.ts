import { useEffect, useState } from 'react'
import type { ClaimFilter, ReviewSort } from '../api'

interface QueuePosition {
  number: number
  sort: ReviewSort
  claimed: ClaimFilter
}

const firstPage: QueuePosition = { number: 1, sort: 'rule_score', claimed: 'all' }

function restoreQueuePosition(key: string): QueuePosition {
  try {
    const saved = sessionStorage.getItem(key)
    if (!saved) return firstPage
    const position: Partial<QueuePosition> | null = JSON.parse(saved)
    return {
      number: typeof position?.number === 'number' && Number.isSafeInteger(position.number) && position.number > 0
        ? position.number : firstPage.number,
      sort: position?.sort === 'model_score' ? 'model_score' : firstPage.sort,
      claimed: position?.claimed === 'mine' ? 'mine' : firstPage.claimed,
    }
  } catch {
    return firstPage
  }
}

export function useQueuePages(username: string) {
  const key = `anti-fraud.review-queue.${username}`
  const [position, setPosition] = useState(() => restoreQueuePosition(key))
  useEffect(() => {
    try {
      sessionStorage.setItem(key, JSON.stringify(position))
    } catch {
      // The current queue position is still available while the queue is mounted.
    }
  }, [key, position])

  return {
    ...position,
    setNumber: (number: number) => setPosition(current => ({ ...current, number })),
    setSort: (sort: ReviewSort) => setPosition(current => ({ ...current, sort, number: 1 })),
    setClaimed: (claimed: ClaimFilter) => setPosition(current => ({ ...current, claimed, number: 1 })),
  }
}
