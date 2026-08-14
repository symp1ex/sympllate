import assert from 'node:assert/strict'
import test from 'node:test'
import { translate, translateImage } from '../src/api.ts'

test('text translation uses the text bindings', async () => {
  const calls: string[] = []
  installWindow({
    Translate: async () => { calls.push('Translate'); return 'text-job' },
    GetTranslation: async (id) => { calls.push(`GetTranslation:${id}`); return { state: 'done', result: { text: 'text result' } } },
  })
  const result = await translate({ text: 'source', source: 'en', target: 'ru' })
  assert.equal(result.text, 'text result')
  assert.deepEqual(calls, ['Translate', 'GetTranslation:text-job'])
})

test('image translation uses only the image bindings and preserves an empty result', async () => {
  const calls: string[] = []
  installWindow({
    TranslateImage: async () => { calls.push('TranslateImage'); return 'image-job' },
    GetImageTranslation: async (id) => { calls.push(`GetImageTranslation:${id}`); return { state: 'done', result: { text: '' } } },
  })
  const result = await translateImage({ dataBase64: 'AA==', mediaType: 'image/png', source: 'auto', target: 'ru' })
  assert.equal(result.text, '')
  assert.deepEqual(calls, ['TranslateImage', 'GetImageTranslation:image-job'])
})

test('image provider error is surfaced to the UI caller', async () => {
  installWindow({
    TranslateImage: async () => 'image-job',
    GetImageTranslation: async () => ({ state: 'error', error: 'PaddleOCR is unavailable' }),
  })
  await assert.rejects(
    translateImage({ dataBase64: 'AA==', mediaType: 'image/png', source: 'en', target: 'ru' }),
    /PaddleOCR is unavailable/,
  )
})

type WindowOverrides = {
  Translate?: (request: unknown) => Promise<string>
  GetTranslation?: (id: string) => Promise<{ state: 'done'; result: { text: string } }>
  TranslateImage?: (request: unknown) => Promise<string>
  GetImageTranslation?: (id: string) => Promise<
    { state: 'done'; result: { text: string } } | { state: 'error'; error: string }
  >
}

function installWindow(overrides: WindowOverrides): void {
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      setTimeout: globalThis.setTimeout,
      Translate: async () => { throw new Error('unexpected text binding') },
      GetTranslation: async () => { throw new Error('unexpected text polling binding') },
      TranslateImage: async () => { throw new Error('unexpected image binding') },
      GetImageTranslation: async () => { throw new Error('unexpected image polling binding') },
      ...overrides,
    },
  })
}
