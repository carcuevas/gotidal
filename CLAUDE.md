# CLAUDE.md

## Build & tooling

- **Go version**: 1.26+
- **Format**: run `gofumpt -w .` after writing or editing any `.go` file
- **Lint (Go)**: run `golangci-lint run` (v2) before finishing a task; fix all reported issues
- **Lint (C)**: run `./lint-c.sh` after editing any `.c`/`.h` file (containerised clang-tidy; `LINT_C_NATIVE=1` to use a local clang-tidy). Config in `.clang-tidy` — its `Checks:` block is a YAML folded scalar and must not contain `#` comments
- **Build**: `go build ./...`
- **CGO**: required — the player package links against libasound (`-lasound`)

## Package overview

### `cmd/gotidal`
Entry point. Handles signal setup, session load/restore from the secrets store, OAuth2 device-flow login on first run, and launches the BubbleTea TUI program.

### `internal/tidal`
Tidal API client.
- `client.go` — OAuth2 device-flow authentication, token refresh, authenticated HTTP client
- `api.go` — REST calls: favorites, search, track lookup, stream URL (quality ladder: HI_RES_LOSSLESS → LOSSLESS → HIGH → LOW), mixes, mix tracks, artist albums/top-tracks/all-tracks
- Daily Mixes use the v1 API: `GET /v1/pages/my_collection_my_mixes` for the list, `GET /v1/mixes/{mixId}/items` for a mix's tracks (fully populated, one request). The v2 `openapi.tidal.com/v2/userRecommendations` resource was removed by Tidal and now 404s — do not reintroduce it. Video mixes and non-`track` items are filtered out, since the player cannot decode video

### `internal/player`
Bit-perfect FLAC playback via CGO + libasound.
- The hand-written C lives in real translation units, not cgo preamble comments: `alsa.h`/`alsa.c` (PCM open + format negotiation, used by `mpv.go`) and `avcodec.h`/`avcodec.c` (FFmpeg decode pipeline, used by `avcodec.go`). The Go files keep only a minimal preamble that `#include`s the header plus the `#cgo` linker directives. `avio_read_cb` is implemented in Go via `//export` and declared in `avcodec.h`
- Opens ALSA `hw:` devices directly, bypassing PipeWire/PulseAudio
- Negotiates the best PCM format the DAC supports using `snd_pcm_hw_params` (no soft resampling)
- Falls back to `plughw:` only when format negotiation itself is refused — i.e. `configure_hw_pcm` fails, tagged with the `errFormatRefused` sentinel — as on fixed-format USB interfaces such as the Focusrite Vocaster. A busy device fails at `open_hw_device` instead, which carries the existing `-EBUSY` retry against `hw:` and is never downgraded. The fallback is memoised per device, and `alsaHandle.bitPerfect` / `Player.AudioPath` propagate the downgrade up to the UI so the quality badge and device readout stop claiming untouched output
- Format preference for 16-bit sources: S32_LE → S16_LE → S24_3LE → S24_LE (S32_LE first because some DACs, e.g. CS43198-based Hidizs S9 Pro Plus, have a broken S16_LE USB endpoint)
- Format preference for 24-bit sources: S24_3LE → S24_LE → S32_LE
- Acquires `org.freedesktop.ReserveDevice1.Audio{N}` on D-Bus before opening the device, asking PipeWire to release if it holds the reservation
- Demuxes and decodes the HTTP stream in-flight via FFmpeg (libavformat/libavcodec/libswresample, CGO) — FLAC, AAC/mp4, and ALAC — resampling to S32LE. A custom AVIO callback feeds bytes straight from the HTTP response. FFmpeg is linked dynamically for local/dev/CI builds (needs the distro's libav*-dev headers); the official distro packages (`packaging/`) bundle a minimal static FFmpeg built from source, selected with the `staticav` build tag
- Volume, pause, and position tracking via atomics
- Auto-detects known DACs (Hidizs S9 Pro, Hidizs S9 Pro Plus "Martha", Focusrite Scarlett Solo) from `/proc/asound/cards`
- `TapPCM()`/`Format()`: a non-blocking tee of the packed PCM buffer (post-volume, pre-`snd_pcm_writei`) for `internal/visualizer`'s CAVA driver. Sends are `select`-with-`default` — a slow/absent consumer drops frames, never blocks the write path. This is the *only* thing in this package that exists for the visualizer; do not add anything else here for it
- `SetInterTrackSilenceMs()`: optional, off-by-default gap of zero-valued PCM written at the gapless track-transition point (`writeSilence` in `mpv.go`), for a downstream CD/DAT recorder's own silence-based auto-track-detection. Never touches either track's own samples. Toggled via the command palette; persisted in bbolt (`SaveInterTrackSilenceMs`/`LoadInterTrackSilenceMs`)

### `internal/store`
Persistent storage.
- OAuth2 session stored securely via `docker/secrets-engine` (system keychain, falling back to age-encrypted file at `~/.config/gotidal/secrets`)
- Volume, selected device, and track metadata cache stored in a bbolt database at `~/.local/share/gotidal/gotidal-cache.db`

### `internal/ui`
BubbleTea TUI model (Model/Update/View). Keybindings and layout follow rmpc
(github.com/mierak/rmpc): a numbered top tab bar (`Section`, 9 entries in
`tabEntries`, `1`-`9` / `Tab` / `gt`/`gT` to switch), not a sidebar.
- `keys.go`: global keys (`handleGlobalKey`), two-key prefix chains (`g`/`o` via `pendingKey` + `handlePendingKey`), generic list nav (`gg`/`G`/half-page/page via `activeCursorRef`), and per-tab handlers
- Queue tab: left column is AlbumArt (square — see `coverBoxDims` in `layout.go`) / Cava (`cava_pane.go`) / Lyrics (`lyrics_pane.go`), right column is the track list (`queueLayout` in `sections.go` degrades gracefully as space runs out)
- AlbumArt rendering: Kitty graphics protocol (`termgfx.go`, `KittySupported()` — ghostty/kitty/WezTerm) or Sixel (`sixel.go`, `SixelSupported()` — foot, detected via `TERM=foot` or, since foot.ini commonly overrides TERM, an ancestor-process walk via `ancestorIsFoot()`/`procParent()`) or Unicode block-art as the fallback (`coverart.go`). **foot supports Sixel, not Kitty graphics** — confirmed by grepping foot's own upstream changelog for every "kitty" mention: all of them are the kitty *keyboard* protocol or OSC-99 notifications, never graphics. Don't re-add foot to `KittySupported()`. Sixel bakes its target pixel size into the encoded data (no Kitty-style "upload once, place many times"), so `syncSixelCover` re-encodes on any geometry change, not just a cover change; `cellPixelSize()` queries the real terminal cell size via `TIOCGWINSZ` to encode at the correct pixel size instead of relying on the terminal to rescale
- Scrollable track and mix lists with a visible window helper
- Artist drill-down (`m.showArtist`): opened with `a` inside the action sheet (`Ctrl+X`); lists the artist's albums plus "Play all tracks" / "Top tracks" entries, loading the chosen tracks into the queue
- Progress bar with playback position, volume display, and device label
- Auto-advances to the next track in the queue when playback finishes
