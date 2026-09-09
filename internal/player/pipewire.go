package player

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/carcuevas/gotidal/internal/sanitize"
)

// pactlTimeout bounds every pactl invocation below — these run on the UI's
// key-handling path (opening the device picker, selecting a sink), so a
// hung or missing pactl must not freeze the TUI.
const pactlTimeout = 3 * time.Second

// ListPipeWireSinks returns the system's PipeWire (or plain PulseAudio)
// playback sinks via `pactl`, for output-device selection when bit-perfect
// mode is off (see Player.SetDACMode): playback then goes through PipeWire's
// shared graph instead of a raw ALSA hw: grab, so *any* output PipeWire
// manages — laptop speakers, HDMI, Bluetooth — is selectable here, not just a
// recognized DAC. Reuses DeviceInfo (HWName holds the pactl sink name, the
// argument SetDefaultPipeWireSink expects) so the existing device-picker
// overlay renders either device source unmodified.
func ListPipeWireSinks() ([]DeviceInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pactlTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pactl", "list", "sinks").Output()
	if err != nil {
		return nil, fmt.Errorf("pactl list sinks: %w", err)
	}

	var devices []DeviceInfo
	var name, desc string
	flush := func() {
		if name == "" {
			return
		}
		longName := desc
		if longName == "" {
			longName = name
		}
		// HWName is passed back to pactl as an argument, so it keeps its exact
		// bytes; the two display fields are stripped of escapes because sink
		// descriptions come from device-supplied strings.
		devices = append(devices, DeviceInfo{
			HWName:   name,
			CardName: sanitize.Text(name),
			LongName: sanitize.Text(longName),
		})
		name, desc = "", ""
	}

	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Sink #"):
			flush()
		case strings.HasPrefix(trimmed, "Name: "):
			name = strings.TrimPrefix(trimmed, "Name: ")
		case strings.HasPrefix(trimmed, "Description: "):
			desc = strings.TrimPrefix(trimmed, "Description: ")
		}
	}
	flush()
	return devices, sc.Err()
}

// DefaultPipeWireSink returns the name of the system's current default sink,
// for marking the active row in the device picker.
func DefaultPipeWireSink() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pactlTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pactl", "get-default-sink").Output()
	if err != nil {
		return "", fmt.Errorf("pactl get-default-sink: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// SetDefaultPipeWireSink makes name (a DeviceInfo.HWName from
// ListPipeWireSinks) the system default sink. PipeWire moves an
// already-connected stream to the new default automatically, so this takes
// effect on the currently playing track, not just the next one.
func SetDefaultPipeWireSink(name string) error {
	if name == "" {
		return errors.New("empty sink name")
	}
	ctx, cancel := context.WithTimeout(context.Background(), pactlTimeout)
	defer cancel()
	//nolint:gosec // G204: name always comes from our own ListPipeWireSinks(), not external input
	if err := exec.CommandContext(ctx, "pactl", "set-default-sink", name).Run(); err != nil {
		return fmt.Errorf("pactl set-default-sink %s: %w", name, err)
	}
	return nil
}
