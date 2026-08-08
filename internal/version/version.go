// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package version carries the running server's release string. Release builds
// inject it via ldflags from the Dockerfile's VERSION build-arg; anything else
// is a local build and says so.
package version

import (
	"fmt"
	"time"
)

// LocalVersion is what an uninjected build reports. It is deliberately not a
// YY.M.revision string: the scheme has exactly three shapes, and all three
// describe something that was published.
//
//	26.8.1                       released
//	26.8.1-rc.1                  candidate
//	26.8.1-nightly.202608080642  built from a merge to main
//
// A binary someone built on their laptop is none of those, so it claims no
// version at all. 0.0.0-dev is valid SemVer, sorts below every real release,
// and cannot be mistaken for one in a bug report.
const LocalVersion = "0.0.0-dev"

// Version is the current release, set at link time via ldflags for release
// builds (e.g. "26.8.0"). Empty means a local build.
var Version = ""

// StartTime is when this process started.
var StartTime = time.Now()

// BuildVersion is the human-readable string for the startup log and the health
// endpoint. Release builds: "26.8.0". Local builds: 0.0.0-dev plus a
// timestamp, so two dev containers are distinguishable at a glance.
var BuildVersion = buildVersion()

func buildVersion() string {
	if Version == "" {
		Version = LocalVersion
		return fmt.Sprintf("%s %s", Version, StartTime.Local().Format("2006-01-02 15:04 MST"))
	}
	return Version
}
