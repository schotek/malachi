// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The mail rules (docs/api.md §4.21, docs/plans/rules.md). Placeholder of
// the contract (task T0): every method answers notImplemented until the
// daemon's task (T1) replaces this file.

// Rules returns the mail rules service.
func (b *Backend) Rules() api.RulesService { return &rulesService{b} }

type rulesService struct{ b *Backend }

func (s *rulesService) List(context.Context, api.RulesListParams) (*api.RulesListResult, error) {
	return nil, api.ErrNotImplemented
}

func (s *rulesService) Create(context.Context, api.RulesCreateParams) (*api.RulesCreateResult, error) {
	return nil, api.ErrNotImplemented
}

func (s *rulesService) Update(context.Context, api.RulesUpdateParams) (*api.RulesUpdateResult, error) {
	return nil, api.ErrNotImplemented
}

func (s *rulesService) Delete(context.Context, api.RulesDeleteParams) (*api.RulesDeleteResult, error) {
	return nil, api.ErrNotImplemented
}

func (s *rulesService) Reorder(context.Context, api.RulesReorderParams) (*api.RulesReorderResult, error) {
	return nil, api.ErrNotImplemented
}

func (s *rulesService) Test(context.Context, api.RulesTestParams) (*api.RulesTestResult, error) {
	return nil, api.ErrNotImplemented
}

func (s *rulesService) Apply(context.Context, api.RulesApplyParams) (*api.RulesApplyResult, error) {
	return nil, api.ErrNotImplemented
}
