// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import "time"

// Update check (docs/api.md §4.20; the design is docs/plans/update-check.md,
// the service docs/services.md §2). The daemon asks the project's signed
// manifest which versions exist and offers the newest one for the package
// the build was installed from. It never downloads or installs anything:
// a client opens the package's URL in the browser.

// UpdateState is what the update check shows.
type UpdateState string

const (
	// UpdateDisabled: the user turned the check off.
	UpdateDisabled UpdateState = "disabled"
	// UpdateUnsupported: the build has no channel (a development build,
	// one from a store) or its version is not a plain MAJOR.MINOR.PATCH.
	UpdateUnsupported UpdateState = "unsupported"
	// UpdateIdle: no check has finished yet.
	UpdateIdle UpdateState = "idle"
	// UpdateChecking: a check is running.
	UpdateChecking UpdateState = "checking"
	// UpdateUpToDate: the last check found nothing newer.
	UpdateUpToDate UpdateState = "upToDate"
	// UpdateAvailable: a newer version is offered (Available).
	UpdateAvailable UpdateState = "available"
	// UpdateFailed: the last check failed (Failure) and nothing is offered.
	UpdateFailed UpdateState = "failed"
)

// UpdateFailure is the class of a failed check.
type UpdateFailure string

const (
	UpdateFailNetwork   UpdateFailure = "network"
	UpdateFailServer    UpdateFailure = "server"
	UpdateFailSignature UpdateFailure = "signature"
	UpdateFailFormat    UpdateFailure = "format"
)

// UpdateDismissAction is what update.dismiss does with an offered version.
type UpdateDismissAction string

const (
	// UpdateSkip: never offer this version again (not a security release).
	UpdateSkip UpdateDismissAction = "skip"
	// UpdateLater: offer it again after UpdateRemindDelay (not a security
	// release).
	UpdateLater UpdateDismissAction = "later"
	// UpdatePrompted: a client showed the question of a security release
	// (UpdateInfo.Prompt); no client asks again for this version.
	UpdatePrompted UpdateDismissAction = "prompted"
)

// UpdateRemindDelay is how long UpdateLater hides a version.
const UpdateRemindDelay = 72 * time.Hour

// UpdateCheckInterval is the shortest time between two checks a user asks
// for; update.check within it returns the status without checking.
const UpdateCheckInterval = time.Minute

// UpdateInfo is the version offered. Every string is plain text the
// daemon checked (docs/services.md §2.3); URL and InfoURL are https on the
// project's hosts.
type UpdateInfo struct {
	Version     string    `json:"version"`
	PublishedAt time.Time `json:"publishedAt"`
	// Security: this release, or one it skips over, fixes a security
	// problem. Neither skip nor later is accepted, and the banner stays.
	Security bool `json:"security"`
	// Notes are the release notes in the language the client asked for,
	// else English.
	Notes   string `json:"notes"`
	InfoURL string `json:"infoUrl"`
	// URL is the package for the build's channel; Size its bytes.
	URL  string `json:"url"`
	Size int64  `json:"size"`
	// Skipped: the user skipped this version (never with Security).
	Skipped bool `json:"skipped"`
	// RemindAfter: the user asked to be reminded later; absent once it
	// passed.
	RemindAfter *time.Time `json:"remindAfter,omitempty"`
	// Prompt: a security release no client has asked about yet; the
	// client asks once (download, then quit) and calls update.dismiss
	// with "prompted".
	Prompt bool `json:"prompt"`
}

// UpdateNotice is a message of the project to the users of this version,
// shown once per ID.
type UpdateNotice struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	URL  string `json:"url,omitempty"`
}

// UpdateStatus is the whole state a client shows.
type UpdateStatus struct {
	// Enabled: the user's preference (default true).
	Enabled bool `json:"enabled"`
	// Channel is the package the build was installed from; absent for a
	// build without one.
	Channel string      `json:"channel,omitempty"`
	Current string      `json:"current"`
	State   UpdateState `json:"state"`
	// CheckedAt is when a check last brought an answer.
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	// Failure: the last check failed; absent once one succeeds.
	Failure   UpdateFailure `json:"failure,omitempty"`
	Available *UpdateInfo   `json:"available,omitempty"`
	Notice    *UpdateNotice `json:"notice,omitempty"`
}

// Every update method takes the client's locale ("cs", "cs-CZ") for the
// texts of the status it returns; absent: English.

type UpdateStatusParams struct {
	Locale string `json:"locale,omitempty"`
}

type UpdateCheckParams struct {
	Locale string `json:"locale,omitempty"`
}

type UpdateSetPrefsParams struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Locale  string `json:"locale,omitempty"`
}

type UpdateDismissParams struct {
	Version string              `json:"version"`
	Action  UpdateDismissAction `json:"action"`
	Locale  string              `json:"locale,omitempty"`
}

type UpdateDismissNoticeParams struct {
	ID     string `json:"id"`
	Locale string `json:"locale,omitempty"`
}

// UpdateChangedNotification: the status changed; clients call
// update.status.
type UpdateChangedNotification struct{}
