import type { SimulationSettings, SimulationStatus } from './api.ts'
import { ApiError } from './request.ts'

export interface SimulationDraft {
  scenario: string
  mode: 'live' | 'backfill'
  seed: string
  rate: string
  count: string
}
export const defaultSimulationDraft: SimulationDraft = {
  scenario: 'mixed',
  mode: 'live',
  seed: '1',
  rate: '5',
  count: '500',
}
export function isActive(status: SimulationStatus): boolean {
  return status === 'starting' || status === 'running' || status === 'stopping'
}
export function scenarioLabel(name: string): string {
  return name
    .split('_')
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ')
}
export function validateSimulation(
  draft: SimulationDraft,
  available: string[],
): { fields: Record<string, string>; settings?: Omit<SimulationSettings, 'request_id'> } {
  const fields: Record<string, string> = {}
  const integer = (text: string, name: string, min: number, max: number) => {
    const value = Number(text)
    if (!/^\d+$/.test(text.trim()) || !Number.isSafeInteger(value) || value < min || value > max)
      fields[name] = `Enter a whole number from ${min} to ${max}.`
    return value
  }
  const seed = integer(draft.seed, 'seed', 0, Number.MAX_SAFE_INTEGER)
  const rate = Number(draft.rate)
  if (!draft.rate.trim() || !Number.isFinite(rate) || rate < 1 || rate > 50)
    fields.rate = 'Enter a rate from 1 to 50 purchases per second.'
  const count = draft.mode === 'backfill' ? integer(draft.count, 'count', 1, 10000) : 0
  if (draft.mode !== 'live' && draft.mode !== 'backfill') fields.mode = 'Choose Live or Backfill.'
  const scenarios = draft.scenario === 'mixed' ? [...available] : [draft.scenario]
  if (!scenarios.length || scenarios.some((name) => !available.includes(name)))
    fields.scenario = 'Choose an available scenario.'
  return Object.keys(fields).length
    ? { fields }
    : { fields, settings: { mode: draft.mode, seed, rate, count, scenarios } }
}
export function uncertainStart(error: unknown): boolean {
  return (
    !(error instanceof ApiError) ||
    error.kind !== 'http' ||
    error.status === 408 ||
    error.status === 429 ||
    (error.status ?? 0) >= 500
  )
}
