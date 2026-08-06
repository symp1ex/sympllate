export type TextSource = { kind: 'text'; text: string }
export type ImageSource = {
  kind: 'image'
  fileName?: string
  mediaType: 'image/png' | 'image/jpeg'
  dataBase64: string
  previewUrl: string
  byteLength: number
}
export type SourceInput = TextSource | ImageSource

export type ImageLimits = {
  maxImageBytes: number
  maxImageBase64Characters: number
}

type ClipboardItemLike = Pick<DataTransferItem, 'kind' | 'type' | 'getAsFile'>

export function findClipboardImage(items: ArrayLike<ClipboardItemLike>): ClipboardItemLike | null {
  for (let index = 0; index < items.length; index += 1) {
    const item = items[index]
    if (item?.kind === 'file' && item.type.toLowerCase().startsWith('image/')) return item
  }
  return null
}

export function getSingleDroppedFile(files: ArrayLike<File>): File {
  if (files.length === 0) throw new Error('Drop one PNG or JPEG image. Folders are not supported.')
  if (files.length > 1) throw new Error('Drop only one image at a time.')
  const file = files[0]
  if (!file) throw new Error('The dropped file is unavailable.')
  return file
}

export async function prepareImageFile(
  file: File,
  limits: ImageLimits,
  createPreviewUrl: (file: File) => string = URL.createObjectURL,
): Promise<ImageSource> {
  if (file.size <= 0) throw new Error('The image is empty.')
  if (file.size > limits.maxImageBytes) {
    throw new Error(`The image is too large. Maximum size is ${formatBytes(limits.maxImageBytes)}.`)
  }
  const bytes = new Uint8Array(await file.arrayBuffer())
  if (bytes.byteLength > limits.maxImageBytes) {
    throw new Error(`The image is too large. Maximum size is ${formatBytes(limits.maxImageBytes)}.`)
  }
  const mediaType = detectImageMediaType(bytes)
  const declaredType = normalizeImageMediaType(file.type)
  if (file.type && !declaredType) throw new Error('Unsupported image format. Use PNG or JPEG.')
  if (declaredType && declaredType !== mediaType) {
    throw new Error(`The file type ${file.type} does not match the image data.`)
  }
  const dataBase64 = bytesToBase64(bytes)
  if (dataBase64.length > limits.maxImageBase64Characters) {
    throw new Error('The encoded image payload is too large.')
  }
  return {
    kind: 'image',
    fileName: file.name || undefined,
    mediaType,
    dataBase64,
    previewUrl: createPreviewUrl(file),
    byteLength: bytes.byteLength,
  }
}

export function releaseImagePreview(source: SourceInput, revoke: (url: string) => void = URL.revokeObjectURL): void {
  if (source.kind === 'image') revoke(source.previewUrl)
}

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function detectImageMediaType(bytes: Uint8Array): ImageSource['mediaType'] {
  const png = bytes.length >= 8 &&
    bytes[0] === 0x89 && bytes[1] === 0x50 && bytes[2] === 0x4e && bytes[3] === 0x47 &&
    bytes[4] === 0x0d && bytes[5] === 0x0a && bytes[6] === 0x1a && bytes[7] === 0x0a
  if (png) return 'image/png'
  if (bytes.length >= 3 && bytes[0] === 0xff && bytes[1] === 0xd8 && bytes[2] === 0xff) return 'image/jpeg'
  throw new Error('Unsupported image format. Use PNG or JPEG.')
}

function normalizeImageMediaType(value: string): ImageSource['mediaType'] | null {
  switch (value.trim().toLowerCase()) {
    case 'image/png': return 'image/png'
    case 'image/jpeg':
    case 'image/jpg': return 'image/jpeg'
    default: return null
  }
}

function bytesToBase64(bytes: Uint8Array): string {
  const chunkSize = 0x8000
  let binary = ''
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + chunkSize))
  }
  return btoa(binary)
}
