import type { Outcome } from '../api'
import type { PipelineFlow } from './pipelineFlow'
import type { PathId, PipelinePath } from './pipelinePaths'

const number = new Intl.NumberFormat()

const CORNER_RADIUS = 16
const EMPTY_STROKE = 1.5

export interface Point { x: number; y: number }
export interface Box { x: number; y: number; width: number; height: number }
export interface FlowLabelLine { text: string; share: string }

export interface DiagramProps {
  flow: PipelineFlow
  paths: Record<PathId, PipelinePath>
  modelRan: boolean
  scored: number
  label: string
  chainModelLines: FlowLabelLine[]
}

export function Stage({ box, title }: { box: Box; title: string }) {
  return (
    <g className="pipeline-stage">
      <rect x={box.x} y={box.y} width={box.width} height={box.height} rx={8} />
      <text className="pipeline-title" x={box.x + box.width / 2} y={box.y + box.height / 2} dy="0.35em" textAnchor="middle">{title}</text>
    </g>
  )
}

export function ModelStage({ box, modelRan, scored }: { box: Box; modelRan: boolean; scored: number }) {
  const x = box.x + box.width / 2
  const line = box.height * 0.26
  const middle = box.y + box.height / 2 + 4
  return (
    <g className={modelRan ? 'pipeline-stage' : 'pipeline-stage pipeline-stage-idle'}>
      <rect x={box.x} y={box.y} width={box.width} height={box.height} rx={8} />
      <text className="pipeline-title" x={x} y={middle - line} textAnchor="middle">3. Model</text>
      <text className="pipeline-note" x={x} y={middle} textAnchor="middle">{modelRan ? `scored ${number.format(scored)}` : 'not run'}</text>
      <text className="pipeline-note" x={x} y={middle + line} textAnchor="middle">never blocks</text>
    </g>
  )
}

export function OutcomeBin({ outcome, name, count, box }: { outcome: Outcome; name: string; count: number; box: Box }) {
  const x = box.x + box.width / 2
  const middle = box.y + box.height / 2
  return (
    <g className={`pipeline-bin pipeline-bin-${outcome}`}>
      <rect x={box.x} y={box.y} width={box.width} height={box.height} rx={8} />
      <text className="pipeline-title" x={x} y={middle - 6} textAnchor="middle">{name}</text>
      <text className="pipeline-count" x={x} y={middle + 13} textAnchor="middle">{number.format(count)}</text>
    </g>
  )
}

// Every route is a list of axis-aligned points that ends at the tip of its arrowhead.
export function FlowPath({ path, route }: { path: PipelinePath; route: Point[] }) {
  const empty = path.width === 0
  const arrow = arrowhead(route, path.width)
  const tone = empty ? 'empty' : path.outcome ?? 'chain'
  return (
    <g className={`pipeline-flow pipeline-flow-${tone}`} data-path={path.id}>
      <path d={roundedRoute(route, arrow.length)} strokeWidth={empty ? EMPTY_STROKE : path.width} />
      <polygon points={arrow.points} />
    </g>
  )
}

function arrowhead(route: Point[], width: number): { length: number; points: string } {
  const tip = route[route.length - 1]
  const direction = unit(route[route.length - 2], tip)
  const length = width === 0 ? 6 : 6 + width / 4
  const halfBase = width / 2 + 4
  const base = { x: tip.x - direction.x * length, y: tip.y - direction.y * length }
  const side = { x: -direction.y * halfBase, y: direction.x * halfBase }
  return {
    length,
    points: `${tip.x},${tip.y} ${base.x + side.x},${base.y + side.y} ${base.x - side.x},${base.y - side.y}`,
  }
}

function roundedRoute(route: Point[], arrowLength: number): string {
  const end = toward(route[route.length - 1], route[route.length - 2], arrowLength - 0.5)
  const corners = route.slice(1, -1).map((corner, index) => {
    const before = toward(corner, route[index], CORNER_RADIUS)
    const after = toward(corner, route[index + 2], CORNER_RADIUS)
    return `L${before.x} ${before.y}Q${corner.x} ${corner.y} ${after.x} ${after.y}`
  })
  return `M${route[0].x} ${route[0].y}${corners.join('')}L${end.x} ${end.y}`
}

function toward(from: Point, to: Point, distance: number): Point {
  const direction = unit(from, to)
  return { x: from.x + direction.x * distance, y: from.y + direction.y * distance }
}

function unit(from: Point, to: Point): Point {
  const length = Math.hypot(to.x - from.x, to.y - from.y) || 1
  return { x: (to.x - from.x) / length, y: (to.y - from.y) / length }
}

export function PathLabel({ path, at, anchor }: { path: PipelinePath; at: Point; anchor?: 'start' | 'middle' | 'end' }) {
  return <FlowLabel at={at} anchor={anchor} lines={[{ text: number.format(path.count), share: path.share }]} />
}

export function FlowLabel({ at, anchor = 'middle', lines }: { at: Point; anchor?: 'start' | 'middle' | 'end'; lines: FlowLabelLine[] }) {
  const lineHeight = 16
  return (
    <text className="pipeline-count" x={at.x} y={at.y} textAnchor={anchor}>
      {lines.map((line, index) => (
        <tspan key={line.text} x={at.x} dy={index === 0 ? 0 : lineHeight}>
          {line.text}
          {line.share && <tspan className="pipeline-share">{` · ${line.share}`}</tspan>}
        </tspan>
      ))}
    </text>
  )
}

export function FlowCaption({ at, text }: { at: Point; text: string }) {
  return <text className="pipeline-note" x={at.x} y={at.y} textAnchor="middle">{text}</text>
}
