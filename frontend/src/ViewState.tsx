import type { ReactNode } from 'react'

export function EmptyState({ children, announce = false }: { children: ReactNode; announce?: boolean }) {
  return <p className="muted" role={announce ? 'status' : undefined}>{children}</p>
}

export function ErrorState({ children, onRetry, retryLabel = 'Retry', polite = false }: {
  children: ReactNode
  onRetry?: () => void
  retryLabel?: string
  polite?: boolean
}) {
  return (
    <div className="alert view-error">
      <div role={polite ? 'status' : 'alert'}>{children}</div>
      {onRetry && <button type="button" className="secondary" onClick={onRetry}>{retryLabel}</button>}
    </div>
  )
}

export function LoadingSkeleton({ label }: { label: string }) {
  return (
    <div className="loading-skeleton" role="status">
      <span className="muted small">{label}</span>
      <div className="skeleton-lines" aria-hidden="true"><span /><span /><span /></div>
    </div>
  )
}
