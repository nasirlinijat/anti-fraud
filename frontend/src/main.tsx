import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './styles/index.css'
import App from './App.tsx'
import { ConsoleTheme } from './ConsoleTheme'
import { SessionProvider } from './SessionProvider'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ConsoleTheme><SessionProvider><App /></SessionProvider></ConsoleTheme>
  </StrictMode>,
)
