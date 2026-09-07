# Synced lyrics

The Queue tab's Lyrics panel shows synced lyrics for the currently displayed
track, highlighting the active line against playback position — the same
model rmpc itself uses for its own Lyrics pane.

## Source

Tidal's API has no public lyrics endpoint, so gotidal fetches from
[LRCLIB](https://lrclib.net) — a free, unauthenticated service most
third-party music clients use for the same purpose. The lookup is an exact
match on artist, title, album, and duration; there is no fuzzy search, so an
unusual edit, live version, or very new release may not be found.

## Caching

Every lookup — a hit or a genuine "not found" — is cached in the same bbolt
database used for track metadata (`~/.local/share/gotidal/gotidal-cache.db`), so
a track without lyrics isn't re-queried every time it's hovered or played.

## Fallback behavior

| LRCLIB response      | Panel shows                                  |
| --------------------- | --------------------------------------------- |
| Synced lyrics (LRC)   | Line-by-line, active line highlighted         |
| Plain lyrics only     | Static text, no highlighting                  |
| No match              | "No lyrics found."                            |
| Fetch error           | "No lyrics found." (fails silently, no retry loop) |

No configuration is required — the panel degrades gracefully in every case
above, and everything else in the app is unaffected either way.
