// Build script for the three bundles Electron needs: the main process, the
// preload script, and the renderer.
//
// esbuild rather than a framework bundler because the shapes differ enough
// that a single config would be a pile of conditionals: main is Node with
// electron external, preload is CommonJS because sandboxed preloads cannot be
// ESM, and the renderer is a browser bundle with no Node access at all.
import * as esbuild from 'esbuild'
import { cp, mkdir, rm } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = dirname(fileURLToPath(import.meta.url))
const out = resolve(root, 'dist')
const watch = process.argv.includes('--watch')
const dev = watch || process.argv.includes('--dev')

/** @type {import('esbuild').BuildOptions} */
const common = {
  bundle: true,
  sourcemap: dev,
  minify: !dev,
  logLevel: 'info',
  define: { 'process.env.NODE_ENV': JSON.stringify(dev ? 'development' : 'production') },
}

const configs = [
  {
    ...common,
    entryPoints: [resolve(root, 'src/main/main.ts')],
    outfile: resolve(out, 'main/main.js'),
    platform: 'node',
    format: 'cjs',
    target: 'node20',
    // Electron and ws resolve from node_modules at runtime; bundling electron
    // is impossible and bundling ws pulls in optional native deps we do not
    // want. Everything else is bundled so the packaged app has one file.
    external: ['electron', 'ws'],
  },
  {
    ...common,
    entryPoints: [resolve(root, 'src/preload/preload.ts')],
    outfile: resolve(out, 'preload/preload.js'),
    platform: 'node',
    // Sandboxed preload scripts are loaded as CommonJS and may only require
    // 'electron' — anything else must be bundled in.
    format: 'cjs',
    target: 'node20',
    external: ['electron'],
  },
  {
    ...common,
    entryPoints: [resolve(root, 'src/renderer/index.tsx')],
    outfile: resolve(out, 'renderer/renderer.js'),
    platform: 'browser',
    format: 'iife',
    target: 'chrome128',
    loader: { '.css': 'css' },
  },
]

await rm(out, { recursive: true, force: true })
await mkdir(resolve(out, 'renderer'), { recursive: true })
await cp(resolve(root, 'src/renderer/index.html'), resolve(out, 'renderer/index.html'))
await cp(resolve(root, 'node_modules/@xterm/xterm/css/xterm.css'), resolve(out, 'renderer/xterm.css'))
await cp(resolve(root, 'assets'), resolve(out, 'assets'), { recursive: true })

if (watch) {
  const contexts = await Promise.all(configs.map((c) => esbuild.context(c)))
  await Promise.all(contexts.map((c) => c.watch()))
  console.log('watching…')
} else {
  await Promise.all(configs.map((c) => esbuild.build(c)))
}
