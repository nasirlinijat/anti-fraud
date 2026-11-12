import { useCallback, useState } from 'react'
import { getReviewNetwork, type CaseNetwork as Network, type LinkedEntity, type NetworkAnchor, type NetworkStatus } from '../api'
import { ApiError } from '../request'
import { usePolling } from '../usePolling'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'
import {
  entityLabels, layoutNetwork, linkedNoun, shortenId, statusLabels, statusOrder, summarizeNetwork,
  type PlacedAnchor, type PlacedNode,
} from './networkLayout'

type NetworkState =
  | { status: 'loading' }
  | { status: 'not-found' }
  | { status: 'failed'; message: string }
  | { status: 'loaded'; network: Network }

const statusGlyphs: Record<NetworkStatus, string> = {
  blocklisted: '⊘',
  rejected: '×',
  blocked: '×',
  in_review: '◷',
  clear: '✓',
}
const NODE_RADIUS = 12
const ANCHOR_WIDTH = 136
const ANCHOR_HEIGHT = 46
const number = new Intl.NumberFormat('en-US')

// The panel loads on its own and is read once, so a failure here leaves the
// rest of the case usable and the case refresh does not repeat the heavier query.
export function CaseNetwork({ purchaseId }: { purchaseId: string }) {
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
      {state.status === 'loaded' && <NetworkView network={state.network} />}
    </section>
  )
}

function NetworkView({ network }: { network: Network }) {
  const summary = summarizeNetwork(network)
  const hasLinks = network.anchors.some(anchor => anchor.blocklisted || anchor.linked_total > 0)
  if (!hasLinks) return <EmptyState>{summary}</EmptyState>

  return (
    <>
      <p className="network-summary">{summary}</p>
      <figure className="network-figure">
        <NetworkDiagram network={network} label={`Diagram of linked players, cards and devices. ${summary}`} />
        <figcaption className="muted small">
          Lines lead from this purchase to its device, card and player, then to the other players or cards that shared
          them within {network.window_days} days before or after it. Each value shows its worst status.
        </figcaption>
        <StatusLegend />
      </figure>
      <NetworkTable network={network} />
    </>
  )
}

function NetworkDiagram({ network, label }: { network: Network; label: string }) {
  const layout = layoutNetwork(network)
  const { center } = layout
  return (
    <svg className="network-diagram" viewBox={`0 0 ${layout.size} ${layout.size}`} role="img" aria-label={label}>
      {layout.anchors.map((placed) => (
        <g key={placed.anchor.type}>
          <line className="network-edge" x1={center} y1={center} x2={placed.x} y2={placed.y} />
          {placed.nodes.map((node) => (
            <line key={nodeKey(node)} className="network-edge" x1={placed.x} y1={placed.y} x2={node.x} y2={node.y} />
          ))}
        </g>
      ))}
      <g transform={`translate(${center} ${center})`}>
        <rect className="network-seed" x={-60} y={-18} width={120} height={36} rx={18} />
        <text className="network-anchor-text" dy="0.35em" textAnchor="middle">This purchase</text>
      </g>
      {layout.anchors.map((placed) => <AnchorNode key={placed.anchor.type} placed={placed} />)}
    </svg>
  )
}

function nodeKey(node: PlacedNode): string {
  return node.kind === 'more' ? 'more' : `${node.entity.type}-${node.entity.value}`
}

function AnchorNode({ placed }: { placed: PlacedAnchor }) {
  const { anchor } = placed
  const halfWidth = ANCHOR_WIDTH / 2
  const halfHeight = ANCHOR_HEIGHT / 2
  return (
    <>
      <g className={anchor.blocklisted ? 'network-anchor network-status-blocklisted' : 'network-anchor'}
        transform={`translate(${placed.x} ${placed.y})`}>
        <title>{`${entityLabels[anchor.type]} ${anchor.value}${anchor.blocklisted ? ', blocklisted' : ''}`}</title>
        {anchor.blocklisted && (
          <rect className="network-outer-ring" x={-halfWidth - 4} y={-halfHeight - 4}
            width={ANCHOR_WIDTH + 8} height={ANCHOR_HEIGHT + 8} rx={halfHeight + 4} />
        )}
        <rect x={-halfWidth} y={-halfHeight} width={ANCHOR_WIDTH} height={ANCHOR_HEIGHT} rx={halfHeight} />
        <text className="network-anchor-text" y={-6} textAnchor="middle">
          {anchor.blocklisted ? `${statusGlyphs.blocklisted} ` : ''}{entityLabels[anchor.type]}
        </text>
        <text className="network-label" y={14} textAnchor="middle">{shortenId(anchor.value)}</text>
      </g>
      {placed.nodes.map((node) => <LinkedNode key={nodeKey(node)} node={node} anchor={anchor} />)}
    </>
  )
}

function LinkedNode({ node, anchor }: { node: PlacedNode; anchor: NetworkAnchor }) {
  const { label } = node
  const text = node.kind === 'more' ? `+${number.format(node.count)} more` : shortenId(node.entity.value)
  return (
    <g>
      <title>{node.kind === 'more' ? moreTitle(node.count, anchor) : entityTitle(node.entity)}</title>
      <g transform={`translate(${node.x} ${node.y})`}>
        {node.kind === 'more' ? <MoreMark /> : <StatusMark status={node.entity.status} />}
      </g>
      <text className="network-label" x={label.x} y={label.y} dy="0.35em" textAnchor={label.textAnchor}
        transform={`rotate(${label.rotation} ${label.x} ${label.y})`}>
        {text}
      </text>
    </g>
  )
}

function entityTitle(entity: LinkedEntity): string {
  const purchases = `${number.format(entity.purchases)} shared ${entity.purchases === 1 ? 'purchase' : 'purchases'}`
  return `${entityLabels[entity.type]} ${entity.value}: ${statusLabels[entity.status]}, ${purchases}`
}

function moreTitle(count: number, anchor: NetworkAnchor): string {
  return `${number.format(count)} more linked ${linkedNoun(anchor.type, count)} not drawn`
}

function StatusMark({ status }: { status: NetworkStatus }) {
  const size = NODE_RADIUS * 2
  return (
    <g className={`network-mark ${statusClass(status)}`}>
      {status === 'blocklisted' && <circle className="network-outer-ring" r={NODE_RADIUS + 4} />}
      {status === 'rejected'
        ? <rect x={-NODE_RADIUS} y={-NODE_RADIUS} width={size} height={size} rx={3} />
        : <circle r={NODE_RADIUS} />}
      <text className="network-glyph" dy="0.35em" textAnchor="middle">{statusGlyphs[status]}</text>
    </g>
  )
}

function MoreMark() {
  return (
    <g className="network-mark network-more">
      <circle r={NODE_RADIUS} />
      <text className="network-glyph" dy="0.35em" textAnchor="middle">…</text>
    </g>
  )
}

function statusClass(status: NetworkStatus): string {
  return `network-status-${status.replace('_', '-')}`
}

function StatusLegend() {
  return (
    <ul className="network-legend" aria-label="Status key">
      {statusOrder.map((status) => (
        <li key={status}>
          <svg viewBox="-18 -18 36 36" width="28" height="28" aria-hidden="true"><StatusMark status={status} /></svg>
          {statusLabels[status]}
        </li>
      ))}
    </ul>
  )
}

function NetworkTable({ network }: { network: Network }) {
  return (
    <details className="network-details">
      <summary>View linked players, cards and devices as a table</summary>
      <div className="table-scroll" role="region" tabIndex={0} aria-label="Linked players, cards and devices">
        <table className="stack-table" role="table">
          <caption>Players and cards sharing this purchase&apos;s device, card or player within {network.window_days} days</caption>
          <thead role="rowgroup">
            <tr role="row">
              <th role="columnheader" scope="col">Linked value</th>
              <th role="columnheader" scope="col">Type</th>
              <th role="columnheader" scope="col" className="num">Shared purchases</th>
              <th role="columnheader" scope="col">Status</th>
            </tr>
          </thead>
          {network.anchors.map((anchor) => <AnchorRows key={anchor.type} anchor={anchor} />)}
        </table>
      </div>
    </details>
  )
}

function AnchorRows({ anchor }: { anchor: NetworkAnchor }) {
  return (
    <tbody role="rowgroup">
      <tr role="row" className="network-anchor-row">
        <th role="rowheader" scope="rowgroup" colSpan={4}>{anchorHeading(anchor)}</th>
      </tr>
      {anchor.linked.length === 0 ? (
        <tr role="row"><td role="cell" colSpan={4}>No other {linkedNoun(anchor.type, 0)} share it.</td></tr>
      ) : anchor.linked.map((entity) => (
        <tr role="row" key={`${entity.type}-${entity.value}`}>
          <th role="rowheader" scope="row">{entity.value}</th>
          <td role="cell" data-label="Type">{entityLabels[entity.type]}</td>
          <td role="cell" data-label="Shared purchases" className="num">{number.format(entity.purchases)}</td>
          <td role="cell" data-label="Status"><StatusBadge status={entity.status} /></td>
        </tr>
      ))}
    </tbody>
  )
}

function anchorHeading(anchor: NetworkAnchor): string {
  const noun = linkedNoun(anchor.type, anchor.linked_total)
  const name = `${entityLabels[anchor.type]} ${anchor.value}${anchor.blocklisted ? ' (blocklisted)' : ''}`
  const listed = anchor.linked_total > anchor.linked.length ? `, ${number.format(anchor.linked.length)} listed` : ''
  return `${name}: ${number.format(anchor.linked_total)} linked ${noun}${listed}`
}

function StatusBadge({ status }: { status: NetworkStatus }) {
  return (
    <span className={`badge network-badge ${statusClass(status)}`}>
      <span aria-hidden="true">{statusGlyphs[status]}</span>
      {statusLabels[status]}
    </span>
  )
}
