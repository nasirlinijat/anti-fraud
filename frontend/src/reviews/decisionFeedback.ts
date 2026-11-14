import type { Decision, ReviewBlocks } from '../api'

// A result shown to the reviewer; a failed one is shown as an error.
export type Feedback = { text: string; failed?: boolean; caseId?: string }

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

// Joins names as an English list: "a", "a and b", "a, b and c".
export function joinNames(names: readonly string[]): string {
  if (names.length === 1) return names[0]
  return `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`
}
