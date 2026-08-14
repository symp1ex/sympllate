import assert from 'node:assert/strict'
import test from 'node:test'
import { cancelImageBatch, openImageBatchWindow, pollImageBatch, selectBatchImageDirectory, selectBatchImageFiles, startImageBatch } from '../src/api.ts'
import type { ImageBatchStatus } from '../src/api.ts'

test('batch polling reports progress and stops at completion', async () => {
  const statuses: ImageBatchStatus[] = [
    { id: 'batch-1', state: 'processing', total: 2, processed: 1, translated: 1, rendered: 1, partial: 0, warnings: 0, noText: 0, failed: 0, currentFile: 'page-1.png' },
    { id: 'batch-1', state: 'completed', total: 2, processed: 2, translated: 2, rendered: 2, partial: 0, warnings: 0, noText: 0, failed: 0 },
  ]
  let index = 0
  Object.defineProperty(globalThis, 'window', { configurable: true, value: { setTimeout: globalThis.setTimeout, GetImageBatchStatus: async () => statuses[index++]! } })
  const observed: string[] = []
  const result = await pollImageBatch('batch-1', (status) => observed.push(status.state), new AbortController().signal, 0)
  assert.equal(result.state, 'completed')
  assert.deepEqual(observed, ['processing', 'completed'])
  assert.equal(index, 2)
})

test('batch polling does not update after cancellation', async () => {
  const controller = new AbortController()
  Object.defineProperty(globalThis, 'window', { configurable: true, value: { setTimeout: globalThis.setTimeout, GetImageBatchStatus: async () => ({ id: 'batch-1', state: 'processing', total: 1, processed: 0, translated: 0, rendered: 0, partial: 0, warnings: 0, noText: 0, failed: 0 }) } })
  controller.abort()
  await assert.rejects(pollImageBatch('batch-1', () => assert.fail('unexpected update'), controller.signal, 0), /cancelled/)
})

test('batch selection, start, and cancel use their dedicated bindings', async () => {
  const calls: string[] = []
  let startRequest: unknown
  Object.defineProperty(globalThis, 'window', { configurable: true, value: {
    SelectBatchImageFiles: async () => { calls.push('files'); return { id: 'files-1', kind: 'files', displayName: 'images', fileCount: 2 } },
    SelectBatchImageDirectory: async () => { calls.push('directory'); return { id: 'dir-1', kind: 'directory', displayName: 'pages', fileCount: 10 } },
    StartImageBatch: async (request: unknown) => { startRequest = request; calls.push('start'); return 'batch-1' },
    CancelImageBatch: async () => { calls.push('cancel') },
    OpenImageBatchWindow: async () => { calls.push('open-window') },
  } })
  assert.equal((await selectBatchImageFiles()).fileCount, 2)
  assert.equal((await selectBatchImageDirectory()).fileCount, 10)
  assert.equal(await startImageBatch({ selectionId: 'dir-1', source: 'auto', target: 'ru', debug: false }), 'batch-1')
  assert.deepEqual(startRequest, { selectionId: 'dir-1', source: 'auto', target: 'ru', debug: false })
  await cancelImageBatch('batch-1')
  await openImageBatchWindow()
  assert.deepEqual(calls, ['files', 'directory', 'start', 'cancel', 'open-window'])
})
