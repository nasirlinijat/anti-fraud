import { FlowCaption, FlowLabel, FlowPath, ModelStage, OutcomeBin, PathLabel, Stage, type Box, type DiagramProps } from './PipelineShapes'
import type { PipelinePath } from './pipelinePaths'

const number = new Intl.NumberFormat()

const MAIN_Y = 130
const STAGE = { width: 96, height: 48, y: MAIN_Y - 24 }
const STAGE_X = { purchases: 2, blocklist: 152, rules: 302, model: 452 }
const MODEL = { x: STAGE_X.model, width: 104, y: MAIN_Y - 42, height: 84 }
const BIN = { x: 676, width: 82 }
const LANE = { review: 40, rulesBlock: 210, blocklistBlock: 238 }
const TERMINAL_LABEL_X = (MODEL.x + MODEL.width + BIN.x) / 2

const stageBox = (x: number): Box => ({ ...STAGE, x })
const centre = (x: number) => x + STAGE.width / 2
const chainRoute = (fromX: number, toX: number) => [{ x: fromX + STAGE.width, y: MAIN_Y }, { x: toX, y: MAIN_Y }]
const chainLabelX = (fromX: number, toX: number) => (fromX + STAGE.width + toX) / 2
const dropRoute = (x: number, lane: number) => [{ x: centre(x), y: STAGE.y + STAGE.height }, { x: centre(x), y: lane }, { x: BIN.x, y: lane }]

export function PipelineWideDiagram({ flow, paths, modelRan, scored, label, chainModelLines }: DiagramProps) {
  const reviewX = MODEL.x + MODEL.width - 20
  const allow = paths['model-allow']
  const review = paths['model-review']
  return (
    <svg className="pipeline-diagram pipeline-diagram-wide" viewBox="0 0 760 262" role="img" aria-label={label}>
      <FlowPath path={paths['chain-blocklist']} route={chainRoute(STAGE_X.purchases, STAGE_X.blocklist)} />
      <FlowPath path={paths['chain-rules']} route={chainRoute(STAGE_X.blocklist, STAGE_X.rules)} />
      <FlowPath path={paths['chain-model']} route={chainRoute(STAGE_X.rules, STAGE_X.model)} />
      <FlowPath path={paths['blocklist-block']} route={dropRoute(STAGE_X.blocklist, LANE.blocklistBlock)} />
      <FlowPath path={paths['rules-block']} route={dropRoute(STAGE_X.rules, LANE.rulesBlock)} />
      <FlowPath path={review} route={[{ x: reviewX, y: MODEL.y }, { x: reviewX, y: LANE.review }, { x: BIN.x, y: LANE.review }]} />
      <FlowPath path={allow} route={[{ x: MODEL.x + MODEL.width, y: MAIN_Y }, { x: BIN.x, y: MAIN_Y }]} />
      <Stage box={stageBox(STAGE_X.purchases)} title="Purchases" />
      <Stage box={stageBox(STAGE_X.blocklist)} title="1. Blocklist" />
      <Stage box={stageBox(STAGE_X.rules)} title="2. Rules" />
      <ModelStage box={MODEL} modelRan={modelRan} scored={scored} />
      <PathLabel path={paths['chain-blocklist']} at={{ x: chainLabelX(STAGE_X.purchases, STAGE_X.blocklist), y: STAGE.y - 8 }} />
      <PathLabel path={paths['chain-rules']} at={{ x: chainLabelX(STAGE_X.blocklist, STAGE_X.rules), y: STAGE.y - 8 }} />
      <FlowLabel at={{ x: chainLabelX(STAGE_X.rules, STAGE_X.model), y: MODEL.y - 24 }} lines={chainModelLines} />
      <LaneLabel x={(centre(STAGE_X.blocklist) + centre(STAGE_X.rules)) / 2} lane={LANE.blocklistBlock} path={paths['blocklist-block']} />
      <LaneLabel x={TERMINAL_LABEL_X} lane={LANE.rulesBlock} path={paths['rules-block']} />
      <LaneLabel x={TERMINAL_LABEL_X} lane={LANE.review} path={review} caption={`${number.format(flow.modelEscalated)} escalated`} />
      <LaneLabel x={TERMINAL_LABEL_X} lane={MAIN_Y} path={allow} caption={`${number.format(flow.modelCleared)} cleared`} />
      <OutcomeBin outcome="review" name="Review" count={flow.review} box={{ ...BIN, y: LANE.review - 24, height: 48 }} />
      <OutcomeBin outcome="allow" name="Allow" count={flow.allow} box={{ ...BIN, y: MAIN_Y - 24, height: 48 }} />
      <OutcomeBin outcome="block" name="Block" count={flow.block} box={{ ...BIN, y: LANE.rulesBlock - 18, height: 64 }} />
    </svg>
  )
}

function LaneLabel({ x, lane, path, caption }: { x: number; lane: number; path: PipelinePath; caption?: string }) {
  const half = path.width / 2
  return (
    <>
      <PathLabel path={path} at={{ x, y: lane - half - 7 }} />
      {caption && <FlowCaption at={{ x, y: lane + half + 17 }} text={caption} />}
    </>
  )
}
