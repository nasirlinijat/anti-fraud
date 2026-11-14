const CASE_ADDRESS = '#/reviews/'

export const QUEUE_ADDRESS = '#/reviews'

export function caseAddress(purchaseId: string): string {
  return CASE_ADDRESS + encodeURIComponent(purchaseId)
}

export function caseInAddress(hash: string): string | null {
  if (!hash.startsWith(CASE_ADDRESS)) return null
  try {
    return decodeURIComponent(hash.slice(CASE_ADDRESS.length)) || null
  } catch {
    return null
  }
}
