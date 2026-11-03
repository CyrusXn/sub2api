package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelQualityPrompt(t *testing.T) {
	candy, err := ModelQualityPrompt("candy", "ignore")
	require.NoError(t, err)
	require.Contains(t, candy, "不使用任何外部工具")
	require.Contains(t, candy, "五角星形")
	require.NotContains(t, candy, "21")
	pelican, err := ModelQualityPrompt("pelican", "")
	require.NoError(t, err)
	require.Contains(t, pelican, "鹈鹕")
	custom, err := ModelQualityPrompt("custom", "  天空之城  ")
	require.NoError(t, err)
	require.Contains(t, custom, "天空之城")
	_, err = ModelQualityPrompt("other", "x")
	require.Error(t, err)
	_, err = ModelQualityPrompt("custom", " ")
	require.Error(t, err)
	_, err = ModelQualityPrompt("custom", strings.Repeat("中", 2001))
	require.Error(t, err)
}

func TestModelQualityResult(t *testing.T) {
	html, err := ModelQualityOutput("pelican", "```html\n<!doctype html><html><body><svg></svg></body></html>\n```")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(html, "<!doctype"))
	answer, err := ModelQualityOutput("candy", "答案是 21 个。")
	require.NoError(t, err)
	require.Equal(t, "答案是 21 个。", answer)
	_, err = ModelQualityOutput("pelican", "没有生成 HTML")
	require.Error(t, err)
	_, err = ModelQualityOutput("candy", " ")
	require.Error(t, err)
}

func TestModelQualityPublicProjection(t *testing.T) {
	run := ModelQualityRun{UserID: 29, AccountID: 72, AccountName: "私有上游", Author: "test@example.com"}
	public := run.Visible(false)
	raw, err := json.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "account")
	require.NotContains(t, string(raw), "user_id")
	require.NotContains(t, string(raw), "test@example.com")
	require.NotEmpty(t, public.Author)
	admin := run.Visible(true)
	raw, err = json.Marshal(admin)
	require.NoError(t, err)
	require.Contains(t, string(raw), "私有上游")
	require.Equal(t, MaskModelQualityAuthor("test@example.com"), public.Author)
}

func TestModelQualityAuthorIsUnlinkable(t *testing.T) {
	first, err := NewModelQualityAuthor()
	require.NoError(t, err)
	second, err := NewModelQualityAuthor()
	require.NoError(t, err)
	require.Len(t, first, 32)
	require.NotEqual(t, first, second)
}
