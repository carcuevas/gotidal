# The goTidal interface

goTidal's terminal interface follows **[rmpc](https://github.com/mierak/rmpc)**:
a numbered top **tab bar** rather than a sidebar, with a now-playing bar and a
context-aware key-hint bar pinned along the bottom. Keybindings match rmpc's
own scheme throughout — see the [README's keybinding table](../README.md#keybindings)
for the full list. This document is a tour of how the pieces fit together.

## Layout

```
┌ 1 ≣ Queue │ 2 ≡ Playlists │ 3 ◎ Artists │ 4 ⊞ Albums │ … ─────────────────┐
│ ╭─ AlbumArt ──╮ ╭─ QUEUE · Late Night · synced ─────────────────────────╮ │
│ │             │ │ 1  May These Noises                            1:18  │ │
│ │  (square)   │ │ 2  Hell Above ♥                                 3:32  │ │
│ │             │ │ …                                                    │ │
│ ├─ Cava ──────┤ │                                                      │ │
│ │ ▁▃▅█▆▄▂▁    │ │                                                      │ │
│ ├─ Lyrics ────┤ │                                                      │ │
│ │ …           │ ╰──────────────────────────────────────────────────────╯ │
├─────────────────┴──────────────────────────────────────────────────────────┤
│ Tangled In The Great Escape                           2:32 / 5:56          │  ← now-playing bar
├──────────────────────────────────────────────────────────────────────────┤
│ j/k Move │ Enter Play │ Ctrl+X Actions │ 1-9 Tabs │ …                     │  ← key hints
└──────────────────────────────────────────────────────────────────────────┘
```

`1`-`9` (or `Tab`/`Shift+Tab`, `gt`/`gT`) switch tabs directly; each tab is a
self-contained view with its own `j`/`k`/`gg`/`G` list navigation. On narrow
terminals the tab bar clips the trailing tabs rather than wrapping, and the
Queue tab's left column (AlbumArt/Cava/Lyrics) hides itself — first Lyrics,
then Cava, then the whole column — as height runs out, so the track list
always stays legible.

## Tabs

- **1 · Queue** — the live playback queue, with the left column showing:
  - **AlbumArt** — the hovered track's cover, rendered as a true visual
    square (Kitty graphics, Sixel, or Unicode block-art, depending on what
    the terminal actually supports — e.g. foot, the default terminal on
    Omarchy, supports Sixel but not the Kitty graphics protocol)
  - **Cava** — a live frequency-bands strip driven by the real `cava` binary
    (see [the visualizer section below](#the-cava-visualizer)); shows a
    placeholder if `cava` isn't installed
  - **Lyrics** — synced lyrics for the current track, highlighted line by
    line (see [docs/lyrics.md](lyrics.md))

  See *The queue* below for the hybrid queue/playlist model.
- **2 · Playlists** — a two-column view: the playlist index on the left
  (sorted alphabetically, case-insensitively — Tidal's own endpoint returns
  them in date order, which moves a playlist every time it's edited), the
  selected playlist's tracks on the right. `Enter`/`l` opens a playlist;
  `Enter` in the detail loads it into the queue and starts playing. `a` adds a
  whole playlist to the queue without opening it, and `d` deletes the selected
  playlist. Deletion goes through a confirmation prompt that only `y` accepts
  (deliberately not `Enter`, the key that opens a playlist in the list behind
  it) — Tidal deletes for good, with no undo and no trash to restore from.
- **3 · Artists** / **4 · Albums** / **5 · Songs** — your Tidal favorites,
  each its own tab. From an artist you can drill into an album to see its
  tracks.
- **6 · Mixes** — your Tidal Daily Mixes; `Enter` loads a mix into the queue.
- **7 · Search** — see *Search* below.
- **8 · History** — recently played tracks, most recent first, persisted
  across sessions.
- **9 · Settings** — the color-scheme picker (see *Themes*).

## The queue (hybrid model)

The queue is your **live workspace**. It is never a saved playlist by itself —
but it remembers where its contents came from, shown in the panel title:

| State | Header | Meaning |
| ----- | ------ | ------- |
| Loaded from a saved playlist, untouched | `QUEUE · <name> · synced` (green) | matches the saved playlist |
| Loaded from a playlist, then edited | `QUEUE · <name> · edited — Ctrl+S save` (amber) | you've added/reordered tracks |
| Built from radio or ad-hoc adds | `QUEUE · radio · unsaved — Ctrl+S save` (amber) | nothing saved yet |

Editing the queue (play-next, add-to-queue) **never** rewrites the saved
playlist it came from. Press `Ctrl+S` (or run the command palette's
*Save queue as playlist…*) to commit the current queue as a brand-new
playlist — either way it asks for a name first, pre-suggesting one you can
accept with a single Enter;
*Save queue to existing playlist…* appends it to one you already have. A green
toast confirms the save.

`d` removes the selected track from the queue, `D` clears it (the current
track keeps playing in both cases), and `K`/`J` reorder a track up or down.

## The action sheet

Press `Ctrl+X` on any track to open a contextual popup of actions, so the same
set of operations is available everywhere — queue, search results, playlist
detail, history:

- ▸ Play now · ⏭ Play next (`n`) · ＋ Add to queue (`e`)
- ≡ Add to playlist… · ∿ Start radio from this (`r`)
- ♫ Go to artist (`a`) · ⊞ Go to album (`A`)
- ♥ Favorite (`f`) · ⎘ Copy Tidal link (`c`)

Each action also has the single-key shortcut shown in parentheses, usable
directly on the list without opening the sheet — note these sheet-local
shortcuts are independent of the outer keybindings (e.g. `a` inside the sheet
means "go to artist", while bare `a` outside it means "add to queue").

## The command palette

Press `:` or `Ctrl+P` to open a fuzzy command palette. Type to filter, `j`/`k`
(or `Ctrl+J`/`Ctrl+K`) to move, `Enter` to run. It groups into:

- **ACTIONS** — *Save queue as playlist…*, *Save queue to existing playlist…*,
  *Clear queue*, *Import from Spotify…*, *Toggle CD-recorder silence gap…*
- **JUMP TO** — every tab

## CD-recorder silence gap

gotidal normally plays gapless — no silence between tracks — which also means
a downstream CD/DAT recorder listening on the DAC's output has nothing to key
its own silence-based auto-track-detection off. The command palette's
*Toggle CD-recorder silence gap…* inserts a 2-second gap of true digital
silence between tracks instead (off by default). The gap is pure zero-valued
PCM written between tracks — it never touches either track's own samples, so
it doesn't affect bit-perfectness either way; it just trades gapless playback
away while it's on. The setting persists across launches.

## Importing from Spotify

*Import from Spotify…* (in the command palette) opens an overlay where you paste a
Spotify track or playlist URL. gotidal finds each song on Tidal, shows a review list
marking unmatched tracks as **not available**, and lets you either create a Tidal
playlist or load the matches into the queue. No Spotify login is needed. See
[spotify-import.md](spotify-import.md) for details and limitations.

## Search

Search queries Tidal across categories and groups the results into **SONGS**,
**ARTISTS**, and **ALBUMS**. The cursor moves across all groups; `Enter` does the
natural thing for the highlighted row — play a song, drill into an artist's
discography, or load an album into the queue. Track rows also accept the action
sheet and the `F`/`r` shortcuts. You can also paste a Spotify URL into the search
box to load the matched Tidal tracks straight into the queue.

## Themes

The **Settings** tab is a live theme picker. Eight schemes ship built in —
goTidal (the default slate look), Catppuccin Mocha, Tokyo Night, Gruvbox Dark,
Nord, Rosé Pine, Dracula, and Amber CRT — plus an **Auto — match terminal**
option that follows your terminal's own colors.

Moving the cursor with `j`/`k` **previews** the scheme by re-theming the whole
interface instantly; `Enter` applies and saves it, and `Esc` cancels the preview
and reverts. The chosen theme persists across launches. On the Settings tab, `t` is a
shortcut that cycles straight to the next scheme without opening the picker.
It is scoped to that tab on purpose: as a global binding it fired while typing
into text prompts (naming a new playlist, for instance) and changed the theme
mid-word.

## The CAVA visualizer

The Queue tab's Cava panel shows a live spectrum-analyzer strip, driven by the
real [`cava`](https://github.com/karlstav/cava) binary — the same architecture
rmpc itself uses, not a reimplemented FFT. gotidal tees a copy of the
already-decoded PCM into a FIFO cava reads, so the visualizer never touches
the bit-perfect ALSA write path: it's a pure additive side channel, and a
consumer that can't keep up just drops frames rather than affecting audio.

`cava` is an **optional** runtime dependency — with it not installed, the
panel shows a static placeholder and nothing else changes.

## Client mode

When a second goTidal instance starts while one is already running, it launches in
**client mode**: it forwards playback to the running instance over D-Bus instead
of opening the audio device itself (and does not run its own Cava visualizer,
since it has no local PCM to tap). A client instance is tinted with a
steel-blue accent and shows a `⇄ CLIENT` badge in the tab bar, so it's
always clear which instance owns playback. The chosen theme still applies; only
the focus accent changes. See [client-server.md](client-server.md) for details.
