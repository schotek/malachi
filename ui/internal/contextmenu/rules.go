// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contextmenu

import "github.com/schotek/malachi/ui/internal/rules"

// rulesLabel is the label of the mail rules' items (CreateRule,
// ApplyRules), "" for any other id. Their texts are the rules package's.
func rulesLabel(tr Translator, id ItemID) string {
	switch id {
	case CreateRule:
		return rules.CreateFromMessageLabel(tr)
	case ApplyRules:
		return rules.ApplyLabel(tr)
	}
	return ""
}
