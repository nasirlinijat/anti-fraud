import { Eye, EyeOff } from '@xsolla/xui-icons-base'
import { useState, type InputHTMLAttributes } from 'react'

interface PasswordFieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'type'> {
  id: string
  label: string
  hint?: string
  error?: string
}

export function PasswordField({ id, label, hint, error, ...input }: PasswordFieldProps) {
  const [shown, setShown] = useState(false)
  const Icon = shown ? EyeOff : Eye
  const described = [hint && `${id}-hint`, error && `${id}-error`].filter(Boolean).join(' ') || undefined
  return (
    <div className={error ? 'field invalid' : 'field'}>
      <label htmlFor={id}>{label}</label>
      <div className="input-with-action">
        <input id={id} type={shown ? 'text' : 'password'} aria-invalid={error ? true : undefined}
          aria-describedby={described} {...input} />
        <button type="button" className="input-action" aria-pressed={shown} aria-label="Show password"
          onClick={() => setShown(current => !current)}>
          <Icon size={18} aria-hidden />
        </button>
      </div>
      {hint && !error && <span id={`${id}-hint`} className="field-hint">{hint}</span>}
      {error && <span id={`${id}-error`} className="error-text">{error}</span>}
    </div>
  )
}
