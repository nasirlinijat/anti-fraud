import { useEffect, useId, useRef, type ReactNode } from 'react'
import type { Decision, Review, ReviewBlocks } from '../api'
import { Button } from '../ui/Button'
import type { BatchAction } from './useBatchReviews'
import type { ReviewAction } from './useDecisions'

interface DecisionDialogProps<T> {
  confirmation: T | null
  blocks: ReviewBlocks
  onBlocksChange: (blocks: ReviewBlocks) => void
  onDismiss: () => void
  onConfirm: (action: T) => void
}

export function DecisionDialog({ confirmation, blocks, onBlocksChange, onDismiss, onConfirm }: DecisionDialogProps<ReviewAction>) {
  const decision = confirmation?.decision ?? null
  return (
    <ConfirmationShell decision={decision} title={`${verb(decision)} this purchase?`} onDismiss={onDismiss}
      onConfirm={() => { if (confirmation) onConfirm(confirmation) }}>
      <p>Transaction {confirmation?.item.transaction_id}. This decision is final.</p>
      {confirmation?.decision === 'reject' && (
        <BlockChoices labels={singleBlockLabels(confirmation.item)} blocks={blocks} onChange={onBlocksChange} />
      )}
    </ConfirmationShell>
  )
}

const batchBlockLabels: BlockLabels = {
  device: "Each purchase's device",
  card: "Each purchase's card",
  player: "Each purchase's player",
}

// The transaction list scrolls inside the dialog, so a long selection keeps the buttons in view.
export function BatchDecisionDialog({ confirmation, blocks, onBlocksChange, onDismiss, onConfirm }: DecisionDialogProps<BatchAction>) {
  const decision = confirmation?.decision ?? null
  const items = confirmation?.items ?? []
  const noun = items.length === 1 ? 'purchase' : 'purchases'
  return (
    <ConfirmationShell decision={decision} title={`${verb(decision)} ${items.length} ${noun}?`} onDismiss={onDismiss}
      onConfirm={() => { if (confirmation) onConfirm(confirmation) }}>
      <div className="batch-transactions" role="region" tabIndex={0} aria-label="Transactions">
        <ul>{items.map(item => <li key={item.purchase_id}>{item.transaction_id}</li>)}</ul>
      </div>
      <p>These decisions are final.</p>
      {decision === 'reject' && <BlockChoices labels={batchBlockLabels} blocks={blocks} onChange={onBlocksChange} />}
    </ConfirmationShell>
  )
}

function verb(decision: Decision | null): string {
  return decision === 'approve' ? 'Approve' : 'Reject'
}

interface ShellProps {
  // The dialog opens when a decision is chosen.
  decision: Decision | null
  title: string
  onDismiss: () => void
  onConfirm: () => void
  children: ReactNode
}

function ConfirmationShell({ decision, title, onDismiss, onConfirm, children }: ShellProps) {
  const dialog = useRef<HTMLDialogElement>(null)
  const titleId = useId()

  useEffect(() => {
    if (decision && !dialog.current?.open) dialog.current?.showModal()
  }, [decision])

  return (
    <dialog className="dialog review-confirmation" ref={dialog} aria-labelledby={titleId}
      onClose={event => {
        const confirmed = event.currentTarget.returnValue === 'confirm'
        event.currentTarget.returnValue = ''
        onDismiss()
        if (confirmed) onConfirm()
      }}>
      <h2 id={titleId}>{title}</h2>
      <div className="dialog-body">{children}</div>
      <form method="dialog" className="dialog-actions">
        <Button type="submit" value="cancel" variant="secondary" autoFocus>Cancel</Button>
        <Button type="submit" value="confirm" variant={decision === 'approve' ? 'primary' : 'danger'}>{verb(decision)}</Button>
      </form>
    </dialog>
  )
}

// A missing device label means the purchase has no device to block.
interface BlockLabels {
  device: string | null
  card: string
  player: string
}

// The Review type carries no card token, so the card choice names no value.
function singleBlockLabels(item: Review): BlockLabels {
  return {
    device: item.device_id === null ? null : `This purchase's device (${item.device_id})`,
    card: "This purchase's card",
    player: `This purchase's player (${item.player_id})`,
  }
}

function BlockChoices({ labels, blocks, onChange }: {
  labels: BlockLabels
  blocks: ReviewBlocks
  onChange: (blocks: ReviewBlocks) => void
}) {
  const hintId = useId()
  const choose = (key: keyof ReviewBlocks, checked: boolean) => onChange({ ...blocks, [key]: checked })
  return (
    <>
      <fieldset className="review-blocks" aria-describedby={hintId}>
        <legend>Also add to the blocklist</legend>
        {labels.device !== null && (
          <BlockChoice label={labels.device} checked={blocks.device} onChange={checked => choose('device', checked)} />
        )}
        <BlockChoice label={labels.card} checked={blocks.card} onChange={checked => choose('card', checked)} />
        <BlockChoice label={labels.player} checked={blocks.player} onChange={checked => choose('player', checked)} />
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
