import { useEffect, useState } from 'react'
import type { ClientConfig, Language } from './api'
import { errorMessage } from './api'
import { ErrorMessage } from './components/ErrorMessage'
import { LoadingIndicator } from './components/LoadingIndicator'
import { TranslationPopup } from './components/TranslationPopup'
import { TranslatorPanel } from './components/TranslatorPanel'
import { WindowChrome } from './components/WindowChrome'

type WindowMode = 'main' | 'popup'
type ReadyState = { config: ClientConfig; languages: Language[] }

export default function App() {
  const [mode, setMode] = useState<WindowMode | null>(null)
  const [ready, setReady] = useState<ReadyState | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let active = true
    const initialize = async () => {
      try {
        const windowMode = await window.GetWindowMode()
        if (!active) return
        setMode(windowMode)
        const [config, languages] = await Promise.all([window.GetConfig(), window.GetSupportedLanguages()])
        if (active) setReady({ config, languages })
      } catch (caught) {
        if (active) setError(errorMessage(caught))
      }
    }
    void initialize()
    return () => { active = false }
  }, [])

  const content = error
    ? <main className="centered"><ErrorMessage message={error} /></main>
    : ready
      ? mode === 'popup'
        ? <TranslationPopup languages={ready.languages} config={ready.config} />
        : <TranslatorPanel config={ready.config} languages={ready.languages} />
      : <main className="centered"><LoadingIndicator /></main>

  if (!mode) return content
  return (
    <WindowChrome mode={mode}>{content}</WindowChrome>
  )
}
