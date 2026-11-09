package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelQualityPrompt(t *testing.T) {
	official, err := ModelQualityPrompt("official", "")
	require.NoError(t, err)
	require.Equal(t, "hi", official)
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

func TestModelQualityMaskActualUserIdentifier(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"admin@emample.com", "ad***@emample.com"},
		{"10023213@qq.com", "10023***@qq.com"},
		{"ab@example.com", "***@example.com"},
		{"", "***"},
		{"小明", "***"},
	} {
		require.Equal(t, tc.want, MaskModelQualityAuthor(tc.input), tc.input)
	}
	first := MaskModelQualityAuthor("测试用户张三丰")
	require.Contains(t, first, "***")
	require.Equal(t, first, MaskModelQualityAuthor("测试用户张三丰"), "刷新不能通过随机位置暴露原始用户标识")
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
