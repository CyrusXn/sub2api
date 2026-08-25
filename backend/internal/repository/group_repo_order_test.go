package repository

import (
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"

	entsql "entgo.io/ent/dialect/sql"
)

func TestGroupListOrderDefaultsToPlatformPriorityThenMultiplier(t *testing.T) {
	for _, params := range []pagination.PaginationParams{
		{},
		{SortBy: "platform", SortOrder: pagination.SortOrderAsc},
	} {
		selector := entsql.Select("id").From(entsql.Table("groups"))
		for _, order := range groupListOrder(params) {
			order(selector)
		}
		query, _ := selector.Query()

		positions := []int{
			strings.Index(query, "CASE"),
			strings.Index(query, "openai"),
			strings.Index(query, "anthropic"),
			strings.Index(query, "grok"),
			strings.Index(query, "gemini"),
			strings.Index(query, "antigravity"),
			strings.Index(query, "composite"),
			strings.Index(query, "ELSE 7"),
			strings.LastIndex(query, "is_exclusive"),
			strings.LastIndex(query, "rate_multiplier"),
			strings.LastIndex(query, "sort_order"),
			strings.LastIndex(query, "id"),
		}
		for index, position := range positions {
			require.GreaterOrEqual(t, position, 0, "missing order segment %d in %s", index, query)
			if index > 0 {
				require.Greater(t, position, positions[index-1], "order segment %d is out of order in %s", index, query)
			}
		}
	}
}
