// A gutter looks authoritative, so the rule these enforce is that a number is
// only shown when the file settles the question — never when it is a guess.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { hunkHeader, lineOf } from '../src/renderer/components/edit-offsets.ts'

const file = ['package main', '', 'func main() {', '\tprintln("hi")', '}', ''].join('\n')

test('a unique match reports the line it starts on, counting from 1', () => {
  assert.equal(lineOf(file, 'package main'), 1)
  assert.equal(lineOf(file, 'func main() {'), 3)
  assert.equal(lineOf(file, '\tprintln("hi")\n}'), 4)
})

test('text the file does not hold has no line', () => {
  assert.equal(lineOf(file, 'func other() {'), null)
})

test('text the file holds twice has no line either', () => {
  const twice = 'a\nsame\nb\nsame\n'
  assert.equal(lineOf(twice, 'same'), null)
})

test('an empty needle or an empty file answers nothing', () => {
  assert.equal(lineOf(file, ''), null)
  assert.equal(lineOf('', 'anything'), null)
})

test('the header counts the lines each side of the patch', () => {
  const diff = [' kept', '-gone', '+added', '+also added'].join('\n')
  assert.equal(hunkHeader(diff, 10), '@@ -10,2 +10,3 @@')
})

test('no start line means no header, so the patch stays unnumbered', () => {
  assert.equal(hunkHeader(' kept\n+added', null), '')
})
