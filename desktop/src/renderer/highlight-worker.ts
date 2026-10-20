// The highlighting worker (spec 65, 2.6).
//
// highlight.js runs a grammar over every line it is given, and a long patch or
// a README full of fences was enough of those to hold the window's one thread
// for longer than a person notices. Here it holds nobody's.
//
// One message shape in each direction: texts and a language in, one HTML string
// per text out, in the same order. Each text is highlighted on its own, which
// is what a diff line and a code fence both are.
import { highlightCode } from './hljs-core.ts'

interface Request {
  id: number
  language: string
  texts: string[]
}

self.onmessage = (event: MessageEvent<Request>) => {
  const { id, language, texts } = event.data
  const html = texts.map((text) => highlightCode(text, language))
  ;(self as unknown as { postMessage(message: unknown): void }).postMessage({ id, html })
}
