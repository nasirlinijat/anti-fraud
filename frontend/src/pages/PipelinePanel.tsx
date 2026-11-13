import type { Stats } from '../api'
import { pipelineFlow, type PipelineFlow as Flow } from './pipelineFlow'
import { pipelinePaths, shareOfEntered } from './pipelinePaths'
import type { DiagramProps, FlowLabelLine } from './PipelineShapes'
import { PipelineTallDiagram } from './PipelineTallDiagram'
import { PipelineWideDiagram } from './PipelineWideDiagram'

const number = new Intl.NumberFormat()

function describe(flow: Flow, modelRan: boolean): string {
  const model = modelRan
    ? `The model cleared ${number.format(flow.modelCleared)} and sent ${number.format(flow.modelEscalated)} to review, and blocked none.`
    : 'The model did not run, and it never blocks.'
  return `Scoring pipeline. ${number.format(flow.entered)} purchases entered. `
    + `The blocklist blocked ${number.format(flow.blocklistBlocks)} and the rules blocked ${number.format(flow.rulesBlocks)}. ${model} `
    + `Result: ${number.format(flow.allow)} allowed, ${number.format(flow.review)} sent to review, ${number.format(flow.block)} blocked.`
}

function rulesOutcomeLines(flow: Flow): FlowLabelLine[] {
  return [
    { text: `${number.format(flow.rulesAllow)} allow`, share: shareOfEntered(flow.rulesAllow, flow.entered) },
    { text: `${number.format(flow.rulesReview)} review`, share: shareOfEntered(flow.rulesReview, flow.entered) },
  ]
}

// Both layouts are rendered and the stylesheet shows one, chosen by the width of the panel.
export function PipelinePanel({ stats }: { stats: Pick<Stats, 'outcomes' | 'model' | 'blocklist_blocks'> }) {
  const flow = pipelineFlow(stats)
  const modelRan = stats.model.scored > 0
  const diagram: DiagramProps = {
    flow,
    paths: pipelinePaths(flow),
    modelRan,
    scored: stats.model.scored,
    label: describe(flow, modelRan),
    chainModelLines: rulesOutcomeLines(flow),
  }
  return (
    <section className="panel pipeline-panel">
      <h2>Scoring pipeline</h2>
      <figure className="pipeline-figure">
        <PipelineWideDiagram {...diagram} />
        <PipelineTallDiagram {...diagram} />
        <PipelineTable flow={flow} />
        <figcaption className="muted small">
          The blocklist and the rules can block a purchase. The model can send a purchase to review or clear a review, but never blocks.
        </figcaption>
      </figure>
    </section>
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
        <tr><th scope="col">Step</th><th scope="col">Purchases</th><th scope="col">Share of purchases entering</th></tr>
      </thead>
      <tbody>
        {rows.map(([step, count]) => (
          <tr key={step}>
            <th scope="row">{step}</th>
            <td>{number.format(count)}</td>
            <td>{shareOfEntered(count, flow.entered)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
