import type { Decision, ReviewBlocks } from '../api'

// The fixed order in which blocked values are named.
const blockOrder = ['device', 'card', 'player'] as const

// Names the saved decision and the blocklist entries it added, such as
// "Rejected tx-1 and blocked its device, card and player."
export function decisionFeedback(transactionId: string, decision: Decision, blocked: ReviewBlocks): string {
  const verb = decision === 'approve' ? 'Approved' : 'Rejected'
  const names = blockOrder.filter(name => blocked[name])
  if (names.length === 0) return `${verb} ${transactionId}.`
  return `${verb} ${transactionId} and blocked its ${joinNames(names)}.`
}

function joinNames(names: readonly string[]): string {
  if (names.length === 1) return names[0]
  return `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`
}
