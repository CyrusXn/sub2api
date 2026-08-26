package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstreamCostSQLExpr_UsesAccountProbeInsteadOfLocalRate(t *testing.T) {
	t.Parallel()

	expr := upstreamCostSQLExpr("ul", "upstream_account")

	require.Contains(t, expr, "upstream_billing_probe")
	require.Contains(t, expr, "resolved_rate_multiplier")
	require.Contains(t, expr, "ul.created_at AT TIME ZONE")
	require.Contains(t, expr, "upstream_account.rate_multiplier")
	require.NotContains(t, expr, "ul.rate_multiplier")
	require.Contains(t, expr, "ul.upstream_cost_base")
}
