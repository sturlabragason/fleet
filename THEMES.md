# Themes from VS Code

fleet ships 20 palettes taken from the 20 most-installed colour themes on the VS Code Marketplace
(ranking read 2026-11-19, icon packs and non-theme extensions left out). Set one with
`theme = "<name>"` under `[defaults]` in `~/.config/fleet/config.toml` and run `fleet up` again, or
press `T` in the overview to preview each theme live and save one with Enter (ADR-175).

fleet uses each theme's published colours: the editor background and foreground, the terminal
ANSI colours, and a token colour where the terminal set has none (`internal/theme/vscode_palettes.go`).
Where a colour is too faint for its job on the theme's own background, fleet lightens it (dark
themes) or darkens it (light themes) only as far as that role's contrast floor (ADR-174). The
`▶` target uses the theme's accent, except where the accent is the theme's yellow, which means
"waiting" in fleet; those themes say which colour they use instead.

Every theme below is MIT-licensed. The copyright notices are the authors'; the MIT licence text
follows the table and applies to each.

| fleet name | Theme | Extension | Copyright |
|---|---|---|---|
| `github-dark`, `github-light` | GitHub Theme | `GitHub.github-vscode-theme` | (c) 2020 Primer |
| `one-dark-pro` | One Dark Pro | `zhuangtongfa.Material-theme` | (c) 2013-2022 Binaryify |
| `dracula` | Dracula Official | `dracula-theme.theme-dracula` | (c) 2016 Dracula Theme |
| `atom-one-dark` | Atom One Dark | `akamud.vscode-theme-onedark` | (c) 2015 Mahmoud Ali |
| `ayu` | Ayu (Dark) | `teabyii.ayu` | (c) 2016 Ike Kurghinyan |
| `winter-is-coming` | Winter is Coming (Dark Blue) | `johnpapa.winteriscoming` | (c) 2015-2017 JohnPapa.net, LLC |
| `night-owl` | Night Owl | `sdras.night-owl` | (c) 2018 Sarah Drasner |
| `tokyo-night` | Tokyo Night | `enkia.tokyo-night` | (c) 2018-present Enkia |
| `one-monokai` | One Monokai | `azemoh.one-monokai` | (c) 2018 Joshua Azemoh |
| `palenight` | Palenight | `whizkydee.material-palenight-theme` | (c) 2017-present Olaolu Olawuyi |
| `synthwave-84` | SynthWave '84 | `RobbOwen.synthwave-vscode` | (c) 2019 Robb Owen |
| `shades-of-purple` | Shades of Purple | `ahmadawais.shades-of-purple` | (c) 2015-∞ Ahmad Awais |
| `cobalt2` | Cobalt2 | `wesbos.theme-cobalt2` | (c) 2018 Wes Bos, Roberto Achar |
| `andromeda` | Andromeda | `EliverLara.andromeda` | (c) 2017 Eliver Lara |
| `catppuccin` | Catppuccin (Mocha) | `Catppuccin.catppuccin-vsc` | (c) 2021 Catppuccin |
| `atom-one-light` | Atom One Light | `akamud.vscode-theme-onelight` | (c) 2015 Mahmoud Ali |
| `noctis` | Noctis | `liviuschera.noctis` | (c) 2018 Liviu Schera |
| `nord` | Nord | `arcticicestudio.nord-visual-studio-code` | (c) 2017-present Arctic Ice Studio, Sven Greb |
| `gruvbox` | Gruvbox (Dark Medium) | `jdinhlife.gruvbox` | © 2017 JD |
| `material` | Sublime Material Theme | `jprestidge.theme-material-theme` | (c) 2016 Jarvis Prestidge |

`catppuccin` was already in fleet, hand-tuned from the Catppuccin palette (ADR-55). Shades of
Purple's licence asks that anything built with it is MIT-licensed as well; fleet is.

Left out of the top 20, and why:

- Monokai Pro: a commercial licence, no source repository.
- Material Theme (Equinusocio): deprecated by its author.
- Bearded Theme: GPL-3.0, which fleet's MIT licence cannot carry.
- Panda Theme: no licence published.
- "Theme" (`tal7aouy.theme`): its colours are the same as One Dark Pro's.
- C/C++ Themes (`ms-vscode.cpptools-themes`): installed as a dependency of the C/C++ extension,
  not chosen as a theme.

The next themes by installs took their places.

## MIT licence

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and
associated documentation files (the "Software"), to deal in the Software without restriction,
including without limitation the rights to use, copy, modify, merge, publish, distribute,
sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or
substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT
NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES
OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN
CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
