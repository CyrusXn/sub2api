package routes

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGeminiInteractionsRoutesAreAdditive(t *testing.T) {
	r := newGatewayRoutesTestRouter()
	have := map[string]bool{}
	for _, route := range r.Routes() {
		have[route.Method+" "+route.Path] = true
	}
	for _, path := range []string{"POST /v1beta/interactions", "GET /v1beta/interactions/:interactionID", "POST /v1beta/interactions/:interactionID/cancel", "DELETE /v1beta/interactions/:interactionID", "POST /v1beta/models/*modelAction", "POST /v1/chat/completions", "POST /v1/responses"} {
		require.True(t, have[path], path)
	}
}
