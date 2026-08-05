import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import type { JsonSettingObject, JsonSettingValue, SelectSetting } from '../api'
import { errorMessage } from '../api'

interface Props { onBack: () => void }

function isPlainObject(value: JsonSettingValue | undefined): value is JsonSettingObject {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

function isSelectSetting(value: JsonSettingValue | undefined): value is SelectSetting {
  return isPlainObject(value)
    && typeof value.active === 'string'
    && Array.isArray(value.list)
    && value.list.every((item) => typeof item === 'string')
}

function cloneConfig(config: JsonSettingObject) {
  return JSON.parse(JSON.stringify(config)) as JsonSettingObject
}

function settingTitle(value: string) {
  return value.replaceAll('_', ' ')
}

export function SettingsPanel({ onBack }: Props) {
  const [config, setConfig] = useState<JsonSettingObject | null>(null)
  const [status, setStatus] = useState('Loading settings...')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)

  const loadConfig = async () => {
    setLoading(true)
    try {
      setConfig(await window.GetSettingsConfig())
      setStatus('Settings loaded')
    } catch (caught) {
      setConfig(null)
      setStatus(`Loading error: ${errorMessage(caught)}`)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { void loadConfig() }, [])

  const updateConfig = (updater: (draft: JsonSettingObject) => void) => {
    setConfig((current) => {
      if (!current) return current
      const next = cloneConfig(current)
      updater(next)
      return next
    })
  }

  const getSetting = (root: JsonSettingObject, path: string[]): JsonSettingValue | undefined => {
    let current: JsonSettingValue = root
    for (const key of path) {
      if (!isPlainObject(current)) return undefined
      current = current[key]
    }
    return current
  }

  const setSetting = (root: JsonSettingObject, path: string[], value: string | number | boolean) => {
    let current: JsonSettingValue = root
    for (let index = 0; index < path.length - 1; index += 1) {
      if (!isPlainObject(current)) return
      current = current[path[index]]
    }
    if (isPlainObject(current)) current[path[path.length - 1]] = value
  }

  const updateSelect = (path: string[], active: string) => {
    updateConfig((draft) => {
      const setting = getSetting(draft, path)
      if (isSelectSetting(setting)) setting.active = active
    })
  }

  const updatePrimitive = (path: string[], value: string | number | boolean) => {
    updateConfig((draft) => setSetting(draft, path, value))
  }

  const saveConfig = async () => {
    if (!config || saving) return
    setSaving(true)
    try {
      await window.SaveSettingsConfig(config)
      setStatus('Settings saved. Restarting application...')
    } catch (caught) {
      setStatus(`Saving error: ${errorMessage(caught)}`)
      setSaving(false)
    }
  }

  const renderPrimitive = (path: string[], label: string, value: JsonSettingValue) => {
    const key = path.join('.')
    if (typeof value === 'boolean') {
      return (
        <label key={key} className="settings-checkbox-row">
          <input type="checkbox" checked={value} onChange={(event) => updatePrimitive(path, event.target.checked)} />
          <span>{settingTitle(label)}</span>
        </label>
      )
    }
    if (typeof value === 'number') {
      return (
        <label key={key} className="settings-field">
          <span>{settingTitle(label)}</span>
          <input type="number" value={value} onChange={(event) => updatePrimitive(path, Number(event.target.value))} />
        </label>
      )
    }
    if (typeof value === 'string') {
      return (
        <label key={key} className="settings-field">
          <span>{settingTitle(label)}</span>
          <input type="text" value={value} onChange={(event) => updatePrimitive(path, event.target.value)} />
        </label>
      )
    }
    return null
  }

  const renderSelect = (path: string[], label: string, value: SelectSetting) => (
    <label key={path.join('.')} className="settings-field">
      <span>{settingTitle(label)}</span>
      <select value={value.active} onChange={(event) => updateSelect(path, event.target.value)}>
        {value.list.map((option) => <option key={option} value={option}>{option}</option>)}
      </select>
    </label>
  )

  const renderSetting = (path: string[], label: string, value: JsonSettingValue, depth = 0): ReactNode => {
    if (isSelectSetting(value)) return renderSelect(path, label, value)
    if (isPlainObject(value)) {
      return (
        <div key={path.join('.')} className={depth > 0 ? 'settings-nested-group' : 'settings-block__fields'}>
          {depth > 0 && <div className="settings-nested-title">{settingTitle(label)}</div>}
          <div className="settings-block__fields">
            {Object.entries(value)
              .filter(([key]) => key !== 'active' && key !== 'list')
              .map(([key, nested]) => renderSetting([...path, key], key, nested, depth + 1))}
          </div>
        </div>
      )
    }
    return renderPrimitive(path, label, value)
  }

  const renderBlock = ([key, value]: [string, JsonSettingValue]) => (
    <section key={key} className="settings-block">
      <h2>{settingTitle(key)}</h2>
      {isSelectSetting(value)
        ? renderSelect([key], key, value)
        : isPlainObject(value)
          ? <div className="settings-block__fields">{Object.entries(value).map(([nestedKey, nested]) => renderSetting([key, nestedKey], nestedKey, nested))}</div>
          : renderPrimitive([key], key, value)}
    </section>
  )

  return (
    <main className="settings-window" aria-label="Settings">
      <section className="settings-content-area">
        <header className="settings-page-header">
          <button type="button" className="settings-back-button" aria-label="Back" title="Back" onClick={onBack}>←</button>
          <div>
            <h1>Settings</h1>
            <p>{status}</p>
          </div>
        </header>
        {loading
          ? <div className="settings-empty">Loading settings...</div>
          : config
            ? <div className="settings-content">{Object.entries(config).map(renderBlock)}</div>
            : <div className="settings-empty">config.json is unavailable</div>}
        <footer className="settings-footer">
          <button type="button" className="settings-secondary-button" onClick={() => void loadConfig()} disabled={loading || saving}>Restore</button>
          <button type="button" className="settings-save-button" onClick={() => void saveConfig()} disabled={loading || saving || !config}>{saving ? 'Saving...' : 'Save'}</button>
        </footer>
      </section>
    </main>
  )
}
