package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type stepUpSettingRepoStub struct {
	SettingRepository
	value string
	err   error
}

func (s *stepUpSettingRepoStub) GetValue(context.Context, string) (string, error) {
	return s.value, s.err
}

func TestIsStepUpEnabledTreatsMissingSettingAsDisabled(t *testing.T) {
	svc := NewSettingService(&stepUpSettingRepoStub{err: ErrSettingNotFound}, &config.Config{})

	enabled, err := svc.IsStepUpEnabled(context.Background())

	require.NoError(t, err)
	require.False(t, enabled)
}
