package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAdminUsageMultiplierFieldsAreAdminOnly(t *testing.T) {
	t.Parallel()

	userMultiplier := 0.8
	user := &service.User{
		ID:                   7,
		Email:                "admin-only-user@example.com",
		AdminUsageMultiplier: &userMultiplier,
	}
	group := &service.Group{
		ID:                   9,
		Name:                 "admin-only-group",
		AdminUsageMultiplier: 1.25,
	}

	userJSON, err := json.Marshal(UserFromService(user))
	require.NoError(t, err)
	require.NotContains(t, string(userJSON), "admin_usage_multiplier")

	groupJSON, err := json.Marshal(GroupFromService(group))
	require.NoError(t, err)
	require.NotContains(t, string(groupJSON), "admin_usage_multiplier")

	adminUser := UserFromServiceAdmin(user)
	require.NotNil(t, adminUser.AdminUsageMultiplier)
	require.InDelta(t, 0.8, *adminUser.AdminUsageMultiplier, 1e-12)

	adminGroup := GroupFromServiceAdmin(group)
	require.InDelta(t, 1.25, adminGroup.AdminUsageMultiplier, 1e-12)
}
