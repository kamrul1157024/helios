# Bundled colour themes

Every file in this directory is a VS Code colour theme, vendored from the
project named below and flattened into a single self-contained file (upstream
themes are often an `include` chain rooted at a VS Code built-in, which only
resolves inside VS Code). Colour values and token rules are unmodified.

Regenerate with `node --experimental-strip-types desktop/scripts/fetch-themes.mjs`.

All of the below are MIT licensed.

| File | Theme | Upstream |
| --- | --- | --- |
| `dark-modern.json` | Dark Modern | [microsoft/vscode](https://github.com/microsoft/vscode) — `extensions/theme-defaults` |
| `light-modern.json` | Light Modern | [microsoft/vscode](https://github.com/microsoft/vscode) — `extensions/theme-defaults` |
| `monokai.json` | Monokai | [microsoft/vscode](https://github.com/microsoft/vscode) — `extensions/theme-monokai`, after the Monokai theme by Wimer Hazenberg |
| `solarized-dark.json` | Solarized Dark | [microsoft/vscode](https://github.com/microsoft/vscode) — `extensions/theme-solarized-dark`, after Solarized by Ethan Schoonover |
| `solarized-light.json` | Solarized Light | [microsoft/vscode](https://github.com/microsoft/vscode) — `extensions/theme-solarized-light`, after Solarized by Ethan Schoonover |
| `tokyo-night.json` | Tokyo Night | [enkia/tokyo-night-vscode-theme](https://github.com/enkia/tokyo-night-vscode-theme) |
| `nord.json` | Nord | [nordtheme/visual-studio-code](https://github.com/nordtheme/visual-studio-code) |
| `one-dark-pro.json` | One Dark Pro | [Binaryify/OneDark-Pro](https://github.com/Binaryify/OneDark-Pro) |
| `catppuccin-mocha.json` | Catppuccin Mocha | [catppuccin/vscode](https://github.com/catppuccin/vscode) |
| `catppuccin-latte.json` | Catppuccin Latte | [catppuccin/vscode](https://github.com/catppuccin/vscode) |
| `dracula.json` | Dracula | [dracula/visual-studio-code](https://github.com/dracula/visual-studio-code) |
| `github-dark.json` | GitHub Dark | [primer/github-vscode-theme](https://github.com/primer/github-vscode-theme) |
| `github-light.json` | GitHub Light | [primer/github-vscode-theme](https://github.com/primer/github-vscode-theme) |
| `gruvbox-dark.json` | Gruvbox Dark Medium | [jdinhlife/gruvbox](https://github.com/jdinhlife/gruvbox) |
| `gruvbox-light.json` | Gruvbox Light Medium | [jdinhlife/gruvbox](https://github.com/jdinhlife/gruvbox) |
| `liquid-glass.json` | Liquid Glass | Helios, deepened from Dark Modern and carrying its token colours |
| `liquid-glass-light.json` | Liquid Glass Light | Helios, from Light Modern and carrying its token colours |

## Translucency

A theme may ask the window to let the desktop through by carrying a
`helios.glass` block, which VS Code ignores:

```json
"helios.glass": { "sidebar": 0.34, "panel": 0.42, "terminal": 0.34 }
```

Each value is the opacity that surface keeps, so 1 is solid and lower is more
desktop. They are clamped to 0.25 at the low end — below that the text on a
surface stops being readable whatever is behind it. Only macOS has the system
material to show; elsewhere a glass theme falls back to its opaque colours.

A light theme needs far more body than a dark one for the same result. Its
surfaces start near white, so every point of transparency drags them towards
whatever is behind; too far and the theme stops reading as light at all and
just turns grey. The bundled light theme sits at 0.72/0.8/0.85 against the
dark theme's 0.34/0.42/0.34.

The terminal is closer to code than to chrome — dense text, read a character at
a time — so it is the surface to keep most solid.

The two Liquid Glass themes are the bundled examples, and are the only files
here that are not vendored unmodified.

## Adding your own

Drop any VS Code colour theme JSON into `~/.helios/themes/` and it appears in
the picker; the filename becomes its id, and a file whose id matches a bundled
theme replaces it. Themes with an unresolved `include` still load — the missing
parent is skipped and anything it would have supplied is derived instead.
