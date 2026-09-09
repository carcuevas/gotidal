package buildinfo

import (
	"runtime/debug"
	"testing"
)

func stamp(settings ...debug.BuildSetting) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Settings: settings}, true
	}
}

func TestDevVersionFromVCSStamp(t *testing.T) {
	cases := []struct {
		name string
		read func() (*debug.BuildInfo, bool)
		want string
	}{
		{
			name: "clean checkout reports the short revision",
			read: stamp(debug.BuildSetting{Key: "vcs.revision", Value: "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"}),
			want: "dev (a1b2c3d)",
		},
		{
			name: "uncommitted changes are called out",
			read: stamp(
				debug.BuildSetting{Key: "vcs.revision", Value: "a1b2c3d4e5f6"},
				debug.BuildSetting{Key: "vcs.modified", Value: "true"},
			),
			want: "dev (a1b2c3d, modified)",
		},
		{
			name: "an explicitly unmodified tree is not annotated",
			read: stamp(
				debug.BuildSetting{Key: "vcs.revision", Value: "a1b2c3d4e5f6"},
				debug.BuildSetting{Key: "vcs.modified", Value: "false"},
			),
			want: "dev (a1b2c3d)",
		},
		{
			// -buildvcs=false, or building from outside a repository.
			name: "no stamp at all",
			read: stamp(),
			want: "dev",
		},
		{
			name: "a revision shorter than the truncation length survives",
			read: stamp(debug.BuildSetting{Key: "vcs.revision", Value: "abc"}),
			want: "dev (abc)",
		},
		{
			name: "unreadable build info",
			read: func() (*debug.BuildInfo, bool) { return nil, false },
			want: "dev",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := devVersion(c.read); got != c.want {
				t.Errorf("devVersion() = %q, want %q", got, c.want)
			}
		})
	}
}

// An injected version must win outright — a release binary should never show
// a commit hash instead of its tag.
func TestInjectedVersionWins(t *testing.T) {
	t.Cleanup(func() { version = "" })
	version = "v1.2.0"
	if got := Version(); got != "v1.2.0" {
		t.Errorf("Version() = %q, want the injected v1.2.0", got)
	}
}

// Whatever the build, Version always has something to show — the Settings row
// and `gotidal -v` must never render an empty string.
func TestVersionIsNeverEmpty(t *testing.T) {
	if Version() == "" {
		t.Error("Version() returned an empty string")
	}
}
