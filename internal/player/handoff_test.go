package player

import (
	"context"
	"testing"
	"time"
)

// awaitNextURL takes a URL that is already waiting, without burning the
// handoff window on it.
func TestAwaitNextURLTakesAQueuedURL(t *testing.T) {
	p := NewPlayer()
	p.nextURLCh <- Single("https://example.com/a.flac")

	start := time.Now()
	got, ok := p.awaitNextURL(context.Background())
	if !ok {
		t.Fatal("awaitNextURL gave up on a URL that was already queued")
	}
	if got.URL() != "https://example.com/a.flac" {
		t.Errorf("got %v", got.URLs)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v — it waited instead of taking the queued URL", elapsed)
	}
}

// The race this exists to close: PlayNext checks the loop is alive and only
// then sends, so a send can land in the same instant the handoff timer fires.
// Go would pick the timeout case, the loop would exit, and the URL would sit
// unread in the buffered channel — the track never starts and its done channel
// never closes. awaitNextURL re-reads the channel once on timeout to catch it.
func TestAwaitNextURLRescuesAURLThatLandsAsTheWindowCloses(t *testing.T) {
	p := NewPlayer()

	// Put the URL in the buffer without ever letting the first select case
	// observe it: the timer has already expired by the time we call, so the
	// only way to find it is the post-timeout re-read.
	p.nextURLCh <- Single("https://example.com/late.flac")

	// takeQueuedURL is the post-timeout re-read itself, called directly so the
	// test neither waits out nextURLTimeout nor has to win a scheduler race to
	// reproduce the simultaneous-fire case.
	got, ok := p.takeQueuedURL()
	if !ok {
		t.Fatal("a URL sent as the handoff window closed was stranded in the channel")
	}
	if got.URL() != "https://example.com/late.flac" {
		t.Errorf("got %v", got.URLs)
	}
}

// With nothing queued the re-read must not block or invent a URL — that is
// what lets awaitNextURL give the ALSA device back at the end of a playlist.
func TestTakeQueuedURLOnEmptyChannel(t *testing.T) {
	p := NewPlayer()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if u, ok := p.takeQueuedURL(); ok {
			t.Errorf("takeQueuedURL invented %v from an empty channel", u.URLs)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("takeQueuedURL blocked on an empty channel")
	}
}

// With nothing forthcoming the loop must still give the device back.
func TestAwaitNextURLGivesUpOnAnEmptyChannel(t *testing.T) {
	p := NewPlayer()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // stand in for playback being cancelled

	if _, ok := p.awaitNextURL(ctx); ok {
		t.Error("awaitNextURL should report false once playback is cancelled")
	}
}

// PlayNext must not hand back a done channel that can never close. With no
// playback loop running at all it falls through to Play, which is the same
// recovery the post-send liveness check performs when the loop has exited.
func TestPlayNextWithNoLoopDoesNotReturnADeadDoneChannel(t *testing.T) {
	p := NewPlayer()
	if p.loopDone != nil {
		t.Fatal("a fresh player should have no playback loop")
	}
	// Play will fail here (no ALSA device in CI), but the contract under test
	// is that PlayNext delegates rather than queueing into a dead channel.
	done, err := p.PlayNext(Single("https://example.com/a.flac"))
	if err == nil && done == nil {
		t.Error("PlayNext returned neither an error nor a done channel")
	}
	if len(p.nextURLCh) != 0 {
		t.Error("PlayNext queued a URL with no loop to consume it")
	}
}
