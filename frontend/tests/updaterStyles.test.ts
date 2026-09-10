import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const styles = readFileSync(new URL('../src/styles.css', import.meta.url), 'utf8')

test('available, installing, and error updater states use the error color', () => {
  assert.match(styles, /custom-titlebar__version--available,\s*\.custom-titlebar button\.custom-titlebar__version--installing,\s*\.custom-titlebar button\.custom-titlebar__version--error \{ color: var\(--error\); \}/)
  assert.match(styles, /custom-titlebar__version--installing:hover \{ color: var\(--error\); background: transparent; \}/)
})
