package migrations

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestGeminiInteractionReviewMigrationDoesNotRefundOrRewriteUsage(t *testing.T) {
	b, err := FS.ReadFile("256_gemini_interaction_review.sql")
	require.NoError(t, err)
	s := string(b)
	require.Contains(t, s, "ADD COLUMN IF NOT EXISTS review_reason")
	require.Contains(t, s, "ADD COLUMN IF NOT EXISTS resolution")
	require.Contains(t, s, "terminal_usage_missing")
	require.NotContains(t, strings.ToLower(s), "update users")
	require.NotContains(t, strings.ToLower(s), "settled = true")
}
