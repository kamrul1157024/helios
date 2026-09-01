# Mermaid Diagrams: Draw the Fence Instead of Printing It

## The claim

An agent asked to explain a system draws it. It writes a ` ```mermaid ` fence — a flowchart of
a request path, a sequence diagram of a handshake, a state machine — into the transcript, or
into the `.md` file it leaves behind. Both clients print that fence as text. The reader is
handed the source of a picture and asked to be the renderer.

This spec makes a mermaid fence draw a diagram in four places: the desktop chat, the desktop
markdown preview, the mobile chat, and the mobile markdown preview. A fence that does not parse
keeps the code block it has today.

**Scope: `desktop/` and `mobile/`.** No daemon route changes, no wire format moves, nothing in
`internal/` or `cmd/`. Both clients already have the markdown in hand; this is about what they
do with one kind of fence.

## Where we are

| | Today |
|---|---|
| Desktop chat | `renderMarkdown` → `dangerouslySetInnerHTML` (`chat.tsx:556`) |
| Desktop file preview | `renderMarkdownBlocks`, one block at a time (`files.tsx:711`) |
| Mobile chat | `MarkdownBody` with a `code` builder (`message_card.dart:181`) |
| Mobile file preview | the same, with different padding (`file_browser_screen.dart:834`) |
| A `mermaid` fence, desktop | escaped source: `normalizeLanguage('mermaid')` is null, so no grammar claims it (`markdown.ts:121`) |
| A `mermaid` fence, mobile | unhighlighted monospace: `highlight` 0.7.0 returns the source as one plain node for a language it does not know |

Two facts make this cheap. Both clients already render markdown at exactly four sites, and both
already run a webview or an injected-HTML path with a policy written for untrusted agent output
— `51-file-previews.md` did that work. A diagram is a smaller version of the same problem.

## What was measured before designing

Mermaid is 3.5 MB of JavaScript going into two apps that both refuse the network on purpose, so
the questions below were answered against mermaid 11.17.2 itself rather than assumed.

| Question | Answer |
|---|---|
| Does it need `unsafe-eval`? | **No.** `dist/mermaid.min.js` contains no `eval(` and no `new Function`. The four `Function("return this")()` are lodash `_root` guards, each reached only when `self.Object !== Object` — never in a browser. The desktop's `script-src 'self'` (`main.ts:294-302`) is enough. |
| Does it need `style-src 'unsafe-inline'`? | **Yes**, and unavoidably: `createUserStyles` writes a `<style>` into the SVG. Both policies already allow it. |
| Does it fetch anything at render time? | **No.** Its default font stack is local. KaTeX math and icon shapes do fetch, which is why neither is enabled. |
| Does it lazy-load diagram types? | **No.** The IIFE build has zero `import(`; all 42 detectors are eager. So the mobile webview needs one file and esbuild's IIFE stays one file. |
| Do the labels survive DOMPurify? | **No — and this is the one that bites.** Mermaid puts flowchart labels in a `<foreignObject>`, which DOMPurify does not allow: it is a classic mXSS vector. The diagram arrives as a set of empty boxes. |

That last row is settled by configuration rather than by weakening the sanitiser:
`htmlLabels: false` makes mermaid emit SVG `<text>` instead. Measured in the running app —
`flowchart: { htmlLabels: false }` on its own is **ignored** by the renderer mermaid 11 actually
uses, and only the top-level key turns the foreign objects off.

## Desktop: mark in the markup, draw over the DOM

Mermaid lays a diagram out by measuring the text in it, so it needs a document. `markdown.ts`
has none — it is a pure string-to-string module, and the file-preview path calls it once per
block. So the work splits in two, which is the same split `useInlineImages`
(`html-preview.tsx:206-256`) already uses for images that the renderer cannot fetch:

**The parser marks.** `isMermaidFence` (`markdown.ts`) recognises the tag, and the `code`
renderer emits `<pre class="code-block mermaid-fence">` holding the escaped source. That is the
fallback, already rendered: a fence nobody ever draws is the code block it is today.

**A hook draws.** `useMermaid(container, revision)` walks the injected HTML, renders each marked
fence into an offscreen stage, sanitises the SVG, and swaps the `<pre>` for a
`<figure class="mermaid">` that keeps its source in `data-mermaid`. A throw leaves the `<pre>`
standing. Two call sites: `chat.tsx` on the assistant body, `files.tsx` beside `useInlineImages`.

Keeping the source on the figure is what makes the theme work. Mermaid's colours are baked into
the SVG at render time, so `bridge.theme.onChanged` re-reads the app's CSS variables —
`--surface-container`, `--on-surface`, `--primary`, `--outline-variant` — and redraws what is on
screen. Without it a diagram wears the palette it was born in.

**Mermaid is a bundle of its own.** It is bigger than the rest of the renderer put together;
bundled in, `renderer.js` goes from 1.7 MB to 4.9 MB for a fence most transcripts never contain.
So `build.mjs` builds `src/renderer/mermaid-entry.ts` to `dist/renderer/mermaid.js` and
`mermaid.ts` appends a `<script src="mermaid.js">` the first time a diagram needs drawing — a
sibling file under `script-src 'self'`, the same way `index.html` loads the renderer. If the
load fails there is nothing to fall back to, and nothing to do: the fences stay code blocks.

## Mobile: the smallest possible webview

Mermaid is JavaScript, so mobile needs a webview. `MermaidDiagram`
(`mobile/lib/widgets/mermaid_diagram.dart`) is a much smaller one than `HtmlPreview`, because
the document is written rather than read: a copy of mermaid from the app's own assets, the
fence's source, and a script that draws one diagram and posts back how tall it came out.

The rules are the HTML preview's, reached the same way — by writing the policy into the
document (`default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'`) and
refusing every navigation. Three differences follow from this being ours rather than the
agent's: the script is trusted because we shipped it, there are no assets to inline, and the
view sits inside an `IgnorePointer` — a platform view in a scrolling list swallows the drags
meant for the list, and a diagram has nothing to tap.

**The view is mounted before its height is known, and has to be.** A webview has no intrinsic
size, so the widget asks the page how tall the diagram came out and sizes a `SizedBox` from the
answer. The tempting shape — mount once the answer arrives — does not work, and was measured
not working: a webview with no place in the layout has no surface, never lays the diagram out,
and never answers. So it is mounted at a provisional height and resized on the report.

Mermaid is **carried, not fetched**: `assets/mermaid/mermaid.min.js`, read once per process.
That is 3.6 MB in the tree and roughly 1 MB in the APK. The alternative is a network the app
does not have.

Both `_SyntaxHighlightBuilder`s gain the same two lines: when the language is `mermaid`, return
a `MermaidDiagram` whose `fallback` is the `HighlightView` they would have returned. The fence
is drawn or it is code, and the builder does not need to know which.

## What we are not doing

- **No zoom, no pan.** A wide diagram scrolls sideways on the desktop and fits the width on
  mobile. Tapping one to open it full-screen is a good idea and a separate one.
- **No KaTeX and no icon shapes.** Both fetch, and the whole design rests on nothing fetching.
- **No ELK layouts.** A separate package, and the built-in layout is what a fence gets.
- **No Go or TUI side.** A terminal cannot draw an SVG, and the daemon never looks at prose.
- **No mermaid in the HTML preview.** A page an agent wrote brings its own scripts and already
  has a switch for them (`52`, `51-file-previews.md`); a diagram in one is that page's business.

## Tests

- `desktop/test/markdown.test.ts` — `isMermaidFence` accepts `mermaid`, tolerates case and a
  trailing `title=…`, and rejects `mermaidish`; `normalizeLanguage('mermaid')` is null, which is
  why the marking is needed at all. `renderMarkdown` itself is not asserted here: DOMPurify has
  no `sanitize` outside a DOM and the node runner has no window.
- Measured in the running desktop app over CDP, against a file holding a flowchart, a sequence
  diagram, a deliberately broken fence and a Go fence: two `figure.mermaid` with SVG, one
  `pre.mermaid-fence` left standing, the Go block untouched, every node and edge label present,
  and no CSP violation in the console. Switching the theme to light and back changed the node
  fill and changed it back. The chat path was measured the same way, on a transcript message
  containing a fence.
- Measured on an Android device against the same file: both diagrams drawn with every label,
  sized to their content, the broken fence still a code block, the Go fence untouched, and the
  list still scrolling when the drag starts on top of a diagram. The chat path was measured on
  a transcript message containing a fence.
