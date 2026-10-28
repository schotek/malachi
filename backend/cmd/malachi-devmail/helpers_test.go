// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import "fmt"

func fmtSscan(s string, v *int) (int, error) { return fmt.Sscan(s, v) }
