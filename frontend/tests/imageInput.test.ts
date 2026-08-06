import assert from 'node:assert/strict'
import test from 'node:test'
import {
  findClipboardImage,
  getSingleDroppedFile,
  prepareImageFile,
  releaseImagePreview,
} from '../src/imageInput.ts'
import type { ImageLimits, SourceInput } from '../src/imageInput.ts'

const limits: ImageLimits = { maxImageBytes: 1024, maxImageBase64Characters: 2048 }

test('ordinary clipboard text remains available for normal paste', () => {
  const item = { kind: 'string', type: 'text/plain', getAsFile: () => null }
  assert.equal(findClipboardImage([item]), null)
})

test('clipboard image is selected before accompanying text', () => {
  const file = fakeFile(pngBytes(), 'image/png', 'clipboard.png')
  const text = { kind: 'string', type: 'text/plain', getAsFile: () => null }
  const image = { kind: 'file', type: 'image/png', getAsFile: () => file }
  assert.equal(findClipboardImage([text, image]), image)
})

test('PNG and JPEG files are prepared for image mode', async () => {
  const png = await prepareImageFile(fakeFile(pngBytes(), 'image/png', 'one.png'), limits, () => 'blob:png')
  assert.equal(png.kind, 'image')
  assert.equal(png.mediaType, 'image/png')
  assert.equal(png.previewUrl, 'blob:png')

  const jpeg = await prepareImageFile(fakeFile(new Uint8Array([0xff, 0xd8, 0xff, 0xd9]), 'image/jpeg', 'two.jpg'), limits, () => 'blob:jpeg')
  assert.equal(jpeg.mediaType, 'image/jpeg')
})

test('unsupported and mismatched files are rejected', async () => {
  await assert.rejects(
    prepareImageFile(fakeFile(new Uint8Array([0x47, 0x49, 0x46]), 'image/gif', 'bad.gif'), limits, () => 'blob:bad'),
    /Unsupported image format/,
  )
  await assert.rejects(
    prepareImageFile(fakeFile(pngBytes(), 'image/jpeg', 'wrong.jpg'), limits, () => 'blob:wrong'),
    /does not match/,
  )
})

test('drop accepts exactly one file', () => {
  const file = fakeFile(pngBytes(), 'image/png', 'one.png')
  assert.equal(getSingleDroppedFile([file]), file)
  assert.throws(() => getSingleDroppedFile([]), /Folders are not supported/)
  assert.throws(() => getSingleDroppedFile([file, file]), /only one image/)
})

test('image preview URL is released on replacement or unmount', () => {
  const source: SourceInput = {
    kind: 'image', fileName: 'one.png', mediaType: 'image/png', dataBase64: 'AA==', previewUrl: 'blob:one', byteLength: 1,
  }
  const released: string[] = []
  releaseImagePreview(source, (url) => released.push(url))
  releaseImagePreview({ kind: 'text', text: '' }, (url) => released.push(url))
  assert.deepEqual(released, ['blob:one'])
})

function fakeFile(bytes: Uint8Array, type: string, name: string): File {
  return {
    name,
    type,
    size: bytes.byteLength,
    arrayBuffer: async () => bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength),
  } as File
}

function pngBytes(): Uint8Array {
  return new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
}
