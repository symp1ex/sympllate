import { useEffect } from 'react'
import type { ClientConfig, Language } from '../api'
import { ImageBatchPanel } from './ImageBatchPanel'

const ignoreBusyChange = () => {}

export function ImageBatchWindow({ config, languages }: { config: ClientConfig; languages: Language[] }) {
  useEffect(() => {
    const closeOnEscape = (event: KeyboardEvent) => { if (event.key === 'Escape') void window.WindowClose() }
    window.addEventListener('keydown', closeOnEscape)
    return () => window.removeEventListener('keydown', closeOnEscape)
  }, [])

  return (
    <main className="batch-window-shell">
      <ImageBatchPanel languages={languages} defaultTarget={config.defaultLanguagePair.first} disabled={false} onBusyChange={ignoreBusyChange} />
    </main>
  )
}
