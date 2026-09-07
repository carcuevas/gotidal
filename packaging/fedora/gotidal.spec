Name:           gotidal
Version:        %{version_macro}
Release:        1%{?dist}
Summary:        Tidal music player for Linux — bit-perfect FLAC via ALSA

License:        Apache-2.0
URL:            https://github.com/carcuevas/gotidal

# The binary is pre-built before rpmbuild is invoked.
# No Go toolchain is required at package-build time.

BuildRequires:  alsa-lib

Requires:       alsa-lib
Recommends:     playerctl
Suggests:       kdeconnect
Suggests:       cava

%description
gotidal is a Tidal music player that delivers bit-perfect, lossless audio
directly to your DAC via ALSA hw: — bypassing PipeWire and PulseAudio
entirely. It can run as an interactive TUI, a headless daemon controlled
via any MPRIS2 client, or a lightweight client that forwards commands to
a running daemon over D-Bus.

goTidal is a fork of tidalt (https://github.com/Benehiko/tidalt), renamed
and extended with an rmpc-style tabbed interface, a CAVA spectrum
visualizer, and synced lyrics.

%install
install -Dm755 %{_sourcedir}/gotidal        %{buildroot}%{_bindir}/gotidal
install -Dm644 %{_sourcedir}/gotidal.desktop %{buildroot}%{_datadir}/applications/gotidal.desktop

%files
%{_bindir}/gotidal
%{_datadir}/applications/gotidal.desktop

%changelog
* Sun Sep 07 2026 carcuevas <https://github.com/carcuevas> - %{version_macro}-1
- First release under the goTidal name (forked and renamed from tidalt).
- rmpc-style tabbed TUI, CAVA spectrum visualizer, synced lyrics, square album art.
