import { useId } from 'react'
import { Typography } from '@xsolla/xui-typography'
import { Bar, BarChart, CartesianGrid, Legend, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import type { DecisionSeries } from '../api'
import { outcomeLabels } from '../displayFormat'

const utcTime = new Intl.DateTimeFormat(undefined, { timeZone: 'UTC', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
const utcDay = new Intl.DateTimeFormat(undefined, { timeZone: 'UTC', month: 'short', day: 'numeric' })
const utcDateTime = new Intl.DateTimeFormat(undefined, {
  timeZone: 'UTC', year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
})
const number = new Intl.NumberFormat()

function bucketSize(seconds: number): string {
  const [size, unit] = seconds % 86400 === 0 ? [seconds / 86400, 'day']
    : seconds % 3600 === 0 ? [seconds / 3600, 'hour'] : [seconds / 60, 'minute']
  return `${size} ${unit}${size === 1 ? '' : 's'}`
}

export default function DecisionChart({ series, since, until }: { series: DecisionSeries; since: string; until: string }) {
  const patternId = useId().replaceAll(':', '')
  const first = Date.parse(since)
  const last = Date.parse(until)
  const width = series.bucket_seconds * 1000
  const crossesDays = utcDay.format(new Date(series.buckets[0].start)) !== utcDay.format(new Date(series.buckets.at(-1)?.start ?? until))
  const tickLabel = (start: string) => {
    const at = new Date(start)
    if (series.bucket_seconds >= 86400) return utcDay.format(at)
    return crossesDays ? `${utcDay.format(at)} ${utcTime.format(at)}` : utcTime.format(at)
  }
  const rangeLabel = (start: string) => {
    const bucketStart = Date.parse(start)
    return `${utcDateTime.formatRange(Math.max(first, bucketStart), Math.min(last, bucketStart + width))} UTC`
  }

  return (
    <section className="panel decision-panel">
      <Typography as="h2" variant="h5" marginBottom={16}>Decisions over time</Typography>
      <figure className="decision-figure">
        <figcaption className="muted small">One bar per {bucketSize(series.bucket_seconds)}. Times are UTC.</figcaption>
        <ResponsiveContainer width="100%" height={260}>
          <BarChart data={series.buckets} title="Decisions over time" margin={{ top: 12, right: 12, bottom: 4, left: 0 }}>
            <defs>
              <pattern id={`${patternId}-review`} width="6" height="6" patternUnits="userSpaceOnUse">
                <rect width="6" height="6" fill="var(--chart-review)" /><path d="M0 6L6 0" stroke="var(--surface)" strokeWidth="1" />
              </pattern>
              <pattern id={`${patternId}-block`} width="6" height="6" patternUnits="userSpaceOnUse">
                <rect width="6" height="6" fill="var(--chart-block)" /><circle cx="3" cy="3" r="1" fill="var(--surface)" />
              </pattern>
            </defs>
            <CartesianGrid stroke="var(--border)" vertical={false} />
            <XAxis dataKey="start" tickFormatter={tickLabel} minTickGap={18} tick={{ fill: 'var(--muted)', fontSize: 12 }} />
            <YAxis allowDecimals={false} tick={{ fill: 'var(--muted)', fontSize: 12 }} width={48} />
            <Tooltip
              labelFormatter={(start) => typeof start === 'string' ? rangeLabel(start) : ''}
              wrapperStyle={{ maxWidth: '100%' }}
              contentStyle={{ backgroundColor: 'var(--surface)', borderColor: 'var(--border)', whiteSpace: 'normal' }}
              labelStyle={{ color: 'var(--text)' }}
              itemStyle={{ color: 'var(--text)' }}
              cursor={{ fill: 'var(--border)', opacity: 0.45 }}
            />
            <Legend itemSorter={null} formatter={(value: string) => <span className="decision-legend-label">{value}</span>} />
            <Bar dataKey="allow" name={outcomeLabels.allow} stackId="outcomes" fill="var(--chart-allow)" stroke="var(--surface)" strokeWidth={1} isAnimationActive={false} />
            <Bar dataKey="review" name={outcomeLabels.review} stackId="outcomes" fill={`url(#${patternId}-review)`} stroke="var(--surface)" strokeWidth={1} isAnimationActive={false} />
            <Bar dataKey="block" name={outcomeLabels.block} stackId="outcomes" fill={`url(#${patternId}-block)`} stroke="var(--surface)" strokeWidth={1} isAnimationActive={false} />
          </BarChart>
        </ResponsiveContainer>
      </figure>
      <details className="decision-details">
        <summary>View decision counts by time</summary>
        <div className="decision-table" tabIndex={0} role="region" aria-label="Decision counts by time">
          <table>
            <caption>Decision counts by time (UTC)</caption>
            <thead>
              <tr>
                <th scope="col">Time bucket</th>
                <th scope="col">{outcomeLabels.allow}</th>
                <th scope="col">{outcomeLabels.review}</th>
                <th scope="col">{outcomeLabels.block}</th>
              </tr>
            </thead>
            <tbody>
              {series.buckets.map((bucket) => (
                <tr key={bucket.start}>
                  <th scope="row">{rangeLabel(bucket.start)}</th>
                  <td className="num">{number.format(bucket.allow)}</td>
                  <td className="num">{number.format(bucket.review)}</td>
                  <td className="num">{number.format(bucket.block)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </section>
  )
}
