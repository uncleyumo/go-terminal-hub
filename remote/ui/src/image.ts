// 手机选中的图片 → 能塞进一条控制消息的 base64。
//
// 为什么要压：手机随手一张照片就是 3~8 MB、4000 px 宽，而
//   - relay 和 agent 两边的单帧读上限都是 4 MB，base64 还要再涨 33%
//   - claude 那侧能看清的上限也就是长边 1568 px，再大纯属白传
// 所以先在手机上压好，链路两头都省事。

/** 长边压到这个尺寸就够了，再大模型也看不出差别。 */
const MAX_EDGE = 1568

/** 单帧的安全线。relay 的读上限是 4 MB，留出余量。 */
const MAX_BASE64 = 3 << 20

/** PNG 压到这个大小以内就用 PNG，超了就换 JPEG。 */
const MAX_PNG_BYTES = 2 << 20

export interface ShrunkImage {
  /** 不带 data: 前缀的 base64。 */
  data: string
  mediaType: string
}

export async function shrinkImage(file: File): Promise<ShrunkImage> {
  const img = await loadImage(file)

  const scale = Math.min(1, MAX_EDGE / Math.max(img.naturalWidth, img.naturalHeight))
  const width = Math.max(1, Math.round(img.naturalWidth * scale))
  const height = Math.max(1, Math.round(img.naturalHeight * scale))

  const canvas = document.createElement('canvas')
  canvas.width = width
  canvas.height = height
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('这个浏览器画不了 canvas')
  ctx.drawImage(img, 0, 0, width, height)

  // 优先 PNG：截图里的文字边缘用 JPEG 会起振铃，模型读起来更容易看错。
  // 照片压不成 PNG（动辄好几 MB），那就退回 JPEG。
  let blob = await toBlob(canvas, 'image/png')
  let mediaType = 'image/png'
  if (!blob || blob.size > MAX_PNG_BYTES) {
    const jpeg = await toBlob(canvas, 'image/jpeg', 0.85)
    if (jpeg) {
      blob = jpeg
      mediaType = 'image/jpeg'
    }
  }
  if (!blob) throw new Error('这张图编码不出来')

  const data = await toBase64(blob)
  if (data.length > MAX_BASE64) throw new Error('这张图压完还是太大，换一张试试')
  return { data, mediaType }
}

function loadImage(file: File): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file)
    const img = new Image()
    img.onload = () => {
      URL.revokeObjectURL(url)
      resolve(img)
    }
    img.onerror = () => {
      URL.revokeObjectURL(url)
      // HEIC 这类格式有的浏览器解不开，报清楚比报「失败」有用
      reject(new Error('这个格式的图片解不开'))
    }
    img.src = url
  })
}

function toBlob(canvas: HTMLCanvasElement, type: string, quality?: number): Promise<Blob | null> {
  return new Promise((resolve) => canvas.toBlob(resolve, type, quality))
}

function toBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const result = String(reader.result)
      // readAsDataURL 给的是 "data:image/png;base64,xxxx"，逗号后面才是数据
      resolve(result.slice(result.indexOf(',') + 1))
    }
    reader.onerror = () => reject(new Error('这张图读不出来'))
    reader.readAsDataURL(blob)
  })
}
