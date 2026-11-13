import type { Outcome } from '../api.ts'
import { pipelineFlow, type FlowCounts } from './pipelineFlow.ts'
import { pathCounts, type PathId } from './pipelinePaths.ts'

// Speeds are in diagram units per second.
export const MIN_FLOW_SPEED = 12
export const MAX_FLOW_SPEED = 160
const RATE_AT_MAX_SPEED = 1000
const EASE_MS = 600
const STOP_SPEED = 0.5
const THIN_FLOW_WIDTH = 10

const number = new Intl.NumberFormat()

export interface RecentFlow {
  decisions: number
  paths: Record<PathId, number>
  bins: Record<Outcome, number>
}

// The recent counts go through the same split as the dashboard statistics, so a
// path moves exactly when its stage sent purchases along it.
export function recentFlow(recent: FlowCounts): RecentFlow {
  const flow = pipelineFlow(recent)
  return {
    decisions: flow.entered,
    paths: pathCounts(flow),
    bins: { allow: flow.allow, review: flow.review, block: flow.block },
  }
}

// The logarithm keeps one decision a second and hundreds a second visibly apart.
// The minimum keeps a single decision visible and the maximum keeps a flood readable.
export function flowSpeed(count: number, intervalSeconds: number): number {
  if (count <= 0 || intervalSeconds <= 0) return 0
  const scale = Math.log1p(count / intervalSeconds) / Math.log1p(RATE_AT_MAX_SPEED)
  return Math.min(MAX_FLOW_SPEED, MIN_FLOW_SPEED + (MAX_FLOW_SPEED - MIN_FLOW_SPEED) * scale)
}

export function flowSpeeds(paths: Record<PathId, number>, intervalSeconds: number): Record<PathId, number> {
  const speeds = { ...paths }
  for (const id of Object.keys(speeds) as PathId[]) speeds[id] = flowSpeed(paths[id], intervalSeconds)
  return speeds
}

// The speed approaches its target exponentially, so a new poll changes it within
// about a second without a jump. A path whose traffic stopped comes to rest.
export function easeSpeed(current: number, target: number, elapsedMs: number): number {
  const next = target + (current - target) * Math.exp(-elapsedMs / EASE_MS)
  return target === 0 && next < STOP_SPEED ? 0 : next
}

// Dots fade in and out with their speed, so a path at rest looks as it does without motion.
export function flowOpacity(speed: number): number {
  return Math.min(1, speed / MIN_FLOW_SPEED)
}

export interface FlowDashes {
  width: number
  pattern: string
  thin: boolean
}

// On a thin path the dots are wider than the line, so that one decision still shows.
export function flowDashes(pathWidth: number): FlowDashes {
  const width = Math.max(4, pathWidth * 0.32)
  const cycle = Math.max(30, pathWidth * 1.4)
  return { width, pattern: `${width} ${cycle - width}`, thin: pathWidth < THIN_FLOW_WIDTH }
}

export function binPulses(bins: Record<Outcome, number>, sequence: number): Partial<Record<Outcome, number>> {
  const pulses: Partial<Record<Outcome, number>> = {}
  for (const outcome of Object.keys(bins) as Outcome[]) {
    if (bins[outcome] > 0) pulses[outcome] = sequence
  }
  return pulses
}

// A null count is the first read, which only marks where counting starts.
export function liveLabel(decisions: number | null, intervalSeconds: number): string {
  if (decisions === null) return 'Live'
  if (decisions === 0) return 'Live · no traffic'
  const noun = decisions === 1 ? 'decision' : 'decisions'
  return `Live · ${number.format(decisions)} ${noun} in the last ${intervalSeconds} s`
}
