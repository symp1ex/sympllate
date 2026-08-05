import type { PointerEvent, ReactNode } from 'react'

interface Props {
  children: ReactNode
  mode: 'main' | 'popup'
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

export function WindowChrome({ children, mode }: Props) {
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
        aria-label="Заголовок окна"
        onPointerDown={(event) => {
          if (event.button === 0) void window.WindowDrag()
        }}
      >
        <div className="custom-titlebar__brand">
          <span className="custom-titlebar__name">Sympllate</span>
        </div>
        <div className="custom-titlebar__actions">
          <button
            type="button"
            className="custom-titlebar__button"
            aria-label="Свернуть"
            title="Свернуть"
            onPointerDown={(event) => event.stopPropagation()}
            onClick={() => void window.WindowMinimize()}
          >
            −
          </button>
          <button
            type="button"
            className="custom-titlebar__button custom-titlebar__button--maximize"
            aria-label="Развернуть или восстановить"
            title="Развернуть или восстановить"
            onPointerDown={(event) => event.stopPropagation()}
            onClick={() => void window.WindowToggleMaximize()}
          >
            □
          </button>
          <button
            type="button"
            className="custom-titlebar__button custom-titlebar__button--close"
            aria-label="Закрыть"
            title="Закрыть"
            onPointerDown={(event) => event.stopPropagation()}
            onClick={() => void window.WindowClose()}
          >
            ×
          </button>
        </div>
      </header>
      <div className="window-content">{children}</div>
    </div>
  )
}
