# Installation

## From a release (recommended)

Pre-built packages are attached to every [GitHub release](https://github.com/carcuevas/gotidal/releases).

### Arch Linux

Download the `.pkg.tar.zst` from the latest release and install with pacman:

```bash
sudo pacman -U gotidal-*.pkg.tar.zst
```

Or install the build files and build it yourself with `makepkg`:

```bash
# Clone just the packaging directory
curl -LO https://github.com/carcuevas/gotidal/releases/latest/download/PKGBUILD
makepkg -si
```

### Debian / Ubuntu

Download the `.deb` from the latest release:

```bash
sudo dpkg -i gotidal_*.deb
sudo apt-get install -f   # resolve any missing dependencies
```

### Fedora

Download the `.rpm` from the latest release:

```bash
sudo dnf install gotidal-*.rpm
```

### Raw binary (any distro)

Each release also attaches standalone `gotidal-linux-amd64` / `gotidal-linux-arm64`
binaries. FFmpeg is statically bundled, so the only runtime requirement is ALSA
(`libasound2` / `alsa-lib`, present on virtually every desktop Linux):

```bash
curl -LO https://github.com/carcuevas/gotidal/releases/latest/download/gotidal-linux-amd64
chmod +x gotidal-linux-amd64
sudo install -Dm755 gotidal-linux-amd64 /usr/local/bin/gotidal
```

---

## Docker

The official image is published to Docker Hub at `carcuevas/gotidal`:

```bash
docker pull carcuevas/gotidal:latest
```

See [docker.md](docker.md) for full usage instructions, including how to expose
your ALSA devices inside the container.

---

## From source

### Prerequisites

Go 1.26+, ALSA development headers, and FFmpeg development headers:

```bash
sudo pacman -S go alsa-lib ffmpeg                                                        # Arch
sudo apt install golang libasound2-dev libavformat-dev libavcodec-dev libavutil-dev libswresample-dev  # Debian / Ubuntu
sudo dnf install golang alsa-lib-devel libavformat-free-devel libavcodec-free-devel libavutil-free-devel libswresample-free-devel  # Fedora
```

> A plain `go build` / `go install` links against the system's shared FFmpeg
> libraries, so the matching runtime libraries (`ffmpeg` on Arch, `libavformat`
> on Debian/Ubuntu, `libavformat-free` on Fedora) must also be installed to run
> the binary. The official distro packages instead bundle a minimal, statically
> linked FFmpeg and have no FFmpeg runtime dependency.

### go install

```bash
go install github.com/carcuevas/gotidal/cmd/gotidal@latest
```

The binary is placed in `$GOPATH/bin` (typically `~/go/bin`). Make sure that
directory is on your `PATH`.

### Git clone

```bash
git clone https://github.com/carcuevas/gotidal.git
cd gotidal
go build -o gotidal ./cmd/gotidal
sudo install -Dm755 gotidal /usr/local/bin/gotidal
```

---

## Post-install setup

### Register the tidal:// URL handler

The `setup` subcommand installs the `.desktop` file and registers the
`tidal://` scheme so clicking **"Open in desktop app"** on tidal.com opens
the track directly in gotidal:

```bash
gotidal setup
```

Output:

```
  -> Creating directory /home/user/.local/share/applications
  -> Writing /home/user/.local/share/applications/gotidal.desktop
  -> $ xdg-mime default gotidal.desktop x-scheme-handler/tidal
  -> $ update-desktop-database /home/user/.local/share/applications

Setup complete.
Clicking "Open in desktop app" on tidal.com will now open gotidal.
```

Some browsers (notably Firefox and Librewolf) require an extra one-time step.
See [browser-url-handler.md](browser-url-handler.md) for per-browser
instructions.

### Run as a background daemon (optional)

Install gotidal as a systemd user service so it starts at login with no
terminal window:

```bash
gotidal setup --daemon
```

Then open the TUI from any terminal with `gotidal`, or control playback with
`playerctl`. See [client-server.md](client-server.md) for details.

---

## Building packages locally

The `packaging/` directory at the root of the repository contains ready-to-use
build recipes for Arch and Debian.

### All distros — docker bake

The easiest way to build all packages at once (requires Docker with buildx):

```bash
docker buildx create --use
docker buildx bake --file docker-bake.hcl --set "*.args.VERSION=1.0.0" --set "*.output=type=local,dest=dist"
```

Artifacts are written to `dist/`.

### Arch — makepkg

```bash
cd packaging/arch
makepkg -si
```

`makepkg` downloads the release tarball, compiles, runs tests, and installs
via `pacman`. To generate real checksums before publishing:

```bash
makepkg -g >> PKGBUILD
```

### Debian — dpkg-buildpackage

Install build dependencies:

```bash
sudo apt install debhelper libasound2-dev libavformat-dev libavcodec-dev libavutil-dev libswresample-dev
```

Go 1.26+ is required to compile the binary. Install it from
[go.dev/dl](https://go.dev/dl/) and ensure it is first on your `PATH`.

Then build from a release tarball:

```bash
VERSION=1.0.0
curl -L "https://github.com/carcuevas/gotidal/archive/refs/tags/v${VERSION}.tar.gz" \
    | tar xz
cd "gotidal-${VERSION}"

# Compile the binary first — debian/rules installs it directly, no Go needed at package time.
CGO_ENABLED=1 go build -trimpath -buildmode=pie \
    -ldflags "-s -w -linkmode=external" \
    -o gotidal-linux-amd64 ./cmd/gotidal

cp -r /path/to/repo/packaging/debian debian
dpkg-buildpackage -us -uc -b
sudo dpkg -i ../gotidal_${VERSION}-1_amd64.deb
```

> The `-dev` packages are only needed for the `go build` step above. The
> `dpkg-buildpackage` step itself has no native build dependencies.
>
> This manual build links FFmpeg dynamically, so the resulting `.deb` needs
> the matching `libavformat` / `libavcodec` runtime packages installed. The
> official packages (built via the `packaging/` Dockerfiles) instead bundle a
> static FFmpeg and carry no FFmpeg runtime dependency.

### Fedora — rpmbuild

```bash
sudo dnf install rpm-build alsa-lib-devel libavformat-free-devel libavcodec-free-devel libavutil-free-devel libswresample-free-devel
```

Go 1.26+ is required. Install from [go.dev/dl](https://go.dev/dl/).

> As with the Debian recipe, this manual build links FFmpeg dynamically and the
> resulting `.rpm` depends on the `libavformat-free` runtime libraries. The
> official packages bundle a static FFmpeg instead.

```bash
VERSION=1.0.0
curl -L "https://github.com/carcuevas/gotidal/archive/refs/tags/v${VERSION}.tar.gz" \
    | tar xz
cd "gotidal-${VERSION}"

CGO_ENABLED=1 go build -trimpath -buildmode=pie \
    -ldflags "-s -w -linkmode=external" \
    -o gotidal ./cmd/gotidal

mkdir -p ~/rpmbuild/SOURCES
cp gotidal ~/rpmbuild/SOURCES/
cp cmd/gotidal/gotidal.desktop ~/rpmbuild/SOURCES/
cp /path/to/repo/packaging/fedora/gotidal.spec ~/rpmbuild/SPECS/

rpmbuild -bb --define "version_macro ${VERSION}" ~/rpmbuild/SPECS/gotidal.spec
sudo dnf install ~/rpmbuild/RPMS/x86_64/gotidal-${VERSION}-1.*.x86_64.rpm
```
