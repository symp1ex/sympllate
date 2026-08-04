import { useState } from 'react'
import type { ClientConfig, Language } from '../api'
import { errorMessage, translate } from '../api'
import { ErrorMessage } from './ErrorMessage'
import { LanguageSelect } from './LanguageSelect'
import { LoadingIndicator } from './LoadingIndicator'

interface Props { config: ClientConfig; languages: Language[] }

export function TranslatorPanel({ config, languages }: Props) {
  const [source, setSource] = useState('auto')
  const [target, setTarget] = useState(config.defaultLanguagePair.first)
  const [sourceText, setSourceText] = useState('')
  const [translatedText, setTranslatedText] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const runTranslation = async () => {
    if (loading) return
    setError('')
    setLoading(true)
    try {
      const result = await translate({ text: sourceText, source, target })
      setTranslatedText(result.text)
    } catch (caught) { setError(errorMessage(caught)) }
    finally { setLoading(false) }
  }

  const swap = () => {
    setSource(target)
    setTarget(source === 'auto' ? config.defaultLanguagePair.second : source)
    setSourceText(translatedText)
    setTranslatedText(sourceText)
    setError('')
  }

  const copyTranslation = async () => {
    setError('')
    try { await window.CopyText(translatedText) }
    catch (caught) { setError(errorMessage(caught)) }
  }

  return (
    <main className="app-shell">
      <header><h1>Ollama Переводчик</h1><p>Локальный перевод без отправки текста в облачный сервис</p></header>
      <section className="language-row">
        <LanguageSelect id="source-language" label="Исходный язык" value={source} languages={languages} onChange={setSource} disabled={loading} />
        <button className="swap" onClick={swap} disabled={loading} aria-label="Поменять языки и тексты местами">⇄</button>
        <LanguageSelect id="target-language" label="Целевой язык" value={target} languages={languages} onChange={setTarget} disabled={loading} allowAuto={false} />
      </section>
      <section className="text-grid">
        <label><span>Исходный текст</span><textarea value={sourceText} maxLength={config.maxInputCharacters} onChange={(event) => setSourceText(event.target.value)} onKeyDown={(event) => { if (event.ctrlKey && event.key === 'Enter') { event.preventDefault(); void runTranslation() } }} placeholder="Введите текст…" disabled={loading} /></label>
        <label><span>Перевод</span><textarea value={translatedText} readOnly placeholder="Здесь появится перевод" /></label>
      </section>
      <div className="actions">
        <button className="primary" onClick={() => void runTranslation()} disabled={loading || !sourceText.trim()}>{loading ? 'Переводим…' : 'Перевести'}</button>
        <button onClick={() => void copyTranslation()} disabled={!translatedText}>Копировать перевод</button>
        {loading && <LoadingIndicator />}
        <span className="hint">Ctrl+Enter</span>
      </div>
      {error && <ErrorMessage message={error} />}
    </main>
  )
}
