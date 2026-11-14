import { useCallback, useEffect, useRef, useState, type RefObject } from 'react'
import { listReviewPage, type ClaimFilter, type NumberedQueuePage } from '../api'
import { TimeDisplayToggle } from '../Display'
import { useAccount } from '../sessionContext'
import { PageHeader } from '../ui/PageHeader'
import { SelectMenu } from '../ui/SelectMenu'
import { usePolling } from '../usePolling'
import { useTimeDisplay } from '../useTimeDisplay'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'
import { caseAddress, caseInAddress, QUEUE_ADDRESS } from './caseAddress'
import { BatchDecisionDialog, DecisionDialog } from './DecisionDialog'
import { ModelPanel } from './ModelPanel'
import { CaseView } from './ReviewCase'
import { ReviewTable } from './ReviewTable'
import { QueuePages } from './QueuePages'
import type { Feedback } from './useDecisions'
import { useQueuePages } from './useQueuePages'
import { useReviewWork } from './useReviewWork'

const REFRESH_MS = 3_000

function ReviewQueue() {
  const heading = useRef<HTMLHeadingElement>(null)
  const { username } = useAccount()
  return (
    <section aria-labelledby="reviews-heading">
      <PageHeader eyebrow="Operations" title="Review queue" headingId="reviews-heading" headingRef={heading}
        lead="Claim a purchase to work on it, then approve or reject it. Only the account that holds a claim can decide." />
      <Queue key={username} heading={heading} />
    </section>
  )
}

function useCaseAddress(): [string | null, (purchaseId: string | null) => void] {
  const [openCase, setOpenCase] = useState(() => caseInAddress(window.location.hash))
  useEffect(() => {
    const follow = () => setOpenCase(caseInAddress(window.location.hash))
    window.addEventListener('hashchange', follow)
    return () => window.removeEventListener('hashchange', follow)
  }, [])
  const open = useCallback((purchaseId: string | null) => {
    window.location.hash = purchaseId === null ? QUEUE_ADDRESS : caseAddress(purchaseId)
  }, [])
  return [openCase, open]
}

function Queue({ heading }: { heading: RefObject<HTMLHeadingElement | null> }) {
  const { username, role } = useAccount()
  const time = useTimeDisplay()
  const pages = useQueuePages(username)
  const { number, sort, claimed } = pages
  const [modelEnabled, setModelEnabled] = useState(false)
  const [page, setPage] = useState<NumberedQueuePage | null>(null)
  const [pagination, setPagination] = useState<NumberedQueuePage['pagination'] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [feedback, setFeedback] = useState<Feedback | null>(null)
  const [openCase, setOpenCase] = useCaseAddress()

  const loadQueue = useCallback(
    (signal: AbortSignal) => listReviewPage({ sort, page: number, claimed }, signal),
    [sort, number, claimed],
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
      work.reconcile(result.items)
      setPage(result)
      setPagination(result.pagination)
      if (result.pagination.page !== number) pages.setNumber(result.pagination.page)
      setLoadError(null)
    },
    onError: (error) => setLoadError(error instanceof Error ? error.message : 'Could not load the review queue.'),
  })

  const work = useReviewWork({ username, refreshQueue: refresh, onFeedback: setFeedback, onDecided: removeDecidedPurchase })
  const { handlers, decisions, batch } = work

  // Changing what the queue shows drops the page on screen until the new one arrives.
  function change(apply: () => void) {
    stop()
    setPage(null)
    setLoadError(null)
    apply()
  }

  function removeDecidedPurchase(id: string) {
    const currentCase = caseInAddress(window.location.hash)
    if (currentCase === id || document.activeElement?.closest('tr')?.dataset.purchaseId === id) heading.current?.focus()
    if (currentCase === id) setOpenCase(null)
    setPage(current => current && { ...current, items: current.items.filter(row => row.purchase_id !== id) })
  }

  // The control that opened a linked case is replaced with the case, so the page heading takes focus.
  function openLinkedCase(id: string) {
    heading.current?.focus()
    setOpenCase(id)
  }

  const dialog = (
    <>
      <DecisionDialog confirmation={decisions.confirmation} blocks={decisions.blocks}
        onBlocksChange={decisions.setBlocks} onDismiss={decisions.dismiss}
        onConfirm={action => void decisions.save(action, openCase === action.item.purchase_id ? openCase : undefined)} />
      <BatchDecisionDialog confirmation={batch.confirmation} blocks={batch.blocks}
        onBlocksChange={batch.setBlocks} onDismiss={batch.dismiss}
        onConfirm={action => void batch.save(action)} />
    </>
  )

  if (openCase !== null) {
    return (
      <>
        <QueueFeedback feedback={feedback} openCase={openCase} />
        <CaseView key={openCase} purchaseId={openCase} time={time} handlers={handlers} linked={work.linked}
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
      {pagination && <QueuePages pagination={pagination} busy={page === null}
        onPage={next => change(() => pages.setNumber(next))} />}
      {page === null && (!loadError || queuePending) && <LoadingSkeleton shape="table" label="Loading review queue…" />}
      {page && page.items.length > 0 && (
        <ReviewTable items={page.items} time={time} sort={sort} modelEnabled={modelEnabled} handlers={handlers} onOpen={setOpenCase} />
      )}
      {page && page.items.length === 0 && (
        <EmptyQueue mine={claimed === 'mine'} admin={role === 'admin'} />
      )}
      <ModelPanel />
      {dialog}
    </>
  )
}

function QueueFeedback({ feedback, openCase, onRetry, pending = false }: {
  feedback: Feedback | null
  openCase?: string
  onRetry?: () => void
  pending?: boolean
}) {
  return (
    <>
      <p className={feedback && !feedback.failed ? 'notice' : 'visually-hidden'} role="status">
        {feedback && !feedback.failed ? feedback.text : ''}
        {feedback?.caseId && feedback.caseId !== openCase && !feedback.failed && (
          <> <a href={caseAddress(feedback.caseId)}>View decision</a></>
        )}
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

function EmptyQueue({ mine, admin }: { mine: boolean; admin: boolean }) {
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

export default ReviewQueue
