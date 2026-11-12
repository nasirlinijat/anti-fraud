import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { ConsoleTheme } from './ConsoleTheme'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ConsoleTheme><App /></ConsoleTheme>
  </StrictMode>,
)
