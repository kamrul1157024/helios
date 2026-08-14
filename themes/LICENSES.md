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

## Adding your own

Drop any VS Code colour theme JSON into `~/.helios/themes/` and it appears in
the picker; the filename becomes its id, and a file whose id matches a bundled
theme replaces it. Themes with an unresolved `include` still load — the missing
parent is skipped and anything it would have supplied is derived instead.
