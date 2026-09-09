package player

import (
	"bytes"
	"testing"
)

// TestPackPCMFormats covers every format alsa.c's ladders can negotiate.
// Before this was format-aware it always wrote 4 bytes per sample at a stride
// of bps, which silently corrupted every sample and ran past the end of the
// buffer for anything narrower than S32_LE — a panic on the first buffer of
// any 24-bit source (S24_3LE is tried first) or any 16-bit source on a DAC
// that refuses S32_LE.
func TestPackPCMFormats(t *testing.T) {
	// 0x12345678 as a 32-bit sample: the top 24 bits are 0x123456, the top 16
	// are 0x1234 — so each narrower format should keep the most significant
	// bits and drop the rest, little-endian.
	samples := []int32{0x12345678, 0x7ABCDEF0}

	cases := []struct {
		name  string
		bps   int
		shift int // 32 - significant bits
		want  []byte
	}{
		{
			name: "S32_LE", bps: 4, shift: 0,
			want: []byte{0x78, 0x56, 0x34, 0x12, 0xF0, 0xDE, 0xBC, 0x7A},
		},
		{
			name: "S24_3LE", bps: 3, shift: 8,
			want: []byte{0x56, 0x34, 0x12, 0xDE, 0xBC, 0x7A},
		},
		{
			// 24 significant bits in a 4-byte slot: same 24-bit value, then a
			// sign-extension byte (0x00 here — both samples are positive).
			name: "S24_LE", bps: 4, shift: 8,
			want: []byte{0x56, 0x34, 0x12, 0x00, 0xDE, 0xBC, 0x7A, 0x00},
		},
		{
			name: "S16_LE", bps: 2, shift: 16,
			want: []byte{0x34, 0x12, 0xBC, 0x7A},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dst := make([]byte, len(samples)*c.bps)
			packPCM(dst, samples, c.bps, c.shift, 1.0)
			if !bytes.Equal(dst, c.want) {
				t.Errorf("packPCM(%s) = % X, want % X", c.name, dst, c.want)
			}
		})
	}
}

// TestPackPCMSignExtension checks that a negative sample keeps its sign — an
// arithmetic shift must fill with 1s, and S24_LE's 4th byte must carry the
// sign extension the device expects rather than zero.
func TestPackPCMSignExtension(t *testing.T) {
	samples := []int32{-0x00345678} // negative, fits comfortably in 24 bits

	dst := make([]byte, 4)
	packPCM(dst, samples, 4, 8, 1.0)
	// -0x00345678 >> 8 == -0x3457 (0xFFFFCBA9), so the top byte is the 0xFF
	// sign extension, not 0x00.
	if dst[3] != 0xFF {
		t.Errorf("S24_LE should sign-extend a negative sample into the 4th byte, got % X", dst)
	}

	dst3 := make([]byte, 3)
	packPCM(dst3, samples, 3, 8, 1.0)
	if bytes.Equal(dst3, []byte{0, 0, 0}) {
		t.Errorf("S24_3LE dropped a negative sample entirely: % X", dst3)
	}
}

// TestPackPCMVolume verifies the volume scale still applies, and that vol==1.0
// is bit-exact (the bit-perfect path must not round-trip through float).
func TestPackPCMVolume(t *testing.T) {
	samples := []int32{1 << 24}

	full := make([]byte, 4)
	packPCM(full, samples, 4, 0, 1.0)
	if full[3] != 0x01 {
		t.Errorf("vol=1.0 should pass the sample through untouched, got % X", full)
	}

	half := make([]byte, 4)
	packPCM(half, samples, 4, 0, 0.5)
	if !bytes.Equal(half, []byte{0x00, 0x00, 0x80, 0x00}) {
		t.Errorf("vol=0.5 should halve the sample, got % X", half)
	}
}

// TestSampleShift maps each negotiated format's reported significant bits to
// the shift packPCM needs, including the fallback when the driver reports
// nothing usable.
func TestSampleShift(t *testing.T) {
	cases := []struct {
		name  string
		sbits int
		bps   int
		want  int
	}{
		{"S32_LE", 32, 4, 0},
		{"S24_LE", 24, 4, 8},
		{"S24_3LE", 24, 3, 8},
		{"S16_LE", 16, 2, 16},
		{"unreported falls back to container width", 0, 3, 8},
		{"negative falls back to container width", -1, 2, 16},
		{"nonsense falls back and never goes negative", 999, 4, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ah := &alsaHandle{significantBits: c.sbits, bytesPerSample: c.bps}
			if got := sampleShift(ah); got != c.want {
				t.Errorf("sampleShift(sbits=%d, bps=%d) = %d, want %d", c.sbits, c.bps, got, c.want)
			}
		})
	}
}
