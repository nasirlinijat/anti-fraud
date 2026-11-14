import { useState } from 'react'
import { exportDecisions, type Period } from '../api'
import { Button } from '../ui/Button'

const number = new Intl.NumberFormat('en-US')

function save(file: Blob, filename: string) {
  const url = URL.createObjectURL(file)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.append(link)
  link.click()
  link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}

export function ExportDecisions({ period, periodName }: { period: Period; periodName: string }) {
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState<string | null>(null)

  const download = async () => {
    setBusy(true)
    setMessage(null)
    try {
      const result = await exportDecisions(period)
      save(result.file, result.filename)
      setMessage(result.truncated
        ? `Downloaded the newest ${number.format(result.rows)} decisions. The period has more; choose a shorter period for the rest.`
        : `Downloaded ${number.format(result.rows)} ${result.rows === 1 ? 'decision' : 'decisions'} as ${result.filename}.`)
    } catch (error) {
      setMessage(`The export failed: ${error instanceof Error ? error.message : 'unknown error'}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="export-decisions">
      <Button variant="secondary" size="sm" onClick={() => void download()} disabled={busy}
        aria-label={`Export the decisions of ${periodName} as CSV`}>
        {busy ? 'Exporting…' : 'Export CSV'}
      </Button>
      <p className={message ? 'muted small' : 'visually-hidden'} role="status">{message}</p>
    </div>
  )
}
