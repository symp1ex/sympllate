import { useCallback, useEffect, useRef, useState } from 'react'
import type { PointerEvent, ReactNode } from 'react'
import type { ApplicationInfo, CheckApplicationUpdateResult } from '../api'

interface Props {
  children: ReactNode
  mode: 'main' | 'popup'
  onSettings?: () => void
  lockContentOverflow?: boolean
}

const resizeHandles = [
  ['left', 10],
  ['right', 11],
  ['top', 12],
  ['top-left', 13],
  ['top-right', 14],
  ['bottom', 15],
  ['bottom-left', 16],
  ['bottom-right', 17],
] as const

type UpdateState = 'disabled' | 'idle' | 'checking' | 'available' | 'installing' | 'error'

export function WindowChrome({ children, mode, onSettings, lockContentOverflow = false }: Props) {
  const [applicationInfo, setApplicationInfo] = useState<ApplicationInfo>({ version: '', updaterEnabled: false })
  const [updateState, setUpdateState] = useState<UpdateState>('disabled')
  const [updateMessage, setUpdateMessage] = useState('')
  const mountedRef = useRef(true)
  const updateStateRef = useRef<UpdateState>('disabled')
  const checkInFlightRef = useRef(false)
  const autoCheckStartedRef = useRef(false)

  useEffect(() => {
    mountedRef.current = true
    return () => { mountedRef.current = false }
  }, [])

  const setUpdateStateValue = useCallback((nextState: UpdateState) => {
    updateStateRef.current = nextState
    if (mountedRef.current) setUpdateState(nextState)
  }, [])

  const showActionMessage = useCallback((message: string) => {
    if (mountedRef.current) setUpdateMessage(message)
  }, [])

  const applyUpdateCheckResult = useCallback((result: CheckApplicationUpdateResult) => {
    checkInFlightRef.current = false
    if (!mountedRef.current) return
    if (result.ok && result.updateAvailable) {
      setUpdateStateValue('available')
      showActionMessage('Update available')
      return
    }
    if (!result.ok) {
      setUpdateStateValue('error')
      if (result.message) showActionMessage(`Update check: ${result.message}`)
      return
    }
    setUpdateStateValue('idle')
    showActionMessage('Application is up to date')
  }, [setUpdateStateValue, showActionMessage])

  const runUpdateCheck = useCallback(async (updaterEnabled = applicationInfo.updaterEnabled) => {
    if (!updaterEnabled) {
      setUpdateStateValue('disabled')
      return
    }
    const currentState = updateStateRef.current
    if (checkInFlightRef.current || currentState === 'available' || currentState === 'installing') return
    if (!window.checkApplicationUpdate) {
      setUpdateStateValue('error')
      showActionMessage('Bridge checkApplicationUpdate is unavailable')
      return
    }

    checkInFlightRef.current = true
    setUpdateStateValue('checking')
    showActionMessage('Checking for updates')
    try {
      const result = await window.checkApplicationUpdate()
      if (!mountedRef.current) return
      if (!result.ok) {
        checkInFlightRef.current = false
        if (result.message === 'updater is disabled') {
          setUpdateStateValue('disabled')
          return
        }
        setUpdateStateValue('error')
        showActionMessage(result.message || 'Failed to start update check')
      }
    } catch (caught) {
      if (!mountedRef.current) return
      checkInFlightRef.current = false
      setUpdateStateValue('error')
      showActionMessage(`Update check failed: ${caught instanceof Error ? caught.message : String(caught)}`)
    }
  }, [applicationInfo.updaterEnabled, setUpdateStateValue, showActionMessage])

  const installUpdate = useCallback(async () => {
    if (updateStateRef.current !== 'available') return
    if (!window.installApplicationUpdate) {
      showActionMessage('Bridge installApplicationUpdate is unavailable')
      return
    }
    setUpdateStateValue('installing')
    try {
      const result = await window.installApplicationUpdate()
      if (!mountedRef.current) return
      if (!result.ok) {
        setUpdateStateValue('available')
        showActionMessage(result.message || 'Failed to start update installation')
        return
      }
      showActionMessage('Starting update installation')
    } catch (caught) {
      if (!mountedRef.current) return
      setUpdateStateValue('available')
      showActionMessage(`Update installation failed: ${caught instanceof Error ? caught.message : String(caught)}`)
    }
  }, [setUpdateStateValue, showActionMessage])

  useEffect(() => {
    if (mode !== 'main') return
    const getApplicationInfo = window.getApplicationInfo
    if (!getApplicationInfo) {
      setUpdateStateValue('disabled')
      return
    }
    const loadApplicationInfo = async () => {
      try {
        const info = await getApplicationInfo()
        if (!mountedRef.current) return
        setApplicationInfo(info)
        setUpdateStateValue(info.updaterEnabled ? 'idle' : 'disabled')
      } catch (caught) {
        if (!mountedRef.current) return
        setUpdateStateValue('disabled')
        showActionMessage(`Failed to load version: ${caught instanceof Error ? caught.message : String(caught)}`)
      }
    }
    void loadApplicationInfo()
  }, [mode, setUpdateStateValue, showActionMessage])

  useEffect(() => {
    if (mode !== 'main') return
    const onApplicationUpdateCheckResult = (event: Event) => {
      const result = (event as CustomEvent<CheckApplicationUpdateResult>).detail
      if (result) applyUpdateCheckResult(result)
    }
    window.addEventListener('application-update-check-result', onApplicationUpdateCheckResult)
    return () => window.removeEventListener('application-update-check-result', onApplicationUpdateCheckResult)
  }, [applyUpdateCheckResult, mode])

  useEffect(() => {
    if (mode !== 'main' || !applicationInfo.updaterEnabled || autoCheckStartedRef.current) return
    autoCheckStartedRef.current = true
    void runUpdateCheck(applicationInfo.updaterEnabled)
  }, [applicationInfo.updaterEnabled, mode, runUpdateCheck])

  const activateVersionAction = () => {
    if (updateState === 'available') {
      void installUpdate()
    } else if (updateState === 'error') {
      setUpdateStateValue('idle')
      void runUpdateCheck()
    } else if (updateState === 'idle') {
      void runUpdateCheck()
    }
  }

  const resize = (event: PointerEvent<HTMLDivElement>, hitTest: number) => {
    if (event.button !== 0) return
    event.preventDefault()
    event.stopPropagation()
    void window.WindowResize(hitTest)
  }

  const versionText = updateState === 'error'
    ? 'Update error'
    : updateState === 'available' || updateState === 'installing'
      ? 'Install update'
      : applicationInfo.version ? `v${applicationInfo.version}` : ''
  const versionAriaLabel = updateState === 'error'
    ? 'Retry update check'
    : updateState === 'available' ? 'Install update' : `Application version ${versionText}`
  const spinnerVisible = updateState === 'checking' || updateState === 'installing'

  return (
    <div className={`window-root window-root--${mode}`}>
      {resizeHandles.map(([edge, hitTest]) => (
        <div
          className={`window-resize-handle window-resize-handle--${edge}`}
          aria-hidden="true"
          key={edge}
          onPointerDown={(event) => resize(event, hitTest)}
        />
      ))}
      <header
        className="custom-titlebar"
        aria-label="Window title"
        onPointerDown={(event) => {
          if (event.button === 0) void window.WindowDrag()
        }}
      >
        <div className="custom-titlebar__brand">
          <span className="custom-titlebar__name">Sympllate</span>
          {mode === 'main' && versionText && (
            <button
              type="button"
              className={`custom-titlebar__version custom-titlebar__version--${updateState}`}
              aria-label={versionAriaLabel}
              aria-disabled={updateState === 'disabled' || updateState === 'checking' || updateState === 'installing'}
              title={updateMessage || versionAriaLabel}
              onPointerDown={(event) => event.stopPropagation()}
              onClick={activateVersionAction}
            >
              {versionText}
            </button>
          )}
          {mode === 'main' && spinnerVisible && (
            <span className="custom-titlebar__spinner" role="status" aria-label={updateState === 'installing' ? 'Starting update installation' : 'Checking for updates'} />
          )}
          {mode === 'main' && updateMessage && <span className="visually-hidden" aria-live="polite">{updateMessage}</span>}
        </div>
        <div className="custom-titlebar__actions">
          {mode === 'main' && onSettings && (
            <button
              type="button"
              className="custom-titlebar__button custom-titlebar__button--settings"
              aria-label="Settings"
              title="Settings"
              onPointerDown={(event) => event.stopPropagation()}
              onClick={onSettings}
            >
              ⚙
            </button>
          )}
          <button
            type="button"
            className="custom-titlebar__button"
            aria-label="Minimize"
            title="Minimize"
            onPointerDown={(event) => event.stopPropagation()}
            onClick={() => void window.WindowMinimize()}
          >
            −
          </button>
          <button
            type="button"
            className="custom-titlebar__button custom-titlebar__button--maximize"
            aria-label="Maximize or restore"
            title="Maximize or restore"
            onPointerDown={(event) => event.stopPropagation()}
            onClick={() => void window.WindowToggleMaximize()}
          >
            □
          </button>
          <button
            type="button"
            className="custom-titlebar__button custom-titlebar__button--close"
            aria-label="Close"
            title="Close"
            onPointerDown={(event) => event.stopPropagation()}
            onClick={() => void window.WindowClose()}
          >
            ×
          </button>
        </div>
      </header>
      <div className={lockContentOverflow ? 'window-content window-content--locked' : 'window-content'}>{children}</div>
    </div>
  )
}
