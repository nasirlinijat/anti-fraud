import { useThemePreference } from './themePreferenceContext'

export function ThemeToggle() {
  const { mode, toggle, error } = useThemePreference()
  return <div className="theme-control">
    <button type="button" className="theme-toggle" aria-label="Dark mode" aria-pressed={mode === 'dark'}
      aria-describedby={error ? 'theme-save-status' : undefined}
      title={mode === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'} onClick={toggle}>
      <span className="theme-track" aria-hidden="true"><span className="theme-thumb">
        <svg className="theme-sun" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
          <circle cx="12" cy="12" r="4" /><path d="M12 1v3m0 16v3M1 12h3m16 0h3M4.2 4.2l2.1 2.1m11.4 11.4 2.1 2.1M4.2 19.8l2.1-2.1M17.7 6.3l2.1-2.1" />
        </svg>
        <svg className="theme-moon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
          <path d="M20.5 14.2A9 9 0 0 1 9.8 3.5a9 9 0 1 0 10.7 10.7Z" />
        </svg>
      </span></span>
    </button>
  </div>
}

export function ThemeSaveStatus() {
  const { error } = useThemePreference()
  return error ? <p id="theme-save-status" className="theme-save-status" role="status">{error}</p> : null
}
