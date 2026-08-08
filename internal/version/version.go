// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package version carries the running server's release string. Dev builds
// derive it from the current date at startup; release builds inject it via
// ldflags from the Dockerfile's VERSION build-arg.
package version

import (
	"fmt"
	"time"
)

// Version is the current release, set at link time via ldflags for release
// builds (e.g. "26.8.0"). When empty (local dev builds), it's auto-computed
// from the current date as "{YY}.{M}.DEV" during package init. Format:
// YY.M.revision, month not zero-padded.
//
// This used to be a hardcoded "26.7.DEV", which meant every dev build claimed
// July until somebody remembered to edit it by hand.
var Version = ""

// StartTime is when this process started.
var StartTime = time.Now()

// BuildVersion is the human-readable string used in the startup log and the
// health endpoint. Release builds: "26.8.0". Dev builds: the auto-computed
// version plus a local timestamp, so two dev containers are distinguishable
// at a glance, e.g. "26.8.DEV 2026-08-08 21:14 EDT".
var BuildVersion = buildVersion()

func buildVersion() string {
	if Version == "" {
		Version = fmt.Sprintf("%d.%d.DEV", StartTime.Year()%100, int(StartTime.Month()))
		return fmt.Sprintf("%s %s", Version, StartTime.Local().Format("2006-01-02 15:04 MST"))
	}
	return Version
}
