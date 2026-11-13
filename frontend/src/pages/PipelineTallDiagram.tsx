import { FlowCaption, FlowLabel, FlowPath, ModelStage, OutcomeBin, PathLabel, Stage, type Box, type DiagramProps } from './PipelineShapes'
import { MAX_PATH_WIDTH, type PipelinePath } from './pipelinePaths'

const number = new Intl.NumberFormat()

const STAGE = { x: 15, width: 116, height: 44 }
const BIN = { x: 250, width: 95, height: 44 }
const TOP = { purchases: 10, blocklist: 104, rules: 198, model: 292 }
const MODEL_HEIGHT = 112
const CHAIN_GAP = 50
const CHAIN_X = STAGE.x + STAGE.width / 2
const CHAIN_LABEL_X = CHAIN_X + MAX_PATH_WIDTH / 2 + 8
const EDGE = { from: STAGE.x + STAGE.width, to: BIN.x, middle: (STAGE.x + STAGE.width + BIN.x) / 2 }
const ROW = {
  blocklist: TOP.blocklist + STAGE.height / 2,
  rules: TOP.rules + STAGE.height / 2,
  review: TOP.model + BIN.height / 2,
  allow: TOP.model + MODEL_HEIGHT - BIN.height / 2,
}

const stageBox = (top: number): Box => ({ ...STAGE, y: top })
const chainRoute = (top: number) => [{ x: CHAIN_X, y: top + STAGE.height }, { x: CHAIN_X, y: top + STAGE.height + CHAIN_GAP }]
const chainLabelAt = (top: number) => ({ x: CHAIN_LABEL_X, y: top + STAGE.height + CHAIN_GAP / 2 + 5 })
const sideRoute = (y: number) => [{ x: EDGE.from, y }, { x: EDGE.to, y }]

export function PipelineTallDiagram({ flow, paths, modelRan, scored, label, chainModelLines, pulses }: DiagramProps) {
  const chainModelAt = chainLabelAt(TOP.rules)
  return (
    <svg className="pipeline-diagram pipeline-diagram-tall" viewBox="0 0 360 424" role="img" aria-label={label}>
      <FlowPath path={paths['chain-blocklist']} route={chainRoute(TOP.purchases)} />
      <FlowPath path={paths['chain-rules']} route={chainRoute(TOP.blocklist)} />
      <FlowPath path={paths['chain-model']} route={chainRoute(TOP.rules)} />
      <FlowPath path={paths['blocklist-block']} route={sideRoute(ROW.blocklist)} />
      <FlowPath path={paths['rules-block']} route={sideRoute(ROW.rules)} />
      <FlowPath path={paths['model-review']} route={sideRoute(ROW.review)} />
      <FlowPath path={paths['model-allow']} route={sideRoute(ROW.allow)} />
      <Stage box={stageBox(TOP.purchases)} title="Purchases" />
      <Stage box={stageBox(TOP.blocklist)} title="1. Blocklist" />
      <Stage box={stageBox(TOP.rules)} title="2. Rules" />
      <ModelStage box={{ ...STAGE, y: TOP.model, height: MODEL_HEIGHT }} modelRan={modelRan} scored={scored} />
      <PathLabel path={paths['chain-blocklist']} at={chainLabelAt(TOP.purchases)} anchor="start" />
      <PathLabel path={paths['chain-rules']} at={chainLabelAt(TOP.blocklist)} anchor="start" />
      <FlowLabel at={{ ...chainModelAt, y: chainModelAt.y - 8 }} anchor="start" lines={chainModelLines} />
      <SideLabel y={ROW.blocklist} path={paths['blocklist-block']} />
      <SideLabel y={ROW.rules} path={paths['rules-block']} />
      <SideLabel y={ROW.review} path={paths['model-review']} caption={`${number.format(flow.modelEscalated)} escalated`} />
      <SideLabel y={ROW.allow} path={paths['model-allow']} caption={`${number.format(flow.modelCleared)} cleared`} />
      <OutcomeBin outcome="block" pulse={pulses.block} name="Block" count={flow.block} box={{ ...BIN, y: TOP.blocklist, height: TOP.rules + STAGE.height - TOP.blocklist }} />
      <OutcomeBin outcome="review" pulse={pulses.review} name="Review" count={flow.review} box={{ ...BIN, y: TOP.model }} />
      <OutcomeBin outcome="allow" pulse={pulses.allow} name="Allow" count={flow.allow} box={{ ...BIN, y: TOP.model + MODEL_HEIGHT - BIN.height }} />
    </svg>
  )
}

function SideLabel({ y, path, caption }: { y: number; path: PipelinePath; caption?: string }) {
  const half = path.width / 2
  return (
    <>
      <PathLabel path={path} at={{ x: EDGE.middle, y: y - half - 6 }} />
      {caption && <FlowCaption at={{ x: EDGE.middle, y: y + half + 15 }} text={caption} />}
    </>
  )
}
