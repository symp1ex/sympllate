import { useEffect, useState } from 'react'
import type { ClientConfig, Language } from './api'
import { errorMessage } from './api'
import { ErrorMessage } from './components/ErrorMessage'
import { LoadingIndicator } from './components/LoadingIndicator'
import { TranslationPopup } from './components/TranslationPopup'
import { TranslatorPanel } from './components/TranslatorPanel'

type ReadyState = { mode: 'main' | 'popup'; config: ClientConfig; languages: Language[] }

export default function App() {
  const [ready, setReady] = useState<ReadyState | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let active = true
    Promise.all([window.GetWindowMode(), window.GetConfig(), window.GetSupportedLanguages()])
      .then(([mode, config, languages]) => { if (active) setReady({ mode, config, languages }) })
      .catch((caught: unknown) => { if (active) setError(errorMessage(caught)) })
    return () => { active = false }
  }, [])
  if (error) return <main className="centered"><ErrorMessage message={error} /></main>
  if (!ready) return <main className="centered"><LoadingIndicator /></main>
  return ready.mode === 'popup' ? <TranslationPopup languages={ready.languages} config={ready.config} /> : <TranslatorPanel config={ready.config} languages={ready.languages} />
}
