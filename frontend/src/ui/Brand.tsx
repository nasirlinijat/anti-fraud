import wordmark from '../assets/xsolla-wordmark.svg'

export function Brand({ compact = false }: { compact?: boolean }) {
  return (
    <div className={compact ? 'console-brand compact' : 'console-brand'}>
      <img className="brand-logo" src={wordmark} alt="Xsolla" width="141" height="38" />
      <span className="brand-product">Anti-Fraud<span className="brand-caption">Decision workspace</span></span>
    </div>
  )
}
