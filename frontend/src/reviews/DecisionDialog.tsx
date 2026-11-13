import { useEffect, useId, useRef } from 'react'
import type { Review, ReviewBlocks } from '../api'
import { Button } from '../ui/Button'
import type { ReviewAction } from './useDecisions'

interface DecisionDialogProps {
  confirmation: ReviewAction | null
  blocks: ReviewBlocks
  onBlocksChange: (blocks: ReviewBlocks) => void
  onDismiss: () => void
  onConfirm: (action: ReviewAction) => void
}

export function DecisionDialog({ confirmation, blocks, onBlocksChange, onDismiss, onConfirm }: DecisionDialogProps) {
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
        {confirmation?.decision === 'reject' && (
          <BlockChoices item={confirmation.item} blocks={blocks} onChange={onBlocksChange} />
        )}
      </div>
      <form method="dialog" className="dialog-actions">
        <Button type="submit" value="cancel" variant="secondary" autoFocus>Cancel</Button>
        <Button type="submit" value="confirm" variant={approving ? 'primary' : 'danger'}>{approving ? 'Approve' : 'Reject'}</Button>
      </form>
    </dialog>
  )
}

// The Review type carries no card token, so the card choice names no value.
function BlockChoices({ item, blocks, onChange }: {
  item: Review
  blocks: ReviewBlocks
  onChange: (blocks: ReviewBlocks) => void
}) {
  const hintId = useId()
  const choose = (key: keyof ReviewBlocks, checked: boolean) => onChange({ ...blocks, [key]: checked })
  return (
    <>
      <fieldset className="review-blocks" aria-describedby={hintId}>
        <legend>Also add to the blocklist</legend>
        {item.device_id !== null && (
          <BlockChoice label={`This purchase's device (${item.device_id})`} checked={blocks.device}
            onChange={checked => choose('device', checked)} />
        )}
        <BlockChoice label="This purchase's card" checked={blocks.card} onChange={checked => choose('card', checked)} />
        <BlockChoice label={`This purchase's player (${item.player_id})`} checked={blocks.player}
          onChange={checked => choose('player', checked)} />
      </fieldset>
      <p className="field-hint" id={hintId}>Every later purchase from a blocked value is blocked.</p>
    </>
  )
}

function BlockChoice({ label, checked, onChange }: { label: string; checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <label className="check">
      <input type="checkbox" checked={checked} onChange={event => onChange(event.target.checked)} />
      <span>{label}</span>
    </label>
  )
}
