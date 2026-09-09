// Package buildinfo reports which build of gotidal is running.
//
// It lives in its own package, rather than as a variable in package main, so
// that internal/ui can show the version in the Settings tab without main
// having to thread it through every constructor.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

// version is the release version, injected at link time by the release
// workflow and the distro packaging with
//
//	-ldflags "-X github.com/carcuevas/gotidal/internal/buildinfo.version=v1.2.0"
//
// It is empty for any build that does not pass that flag — notably a plain
// `go build`, where Version falls back to the VCS stamp instead.
var version string

// Version returns a human-readable identifier for this build.
//
// A released binary reports its tag ("v1.2.0"). A local `go build` has no
// injected version, so rather than the bare "dev" that used to be reported —
// which says nothing about *which* local build you are running — it reports
// the commit the toolchain stamped in, e.g. "dev (a1b2c3d, modified)". That
// is the difference between a bug report we can place in history and one we
// cannot.
func Version() string {
	if version != "" {
		return version
	}
	return devVersion(debug.ReadBuildInfo)
}

// devVersion derives a version from Go's embedded VCS stamp. The read function
// is a parameter so tests can supply a stamp instead of depending on the
// repository state they happen to be built from.
func devVersion(read func() (*debug.BuildInfo, bool)) string {
	bi, ok := read()
	if !ok {
		return "dev"
	}

	var revision string
	var modified bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}

	// Builds with -buildvcs=false, or from outside a repository, carry no
	// stamp at all; there is nothing more specific to say than "dev".
	if revision == "" {
		return "dev"
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}

	var b strings.Builder
	b.WriteString("dev (")
	b.WriteString(revision)
	if modified {
		// Uncommitted changes mean the commit alone does not describe what is
		// running, which is worth saying out loud in a bug report.
		b.WriteString(", modified")
	}
	b.WriteString(")")
	return b.String()
}
