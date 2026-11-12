import { useState } from 'react'
import { listAssignments, type OpenClaim, type ReviewerWork } from '../accountApi'
import { releaseClaim } from '../api'
import { ModelRiskBadge, RuleScoreBadge, TimeDisplayToggle, Timestamp } from '../Display'
import { formatUsdMinor } from '../displayFormat'
import { Button } from '../ui/Button'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { PageHeader } from '../ui/PageHeader'
import { usePolling } from '../usePolling'
import { useTimeDisplay, type TimeDisplay } from '../useTimeDisplay'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'

const REFRESH_MS = 5_000
const number = new Intl.NumberFormat('en-US')
type Release = { username: string; claim: OpenClaim }

function AssignmentsPage() {
  const [reviewers, setReviewers] = useState<ReviewerWork[] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [releaseError, setReleaseError] = useState<string | null>(null)
  const [toRelease, setToRelease] = useState<Release | null>(null)
  const time = useTimeDisplay()

  const { refresh, retry, pending } = usePolling(listAssignments, {
    intervalMs: REFRESH_MS,
    waitAfterCompletion: true,
    onSuccess: (list) => {
      setReviewers(list)
      setLoadError(null)
    },
    onError: (error) => setLoadError(error instanceof Error ? error.message : 'The assignments cannot be loaded.'),
  })

  const release = async ({ username, claim }: Release) => {
    setToRelease(null)
    setNotice(null)
    setReleaseError(null)
    const result = await releaseClaim(claim.purchase_id)
    if (result.ok) setNotice(`Purchase ${claim.purchase_id} is back in the shared queue. ${username} no longer holds it.`)
    else setReleaseError(result.error.error)
    refresh()
  }

  const openClaims = reviewers?.reduce((total, reviewer) => total + reviewer.open_claims.length, 0) ?? 0

  return (
    <div className="assignments">
      <PageHeader eyebrow="Operations" title="Assignments"
        lead="See what each reviewer has claimed and decided. Release a claim to return a stuck purchase to the shared queue.">
        {reviewers && <TimeDisplayToggle display={time} />}
      </PageHeader>
      <p className={notice ? 'notice' : 'visually-hidden'} role="status">{notice}</p>
      {loadError && (
        <ErrorState polite onRetry={retry} pending={pending}>
          {loadError} {reviewers && 'Showing the last successful update. '}Retrying automatically.
        </ErrorState>
      )}
      {releaseError && <ErrorState>{releaseError}</ErrorState>}
      {!reviewers && (!loadError || pending) && <LoadingSkeleton shape="table" label="Loading assignments…" />}
      {reviewers && reviewers.length === 0 && (
        <EmptyState>No reviewers yet. Create a reviewer account in <a href="#/accounts">Accounts</a>.</EmptyState>
      )}
      {reviewers && reviewers.length > 0 && (
        <>
          <p className="muted small">
            {number.format(reviewers.length)} {reviewers.length === 1 ? 'account' : 'accounts'}, {number.format(openClaims)} open {openClaims === 1 ? 'claim' : 'claims'}.
            Refreshes every {REFRESH_MS / 1000} seconds.
          </p>
          <div className="reviewer-list">
            {reviewers.map(reviewer => (
              <ReviewerPanel key={reviewer.username} reviewer={reviewer} time={time}
                onRelease={claim => setToRelease({ username: reviewer.username, claim })} />
            ))}
          </div>
        </>
      )}
      <ConfirmDialog open={toRelease !== null} title="Release this claim?" confirmLabel="Release claim"
        onCancel={() => setToRelease(null)} onConfirm={() => { if (toRelease) void release(toRelease) }}>
        <p>
          Purchase {toRelease?.claim.purchase_id} returns to the shared queue, and {toRelease?.username} can no longer decide it
          without claiming it again.
        </p>
      </ConfirmDialog>
    </div>
  )
}

function ReviewerPanel({ reviewer, time, onRelease }: {
  reviewer: ReviewerWork
  time: TimeDisplay
  onRelease: (claim: OpenClaim) => void
}) {
  const headingId = `reviewer-${reviewer.username}`
  return (
    <section className="panel reviewer-panel" aria-labelledby={headingId}>
      <div className="panel-head">
        <h2 id={headingId}>
          {reviewer.username}
          <span className="badge badge-muted">{reviewer.role === 'admin' ? 'Admin' : 'Reviewer'}</span>
          {reviewer.disabled && <span className="badge badge-danger">Disabled</span>}
        </h2>
        <dl className="inline-stats">
          <div><dt>Open claims</dt><dd>{number.format(reviewer.open_claims.length)}</dd></div>
          <div><dt>Decided</dt><dd>{number.format(reviewer.decided)}</dd></div>
        </dl>
      </div>
      {reviewer.open_claims.length === 0
        ? <EmptyState>No open claims.</EmptyState>
        : <ClaimsTable reviewer={reviewer} time={time} onRelease={onRelease} />}
    </section>
  )
}

function ClaimsTable({ reviewer, time, onRelease }: {
  reviewer: ReviewerWork
  time: TimeDisplay
  onRelease: (claim: OpenClaim) => void
}) {
  return (
    <div className="table-scroll" role="region" tabIndex={0} aria-label={`Open claims of ${reviewer.username}`}>
      <table className="stack-table" role="table">
        <thead role="rowgroup">
          <tr role="row">
            <th role="columnheader" scope="col">Purchase</th>
            <th role="columnheader" scope="col" className="num">Amount (USD)</th>
            <th role="columnheader" scope="col">Rule score</th>
            <th role="columnheader" scope="col">Model risk</th>
            <th role="columnheader" scope="col">Claimed</th>
            <th role="columnheader" scope="col"><span className="visually-hidden">Actions</span></th>
          </tr>
        </thead>
        <tbody role="rowgroup">
          {reviewer.open_claims.map(claim => (
            <tr role="row" key={claim.purchase_id}>
              <th role="rowheader" scope="row">{claim.purchase_id}</th>
              <td role="cell" data-label="Amount (USD)" className="num">{formatUsdMinor(claim.amount_minor)}</td>
              <td role="cell" data-label="Rule score"><RuleScoreBadge value={claim.rule_score} /></td>
              <td role="cell" data-label="Model risk"><ModelRiskBadge value={claim.model_score} /></td>
              <td role="cell" data-label="Claimed"><Timestamp value={claim.claimed_at} display={time} /></td>
              <td role="cell" className="cell-action">
                <Button variant="secondary" size="sm" aria-label={`Release purchase ${claim.purchase_id}`}
                  onClick={() => onRelease(claim)}>Release</Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export default AssignmentsPage
