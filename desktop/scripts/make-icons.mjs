// Generates the app's PNG icons from scratch, so the repo carries no binary
// blob that nobody can edit. Run with `npm run icons` after changing a shape.
//
// The tray glyph is a template image: macOS only reads its alpha channel and
// tints the result for the menu bar, so it is drawn in pure black.
import { deflateSync } from 'node:zlib'
import { mkdir, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = dirname(dirname(fileURLToPath(import.meta.url)))

/** Writes an RGBA buffer as a PNG. width*height*4 bytes, row-major. */
function encodePNG(width, height, rgba) {
  const raw = Buffer.alloc((width * 4 + 1) * height)
  for (let y = 0; y < height; y++) {
    // Filter byte 0 (None) precedes each scanline.
    raw[y * (width * 4 + 1)] = 0
    rgba.copy(raw, y * (width * 4 + 1) + 1, y * width * 4, (y + 1) * width * 4)
  }

  const chunk = (type, data) => {
    const length = Buffer.alloc(4)
    length.writeUInt32BE(data.length)
    const body = Buffer.concat([Buffer.from(type, 'ascii'), data])
    const crc = Buffer.alloc(4)
    crc.writeUInt32BE(crc32(body) >>> 0)
    return Buffer.concat([length, body, crc])
  }

  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(width, 0)
  ihdr.writeUInt32BE(height, 4)
  ihdr[8] = 8 // bit depth
  ihdr[9] = 6 // colour type: RGBA
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', ihdr),
    chunk('IDAT', deflateSync(raw, { level: 9 })),
    chunk('IEND', Buffer.alloc(0)),
  ])
}

const CRC_TABLE = Array.from({ length: 256 }, (_, n) => {
  let c = n
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
  return c >>> 0
})

function crc32(buf) {
  let c = 0xffffffff
  for (const byte of buf) c = CRC_TABLE[(c ^ byte) & 0xff] ^ (c >>> 8)
  return c ^ 0xffffffff
}

/**
 * Draws the mark: a ring with a solid core — a sun, for Helios. Sampled 4× per
 * axis so the edges are not jagged at 16 px.
 */
function draw(size, colour) {
  const rgba = Buffer.alloc(size * size * 4)
  const centre = (size - 1) / 2
  const outer = size * 0.46
  const inner = size * 0.33
  const core = size * 0.17
  const samples = 4

  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      let hits = 0
      for (let sy = 0; sy < samples; sy++) {
        for (let sx = 0; sx < samples; sx++) {
          const px = x + (sx + 0.5) / samples - 0.5
          const py = y + (sy + 0.5) / samples - 0.5
          const d = Math.hypot(px - centre, py - centre)
          if ((d <= outer && d >= inner) || d <= core) hits++
        }
      }
      const alpha = Math.round((hits / (samples * samples)) * 255)
      const i = (y * size + x) * 4
      rgba[i] = colour[0]
      rgba[i + 1] = colour[1]
      rgba[i + 2] = colour[2]
      rgba[i + 3] = alpha
    }
  }
  return rgba
}

const assets = resolve(root, 'assets')
await mkdir(assets, { recursive: true })

// Tray: black, template-tinted by the OS.
await writeFile(resolve(assets, 'trayTemplate.png'), encodePNG(16, 16, draw(16, [0, 0, 0])))
await writeFile(resolve(assets, 'trayTemplate@2x.png'), encodePNG(32, 32, draw(32, [0, 0, 0])))

// App icon: the same mark in the accent colour, at the size electron-builder
// wants as a source for .icns and .ico.
await writeFile(resolve(assets, 'icon.png'), encodePNG(512, 512, draw(512, [0xff, 0xb0, 0x3a])))

console.log('wrote assets/trayTemplate.png, assets/trayTemplate@2x.png, assets/icon.png')
