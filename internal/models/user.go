// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package models

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID              uuid.UUID `json:"id"`
	Username        string    `json:"username"`
	Email           *string   `json:"email,omitempty"`
	PasswordHash    string    `json:"-"`
	DisplayName     *string   `json:"display_name,omitempty"`
	Role            string    `json:"role"` // admin | member | viewer
	IsInstanceAdmin bool      `json:"is_instance_admin"`
	IsActive        bool      `json:"is_active"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// APIToken is a personal access token record. The raw value is never stored.
type APIToken struct {
	ID          uuid.UUID  `json:"id"`
	UserID      uuid.UUID  `json:"user_id"`
	Name        string     `json:"name"`
	TokenSuffix string     `json:"token_suffix"`
	Scopes      []string   `json:"scopes"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// KicadLibraryToken is a credential issued to one KiCad workstation. As with a
// PAT the raw value is never stored, so a lost .kicad_httplib means issuing a
// replacement rather than recovering the old one.
//
// Name is the device it was issued for, which is the whole point: it is what
// makes revoking one machine possible without disturbing the others.
type KicadLibraryToken struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	TokenSuffix string     `json:"token_suffix"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}
