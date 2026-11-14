import type { BlocklistPage } from '../api'
import { Button } from '../ui/Button'

const number = new Intl.NumberFormat()

export function BlocklistPages({ pagination, busy, onPage }: {
  pagination: BlocklistPage['pagination']
  busy: boolean
  onPage: (page: number) => void
}) {
  const { page, total_pages: totalPages, total_items: totalItems } = pagination
  const firstVisible = Math.max(1, Math.min(page - 2, totalPages - 4))
  const nearby = Array.from({ length: Math.min(5, totalPages) }, (_, index) => firstVisible + index)

  function goTo(target: number) {
    if (!busy && target >= 1 && target <= totalPages && target !== page) onPage(target)
  }

  return (
    <nav className="pager" aria-label="Blocklist pagination">
      <span role="status">Page {number.format(page)} of {number.format(totalPages)} · {number.format(totalItems)} matching entries</span>
      {totalPages > 1 && (
        <div className="pager-controls">
          <div className="pager-buttons">
            <Button variant="secondary" size="sm" aria-disabled={busy || page === 1} onClick={() => goTo(1)}>First</Button>
            <Button variant="secondary" size="sm" aria-disabled={busy || page === 1} onClick={() => goTo(page - 1)}>Previous</Button>
          </div>
          <div className="pager-buttons">
            {nearby.map(target => (
              <Button key={target} variant={target === page ? 'primary' : 'secondary'} size="sm"
                aria-label={`Page ${number.format(target)}`} aria-current={target === page ? 'page' : undefined}
                aria-disabled={busy || target === page} onClick={() => goTo(target)}>{number.format(target)}</Button>
            ))}
          </div>
          <div className="pager-buttons">
            <Button variant="secondary" size="sm" aria-disabled={busy || page === totalPages} onClick={() => goTo(page + 1)}>Next</Button>
            <Button variant="secondary" size="sm" aria-disabled={busy || page === totalPages} onClick={() => goTo(totalPages)}>Last</Button>
          </div>
        </div>
      )}
    </nav>
  )
}
