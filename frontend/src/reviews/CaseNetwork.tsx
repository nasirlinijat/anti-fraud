import { useCallback, useId, useRef, useState } from 'react'
import { getReviewNetwork, type CaseNetwork as Network, type NetworkStatus } from '../api'
import { ApiError } from '../request'
import { usePolling } from '../usePolling'
import type { TimeDisplay } from '../useTimeDisplay'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'
import { LinkedPurchasesPanel } from './LinkedPurchasesPanel'
import { NetworkDiagram } from './NetworkDiagram'
import { summarizeNetwork } from './networkLayout'
import {
  selectionKey, toggleSelection, toggleStatus, type NetworkInteraction, type NetworkSelection, type StatusFilter,
} from './networkSelection'
import { NetworkStatusFilter } from './NetworkStatusFilter'
import { NetworkTable } from './NetworkTable'

type NetworkState =
  | { status: 'loading' }
  | { status: 'not-found' }
  | { status: 'failed'; message: string }
  | { status: 'loaded'; network: Network }

interface CaseNetworkProps {
  purchaseId: string
  time: TimeDisplay
  onOpen: (purchaseId: string) => void
}

// The panel loads on its own and is read once, so a failure here leaves the
// rest of the case usable and the case refresh does not repeat the heavier query.
export function CaseNetwork({ purchaseId, time, onOpen }: CaseNetworkProps) {
  const [state, setState] = useState<NetworkState>({ status: 'loading' })
  const load = useCallback((signal: AbortSignal) => getReviewNetwork(purchaseId, signal), [purchaseId])
  const { retry, pending } = usePolling(load, {
    onSuccess: (network) => setState({ status: 'loaded', network }),
    onError: (error) => {
      if (error instanceof ApiError && error.status === 404) setState({ status: 'not-found' })
      else setState({ status: 'failed', message: error instanceof Error ? error.message : 'Could not load the linked players, cards and devices.' })
    },
  })

  return (
    <section className="panel case-network" aria-labelledby="case-network">
      <h3 id="case-network">Linked players, cards and devices</h3>
      {state.status === 'loading' && <LoadingSkeleton shape="chart" label="Loading linked players, cards and devices…" />}
      {state.status === 'not-found' && (
        <EmptyState announce>This purchase is no longer awaiting review, so its links are not shown.</EmptyState>
      )}
      {state.status === 'failed' && <ErrorState onRetry={retry} pending={pending}>{state.message}</ErrorState>}
      {state.status === 'loaded' && <NetworkView network={state.network} purchaseId={purchaseId} time={time} onOpen={onOpen} />}
    </section>
  )
}

function NetworkView({ network, purchaseId, time, onOpen }: CaseNetworkProps & { network: Network }) {
  const summary = summarizeNetwork(network)
  const hasLinks = network.anchors.some(anchor => anchor.blocklisted || anchor.linked_total > 0)
  const view = useNetworkInteraction()
  if (!hasLinks) return <EmptyState>{summary}</EmptyState>

  const { interaction, selection } = view
  return (
    <>
      <p className="network-summary">{summary}</p>
      <figure className="network-figure">
        <NetworkDiagram network={network} label={`Diagram of linked players, cards and devices. ${summary}`} interaction={interaction} />
        <figcaption className="muted small">
          Lines lead from this purchase to its device, card and player, then to the other players or cards that shared
          them within {network.window_days} days before or after it. Each value shows its worst status. Select a value
          to see its purchases.
        </figcaption>
        <NetworkStatusFilter statuses={interaction.statuses} onToggle={view.toggleStatus} onShowAll={view.showAll} />
      </figure>
      {selection && (
        <LinkedPurchasesPanel key={selectionKey(selection)} purchaseId={purchaseId} selection={selection} time={time}
          onOpen={onOpen} onClose={view.close} />
      )}
      <NetworkTable network={network} interaction={interaction} rowIdPrefix={view.rowIdPrefix} detailsRef={view.details} />
    </>
  )
}

// useNetworkInteraction keeps the selected value, the status filter, and the
// control that made the selection, so closing the details returns focus there.
function useNetworkInteraction() {
  const [selection, setSelection] = useState<NetworkSelection | null>(null)
  const [statuses, setStatuses] = useState<StatusFilter>(() => new Set<NetworkStatus>())
  const origin = useRef<HTMLElement | SVGElement | null>(null)
  const details = useRef<HTMLDetailsElement>(null)
  const rowIdPrefix = useId()

  const interaction: NetworkInteraction = {
    selection,
    statuses,
    onSelect: (next, from) => {
      origin.current = from
      setSelection(current => toggleSelection(current, next))
    },
    onShowMore: (anchor) => showAnchorRows(details.current, `${rowIdPrefix}-${anchor}`),
  }
  return {
    interaction,
    selection,
    details,
    rowIdPrefix,
    toggleStatus: (status: NetworkStatus) => setStatuses(current => toggleStatus(current, status)),
    showAll: () => setStatuses(new Set()),
    close: () => {
      setSelection(null)
      returnFocus(origin.current, selection)
    },
  }
}

// A table row can be filtered out while its details are open, so the same
// value's node in the diagram takes focus when the row is gone.
function returnFocus(origin: HTMLElement | SVGElement | null, selection: NetworkSelection | null) {
  if (origin?.isConnected) {
    origin.focus()
    return
  }
  if (!selection) return
  document.querySelector<SVGElement>(`.network-diagram [data-selection="${CSS.escape(selectionKey(selection))}"]`)?.focus()
}

// The rows already exist inside the closed table view, so opening it makes
// the anchor's heading row visible and focusable at once.
function showAnchorRows(details: HTMLDetailsElement | null, rowId: string) {
  const row = document.getElementById(rowId)
  if (!details || !row) return
  details.open = true
  row.focus({ preventScroll: true })
  const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  row.scrollIntoView({ behavior: reduceMotion ? 'auto' : 'smooth', block: 'start' })
}
