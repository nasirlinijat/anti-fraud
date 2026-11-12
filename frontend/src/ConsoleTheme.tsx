import { useEffect, type ReactNode } from 'react'
import { XUIProvider, useDesignSystem, getTypographyTokens } from '@xsolla/xui-core'
import { createGlobalStyle } from 'styled-components'

export function ConsoleTheme({ children }: { children: ReactNode }) {
  const mode = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  return <XUIProvider initialMode={mode} initialProductContext="b2b"><SystemTheme />{children}</XUIProvider>
}

function SystemTheme() {
  const { mode, setMode, theme } = useDesignSystem()
  useEffect(() => {
    const preference = window.matchMedia('(prefers-color-scheme: dark)')
    const sync = () => setMode(preference.matches ? 'dark' : 'light')
    sync()
    preference.addEventListener('change', sync)
    return () => preference.removeEventListener('change', sync)
  }, [setMode])
  const colors = theme.colors
  const type = getTypographyTokens('b2b')
  return <ThemeVariables $mode={mode} $tokens={{
    '--bg': colors.background.secondary, '--surface': colors.background.primary,
    '--surface-subtle': colors.background.secondary, '--text': colors.content.primary,
    '--muted': colors.content.secondary, '--border': colors.border.secondary,
    '--control-border': colors.content.tertiary, '--accent': colors.control.brand.primary.bg,
    '--accent-text': colors.control.brand.text.primary, '--focus': colors.content.primary,
    '--allow': colors.content.success.primary, '--review': colors.content.warning.primary,
    '--block': colors.control.alert.text.tertiary, '--error-bg': colors.background.alert.secondary,
    '--allow-bg': colors.background.success.secondary, '--review-bg': colors.background.warning.secondary,
    '--block-bg': colors.background.alert.secondary, '--chart-allow': colors.content.success.primary,
    '--chart-review': colors.content.warning.primary, '--chart-block': colors.content.alert.primary,
    '--font-body': theme.fonts.body, '--font-heading': theme.fonts.heading,
    '--space-xs': `${theme.spacing.xs}px`, '--space-sm': `${theme.spacing.sm}px`,
    '--space-md': `${theme.spacing.md}px`, '--space-lg': `${theme.spacing.lg}px`, '--space-xl': `${theme.spacing.xl}px`,
    '--type-body': `${type.basic['body-md'].fontSize / 16}rem`,
    '--type-label': `${type.basic['body-sm'].fontSize / 16}rem`,
    '--type-note': `${type.basic['body-xs'].fontSize / 16}rem`,
    '--type-number': `${type.heading.h3.fontSize / 16}rem`,
    '--radius': `${theme.shape.cell.borderRadius}px`,
  }} />
}

const ThemeVariables = createGlobalStyle<{ $mode: string; $tokens: Record<string, string> }>`
  :root {
    ${props => Object.entries(props.$tokens).map(([key, value]) => `${key}: ${value};`).join('\n')}
    color-scheme: ${props => props.$mode};
    font-family: var(--font-body);
  }
`
