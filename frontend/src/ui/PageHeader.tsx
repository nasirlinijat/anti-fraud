import type { ReactNode, Ref } from 'react'

interface PageHeaderProps {
  eyebrow: string
  title: string
  lead?: ReactNode
  headingId?: string
  // The heading takes focus when the content it introduces is replaced.
  headingRef?: Ref<HTMLHeadingElement>
  children?: ReactNode
}

export function PageHeader({ eyebrow, title, lead, headingId, headingRef, children }: PageHeaderProps) {
  return (
    <header className="page-header">
      <div className="page-heading">
        <p className="eyebrow">{eyebrow}</p>
        <h1 id={headingId} ref={headingRef} tabIndex={headingRef ? -1 : undefined}>{title}</h1>
        {lead && <p className="page-lead">{lead}</p>}
      </div>
      {children && <div className="page-actions">{children}</div>}
    </header>
  )
}
