import { useCallback, useRef, useState, type RefObject } from 'react'
import { listReviews, type ClaimFilter, type QueuePage, type ReviewSort } from '../api'
import { TimeDisplayToggle } from '../Display'
import { useAccount } from '../sessionContext'
import { Button } from '../ui/Button'
import { PageHeader } from '../ui/PageHeader'
import { SelectMenu } from '../ui/SelectMenu'
import { usePolling } from '../usePolling'
import { useTimeDisplay } from '../useTimeDisplay'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'
import { DecisionDialog } from './DecisionDialog'
import type { ReviewHandlers } from './ReviewActions'
import { CaseView } from './ReviewCase'
import { ReviewTable } from './ReviewTable'
import { useClaims, useDecisions, type Feedback } from './useDecisions'

const REFRESH_MS = 3_000

function ReviewQueue() {
  const heading = useRef<HTMLHeadingElement>(null)
  return (
    <section aria-labelledby="reviews-heading">
      <PageHeader eyebrow="Operations" title="Review queue" headingId="reviews-heading" headingRef={heading}
        lead="Claim a purchase to work on it, then approve or reject it. Only the account that holds a claim can decide." />
      <Queue heading={heading} />
    </section>
  )
}

function useQueuePages() {
  const [cursors, setCursors] = useState<(string | null)[]>([null])
  const [sort, setSort] = useState<ReviewSort>('rule_score')
  const [claimed, setClaimed] = useState<ClaimFilter>('all')
  return {
    cursors, sort, claimed, cursor: cursors[cursors.length - 1],
    setCursors,
    // A new order or filter starts again from its first page.
    setSort: (next: ReviewSort) => { setSort(next); setCursors([null]) },
    setClaimed: (next: ClaimFilter) => { setClaimed(next); setCursors([null]) },
  }
}

function Queue({ heading }: { heading: RefObject<HTMLHeadingElement | null> }) {
  const { username, role } = useAccount()
  const time = useTimeDisplay()
  const pages = useQueuePages()
  const { cursor, sort, claimed } = pages
  const [modelEnabled, setModelEnabled] = useState(false)
  const [page, setPage] = useState<QueuePage | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [feedback, setFeedback] = useState<Feedback | null>(null)
  const [openCase, setOpenCase] = useState<string | null>(null)

  const loadQueue = useCallback(
    (signal: AbortSignal) => listReviews({ sort, cursor, claimed }, signal),
    [sort, cursor, claimed],
  )
  const { stop, refresh, retry, pending: queuePending } = usePolling(loadQueue, {
    intervalMs: REFRESH_MS,
    waitAfterCompletion: true,
    onSuccess: (result) => {
      setModelEnabled(result.model_enabled)
      if (!result.model_enabled && sort === 'model_score') {
        change(() => pages.setSort('rule_score'))
        return
      }
      decisions.requests.reconcile(result.items)
      setPage(result)
      setLoadError(null)
    },
    onError: (error) => setLoadError(error instanceof Error ? error.message : 'Could not load the review queue.'),
  })

  const decisions = useDecisions({ refresh, onFeedback: setFeedback, onDecided: removeDecidedPurchase })
  const claims = useClaims({ refresh, onFeedback: setFeedback })
  const handlers: ReviewHandlers = {
    username,
    busy: new Set([...decisions.requests.pending, ...claims.pending]),
    onClaim: item => void claims.claim(item),
    onDecision: decisions.choose,
  }

  // Changing what the queue shows drops the page on screen until the new one arrives.
  function change(apply: () => void) {
    stop()
    setPage(null)
    setLoadError(null)
    apply()
  }

  function removeDecidedPurchase(id: string) {
    if (openCase === id || document.activeElement?.closest('tr')?.dataset.purchaseId === id) heading.current?.focus()
    if (openCase === id) setOpenCase(null)
    setPage(current => current && { ...current, items: current.items.filter(row => row.purchase_id !== id) })
  }

  // The control that opened a linked case is replaced with the case, so the page heading takes focus.
  function openLinkedCase(id: string) {
    heading.current?.focus()
    setOpenCase(id)
  }

  const nextCursor = page?.next_cursor ?? null
  const dialog = (
    <DecisionDialog confirmation={decisions.confirmation} blockDevice={decisions.blockDevice}
      onBlockDevice={decisions.setBlockDevice} onDismiss={decisions.dismiss}
      onConfirm={action => void decisions.save(action)} />
  )

  if (openCase !== null) {
    return (
      <>
        <QueueFeedback feedback={feedback} />
        <CaseView key={openCase} purchaseId={openCase} time={time} handlers={handlers}
          onBack={() => setOpenCase(null)} onOpen={openLinkedCase} />
        {dialog}
      </>
    )
  }

  return (
    <>
      <div className="toolbar">
        <ClaimFilterButtons claimed={claimed} onChange={next => change(() => pages.setClaimed(next))} />
        {modelEnabled && (
          <SelectMenu label="Sort by" value={sort} options={sortOptions} onChange={next => change(() => pages.setSort(next))} />
        )}
        <TimeDisplayToggle display={time} />
      </div>
      <QueueFeedback feedback={feedback} onRetry={loadError ? undefined : retry} pending={queuePending} />
      {loadError && (
        <ErrorState polite onRetry={retry} pending={queuePending}>
          {loadError} {page && 'Showing the last successful update for this page. '}Retrying automatically.
        </ErrorState>
      )}
      {page === null && (!loadError || queuePending) && <LoadingSkeleton shape="table" label="Loading review queue…" />}
      {page && page.items.length > 0 && (
        <ReviewTable items={page.items} time={time} sort={sort} modelEnabled={modelEnabled} handlers={handlers} onOpen={setOpenCase} />
      )}
      {page && page.items.length === 0 && (
        <EmptyQueue firstPage={cursor === null} mine={claimed === 'mine'} admin={role === 'admin'} />
      )}
      <QueuePages
        number={pages.cursors.length}
        onPrevious={pages.cursors.length > 1 ? () => change(() => pages.setCursors(pages.cursors.slice(0, -1))) : undefined}
        onNext={nextCursor !== null ? () => change(() => pages.setCursors([...pages.cursors, nextCursor])) : undefined}
      />
      {dialog}
    </>
  )
}

function QueueFeedback({ feedback, onRetry, pending = false }: {
  feedback: Feedback | null
  onRetry?: () => void
  pending?: boolean
}) {
  return (
    <>
      <p className={feedback && !feedback.failed ? 'notice' : 'visually-hidden'} role="status">
        {feedback && !feedback.failed ? feedback.text : ''}
      </p>
      {feedback?.failed && <ErrorState polite onRetry={onRetry} pending={pending}>{feedback.text}</ErrorState>}
    </>
  )
}

const claimFilters: [ClaimFilter, string][] = [['all', 'All purchases'], ['mine', 'My claims']]

function ClaimFilterButtons({ claimed, onChange }: { claimed: ClaimFilter; onChange: (claimed: ClaimFilter) => void }) {
  return (
    <div className="segmented" role="group" aria-label="Queue filter">
      {claimFilters.map(([value, label]) => (
        <button key={value} type="button" aria-pressed={claimed === value}
          onClick={() => { if (claimed !== value) onChange(value) }}>{label}</button>
      ))}
    </div>
  )
}

const sortOptions = [['rule_score', 'Rule score'], ['model_score', 'Model risk']] as const

function EmptyQueue({ firstPage, mine, admin }: { firstPage: boolean; mine: boolean; admin: boolean }) {
  if (!firstPage) {
    return <EmptyState>No purchases on this page. Choose Previous to see the earlier page.</EmptyState>
  }
  if (mine) {
    return <EmptyState>You hold no claims. Choose All purchases and claim one to start.</EmptyState>
  }
  return (
    <EmptyState>
      No purchases are waiting for review.
      {admin && <> Score a purchase in <a href="#/tester">Tester</a> or start a run in <a href="#/simulator">Simulator</a> to generate traffic.</>}
    </EmptyState>
  )
}

// A missing handler means there is no page in that direction.
function QueuePages({ number, onPrevious, onNext }: {
  number: number
  onPrevious?: () => void
  onNext?: () => void
}) {
  return (
    <nav className="pager" aria-label="Review queue pagination">
      <Button variant="secondary" size="sm" aria-disabled={!onPrevious} onClick={() => onPrevious?.()}>Previous</Button>
      <span aria-live="polite">Page {number}</span>
      <Button variant="secondary" size="sm" aria-disabled={!onNext} onClick={() => onNext?.()}>Next</Button>
    </nav>
  )
}

export default ReviewQueue
