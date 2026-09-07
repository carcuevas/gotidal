# Client-server architecture

gotidal uses a single-server, many-client model built on D-Bus. One process owns
the ALSA device and the MPRIS2 bus name; every other `gotidal` invocation
becomes a lightweight client that forwards commands to it.

---

## Why this design?

### Only one process touches the DAC

ALSA `hw:` devices cannot be shared between processes. If two programs both try
to open `hw:1,0` the second one fails. gotidal solves this by making the first
instance the exclusive owner of the device for the duration of its lifetime.
Subsequent invocations detect the running server over D-Bus and operate as
clients — they never attempt to open the sound card themselves.

### Browser `tidal://` links work without a full TUI

When you click **"Open in desktop app"** on tidal.com, the OS invokes
`gotidal play tidal://track/<id>`. If a server is already running the `play`
subcommand delivers the track ID over D-Bus in milliseconds and exits — no
terminal, no second TUI. Without this design the browser handler would need
to open a terminal and start a second, conflicting player instance.

### Daemon mode: music in the background

`gotidal daemon` runs the full playback engine with no terminal and no UI. You
can start it at login via systemd (`gotidal setup --daemon`), then control it
any time using:

- `gotidal` — opens the full TUI in client mode
- `playerctl` — standard MPRIS2 CLI
- media keys — via MPRIS2 (works system-wide, no TUI focus needed)
- browser links — forwarded automatically by `gotidal play`

---

## How instances discover each other

gotidal claims the D-Bus name `org.mpris.MediaPlayer2.gotidal` on the session
bus at startup. If the name is already taken (`ErrAlreadyRunning`) the process
switches to client mode instead of exiting.

```
gotidal (server)          gotidal (client)         gotidal play <url>
──────────────           ───────────────         ─────────────────
owns ALSA hw:            no audio device         no audio device
owns D-Bus name          connects to name        sends one D-Bus call
runs playback loop       forwards commands        exits immediately
serves MPRIS2            shows TUI               (spawned by browser)
```

---

## Running modes

| Invocation | Behaviour |
|---|---|
| `gotidal` | Full TUI. First instance → server. Second instance → client TUI. |
| `gotidal daemon` | Headless server. No TUI, no terminal required. |
| `gotidal play <url>` | Forwards URL to running server; starts a terminal TUI if none is running. |
| `gotidal setup` | Registers the `tidal://` URL handler with XDG. |
| `gotidal setup --daemon` | Installs and starts a systemd `--user` service. |

---

## Daemon mode

### Starting manually

```bash
gotidal daemon
```

The process stays in the foreground (so you can see log output). Press
`Ctrl+C` or send `SIGTERM` to stop.

The ALSA device and D-Bus audio reservation are **not** acquired at startup.
The daemon holds no audio hardware until a track actually begins playing, at
which point it claims the device exclusively. The device is released again when
playback stops or the daemon exits.

### Installing as a systemd user service

```bash
gotidal setup --daemon
```

This writes `~/.config/systemd/user/gotidal.service`, then runs:

```
systemctl --user daemon-reload
systemctl --user enable gotidal.service
systemctl --user start  gotidal.service
```

The service starts automatically after your graphical session (display
manager) is ready and restarts on failure.

Useful commands:

```bash
systemctl --user status gotidal          # is it running?
journalctl --user -u gotidal -f          # live logs
systemctl --user stop gotidal            # stop now
systemctl --user disable --now gotidal   # stop and remove from autostart
```

### Controlling the daemon

Once `gotidal daemon` is running, open the TUI from any terminal:

```bash
gotidal          # full TUI in client mode
```

Or use playerctl:

```bash
playerctl --player=gotidal play-pause
playerctl --player=gotidal next
playerctl --player=gotidal previous
```

Or click **"Open in desktop app"** on tidal.com — it calls `gotidal play
tidal://track/<id>` which the daemon handles without opening a terminal.

---

## D-Bus interfaces

### Standard MPRIS2 (`org.mpris.MediaPlayer2.*`)

Exposed on bus name `org.mpris.MediaPlayer2.gotidal`, object path
`/org/mpris/MediaPlayer2`.

### Private interface (`io.gotidal.App`)

Used by client instances to communicate with the server. Not part of the
MPRIS2 spec.

| Method | Arguments | Description |
|---|---|---|
| `OpenURL` | `url: string` | Queue and play a `tidal://` or `https://tidal.com/` URL |
| `PlayTrackID` | `trackID: int32` | Play a track by its Tidal numeric ID |
| `GetState` | — | Returns current track JSON, playlist JSON, status, position, duration, volume, device, shuffle mode |

See [mpris2.md](mpris2.md) for the full MPRIS2 interface documentation.
