import type { KeyboardEvent, ReactNode } from 'react'
import type { CaseNetwork as Network, LinkedEntity, NetworkAnchor } from '../api'
import { entityLabels, layoutNetwork, linkedNoun, shortenId, statusLabels, type PlacedAnchor, type PlacedNode } from './networkLayout'
import { isStatusShown, sameSelection, selectionKey, type NetworkInteraction } from './networkSelection'
import { BlocklistedGlyph, MoreMark, NODE_RADIUS, StatusMark } from './NetworkStatusMarks'

const ANCHOR_WIDTH = 136
const ANCHOR_HEIGHT = 46
const number = new Intl.NumberFormat('en-US')

interface DiagramProps {
  network: Network
  label: string
  interaction: NetworkInteraction
}

// Each linked value is drawn with the line that leads to it, so hovering or
// focusing a value can highlight its line. Anchors are drawn last, on top of
// the line ends that meet them.
export function NetworkDiagram({ network, label, interaction }: DiagramProps) {
  const layout = layoutNetwork(network)
  const { center } = layout
  return (
    <svg className="network-diagram" viewBox={`0 0 ${layout.size} ${layout.size}`} role="group" aria-label={label}>
      {layout.anchors.map((placed) => (
        <line key={placed.anchor.type} className="network-edge" x1={center} y1={center} x2={placed.x} y2={placed.y} />
      ))}
      {layout.anchors.map((placed) => placed.nodes.map((node) => (
        <LinkedNode key={`${placed.anchor.type}-${nodeKey(node)}`} node={node} placed={placed} interaction={interaction} />
      )))}
      <g transform={`translate(${center} ${center})`}>
        <rect className="network-seed" x={-60} y={-18} width={120} height={36} rx={18} />
        <text className="network-anchor-text" dy="0.35em" textAnchor="middle">This purchase</text>
      </g>
      {layout.anchors.map((placed) => <AnchorNode key={placed.anchor.type} anchor={placed.anchor} placed={placed} />)}
    </svg>
  )
}

function nodeKey(node: PlacedNode): string {
  return node.kind === 'more' ? 'more' : `${node.entity.type}-${node.entity.value}`
}

function AnchorNode({ anchor, placed }: { anchor: NetworkAnchor; placed: PlacedAnchor }) {
  const halfWidth = ANCHOR_WIDTH / 2
  const halfHeight = ANCHOR_HEIGHT / 2
  const title = `${entityLabels[anchor.type]} ${anchor.value}${anchor.blocklisted ? ', blocklisted' : ''}`
  return (
    <g className={anchor.blocklisted ? 'network-anchor network-status-blocklisted' : 'network-anchor'}
      transform={`translate(${placed.x} ${placed.y})`} role="img" aria-label={title}>
      <title>{title}</title>
      {anchor.blocklisted && (
        <rect className="network-outer-ring" x={-halfWidth - 4} y={-halfHeight - 4}
          width={ANCHOR_WIDTH + 8} height={ANCHOR_HEIGHT + 8} rx={halfHeight + 4} />
      )}
      <rect x={-halfWidth} y={-halfHeight} width={ANCHOR_WIDTH} height={ANCHOR_HEIGHT} rx={halfHeight} />
      <text className="network-anchor-text" y={-6} textAnchor="middle">
        {anchor.blocklisted && <BlocklistedGlyph />}{entityLabels[anchor.type]}
      </text>
      <text className="network-label" y={14} textAnchor="middle">{shortenId(anchor.value)}</text>
    </g>
  )
}

function LinkedNode({ node, placed, interaction }: { node: PlacedNode; placed: PlacedAnchor; interaction: NetworkInteraction }) {
  const { anchor } = placed
  if (node.kind === 'more') {
    return (
      <NodeButton node={node} from={placed} label={`${moreTitle(node.count, anchor)}. Show them in the table.`}
        onActivate={() => interaction.onShowMore(anchor.type)}>
        <MoreMark />
      </NodeButton>
    )
  }
  const selection = { anchor: anchor.type, entity: node.entity }
  const selected = sameSelection(interaction.selection, selection)
  return (
    <NodeButton node={node} from={placed} label={entityTitle(node.entity)} pressed={selected} selectionKey={selectionKey(selection)}
      dimmed={!isStatusShown(node.entity.status, interaction.statuses)}
      onActivate={origin => interaction.onSelect(selection, origin)}>
      <StatusMark status={node.entity.status} />
    </NodeButton>
  )
}

function entityTitle(entity: LinkedEntity): string {
  const purchases = `${number.format(entity.purchases)} shared ${entity.purchases === 1 ? 'purchase' : 'purchases'}`
  return `${entityLabels[entity.type]} ${entity.value}: ${statusLabels[entity.status]}, ${purchases}`
}

function moreTitle(count: number, anchor: NetworkAnchor): string {
  return `${number.format(count)} more linked ${linkedNoun(anchor.type, count)} not drawn`
}

interface NodeButtonProps {
  node: PlacedNode
  from: { x: number; y: number }
  label: string
  // Only a value that can be selected has a pressed state.
  pressed?: boolean
  selectionKey?: string
  dimmed?: boolean
  onActivate: (origin: SVGGElement) => void
  children: ReactNode
}

function NodeButton({ node, from, label, pressed, selectionKey, dimmed = false, onActivate, children }: NodeButtonProps) {
  function activateByKey(event: KeyboardEvent<SVGGElement>) {
    if (event.key !== 'Enter' && event.key !== ' ') return
    event.preventDefault()
    if (!event.repeat) onActivate(event.currentTarget)
  }
  const { label: placement } = node
  const classes = ['network-node', pressed && 'network-node-selected', dimmed && 'network-node-dimmed'].filter(Boolean).join(' ')
  return (
    <g className={classes} role="button" tabIndex={0} aria-label={label} aria-pressed={pressed} data-selection={selectionKey}
      onClick={event => onActivate(event.currentTarget)} onKeyDown={activateByKey}>
      <title>{label}</title>
      <line className="network-edge" x1={from.x} y1={from.y} x2={node.x} y2={node.y} />
      <g transform={`translate(${node.x} ${node.y})`}>
        <circle className="network-focus-ring" r={NODE_RADIUS + 11} />
        {pressed && <circle className="network-selected-ring" r={NODE_RADIUS + 7} />}
        {children}
      </g>
      <text className="network-label" x={placement.x} y={placement.y} dy="0.35em" textAnchor={placement.textAnchor}
        transform={`rotate(${placement.rotation} ${placement.x} ${placement.y})`}>
        {node.kind === 'more' ? `+${number.format(node.count)} more` : shortenId(node.entity.value)}
      </text>
    </g>
  )
}
