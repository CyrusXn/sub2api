//go:build unit

package handler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProvideSettingHandlerPublicVersion(t *testing.T) {
	for _, edition := range []string{"", "xnkaixin.20261009"} {
		t.Run(edition, func(t *testing.T) {
			h := ProvideSettingHandler(nil, BuildInfo{Version: " 0.2.14 ", Edition: edition}, nil)
			require.Equal(t, "0.2.14", h.version)
		})
	}
}
