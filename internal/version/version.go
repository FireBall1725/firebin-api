// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package version

// Version is the build version, injected at release time via -ldflags
// "-X github.com/firelabsca/firebin-api/internal/version.Version=YY.M.rev".
// Dev builds carry the DEV placeholder.
var Version = "26.7.DEV"
