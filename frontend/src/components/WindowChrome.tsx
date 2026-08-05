import type { PointerEvent, ReactNode } from 'react'

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

export function WindowChrome({ children, mode, onSettings, lockContentOverflow = false }: Props) {
  const resize = (event: PointerEvent<HTMLDivElement>, hitTest: number) => {
    if (event.button !== 0) return
    event.preventDefault()
    event.stopPropagation()
    void window.WindowResize(hitTest)
  }

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
