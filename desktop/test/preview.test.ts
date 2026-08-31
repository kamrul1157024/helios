import assert from 'node:assert/strict'
import test from 'node:test'

import {
  dataUrl,
  extensionOf,
  isHtmlPath,
  isImagePath,
  kindOf,
  mimeForPath,
} from '../src/renderer/filetype.ts'
import {
  MAX_ASSETS,
  planAssets,
  resolveAsset,
  withinRoot,
  type AssetRef,
} from '../src/renderer/preview.ts'

const ROOT = '/Users/kim/work/repo'
const PAGE = '/Users/kim/work/repo/docs/report.html'

test('an extension is the part after the last dot, lowercased', () => {
  assert.equal(extensionOf('/a/b/c.PNG'), 'png')
  assert.equal(extensionOf('/a/b/archive.tar.gz'), 'gz')
  assert.equal(extensionOf('/a/b/Makefile'), '')
  // A dotfile is a name, not an extension.
  assert.equal(extensionOf('/a/b/.gitignore'), '')
  assert.equal(extensionOf('/a/b.d/plain'), '')
})

test('a file knows whether it is a picture, a page, or text', () => {
  assert.equal(kindOf('/a/logo.svg'), 'image')
  assert.equal(kindOf('/a/shot.JPEG'), 'image')
  assert.equal(kindOf('/a/index.html'), 'html')
  assert.equal(kindOf('/a/main.go'), 'text')
  assert.equal(kindOf('/a/.gitignore'), 'text')

  assert.equal(isImagePath('/a/x.webp'), true)
  assert.equal(isImagePath('/a/x.txt'), false)
  assert.equal(isHtmlPath('/a/x.htm'), true)
  assert.equal(isHtmlPath('/a/x.html.bak'), false)
})

test('the mime is the one a data URL needs', () => {
  assert.equal(mimeForPath('/a/x.png'), 'image/png')
  assert.equal(mimeForPath('/a/x.jpg'), 'image/jpeg')
  assert.equal(mimeForPath('/a/x.svg'), 'image/svg+xml')
  assert.equal(mimeForPath('/a/x.css'), 'text/css')
  assert.equal(mimeForPath('/a/x.bin'), 'application/octet-stream')
  assert.equal(dataUrl('/a/x.png', 'QUJD'), 'data:image/png;base64,QUJD')
})

test('a relative reference resolves against the file that holds it', () => {
  assert.equal(resolveAsset(PAGE, './chart.png', ROOT), `${ROOT}/docs/chart.png`)
  assert.equal(resolveAsset(PAGE, 'chart.png', ROOT), `${ROOT}/docs/chart.png`)
  assert.equal(resolveAsset(PAGE, 'img/chart.png', ROOT), `${ROOT}/docs/img/chart.png`)
  assert.equal(resolveAsset(PAGE, '../assets/logo.svg', ROOT), `${ROOT}/assets/logo.svg`)
  assert.equal(resolveAsset(PAGE, './x.png?v=2#frag', ROOT), `${ROOT}/docs/x.png`)
  assert.equal(resolveAsset(PAGE, 'my%20chart.png', ROOT), `${ROOT}/docs/my chart.png`)
})

test('an absolute reference means the checkout, not the filesystem', () => {
  assert.equal(resolveAsset(PAGE, '/assets/logo.svg', ROOT), `${ROOT}/assets/logo.svg`)
  // Which is also what stops a page asking for a file outside the checkout by
  // writing a leading slash.
  assert.equal(resolveAsset(PAGE, '/etc/passwd', ROOT), `${ROOT}/etc/passwd`)
})

test('a reference that leaves the root is refused', () => {
  // The case this function exists for.
  assert.equal(resolveAsset(PAGE, '../../../../.ssh/id_rsa', ROOT), null)
  assert.equal(resolveAsset(PAGE, '../../../etc/passwd', ROOT), null)
  assert.equal(resolveAsset(PAGE, './../../..', ROOT), null)
})

test('anything that is not a path on this machine is refused', () => {
  for (const href of [
    'http://example.com/x.png',
    'https://example.com/x.png',
    '//example.com/x.png',
    'data:image/png;base64,AAAA',
    'file:///etc/passwd',
    'javascript:alert(1)',
    '#anchor',
    '',
    '   ',
  ]) {
    assert.equal(resolveAsset(PAGE, href, ROOT), null, `for ${JSON.stringify(href)}`)
  }
})

test('a page does not inline itself', () => {
  assert.equal(resolveAsset(PAGE, './report.html', ROOT), null)
})

test('within is not fooled by a sibling that shares a prefix', () => {
  assert.equal(withinRoot('/a/repo', '/a/repo/x'), true)
  assert.equal(withinRoot('/a/repo', '/a/repo'), true)
  assert.equal(withinRoot('/a/repo', '/a/repo-evil/x'), false)
  assert.equal(withinRoot('/a/repo/', '/a/repo/x'), true)
})

test('planning dedupes by path and keeps the order it found them', () => {
  const refs: AssetRef[] = [
    { kind: 'img', href: './a.png' },
    { kind: 'img', href: 'a.png' },
    { kind: 'style', href: './s.css' },
    { kind: 'img', href: './b.png' },
  ]
  const planned = planAssets(refs, PAGE, ROOT)
  assert.deepEqual(
    planned.map((asset) => `${asset.kind} ${asset.path}`),
    [`img ${ROOT}/docs/a.png`, `style ${ROOT}/docs/s.css`, `img ${ROOT}/docs/b.png`],
  )
})

test('planning drops what it cannot resolve rather than failing', () => {
  const planned = planAssets(
    [
      { kind: 'img', href: 'https://example.com/x.png' },
      { kind: 'img', href: './good.png' },
      { kind: 'img', href: '../../../../etc/passwd' },
    ],
    PAGE,
    ROOT,
  )
  assert.equal(planned.length, 1)
  assert.equal(planned[0]?.path, `${ROOT}/docs/good.png`)
})

test('planning stops at the cap', () => {
  const refs: AssetRef[] = Array.from({ length: MAX_ASSETS + 10 }, (_, i) => ({
    kind: 'img' as const,
    href: `./img-${i}.png`,
  }))
  assert.equal(planAssets(refs, PAGE, ROOT).length, MAX_ASSETS)
})
