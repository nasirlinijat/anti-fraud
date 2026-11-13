import type { NetworkStatus } from '../api'
import { statusLabels } from './networkLayout'

export const NODE_RADIUS = 12

const statusGlyphs: Record<NetworkStatus, string> = {
  blocklisted: '⊘',
  rejected: '×',
  blocked: '×',
  in_review: '◷',
  clear: '✓',
}

function statusClass(status: NetworkStatus): string {
  return `network-status-${status.replace('_', '-')}`
}

export function StatusMark({ status }: { status: NetworkStatus }) {
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

export function MoreMark() {
  return (
    <g className="network-mark network-more">
      <circle r={NODE_RADIUS} />
      <text className="network-glyph" dy="0.35em" textAnchor="middle">…</text>
    </g>
  )
}

export function BlocklistedGlyph() {
  return <>{statusGlyphs.blocklisted} </>
}

export function StatusBadge({ status }: { status: NetworkStatus }) {
  return (
    <span className={`badge network-badge ${statusClass(status)}`}>
      <span aria-hidden="true">{statusGlyphs[status]}</span>
      {statusLabels[status]}
    </span>
  )
}

export function StatusIcon({ status }: { status: NetworkStatus }) {
  return <svg viewBox="-18 -18 36 36" width="28" height="28" aria-hidden="true"><StatusMark status={status} /></svg>
}
