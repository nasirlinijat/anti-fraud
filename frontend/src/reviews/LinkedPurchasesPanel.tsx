import { useCallback, useEffect, useRef, useState } from 'react'
import { getLinkedPurchases, type LinkedPurchases } from '../api'
import { ApiError } from '../request'
import { Button } from '../ui/Button'
import { usePolling } from '../usePolling'
import type { TimeDisplay } from '../useTimeDisplay'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'
import { LinkedPurchaseTable } from './LinkedPurchaseTable'
import { entityLabels } from './networkLayout'
import { listedPurchasesNote, type NetworkSelection } from './networkSelection'
import { StatusBadge } from './NetworkStatusMarks'

type PurchasesState =
  | { status: 'loading' }
  | { status: 'not-found' }
  | { status: 'failed'; message: string }
  | { status: 'loaded'; result: LinkedPurchases }

interface PanelProps {
  purchaseId: string
  selection: NetworkSelection
  time: TimeDisplay
  onOpen: (purchaseId: string) => void
  onClose: () => void
}

// The panel is keyed by its selection, so each selection reads its purchases
// once and the heading takes focus as the panel appears, as page headings do
// when the content they introduce is replaced.
export function LinkedPurchasesPanel({ purchaseId, selection, time, onOpen, onClose }: PanelProps) {
  const heading = useRef<HTMLHeadingElement>(null)
  useEffect(() => heading.current?.focus(), [])
  const { entity } = selection
  const name = `${entityLabels[entity.type]} ${entity.value}`
  return (
    <section className="network-selection" aria-labelledby="network-selection-heading">
      <div className="network-selection-head">
        <div>
          <h4 id="network-selection-heading" ref={heading} tabIndex={-1}>{name}</h4>
          <p className="muted small">Shares this purchase&apos;s {entityLabels[selection.anchor].toLowerCase()}.</p>
        </div>
        <StatusBadge status={entity.status} />
        <div className="button-row">
          <CopyIdButton value={entity.value} />
          <Button variant="secondary" size="sm" onClick={onClose}>Close</Button>
        </div>
      </div>
      <LinkedPurchasesView purchaseId={purchaseId} selection={selection} time={time} onOpen={onOpen} />
    </section>
  )
}

function CopyIdButton({ value }: { value: string }) {
  const [message, setMessage] = useState('')
  async function copy() {
    try {
      await navigator.clipboard.writeText(value)
      setMessage('ID copied.')
    } catch (error) {
      const reason = error instanceof Error ? ` ${error.message}` : ''
      setMessage(`Could not copy the ID. Select it in the heading and copy it instead.${reason}`)
    }
  }
  return (
    <>
      <Button variant="secondary" size="sm" onClick={() => void copy()}>Copy ID</Button>
      <span className="network-copy-status small" role="status">{message}</span>
    </>
  )
}

function LinkedPurchasesView({ purchaseId, selection, time, onOpen }: Omit<PanelProps, 'onClose'>) {
  const [state, setState] = useState<PurchasesState>({ status: 'loading' })
  const { anchor, entity } = selection
  const load = useCallback(
    (signal: AbortSignal) => getLinkedPurchases(purchaseId, anchor, entity.value, signal),
    [purchaseId, anchor, entity.value],
  )
  const { retry, pending } = usePolling(load, {
    onSuccess: (result) => setState({ status: 'loaded', result }),
    onError: (error) => {
      if (error instanceof ApiError && error.status === 404) setState({ status: 'not-found' })
      else setState({ status: 'failed', message: error instanceof Error ? error.message : 'Could not load the linked purchases.' })
    },
  })

  if (state.status === 'loading') return <LoadingSkeleton shape="table" label="Loading linked purchases…" />
  if (state.status === 'not-found') {
    return <EmptyState announce>This purchase is no longer awaiting review, so its links are not shown.</EmptyState>
  }
  if (state.status === 'failed') return <ErrorState onRetry={retry} pending={pending}>{state.message}</ErrorState>
  return <LoadedPurchases result={state.result} name={`${entityLabels[entity.type]} ${entity.value}`} time={time} onOpen={onOpen} />
}

function LoadedPurchases({ result, name, time, onOpen }: {
  result: LinkedPurchases
  name: string
  time: TimeDisplay
  onOpen: (purchaseId: string) => void
}) {
  if (result.purchases.length === 0) {
    return <EmptyState>No purchases for {name} within {result.window_days} days of this purchase.</EmptyState>
  }
  const note = listedPurchasesNote(result.purchases.length, result.total)
  return (
    <>
      <LinkedPurchaseTable purchases={result.purchases} name={name} time={time} onOpen={onOpen} />
      {note && <p className="muted small">{note}</p>}
    </>
  )
}
