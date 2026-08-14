import { HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { EditorView } from '@codemirror/view'
import { tags } from '@lezer/highlight'
import type { Extension } from '@codemirror/state'

/**
 * The editor, in terms of the same variables the rest of the app uses.
 *
 * Every colour is a `var()` rather than a resolved value, so this extension is
 * built once and still follows the theme: CodeMirror injects it as a stylesheet
 * and the browser resolves the variables against `<html>` at paint. Rebuilding
 * the editor on a theme change would be the alternative, and it would discard
 * the undo history to recolour a buffer.
 */
const highlight = HighlightStyle.define([
  { tag: [tags.comment, tags.lineComment, tags.blockComment], color: 'var(--syn-comment)', fontStyle: 'italic' },
  { tag: [tags.keyword, tags.modifier, tags.controlKeyword, tags.moduleKeyword], color: 'var(--syn-keyword)' },
  { tag: [tags.string, tags.special(tags.string), tags.inserted], color: 'var(--syn-string)' },
  { tag: tags.regexp, color: 'var(--syn-regexp)' },
  { tag: [tags.number, tags.integer, tags.float], color: 'var(--syn-number)' },
  { tag: [tags.bool, tags.null, tags.atom], color: 'var(--syn-literal)' },
  { tag: [tags.constant(tags.variableName), tags.standard(tags.name)], color: 'var(--syn-constant)' },
  { tag: [tags.function(tags.variableName), tags.function(tags.propertyName)], color: 'var(--syn-function)' },
  { tag: [tags.typeName, tags.className, tags.namespace, tags.self], color: 'var(--syn-type)' },
  { tag: [tags.variableName, tags.definition(tags.variableName)], color: 'var(--syn-variable)' },
  { tag: [tags.propertyName, tags.definition(tags.propertyName)], color: 'var(--syn-property)' },
  { tag: [tags.attributeName, tags.attributeValue], color: 'var(--syn-attribute)' },
  { tag: [tags.tagName, tags.angleBracket, tags.deleted], color: 'var(--syn-tag)' },
  { tag: [tags.meta, tags.processingInstruction, tags.heading], color: 'var(--syn-meta)' },
  { tag: [tags.operator, tags.operatorKeyword, tags.derefOperator], color: 'var(--syn-operator)' },
  { tag: [tags.punctuation, tags.separator, tags.bracket], color: 'var(--syn-punctuation)' },
  { tag: tags.invalid, color: 'var(--error)' },
  { tag: tags.link, color: 'var(--primary)', textDecoration: 'underline' },
  { tag: tags.emphasis, fontStyle: 'italic' },
  { tag: tags.strong, fontWeight: 'bold' },
])

const chrome = EditorView.theme({
  '&': { color: 'var(--syn-fg)', backgroundColor: 'var(--surface)' },
  '.cm-content': { caretColor: 'var(--primary)' },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--primary)' },
  '&.cm-focused .cm-selectionBackgroundValue, .cm-selectionBackground, .cm-content ::selection': {
    backgroundColor: 'var(--primary-container)',
  },
  '.cm-gutters': {
    backgroundColor: 'var(--surface)',
    color: 'var(--on-surface-variant)',
    border: 'none',
    borderRight: '1px solid var(--outline-variant)',
  },
  '.cm-activeLine': { backgroundColor: 'var(--surface-low)' },
  '.cm-activeLineGutter': { backgroundColor: 'var(--surface-low)', color: 'var(--on-surface)' },
  '.cm-foldPlaceholder': {
    backgroundColor: 'var(--surface-high)',
    color: 'var(--on-surface-variant)',
    border: 'none',
  },
  '.cm-searchMatch': { backgroundColor: 'var(--primary-container)', outline: '1px solid var(--outline)' },
  '.cm-searchMatch.cm-searchMatch-selected': { backgroundColor: 'var(--primary)', color: 'var(--on-primary)' },
  '.cm-selectionMatch': { backgroundColor: 'var(--surface-highest)' },
  '.cm-matchingBracket, .cm-nonmatchingBracket': {
    backgroundColor: 'var(--surface-highest)',
    outline: '1px solid var(--outline)',
  },
  '.cm-tooltip': {
    backgroundColor: 'var(--surface-high)',
    color: 'var(--on-surface)',
    border: '1px solid var(--outline-variant)',
  },
  '.cm-tooltip .cm-tooltip-arrow:after': { borderTopColor: 'var(--surface-high)' },
  '.cm-panels': { backgroundColor: 'var(--surface-container)', color: 'var(--on-surface)' },
})

export const heliosEditorTheme: Extension = [chrome, syntaxHighlighting(highlight)]
