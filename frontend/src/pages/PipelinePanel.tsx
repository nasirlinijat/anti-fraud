import { useId } from 'react'
import type { Outcome, Stats } from '../api'
import { pipelineFlow, type PipelineFlow as Flow } from './pipelineFlow'

const number = new Intl.NumberFormat()

const VIEW_WIDTH = 360
const VIEW_HEIGHT = 410
const STAGE = { x: 15, width: 116, height: 44 }
const BIN = { x: 250, width: 95, height: 44 }
const EDGE = { from: STAGE.x + STAGE.width, to: BIN.x, middle: (STAGE.x + STAGE.width + BIN.x) / 2 }
const STAGE_TOP = { purchases: 10, blocklist: 104, rules: 198, model: 292 }
const MODEL_HEIGHT = 100
const CHAIN_GAP = 50
const CHAIN_X = STAGE.x + STAGE.width / 2

const rowY = (top: number) => top + STAGE.height / 2

function describe(flow: Flow, modelRan: boolean): string {
  const model = modelRan
    ? `The model cleared ${number.format(flow.modelCleared)} and sent ${number.format(flow.modelEscalated)} to review, and blocked none.`
    : 'The model did not run, and it never blocks.'
  return `Scoring pipeline. ${number.format(flow.entered)} purchases entered. `
    + `The blocklist blocked ${number.format(flow.blocklistBlocks)} and the rules blocked ${number.format(flow.rulesBlocks)}. ${model} `
    + `Result: ${number.format(flow.allow)} allowed, ${number.format(flow.review)} sent to review, ${number.format(flow.block)} blocked.`
}

export function PipelinePanel({ stats }: { stats: Pick<Stats, 'outcomes' | 'model' | 'blocklist_blocks'> }) {
  const flow = pipelineFlow(stats)
  const modelRan = stats.model.scored > 0
  return (
    <section className="panel pipeline-panel">
      <h2>Scoring pipeline</h2>
      <figure className="pipeline-figure">
        <PipelineDiagram flow={flow} modelRan={modelRan} scored={stats.model.scored} />
        <PipelineTable flow={flow} />
        <figcaption className="muted small">
          The blocklist and the rules can block a purchase. The model can send a purchase to review or clear a review, but never blocks.
        </figcaption>
      </figure>
    </section>
  )
}

function PipelineDiagram({ flow, modelRan, scored }: { flow: Flow; modelRan: boolean; scored: number }) {
  const arrowId = useId().replaceAll(':', '')
  const modelBottom = STAGE_TOP.model + MODEL_HEIGHT
  const reviewY = rowY(STAGE_TOP.model)
  const allowY = rowY(modelBottom - STAGE.height)
  return (
    <svg className="pipeline-diagram" viewBox={`0 0 ${VIEW_WIDTH} ${VIEW_HEIGHT}`} role="img" aria-label={describe(flow, modelRan)}>
      <defs>
        <marker id={arrowId} markerUnits="userSpaceOnUse" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">
          <path className="pipeline-arrow" d="M0 0L8 4L0 8z" />
        </marker>
      </defs>
      <g markerEnd={`url(#${arrowId})`}>
        <ChainLink top={STAGE_TOP.purchases} />
        <ChainLink top={STAGE_TOP.blocklist} />
        <ChainLink top={STAGE_TOP.rules} />
        <SideEdge y={rowY(STAGE_TOP.blocklist)} />
        <SideEdge y={rowY(STAGE_TOP.rules)} />
        <SideEdge y={reviewY} />
        <SideEdge y={allowY} />
      </g>
      <Stage top={STAGE_TOP.purchases} title="Purchases" />
      <Stage top={STAGE_TOP.blocklist} title="1. Blocklist" />
      <Stage top={STAGE_TOP.rules} title="2. Rules" />
      <ModelStage modelRan={modelRan} scored={scored} />
      <ChainLabel top={STAGE_TOP.purchases} lines={[number.format(flow.entered)]} />
      <ChainLabel top={STAGE_TOP.blocklist} lines={[number.format(flow.passedBlocklist)]} />
      <ChainLabel top={STAGE_TOP.rules} lines={[`${number.format(flow.rulesAllow)} allow`, `${number.format(flow.rulesReview)} review`]} />
      <EdgeLabel y={rowY(STAGE_TOP.blocklist)} count={number.format(flow.blocklistBlocks)} />
      <EdgeLabel y={rowY(STAGE_TOP.rules)} count={number.format(flow.rulesBlocks)} />
      <EdgeLabel y={reviewY} count={number.format(flow.review)} caption={`${number.format(flow.modelEscalated)} escalated`} />
      <EdgeLabel y={allowY} count={number.format(flow.allow)} caption={`${number.format(flow.modelCleared)} cleared`} />
      <OutcomeBin outcome="block" name="Block" count={flow.block} top={STAGE_TOP.blocklist} height={STAGE_TOP.rules + STAGE.height - STAGE_TOP.blocklist} />
      <OutcomeBin outcome="review" name="Review" count={flow.review} top={STAGE_TOP.model} height={BIN.height} />
      <OutcomeBin outcome="allow" name="Allow" count={flow.allow} top={modelBottom - BIN.height} height={BIN.height} />
    </svg>
  )
}

function ChainLink({ top }: { top: number }) {
  const bottom = top + STAGE.height
  return <line className="pipeline-edge" x1={CHAIN_X} x2={CHAIN_X} y1={bottom} y2={bottom + CHAIN_GAP} />
}

function SideEdge({ y }: { y: number }) {
  return <line className="pipeline-edge" x1={EDGE.from} x2={EDGE.to} y1={y} y2={y} />
}

function Stage({ top, title }: { top: number; title: string }) {
  return (
    <g className="pipeline-stage">
      <rect x={STAGE.x} y={top} width={STAGE.width} height={STAGE.height} rx={8} />
      <text className="pipeline-title" x={CHAIN_X} y={top + STAGE.height / 2} dy="0.35em" textAnchor="middle">{title}</text>
    </g>
  )
}

function ModelStage({ modelRan, scored }: { modelRan: boolean; scored: number }) {
  const top = STAGE_TOP.model
  return (
    <g className={modelRan ? 'pipeline-stage' : 'pipeline-stage pipeline-stage-idle'}>
      <rect x={STAGE.x} y={top} width={STAGE.width} height={MODEL_HEIGHT} rx={8} />
      <text className="pipeline-title" x={CHAIN_X} y={top + 26} textAnchor="middle">3. Model</text>
      <text className="pipeline-note" x={CHAIN_X} y={top + 50} textAnchor="middle">
        {modelRan ? `scored ${number.format(scored)}` : 'not run'}
      </text>
      <text className="pipeline-note" x={CHAIN_X} y={top + 80} textAnchor="middle">never blocks</text>
    </g>
  )
}

function ChainLabel({ top, lines }: { top: number; lines: string[] }) {
  const lineHeight = 16
  const first = top + STAGE.height + CHAIN_GAP / 2 + 4 - ((lines.length - 1) * lineHeight) / 2
  return (
    <text className="pipeline-count" x={CHAIN_X + 12} y={first}>
      {lines.map((line, index) => <tspan key={line} x={CHAIN_X + 12} dy={index === 0 ? 0 : lineHeight}>{line}</tspan>)}
    </text>
  )
}

function EdgeLabel({ y, count, caption }: { y: number; count: string; caption?: string }) {
  return (
    <g className="pipeline-edge-label">
      <text className="pipeline-count" x={EDGE.middle} y={y - 6} textAnchor="middle">{count}</text>
      {caption && <text className="pipeline-note" x={EDGE.middle} y={y + 16} textAnchor="middle">{caption}</text>}
    </g>
  )
}

function OutcomeBin({ outcome, name, count, top, height }: { outcome: Outcome; name: string; count: number; top: number; height: number }) {
  const middle = top + height / 2
  return (
    <g className={`pipeline-bin pipeline-bin-${outcome}`}>
      <rect x={BIN.x} y={top} width={BIN.width} height={height} rx={8} />
      <text className="pipeline-title" x={BIN.x + BIN.width / 2} y={middle - 8} textAnchor="middle">{name}</text>
      <text className="pipeline-count" x={BIN.x + BIN.width / 2} y={middle + 12} textAnchor="middle">{number.format(count)}</text>
    </g>
  )
}

function PipelineTable({ flow }: { flow: Flow }) {
  const rows: [string, number][] = [
    ['Purchases entering the pipeline', flow.entered],
    ['Blocked by the blocklist', flow.blocklistBlocks],
    ['Passing the blocklist', flow.passedBlocklist],
    ['Blocked by the rules', flow.rulesBlocks],
    ['Allowed by the rules, before the model', flow.rulesAllow],
    ['Sent to review by the rules, before the model', flow.rulesReview],
    ['Cleared by the model, from review to allow', flow.modelCleared],
    ['Sent to review by the model, from allow to review', flow.modelEscalated],
    ['Allowed in the end', flow.allow],
    ['Sent to review in the end', flow.review],
    ['Blocked in the end', flow.block],
  ]
  return (
    <table className="visually-hidden">
      <caption>Scoring pipeline counts</caption>
      <thead>
        <tr><th scope="col">Step</th><th scope="col">Purchases</th></tr>
      </thead>
      <tbody>
        {rows.map(([step, count]) => <tr key={step}><th scope="row">{step}</th><td>{number.format(count)}</td></tr>)}
      </tbody>
    </table>
  )
}
