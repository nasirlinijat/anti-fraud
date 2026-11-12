import type { ReactNode } from 'react'
import { Button } from './ui/Button'

export function EmptyState({ children, announce = false }: { children: ReactNode; announce?: boolean }) {
  return <p className="empty-state" role={announce ? 'status' : undefined}>{children}</p>
}

export function ErrorState({ children, onRetry, retryLabel = 'Retry', pending = false, polite = false }: {
  children: ReactNode
  onRetry?: () => void
  retryLabel?: string
  pending?: boolean
  polite?: boolean
}) {
  return (
    <div className="alert view-error">
      <div role={polite ? 'status' : 'alert'}>{children}</div>
      {onRetry && (
        <Button variant="secondary" size="sm" aria-disabled={pending}
          onClick={() => { if (!pending) onRetry() }}>
          {pending ? 'Checking…' : retryLabel}
        </Button>
      )}
    </div>
  )
}

type SkeletonShape = 'metrics' | 'chart' | 'table' | 'form' | 'case' | 'result'

function Lines({ count }: { count: number }) {
  return <div className="skeleton-lines">{Array.from({ length: count }, (_, index) => <span key={index} />)}</div>
}

export function LoadingSkeleton({ label, shape }: { label: string; shape: SkeletonShape }) {
  return (
    <div className={`loading-skeleton skeleton-${shape}`} aria-busy="true">
      <span className="muted small">{label}</span>
      <div className="skeleton-content" aria-hidden="true">
        <SkeletonContent shape={shape} />
      </div>
    </div>
  )
}

function SkeletonContent({ shape }: { shape: SkeletonShape }) {
  if (shape === 'metrics') {
    return (
      <div className="tiles">
        {Array.from({ length: 7 }, (_, index) => <div className="tile" key={index}><Lines count={2} /></div>)}
      </div>
    )
  }
  if (shape === 'chart') return <div className="skeleton-chart-frame" />
  if (shape === 'table') {
    return (
      <div className="skeleton-table-body">
        {Array.from({ length: 4 }, (_, index) => <Lines count={5} key={index} />)}
      </div>
    )
  }
  return (
    <>
      <Lines count={3} />
      <Lines count={shape === 'form' ? 3 : 2} />
    </>
  )
}
