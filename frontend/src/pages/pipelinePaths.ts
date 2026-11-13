import type { Outcome } from '../api.ts'
import type { PipelineFlow } from './pipelineFlow.ts'

export const MAX_PATH_WIDTH = 28
export const MIN_PATH_WIDTH = 2

export type PathId =
  | 'chain-blocklist'
  | 'chain-rules'
  | 'chain-model'
  | 'blocklist-block'
  | 'rules-block'
  | 'model-review'
  | 'model-allow'

export interface PipelinePath {
  id: PathId
  outcome: Outcome | null
  count: number
  width: number
  share: string
}

// Widths are linear in the share of purchases entering, so a path twice as wide carries twice as many.
// The minimum keeps a handful of purchases visible next to a path that carries thousands.
export function pathWidth(count: number, entered: number): number {
  if (count <= 0 || entered <= 0) return 0
  return Math.max(MIN_PATH_WIDTH, (MAX_PATH_WIDTH * Math.min(count, entered)) / entered)
}

export function shareOfEntered(count: number, entered: number): string {
  if (entered <= 0) return ''
  if (count <= 0) return '0%'
  const percent = (100 * count) / entered
  if (percent < 0.05) return '<0.1%'
  return `${percent.toFixed(1)}%`
}

// The model has no path to block: its purchases leave only for review or allow.
export function pathCounts(flow: PipelineFlow): Record<PathId, number> {
  return {
    'chain-blocklist': flow.entered,
    'chain-rules': flow.passedBlocklist,
    'chain-model': flow.rulesAllow + flow.rulesReview,
    'blocklist-block': flow.blocklistBlocks,
    'rules-block': flow.rulesBlocks,
    'model-review': flow.review,
    'model-allow': flow.allow,
  }
}

export function pipelinePaths(flow: PipelineFlow): Record<PathId, PipelinePath> {
  const counts = pathCounts(flow)
  const path = (id: PathId, outcome: Outcome | null): PipelinePath => ({
    id, outcome, count: counts[id], width: pathWidth(counts[id], flow.entered), share: shareOfEntered(counts[id], flow.entered),
  })
  return {
    'chain-blocklist': path('chain-blocklist', null),
    'chain-rules': path('chain-rules', null),
    'chain-model': path('chain-model', null),
    'blocklist-block': path('blocklist-block', 'block'),
    'rules-block': path('rules-block', 'block'),
    'model-review': path('model-review', 'review'),
    'model-allow': path('model-allow', 'allow'),
  }
}
