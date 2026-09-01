// Mermaid, as a bundle of its own.
//
// It is 3.5 MB — bigger than the rest of the renderer put together — and most
// windows never open a transcript that contains a diagram. So it is built
// separately and fetched by mermaid.ts the first time a fence needs drawing,
// rather than parsed at startup by every window that will never use it.

import mermaid from 'mermaid'

export default mermaid
