import { useEffect, useRef } from 'react'
import { Button } from '../ui/Button'
import type { ReviewAction } from './useDecisions'

interface DecisionDialogProps {
  confirmation: ReviewAction | null
  blockDevice: boolean
  onBlockDevice: (block: boolean) => void
  onDismiss: () => void
  onConfirm: (action: ReviewAction) => void
}

export function DecisionDialog({ confirmation, blockDevice, onBlockDevice, onDismiss, onConfirm }: DecisionDialogProps) {
  const dialog = useRef<HTMLDialogElement>(null)
  const approving = confirmation?.decision === 'approve'

  useEffect(() => {
    if (confirmation && !dialog.current?.open) dialog.current?.showModal()
  }, [confirmation])

  return (
    <dialog className="dialog review-confirmation" ref={dialog} aria-labelledby="review-confirmation-title"
      onClose={event => {
        const confirmed = event.currentTarget.returnValue === 'confirm'
        event.currentTarget.returnValue = ''
        onDismiss()
        if (confirmed && confirmation) onConfirm(confirmation)
      }}>
      <h2 id="review-confirmation-title">{approving ? 'Approve' : 'Reject'} this purchase?</h2>
      <div className="dialog-body">
        <p>Transaction {confirmation?.item.transaction_id}. This decision is final.</p>
        {confirmation?.decision === 'reject' && confirmation.item.device_id !== null && (
          <label className="check">
            <input type="checkbox" checked={blockDevice} onChange={event => onBlockDevice(event.target.checked)} />
            <span>Also block this purchase&apos;s device ({confirmation.item.device_id})</span>
          </label>
        )}
      </div>
      <form method="dialog" className="dialog-actions">
        <Button type="submit" value="cancel" variant="secondary" autoFocus>Cancel</Button>
        <Button type="submit" value="confirm" variant={approving ? 'primary' : 'danger'}>{approving ? 'Approve' : 'Reject'}</Button>
      </form>
    </dialog>
  )
}
