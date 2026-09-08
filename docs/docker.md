# Running gotidal in Docker

The official image is published to the GitHub Container Registry at
`ghcr.io/carcuevas/gotidal` and is built for `linux/amd64` and `linux/arm64`.

---

## Audio devices

gotidal opens ALSA `hw:` devices directly. Two things are needed to make that work
inside a container:

### 1. Expose `/dev/snd`

Pass `--device /dev/snd` to give the container access to all ALSA PCM and control
nodes. This also makes `/proc/asound` readable inside the container, which is how
gotidal discovers available cards.

### 2. Join the `audio` group

The devices under `/dev/snd` are owned by `root:audio` on the host
(`crw-rw---- 1 root audio`). The process inside the container must be in the
`audio` group to open them.

```bash
--group-add $(getent group audio | cut -d: -f3)
```

### List available devices on your host

```bash
cat /proc/asound/cards
```

The same output is visible inside a running container — no extra flags needed,
`/proc` is already mounted:

```bash
docker run --rm --device /dev/snd ghcr.io/carcuevas/gotidal:latest cat /proc/asound/cards
```

---

## Persistent data

gotidal writes two kinds of data that should survive container restarts:

| Path in container              | Contents                                    |
| ------------------------------ | ------------------------------------------- |
| `/root/.config/gotidal/`        | OAuth2 session (age-encrypted fallback)     |
| `/root/.local/share/gotidal/`   | Volume, device preference, metadata cache   |

Mount them from the host:

```
-v ~/.config/gotidal:/root/.config/gotidal
-v ~/.local/share/gotidal:/root/.local/share/gotidal
```

---

## D-Bus (WirePlumber / PipeWire)

Before opening a `hw:` device, gotidal asks WirePlumber to release it via D-Bus.
If D-Bus is unreachable the reservation is silently skipped and gotidal opens the
device directly — this is fine on most setups. If you run PipeWire and want tidy
hand-off, forward the session bus socket:

```bash
-v /run/user/$(id -u)/bus:/run/user/1000/bus
-e DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus
```

---

## Examples

### Interactive TUI

```bash
docker run -it --rm \
  --device /dev/snd \
  --group-add $(getent group audio | cut -d: -f3) \
  -v ~/.config/gotidal:/root/.config/gotidal \
  -v ~/.local/share/gotidal:/root/.local/share/gotidal \
  ghcr.io/carcuevas/gotidal:latest
```

### Headless daemon

```bash
docker run -d \
  --name gotidal \
  --restart unless-stopped \
  --device /dev/snd \
  --group-add $(getent group audio | cut -d: -f3) \
  -v ~/.config/gotidal:/root/.config/gotidal \
  -v ~/.local/share/gotidal:/root/.local/share/gotidal \
  ghcr.io/carcuevas/gotidal:latest daemon
```

Then attach the TUI from any terminal on the host:

```bash
gotidal  # connects to the running daemon over D-Bus
```

Or control playback with `playerctl` — see [mpris2.md](mpris2.md).

### With D-Bus forwarding

```bash
docker run -d \
  --name gotidal \
  --restart unless-stopped \
  --device /dev/snd \
  --group-add $(getent group audio | cut -d: -f3) \
  -v ~/.config/gotidal:/root/.config/gotidal \
  -v ~/.local/share/gotidal:/root/.local/share/gotidal \
  -v /run/user/$(id -u)/bus:/run/user/1000/bus \
  -e DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus \
  ghcr.io/carcuevas/gotidal:latest daemon
```

---

## Debug logging

Pass `GOTIDAL_DEBUG=true` to write a timestamped log to
`/root/.local/share/gotidal/debug-*.log`:

```bash
docker run -it --rm \
  --device /dev/snd \
  --group-add $(getent group audio | cut -d: -f3) \
  -v ~/.config/gotidal:/root/.config/gotidal \
  -v ~/.local/share/gotidal:/root/.local/share/gotidal \
  -e GOTIDAL_DEBUG=true \
  ghcr.io/carcuevas/gotidal:latest
```

See [debugging.md](debugging.md) for more detail.
