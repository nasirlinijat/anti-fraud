const PURCHASE_ADDRESS = '#/purchases/'

export function purchaseAddress(purchaseId: string): string {
  return PURCHASE_ADDRESS + encodeURIComponent(purchaseId)
}

export function purchaseInAddress(hash: string): string | null {
  if (!hash.startsWith(PURCHASE_ADDRESS)) return null
  try {
    return decodeURIComponent(hash.slice(PURCHASE_ADDRESS.length)) || null
  } catch {
    return null
  }
}
