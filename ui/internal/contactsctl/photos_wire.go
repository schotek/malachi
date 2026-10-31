// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contactsctl

import "github.com/schotek/malachi/backend/pkg/api"

// The wire of contact photos (docs/api.md §4.19, plan F8) as Photos reads
// it, until backend/pkg/api has its types.

const methodContactPhoto = "contact.photo"

type lookupPhotosParams struct {
	Addresses  []string `json:"addresses"`
	WithPhotos bool     `json:"withPhotos,omitempty"`
}

type photoRef struct {
	ContactID api.ContactID `json:"contactId"`
	State     string        `json:"state"`
}

type lookupPhotosResult struct {
	Known  map[string]api.ContactID `json:"known"`
	Photos map[string]photoRef      `json:"photos,omitempty"`
}

type photoParams struct {
	ContactID api.ContactID `json:"contactId"`
}

type photoResult struct {
	State    string `json:"state"`
	MIME     string `json:"mime,omitempty"`
	Data     []byte `json:"data,omitempty"`
	Revision string `json:"revision,omitempty"`
}
