// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package eds

import "testing"

func TestParseKeyFile(t *testing.T) {
	data := "stray=before any group\n" +
		"# a comment\n" +
		"[Data Source]\r\n" +
		"DisplayName=Jan\\sNovák\n" +
		"DisplayName[cs]=Honza\n" +
		"Enabled=true\n" +
		"Parent=\n" +
		"Url=https://a.example/?b=c\n" +
		"no equals sign here\n" +
		"\n" +
		"[Address Book]\n" +
		"BackendName = carddav \n" +
		"[Empty]\n" +
		"[Autocomplete]\n" +
		"IncludeMe=false\n" +
		"[Data Source]\n" +
		"Tail=\\t\\n\\r\\\\end\n"
	kf := parseKeyFile(data)

	if got := kf.get("Data Source", "DisplayName"); got != `Jan Novák` {
		t.Errorf("DisplayName = %q", got)
	}
	if got := kf.get("Data Source", "DisplayName[cs]"); got != "Honza" {
		t.Errorf("localised key = %q", got)
	}
	if got := kf.get("Data Source", "Url"); got != "https://a.example/?b=c" {
		t.Errorf("value with '=' = %q", got)
	}
	if got := kf.get("Data Source", "Tail"); got != "\t\n\r\\end" {
		t.Errorf("escapes = %q", got)
	}
	if got := kf.get("Address Book", "BackendName"); got != "carddav" {
		t.Errorf("trimmed value = %q", got)
	}
	if !kf.has("Empty") || kf.has("Missing") || kf.has("stray") {
		t.Error("group presence wrong")
	}
	if kf.get("Missing", "Key") != "" || kf.get("Data Source", "Missing") != "" {
		t.Error("absent key not empty")
	}
	if kf.isFalse("Data Source", "Enabled") || !kf.isFalse("Autocomplete", "IncludeMe") || kf.isFalse("Autocomplete", "Missing") {
		t.Error("isFalse wrong")
	}
	if len(parseKeyFile("")) != 0 {
		t.Error("empty input produced groups")
	}
}
