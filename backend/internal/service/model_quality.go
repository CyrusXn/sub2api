package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const ModelQualityMaxOutput = 512 * 1024

const ModelQualityCandyPrompt = `不使用任何外部工具回答以下问题：
在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）
          苹果味  桃子味  西瓜味
圆形         7      9      8
五角星形     7      6      4`

const modelQualityHTMLInstruction = "不使用任何外部工具。请创建一个完整的 HTML，使用内联 SVG 和 CSS 动画绘制以下场景。要求画面细腻、动作自然，只返回 HTML 代码；不要外部资源、JavaScript、链接、表单或嵌入页面。场景："

func ModelQualityPrompt(theme, custom string) (string, error) {
	switch theme {
	case "candy":
		return ModelQualityCandyPrompt, nil
	case "pelican":
		return modelQualityHTMLInstruction + "一只鹈鹕骑着自行车，双脚踩动踏板，车轮转动，沿着海边前行。", nil
	case "custom":
		custom = strings.TrimSpace(custom)
		if custom == "" || utf8.RuneCountInString(custom) > 2000 {
			return "", errors.New("请输入 1 至 2000 字的自定义提示词")
		}
		return modelQualityHTMLInstruction + custom, nil
	default:
		return "", errors.New("不支持的检测主题")
	}
}

func ModelQualityOutput(theme, output string) (string, error) {
	output = strings.TrimSpace(output)
	if output == "" || len(output) > ModelQualityMaxOutput {
		return "", errors.New("模型返回内容为空或过大")
	}
	if theme == "candy" {
		return output, nil
	}
	// 只剥离代码围栏，不将解释文字误当作可预览作品。
	if strings.HasPrefix(output, "```") {
		if i := strings.IndexByte(output, '\n'); i >= 0 {
			output = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(output[i+1:]), "```"))
		}
	}
	lower := strings.ToLower(output)
	if !strings.Contains(lower, "<svg") || (!strings.Contains(lower, "<html") && !strings.HasPrefix(lower, "<svg")) {
		return "", errors.New("模型未返回有效的 SVG / HTML 作品")
	}
	return output, nil
}

type ModelQualityRun struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"-"`
	APIKeyID    int64     `json:"-"`
	RequestID   string    `json:"-"`
	Author      string    `json:"author"`
	GroupName   string    `json:"group_name"`
	Model       string    `json:"model"`
	Theme       string    `json:"theme"`
	Prompt      string    `json:"prompt"`
	Status      string    `json:"status"`
	Content     string    `json:"content"`
	Error       string    `json:"error,omitempty"`
	AccountID   int64     `json:"account_id,omitempty"`
	AccountName string    `json:"account_name,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func MaskModelQualityAuthor(author string) string {
	digest := sha256.Sum256([]byte(author))
	return fmt.Sprintf("用户·%x", digest[:4])
}

// NewModelQualityAuthor 生成不可反查、不可跨记录关联的作者代号。
func NewModelQualityAuthor() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func (r ModelQualityRun) Visible(admin bool) ModelQualityRun {
	r.Author = MaskModelQualityAuthor(r.Author)
	if !admin {
		r.AccountID = 0
		r.AccountName = ""
	}
	return r
}

type ModelQualityRepository interface {
	Create(context.Context, *ModelQualityRun) error
	List(context.Context, int, string, string) ([]ModelQualityRun, int64, error)
}

type ModelQualityService struct{ repo ModelQualityRepository }

func NewModelQualityService(repo ModelQualityRepository) *ModelQualityService {
	return &ModelQualityService{repo: repo}
}
func (s *ModelQualityService) Create(ctx context.Context, run *ModelQualityRun) error {
	return s.repo.Create(ctx, run)
}
func (s *ModelQualityService) List(ctx context.Context, page int, theme, status string, admin bool) ([]ModelQualityRun, int64, error) {
	runs, total, err := s.repo.List(ctx, page, theme, status)
	if err != nil {
		return nil, 0, err
	}
	for i := range runs {
		runs[i] = runs[i].Visible(admin)
	}
	return runs, total, nil
}
