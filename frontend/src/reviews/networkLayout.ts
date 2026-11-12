import type { CaseNetwork, LinkedEntity, NetworkAnchor, NetworkEntityType, NetworkStatus } from '../api.ts'

export const VIEW_SIZE = 640
export const MAX_DRAWN_PER_ANCHOR = 8

const CENTER = VIEW_SIZE / 2
const ANCHOR_RADIUS = 120
const LINKED_RADIUS = 195
const LABEL_OFFSET = 19
const MAX_STEP_DEGREES = 14
const SECTOR_SHARE = 0.8

export const statusOrder: readonly NetworkStatus[] = ['blocklisted', 'rejected', 'blocked', 'in_review', 'clear']

export const statusLabels: Record<NetworkStatus, string> = {
  blocklisted: 'Blocklisted',
  rejected: 'Rejected',
  blocked: 'Blocked',
  in_review: 'In review',
  clear: 'Clear',
}

export const entityLabels: Record<NetworkEntityType, string> = {
  device: 'Device',
  card: 'Card',
  player: 'Player',
}

export interface Label {
  x: number
  y: number
  rotation: number
  textAnchor: 'start' | 'end'
}

export type PlacedNode =
  | { kind: 'entity'; entity: LinkedEntity; x: number; y: number; label: Label }
  | { kind: 'more'; count: number; x: number; y: number; label: Label }

export interface PlacedAnchor {
  anchor: NetworkAnchor
  x: number
  y: number
  nodes: PlacedNode[]
}

export interface NetworkLayout {
  size: number
  center: number
  anchors: PlacedAnchor[]
}

// Lower ranks are worse, matching the order the backend sorts linked entities in.
export function statusRank(status: NetworkStatus): number {
  return statusOrder.indexOf(status)
}

// Generated IDs often share a long prefix, so the shortened form keeps more of the end.
export function shortenId(value: string): string {
  if (value.length <= 14) return value
  return `${value.slice(0, 3)}…${value.slice(-10)}`
}

export function layoutNetwork(network: CaseNetwork): NetworkLayout {
  const count = network.anchors.length
  const sectorDegrees = (360 / Math.max(count, 1)) * SECTOR_SHARE
  const anchors = network.anchors.map((anchor, index) => {
    const angle = -90 + (index * 360) / count
    return { anchor, ...pointAt(angle, ANCHOR_RADIUS), nodes: placeLinked(anchor, angle, sectorDegrees) }
  })
  return { size: VIEW_SIZE, center: CENTER, anchors }
}

function placeLinked(anchor: NetworkAnchor, centerAngle: number, sectorDegrees: number): PlacedNode[] {
  const drawn = anchor.linked.slice(0, MAX_DRAWN_PER_ANCHOR)
  const hidden = anchor.linked_total - drawn.length
  const slots = drawn.length + (hidden > 0 ? 1 : 0)
  const step = slots > 1 ? Math.min(sectorDegrees / (slots - 1), MAX_STEP_DEGREES) : 0
  const firstAngle = centerAngle - (step * (slots - 1)) / 2
  const nodes: PlacedNode[] = drawn.map((entity, index) => {
    const angle = firstAngle + index * step
    return { kind: 'entity', entity, ...pointAt(angle, LINKED_RADIUS), label: labelAt(angle) }
  })
  if (hidden > 0) {
    const angle = firstAngle + drawn.length * step
    nodes.push({ kind: 'more', count: hidden, ...pointAt(angle, LINKED_RADIUS), label: labelAt(angle) })
  }
  return nodes
}

function pointAt(angleDegrees: number, radius: number) {
  const radians = (angleDegrees * Math.PI) / 180
  return { x: round(CENTER + radius * Math.cos(radians)), y: round(CENTER + radius * Math.sin(radians)) }
}

// Labels run outward along the radius and are turned upright on the left half
// of the ring, so no label is drawn upside down.
function labelAt(angleDegrees: number): Label {
  const position = pointAt(angleDegrees, LINKED_RADIUS + LABEL_OFFSET)
  const onRight = Math.cos((angleDegrees * Math.PI) / 180) >= 0
  return {
    ...position,
    rotation: round(onRight ? angleDegrees : angleDegrees + 180),
    textAnchor: onRight ? 'start' : 'end',
  }
}

function round(value: number): number {
  return Math.round(value * 100) / 100
}

export function summarizeNetwork(network: CaseNetwork): string {
  const riskiest = riskiestAnchor(network.anchors)
  if (!riskiest) return nothingLinkedText(network)

  const name = `${entityLabels[riskiest.type]} ${riskiest.value}${riskiest.blocklisted ? ' (blocklisted)' : ''}`
  const noun = linkedNoun(riskiest.type, riskiest.linked_total)
  if (riskiest.linked_total === 0) return `${name}: no other ${noun} share it.`
  return `${name}: ${riskiest.linked_total} ${noun}, ${worstStatusText(riskiest)}`
}

function riskiestAnchor(anchors: NetworkAnchor[]): NetworkAnchor | undefined {
  const linkedOrFlagged = anchors.filter(anchor => anchor.blocklisted || anchor.linked_total > 0)
  return linkedOrFlagged.reduce<NetworkAnchor | undefined>((riskiest, anchor) => {
    if (!riskiest) return anchor
    const rankDifference = anchorRank(anchor) - anchorRank(riskiest)
    if (rankDifference !== 0) return rankDifference < 0 ? anchor : riskiest
    return anchor.linked_total > riskiest.linked_total ? anchor : riskiest
  }, undefined)
}

function anchorRank(anchor: NetworkAnchor): number {
  const ranks = anchor.linked.map(entity => statusRank(entity.status))
  if (anchor.blocklisted) ranks.push(statusRank('blocklisted'))
  return Math.min(statusOrder.length, ...ranks)
}

// The backend lists the worst statuses first, so when every listed entity has
// the worst status and more were left out, the true count can only be higher.
function worstStatusText(anchor: NetworkAnchor): string {
  if (anchor.linked.length === 0) return 'none listed.'
  const worst = anchor.linked.reduce((rank, entity) => Math.min(rank, statusRank(entity.status)), statusOrder.length)
  const status = statusOrder[worst]
  if (status === 'clear') return 'none flagged.'
  const matching = anchor.linked.filter(entity => entity.status === status).length
  const truncated = matching === anchor.linked.length && anchor.linked_total > anchor.linked.length
  return `${truncated ? 'at least ' : ''}${matching} ${statusLabels[status].toLowerCase()}.`
}

export function linkedNoun(anchorType: NetworkEntityType, count: number): string {
  const noun = anchorType === 'player' ? 'card' : 'player'
  return count === 1 ? noun : `${noun}s`
}

function nothingLinkedText(network: CaseNetwork): string {
  const hasDevice = network.anchors.some(anchor => anchor.type === 'device')
  const shared = hasDevice ? 'player, card or device' : 'player or card'
  return `No other players or cards share this purchase's ${shared} in the ${network.window_days} days around it.`
}
