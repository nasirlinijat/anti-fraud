import type { QualityReport } from './api'

const percent = new Intl.NumberFormat(undefined, { style: 'percent', maximumFractionDigits: 2 })

export function formatQualityRate(value: number | null): string {
  return value === null ? 'Not applicable' : percent.format(value)
}

export function qualityCoverage(report: QualityReport): string {
  const { coverage, labelled_decisions: labelled } = report
  const excluded = `${coverage.pending} pending; ${coverage.quarantined} quarantined intents excluded.`
  if (coverage.scored_decisions === null) {
    return `${labelled} labelled decisions evaluated. Total run coverage is unknown. ${excluded}`
  }
  const unfinished = coverage.run_status !== 'completed' && coverage.run_status !== 'stopped'
  const incomplete = unfinished || labelled !== coverage.scored_decisions || coverage.pending > 0 || coverage.quarantined > 0
  const state = incomplete ? 'Partial results' : 'All recorded decisions labelled'
  return `${state}: ${labelled} labelled of ${coverage.scored_decisions} recorded decisions. ${excluded}`
}
