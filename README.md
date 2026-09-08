![gotidal TUI](docs/gotidal.png)

**goTidal** is a Tidal music player for Linux that delivers **bit-perfect, lossless audio** directly to your DAC by default — no PipeWire, no PulseAudio, no resampling. No DAC? A PipeWire mode is one toggle away (Settings tab or command palette), trading bit-perfectness for playback through whatever output PipeWire already manages — laptop speakers, HDMI, Bluetooth. (A small number of USB interfaces expose a fixed native format and cannot be driven bit-perfect either way; see [fixed-format audio interfaces](#fixed-format-audio-interfaces).)

It is built on top of the Tidal API and can run in three ways:

- **Interactive TUI** — browse, search, and control playback from the terminal
- **Daemon** — headless background process, controlled via the TUI or any MPRIS2 client
- **Client** — lightweight TUI that forwards commands to a running daemon over D-Bus

All three modes share the same playback engine. The daemon holds exclusive access to the audio device only while a track is actually playing — releasing it on pause so other applications can use it freely.

> 100% vibe coded with [Claude](https://claude.ai) — grown out of an earlier player called tidalt, renamed and extended into goTidal.

**Linux only.** Requires a Tidal HiFi or HiFi Plus subscription.

---

## Install

Pre-built packages are available on the [releases page](https://github.com/carcuevas/gotidal/releases).
The official Docker image is available at [`ghcr.io/carcuevas/gotidal`](https://github.com/carcuevas/gotidal/pkgs/container/gotidal) — see [docs/docker.md](docs/docker.md) for usage.

### Arch Linux

```bash
sudo pacman -U gotidal-*.pkg.tar.zst
```

### Debian / Ubuntu

```bash
sudo dpkg -i gotidal_*.deb
sudo apt-get install -f
```

### Fedora

```bash
sudo dnf install gotidal-*.rpm
```

### Build packages locally with Docker

All packages (Arch, Debian, Fedora — amd64 and arm64) can be built locally
with a single command using Docker Buildx bake:

```bash
# One-time: create a multi-platform builder
docker buildx create --use

# Build all packages (replace VERSION as needed)
docker buildx bake \
  --file docker-bake.hcl \
  --set "*.args.VERSION=1.0.0" \
  --set "*.output=type=local,dest=dist"
```

Artifacts land in `dist/`:

```
dist/
  gotidal-1.0.0-1-x86_64.pkg.tar.zst   # Arch
  gotidal_1.0.0-1_amd64.deb            # Debian / Ubuntu (amd64)
  gotidal_1.0.0-1_arm64.deb            # Debian / Ubuntu (arm64)
  gotidal-1.0.0-1.fc43.x86_64.rpm      # Fedora (amd64)
  gotidal-1.0.0-1.fc43.aarch64.rpm     # Fedora (arm64)
```

To build a single target: append `debian`, `arch`, or `fedora` to the command.

See [docs/installation.md](docs/installation.md) for building packages locally
without Docker or installing from source.

### Post-install

Register the `tidal://` URL handler so clicking **"Open in desktop app"** on
tidal.com opens the track directly in gotidal:

```bash
gotidal setup
```

Optionally install gotidal as a systemd user service (starts at login, no
terminal window):

```bash
gotidal setup --daemon
```

---

On first launch you will be prompted to log in via the Tidal OAuth2 device flow. Your session is saved to the system keychain (or an age-encrypted file at `~/.config/gotidal/secrets`) and reused on subsequent runs.

---

## Features

- **rmpc-style numbered tab bar** — Queue, Playlists, Artists, Albums, Songs, Mixes, Search, History, and Settings, each its own tab (`1`-`9`, `Tab`/`Shift+Tab`); keybindings follow [rmpc](https://github.com/mierak/rmpc) throughout, so if you already know rmpc you already know gotidal
- **Square album art** — the Queue tab's cover-art panel is sized to render as a true visual square (accounting for the terminal's cell aspect ratio), not just a square cell count
- **CAVA spectrum visualizer** — a live frequency-bands strip in the Queue tab, driven by the real [`cava`](https://github.com/karlstav/cava) binary over a FIFO tee of already-decoded audio. Optional: with no `cava` installed the pane just shows a placeholder, and nothing else changes — the tee never touches the bit-perfect ALSA write path
- **Synced lyrics** — a Lyrics panel in the Queue tab, fetched from [LRCLIB](https://lrclib.net) and highlighted line-by-line against playback position; falls back to plain lyrics or "No lyrics found" gracefully
- **Contextual action sheet** (`Ctrl+X`) — from any track, open a popup of actions: play now, play next, add to queue, add to playlist, start radio, go to artist/album, favorite, copy link
- **Command palette** (`:` or `Ctrl+P`) — fuzzy-run any action or jump to any tab
- **Hybrid queue / playlist model** — the queue is your live workspace; opening a saved playlist loads it and tracks its origin. The header shows `synced`, `edited — Ctrl+S a save`, or `radio · unsaved — Ctrl+S a save`, and `Ctrl+S a` saves the queue as a new playlist. Edits never silently change a saved playlist
- **First-class favorites** — browse favorite songs, artists, and albums as their own tabs
- **Grouped search** — results are split into Songs / Artists / Albums; drill into an artist or album from any hit
- **Import from Spotify** — paste a Spotify track or playlist URL (command palette → "Import from Spotify…") and gotidal finds each song on Tidal, flagging anything it can't match as _not available_; then create a Tidal playlist or load the matches into the queue. No Spotify login required. See [docs/spotify-import.md](docs/spotify-import.md)
- **In-app theme picker** — ten built-in color schemes (goTidal, Catppuccin Mocha, Tokyo Night, Gruvbox, Nord, Rosé Pine, Dracula, Amber CRT, Golden Hour, Misty Forest) plus an "Auto — match terminal" option, opened from Settings' Themes row as its own floating popup, with live preview as you move the cursor; the choice is persisted
- **CD-recorder silence gap** (command palette → "Toggle CD-recorder silence gap…") — inserts a 2-second gap of true digital silence between tracks instead of gapless playback, so a downstream CD/DAT recorder's own silence-based auto-track-detection has something to key off. Off by default; never touches either track's own samples, so it doesn't affect bit-perfectness either way
- Artist view — browse an artist's full discography and play everything, their top tracks, or a single album
- Song radio — build a queue of similar tracks for any song
- Shuffle toggle (`x`) plus a one-shot queue reshuffle (`X`)
- Bit-perfect FLAC playback via direct ALSA `hw:`, bypassing PipeWire/PulseAudio entirely (see [fixed-format devices](#fixed-format-audio-interfaces)) — or toggle **PipeWire mode** (Settings tab / command palette) to play through any PipeWire-managed output instead, no DAC required
- Auto-negotiates the best PCM format your DAC supports
- Auto-advances through the queue; respects shuffle mode
- Volume control and output device selection, both persisted between sessions
- Session and playback position restored on next launch
- MPRIS2 registration — media keys and `playerctl` work without TUI focus
- Daemon mode — run headless in the background, control via TUI client or playerctl

See [docs/ui.md](docs/ui.md) for a full tour of the interface.

---

## Keybindings

### In-TUI

Keybindings follow **[rmpc](https://github.com/mierak/rmpc)** — if you already
use rmpc, you already know gotidal. The interface is a numbered top tab bar
(`1`-`9`) rather than a sidebar; each tab is its own self-contained view.

| Key                 | Action                                                                     |
| ------------------- | --------------------------------------------------------------------------|
| `1`-`9`             | Jump to a tab (Queue, Playlists, Artists, Albums, Songs, Mixes, Search, History, Settings) |
| `Tab` / `gt`        | Next tab                                                                   |
| `Shift+Tab` / `gT`  | Previous tab                                                               |
| `j` / `k` (`↓`/`↑`) | Move the cursor                                                            |
| `gg` / `G`          | Jump to the top / bottom of the current list                              |
| `Ctrl+u` / `Ctrl+d` | Half-page up / down                                                        |
| `Ctrl+b` / `Ctrl+f` | Page up / down                                                             |
| `Enter`             | Open the tab's item / play the selected track / confirm                    |
| `p`                 | Play / pause                                                               |
| `s`                 | Stop (pause and rewind to the start)                                       |
| `f` / `b`           | Seek forward / back 10 seconds                                             |
| `>` / `<`           | Next / previous track                                                     |
| `.` / `,`           | Volume up / down 5%                                                        |
| `x` / `X`           | Toggle shuffle / one-shot reshuffle the queue (Queue tab)                  |
| `a` / `A`           | Add the selected track / add every visible track to the queue              |
| `d` / `D`           | Remove the selected track from the queue / clear the queue (Queue tab)     |
| `K` / `J`           | Move a queue item up / down (Queue tab)                                    |
| `F`                 | Toggle favorite on the selected track                                     |
| `r`                 | Start a radio queue from the selected track                                |
| `y`                 | Copy the current track's Tidal link to the clipboard                       |
| `Ctrl+X`            | Open the contextual action sheet for the selected track                    |
| `oo`                | Open the output device selector                                            |
| `oI`                | Show current-song info                                                     |
| `Ctrl+S a`          | Save the current queue as a new playlist                                   |
| `t`                 | Cycle the color theme                                                      |
| `:` / `Ctrl+P`      | Open the command palette                                                   |
| `/`                 | Jump to Search                                                             |
| `?`                 | Show the full keybinding reference                                        |
| `Esc`               | Close an overlay / back out of the artist view / playlist detail          |
| `q` / `Ctrl+C`      | Quit                                                                       |

### Global shortcuts (MPRIS2)

> **Daemon mode required.** These shortcuts only work when `gotidal` is running as a background daemon (via `gotidal setup --daemon` / systemd). A plain `gotidal` TUI session does not register a persistent MPRIS2 service, so media keys and `playerctl` will have no effect when the TUI is closed.

When the daemon is running, `gotidal` registers as an MPRIS2 media player so playback can be controlled from any MPRIS2 client — `playerctl`, KDE Connect, your desktop environment's media key handler — without a TUI open.

#### Standard media keys

Many keyboards and desktop environments map dedicated media keys directly to MPRIS2:

| Key        | Action         |
| ---------- | -------------- |
| `fn` + `.` | Play / pause   |
| `fn` + `,` | Previous track |
| `fn` + `/` | Next track     |

These are handled by your desktop environment via MPRIS2 — gotidal does not implement any special key capture itself.

#### Custom bindings (65% keyboards)

On keyboards without dedicated media keys, bind [`playerctl`](https://github.com/altdesktop/playerctl) to custom shortcuts via your desktop environment:

| Shortcut | Action         |
| -------- | -------------- |
| `Alt+0`  | Previous track |
| `Alt+-`  | Play / pause   |
| `Alt+=`  | Next track     |

See [docs/media-keys.md](docs/media-keys.md) for full setup instructions.

---

## Supported DACs

Auto-detection scans `/proc/asound/cards`. Any ALSA-visible device can be selected manually with the `d` key.

| DAC                          |  Auto-detected   |
| ---------------------------- | :--------------: |
| Hidizs S9 Pro                |       Yes        |
| Hidizs S9 Pro Plus ("Martha") |       Yes        |
| Focusrite Scarlett Solo      |       Yes        |
| Any ALSA-visible device      | Manual (`d` key) |

### Fixed-format audio interfaces

Most DACs let gotidal negotiate the stream's native shape on the `hw:` endpoint, which is what makes bit-perfect output possible. A few USB audio interfaces — Focusrite's Vocaster line, for example — instead expose a *fixed* native channel count, sample rate, and format, and reject anything else outright.

When gotidal detects that refusal it reopens the device through ALSA's plug layer (`plughw:`), which resamples and remixes to whatever the hardware accepts. Playback works, but the output is **no longer bit-perfect**. The now-playing bar makes this visible: the device readout shows the `plughw:` device actually in use, and the quality badge is marked `(converted)`.

This fallback only engages on a genuine format refusal. Transient failures — such as PipeWire not having finished releasing the device — are retried against `hw:` as before, so a device that can do bit-perfect output is never silently downgraded.

---

## Data & Storage

| What                       | Where                                                         |
| -------------------------- | ------------------------------------------------------------- |
| OAuth2 session             | System keychain or `~/.config/gotidal/secrets` (age-encrypted) |
| Volume & device preference | `~/.local/share/gotidal/gotidal-cache.db`                        |
| Track metadata cache       | Same database                                                 |
| Lyrics cache (hit or miss) | Same database                                                 |

---

## License

gotidal is licensed under the **[Apache License, Version 2.0](LICENSE)**.

This repository is a fork of [Benehiko/tidalt](https://github.com/Benehiko/tidalt)
(also Apache-2.0); files have been modified from the original — see
[NOTICE](NOTICE) for the attribution this carries under the license.

Two features in this fork talk to third-party software at runtime, neither of
which is bundled or linked into gotidal itself:

- The spectrum visualizer invokes the system's **[`cava`](https://github.com/karlstav/cava)**
  binary (GPL-3.0) as a subprocess, if installed — an optional runtime
  dependency, not a bundled or linked one, so no copyleft obligation attaches
  to gotidal.
- Synced lyrics are fetched at runtime from the free, public **[LRCLIB](https://lrclib.net)**
  API.

Sixel cover-art encoding is different: it's a real, compiled-in dependency —
**[mattn/go-sixel](https://github.com/mattn/go-sixel)** and
**[soniakeys/quant](https://github.com/soniakeys/quant)**, both MIT-licensed,
vendored under `vendor/` with their upstream `LICENSE` files intact. MIT is
permissive and Apache-2.0-compatible; it asks only that those notices be
preserved, which the vendored copies already do.

---

## Further reading

- [Installation (packages, source, post-install setup)](docs/installation.md)
- [Running in Docker](docs/docker.md)
- [Architecture & audio pipeline](docs/architecture.md)
- [Development (building, linting, git hooks)](docs/development.md)
- [Client-server architecture & daemon mode](docs/client-server.md)
- [MPRIS2 support](docs/mpris2.md)
- [Importing from Spotify](docs/spotify-import.md)
- [DAC compatibility](docs/dac-compatibility.md)
- [Synced lyrics](docs/lyrics.md)
- [Media keys & MPRIS2 setup](docs/media-keys.md)
- [Browser URL handler troubleshooting](docs/browser-url-handler.md)
- [Debugging](docs/debugging.md)
