package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidClientFirstPacket(t *testing.T) {
	for _, tt := range []struct {
		id    string
		ms    int
		valid bool
	}{
		{"server-issued-id", 1560, true},
		{"server-issued-id", 0, true},
		{"", 1560, false},
		{"invalid\nheader", 1560, false},
		{"server-issued-id", -1, false},
		{"server-issued-id", 86400001, false},
	} {
		require.Equal(t, tt.valid, ValidClientFirstPacket(tt.id, tt.ms))
	}
}
