package repository

import (
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"

	entsql "entgo.io/ent/dialect/sql"
)

func TestAdminUsageMultiplierListOrders(t *testing.T) {
	tests := []struct {
		name  string
		table string
		order func(pagination.PaginationParams) []func(*entsql.Selector)
	}{
		{name: "account", table: "accounts", order: accountListOrder},
		{name: "user", table: "users", order: userListOrder},
		{name: "group", table: "groups", order: groupListOrder},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, direction := range []string{pagination.SortOrderAsc, pagination.SortOrderDesc} {
				selector := entsql.Select("id").From(entsql.Table(tc.table))
				for _, applyOrder := range tc.order(pagination.PaginationParams{
					SortBy:    "admin_usage_multiplier",
					SortOrder: direction,
				}) {
					applyOrder(selector)
				}
				query, _ := selector.Query()
				require.Contains(t, query, "admin_usage_multiplier")
				require.Contains(t, query, "id")
				if direction == pagination.SortOrderDesc {
					require.Contains(t, strings.ToUpper(query), "DESC")
				}
			}
		})
	}
}
