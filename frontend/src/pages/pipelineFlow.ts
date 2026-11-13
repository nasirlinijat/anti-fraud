import type { Stats } from '../api.ts'

// The dashboard statistics and the recent pipeline counts both have this shape.
export interface FlowCounts {
  outcomes: Stats['outcomes']
  blocklist_blocks: number
  model: Pick<Stats['model'], 'flagged' | 'cleared'>
}

export interface PipelineFlow {
  entered: number
  passedBlocklist: number
  blocklistBlocks: number
  rulesBlocks: number
  rulesReview: number
  rulesAllow: number
  modelCleared: number
  modelEscalated: number
  allow: number
  review: number
  block: number
}

// A response from a period boundary can be off by one, so derived counts never go below zero.
function atLeastZero(value: number): number {
  return Math.max(0, value)
}

// The model has no block count: it can only move a purchase between review and allow.
export function pipelineFlow(stats: FlowCounts): PipelineFlow {
  const { outcomes, model } = stats
  return {
    entered: outcomes.total,
    passedBlocklist: atLeastZero(outcomes.total - stats.blocklist_blocks),
    blocklistBlocks: atLeastZero(stats.blocklist_blocks),
    rulesBlocks: atLeastZero(outcomes.block - stats.blocklist_blocks),
    rulesReview: atLeastZero(outcomes.review - model.flagged + model.cleared),
    rulesAllow: atLeastZero(outcomes.allow - model.cleared + model.flagged),
    modelCleared: model.cleared,
    modelEscalated: model.flagged,
    allow: outcomes.allow,
    review: outcomes.review,
    block: outcomes.block,
  }
}
