import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { setImmediate } from 'node:timers/promises'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import { isValidElement } from 'react'
import type { ReactElement, ReactNode } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import ts from 'typescript'
import type { JsonSettingObject } from '../src/api.ts'
import { errorMessage } from '../src/api.ts'

// The project uses node:test without a browser DOM. Run the actual TSX with
// controlled hook state, exercise its event handlers, and render its elements.
const require = createRequire(import.meta.url)
const compiled = ts.transpileModule(readFileSync(new URL('../src/components/SettingsPanel.tsx', import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX },
}).outputText

type Element = ReactElement<Record<string, unknown>>
function elements(node: ReactNode): Element[] {
  if (Array.isArray(node)) return node.flatMap(elements)
  if (!isValidElement<Record<string, unknown>>(node)) return []
  return [node, ...elements(node.props.children as ReactNode)]
}

async function mountSettings(initialModels: string[], modelFile = '', modelError = '') {
  let models = initialModels
  const config: JsonSettingObject = { localModel: { modelFile, profile: 'translategemma', startupTimeoutSeconds: 180 } }
  const saved: JsonSettingObject[] = []
  let listCalls = 0
  const window = {
    GetSettingsConfig: async () => structuredClone(config),
    GetLocalModels: async () => { listCalls++; if (modelError) throw new Error(modelError); return [...models] },
    SaveSettingsConfig: async (value: JsonSettingObject) => { saved.push(structuredClone(value)) },
  }
  const state: unknown[] = []
  let cursor = 0
  let mounted = false
  const effects: (() => void)[] = []
  const exports: { SettingsPanel?: (props: { onBack: () => void }) => ReactNode } = {}
  runInNewContext(compiled, {
    exports, window,
    require: (name: string) => {
      if (name === '../api') return { errorMessage }
      if (name !== 'react') return require(name)
      return {
        useState: (initial: unknown) => {
          const index = cursor++
          if (!mounted) state[index] = initial
          return [state[index], (value: unknown) => { state[index] = typeof value === 'function' ? value(state[index]) : value }]
        },
        useEffect: (effect: () => void) => { if (!mounted) effects.push(effect) },
      }
    },
  })
  const render = () => { cursor = 0; return exports.SettingsPanel!({ onBack: () => {} }) }
  render()
  mounted = true
  effects.forEach((effect) => effect())
  await setImmediate()
  const field = (name: string) => {
    const label = elements(render()).find((element) => element.type === 'label' && elements(element).some((child) => child.type === 'span' && child.props.children === name))
    assert.ok(label, `missing field ${name}`)
    const select = elements(label).find((element) => element.type === 'select')
    assert.ok(select, `missing selector ${name}`)
    return select
  }
  const change = (name: string, value: string) => {
    const handler = field(name).props.onChange as (event: { target: { value: string } }) => void
    handler({ target: { value } })
  }
  const click = async (label: string) => {
    const button = elements(render()).find((element) => element.type === 'button' && element.props.children === label)
    assert.ok(button)
    assert.equal(button.props.disabled, false)
    ;(button.props.onClick as () => void)()
    await setImmediate()
  }
  return { field, change, click, saved, markup: () => renderToStaticMarkup(render()), listCalls: () => listCalls, setModels: (next: string[]) => { models = next } }
}

test('Settings obtains current models and renders model and default profile selectors', async () => {
  const panel = await mountSettings(['models/one.gguf', 'models/two.GGUF'])
  assert.equal(panel.listCalls(), 1)
  assert.equal(panel.field('modelFile').props.value, '')
  assert.equal(panel.field('profile').props.value, 'translategemma')
  const markup = panel.markup()
  assert.match(markup, /models\/one.gguf/)
  assert.match(markup, /models\/two.GGUF/)
  assert.match(markup, /<option value="translategemma" selected="">translategemma/)
  assert.match(markup, /<option value="generic">generic/)
})

test('Settings saves explicit modelFile and generic without persisting the model list', async () => {
  const panel = await mountSettings(['models/one.gguf', 'models/two.gguf'])
  panel.change('modelFile', 'models/two.gguf')
  panel.change('profile', 'generic')
  assert.equal(panel.field('profile').props.value, 'generic')
  await panel.click('Save')
  assert.deepEqual(panel.saved, [{ localModel: { modelFile: 'models/two.gguf', profile: 'generic', startupTimeoutSeconds: 180 } }])
  assert.match(panel.markup(), /Restarting application/)
})

test('one model retains automatic selection and saves an empty modelFile', async () => {
  const panel = await mountSettings(['models/only.gguf'])
  assert.match(panel.markup(), /Automatic: models\/only.gguf/)
  assert.equal(panel.field('modelFile').props.value, '')
  await panel.click('Save')
  assert.deepEqual(panel.saved[0].localModel, { modelFile: '', profile: 'translategemma', startupTimeoutSeconds: 180 })
})

test('Restore refreshes the model list from disk without selecting an arbitrary model', async () => {
  const panel = await mountSettings(['models/old.gguf'])
  panel.setModels(['models/new.gguf', 'models/another.gguf'])
  await panel.click('Restore')
  assert.equal(panel.listCalls(), 2)
  assert.doesNotMatch(panel.markup(), /models\/old.gguf/)
  assert.match(panel.markup(), /models\/new.gguf/)
  assert.equal(panel.field('modelFile').props.value, '')
})

test('a configured external or missing model path is preserved', async () => {
  const selected = 'C:\\custom\\missing.gguf'
  const panel = await mountSettings(['models/only.gguf'], selected)
  assert.equal(panel.field('modelFile').props.value, selected)
  assert.match(panel.markup(), /configured path/)
  await panel.click('Save')
  assert.equal((panel.saved[0].localModel as JsonSettingObject).modelFile, selected)
})

test('a model discovery error leaves settings available and does not replace the selected path', async () => {
  const panel = await mountSettings([], 'models/selected.gguf', 'read models denied')
  assert.match(panel.markup(), /Model list unavailable: read models denied/)
  assert.equal(panel.field('modelFile').props.value, 'models/selected.gguf')
})
