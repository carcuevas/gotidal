package player

import (
	"slices"
	"sync/atomic"
	"testing"
)

// segSource builds a source of one init segment plus n uniform media segments.
func segSource(n int, segSeconds float64) StreamSource {
	urls := []string{"https://x/init.mp4"}
	for i := 1; i <= n; i++ {
		urls = append(urls, "https://x/seg-"+itoa(i)+".mp4")
	}
	return StreamSource{URLs: urls, InitURLs: 1, SegmentSeconds: segSeconds}
}

// Seeking used to reopen at segment zero and decode-and-discard forward, so a
// seek into the middle of a hi-res track re-fetched tens of megabytes while
// producing no audio. The reopen must start at the segment holding the target.
func TestSeekToTrimsToTheTargetSegment(t *testing.T) {
	src := segSource(10, 4) // 40s across 10 segments

	cases := []struct {
		name        string
		seconds     float64
		wantFirst   string // first media URL after the init segment
		wantCount   int    // total URLs, init included
		wantOffsetS float64
	}{
		{"start of track", 0, "https://x/seg-1.mp4", 11, 0},
		{"inside the first segment", 2, "https://x/seg-1.mp4", 11, 2},
		{"exactly on a boundary", 8, "https://x/seg-3.mp4", 9, 0},
		{"midway through a later segment", 10, "https://x/seg-3.mp4", 9, 2},
		{"into the last segment", 38, "https://x/seg-10.mp4", 2, 2},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, offset := src.SeekTo(c.seconds)

			if len(got.URLs) != c.wantCount {
				t.Errorf("kept %d URLs, want %d: %v", len(got.URLs), c.wantCount, got.URLs)
			}
			// The initialization segment carries the moov box, so it must lead
			// every reopen or the media segments are undemuxable.
			if got.URLs[0] != "https://x/init.mp4" {
				t.Errorf("init segment missing after seek: %v", got.URLs)
			}
			if len(got.URLs) > 1 && got.URLs[1] != c.wantFirst {
				t.Errorf("first media URL = %s, want %s", got.URLs[1], c.wantFirst)
			}
			// Whatever was trimmed has to be made up by decoding, so the
			// offset must land the listener at the position they asked for.
			if offset != c.wantOffsetS {
				t.Errorf("offset = %v, want %v", offset, c.wantOffsetS)
			}
			trimmed := len(src.URLs) - len(got.URLs)
			if reconstructed := float64(trimmed)*src.SegmentSeconds + offset; reconstructed != c.seconds {
				t.Errorf("trimmed %d segments + %vs offset = %vs, want %vs",
					trimmed, offset, reconstructed, c.seconds)
			}
		})
	}
}

// Seeking past the end must clamp to the final segment rather than producing
// an empty URL list, which would fail the reopen outright.
func TestSeekToClampsPastTheEnd(t *testing.T) {
	src := segSource(5, 4) // 20s
	got, offset := src.SeekTo(1000)

	if len(got.URLs) != 2 {
		t.Errorf("kept %d URLs, want init + the last segment: %v", len(got.URLs), got.URLs)
	}
	if got.URLs[len(got.URLs)-1] != "https://x/seg-5.mp4" {
		t.Errorf("should clamp to the last segment, got %v", got.URLs)
	}
	if offset < 0 {
		t.Errorf("offset = %v, must not be negative", offset)
	}
}

func TestSeekToClampsNegative(t *testing.T) {
	src := segSource(5, 4)
	got, offset := src.SeekTo(-10)
	if offset != 0 {
		t.Errorf("offset = %v, want 0 for a negative seek", offset)
	}
	if len(got.URLs) != len(src.URLs) {
		t.Errorf("a negative seek should keep the whole stream, got %v", got.URLs)
	}
}

// A single-file stream has nothing to trim: the whole thing must be returned
// untouched, with the offset passed straight through for the decoder to skip.
func TestSeekToLeavesAPlainStreamAlone(t *testing.T) {
	src := Single("https://x/track.flac")
	got, offset := src.SeekTo(42)

	if !slices.Equal(got.URLs, src.URLs) {
		t.Errorf("URLs = %v, want them unchanged", got.URLs)
	}
	if offset != 42 {
		t.Errorf("offset = %v, want the full 42s left for the decoder to skip", offset)
	}
}

// Without a segment duration there is no way to know which segment holds the
// target, so the stream must be left whole rather than trimmed by guesswork.
func TestSeekToWithoutSegmentDurationDoesNotTrim(t *testing.T) {
	src := segSource(10, 0) // duration unknown
	got, offset := src.SeekTo(20)

	if len(got.URLs) != len(src.URLs) {
		t.Errorf("trimmed %d URLs with no segment duration known", len(src.URLs)-len(got.URLs))
	}
	if offset != 20 {
		t.Errorf("offset = %v, want 20", offset)
	}
}

// The original source must not be aliased: the playback loop keeps it for the
// next seek, and a trimmed copy leaking back would make each seek trim from
// the previous one instead of from the whole track.
func TestSeekToDoesNotMutateTheOriginal(t *testing.T) {
	src := segSource(10, 4)
	before := slices.Clone(src.URLs)

	first, _ := src.SeekTo(20)
	if !slices.Equal(src.URLs, before) {
		t.Fatalf("SeekTo mutated the source: %v", src.URLs)
	}

	// A second seek from the same source must be measured from the start of
	// the track, not from where the first one landed.
	second, _ := src.SeekTo(8)
	if len(second.URLs) <= len(first.URLs) {
		t.Errorf("seeking back to 8s kept %d URLs but seeking to 20s kept %d; "+
			"the second seek was measured from the first",
			len(second.URLs), len(first.URLs))
	}
}

// Reported: the progress bar would jump to the seek target, then snap back to
// the old position for a moment, before jumping forward again once the seek
// actually landed. GetPosition reads samplesPlayed, which used to stay frozen
// at the pre-seek value for the whole reopen (network fetch, format probe,
// decode-discard) — a UI tick landing in that window read the stale value
// back. Seek must update samplesPlayed itself, immediately, not leave it to
// the playback loop to catch up on its own time.
func TestSeekReportsThePositionImmediately(t *testing.T) {
	p := &Player{seekCh: make(chan uint64, 1)}
	p.sampleRate = 44100
	p.totalSamples = 44100 * 300 // 5 minutes
	atomic.StoreUint64(&p.samplesPlayed, 44100*10)

	if err := p.Seek(60); err != nil {
		t.Fatalf("Seek: %v", err)
	}

	pos, err := p.GetPosition()
	if err != nil {
		t.Fatalf("GetPosition: %v", err)
	}
	if pos != 60 {
		t.Errorf("GetPosition() = %v immediately after Seek(60), want 60 — the playback loop has not even had a chance to run yet", pos)
	}
}

// A seek target is clamped to the track's real length; the reported position
// must match what Seek actually clamped to, not the raw request.
func TestSeekReportsTheClampedPosition(t *testing.T) {
	p := &Player{seekCh: make(chan uint64, 1)}
	p.sampleRate = 44100
	p.totalSamples = 44100 * 100 // 100 seconds

	if err := p.Seek(500); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	if pos, _ := p.GetPosition(); pos != 100 {
		t.Errorf("GetPosition() = %v, want 100 (clamped to the track length)", pos)
	}

	if err := p.Seek(-30); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	if pos, _ := p.GetPosition(); pos != 0 {
		t.Errorf("GetPosition() = %v, want 0 (clamped to the start)", pos)
	}
}

// With no format known yet (nothing has played), Seek must be a no-op rather
// than reporting a position computed against a zero sample rate.
func TestSeekWithNoFormatIsANoOp(t *testing.T) {
	p := &Player{seekCh: make(chan uint64, 1)}
	if err := p.Seek(30); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	if pos, _ := p.GetPosition(); pos != 0 {
		t.Errorf("GetPosition() = %v, want 0", pos)
	}
}
