package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const modelQualityOfficialSetting = "model_quality_official_v1"

type modelQualitySettings interface {
	GetValue(context.Context, string) (string, error)
	Set(context.Context, string, string) error
}
type modelQualityKeyReader interface {
	GetByID(context.Context, int64) (*APIKey, error)
}

type ModelQualityOfficialConfig struct {
	Enabled         bool   `json:"enabled"`
	APIKeyID        int64  `json:"api_key_id,omitempty"`
	Model           string `json:"model"`
	IntervalSeconds int    `json:"interval_seconds"`
	OwnerID         int64  `json:"owner_id,omitempty"`
}

type ModelQualityOfficialService struct {
	repo                ModelQualityRepository
	settings            modelQualitySettings
	keys                modelQualityKeyReader
	baseURL             string
	client              *http.Client
	cancel              context.CancelFunc
	startOnce, stopOnce sync.Once
	wg                  sync.WaitGroup
}

func NewModelQualityOfficialService(repo ModelQualityRepository, settings modelQualitySettings, keys modelQualityKeyReader, baseURL string) *ModelQualityOfficialService {
	return &ModelQualityOfficialService{repo: repo, settings: settings, keys: keys, baseURL: strings.TrimRight(baseURL, "/"), client: &http.Client{
		Timeout:       8 * time.Minute,
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("不允许重定向检测请求") },
	}}
}

func (s *ModelQualityOfficialService) load(ctx context.Context) (ModelQualityOfficialConfig, error) {
	var cfg ModelQualityOfficialConfig
	raw, err := s.settings.GetValue(ctx, modelQualityOfficialSetting)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && raw == "") {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal([]byte(raw), &cfg)
	return cfg, err
}

func (s *ModelQualityOfficialService) Status(ctx context.Context, admin bool) (ModelQualityOfficialConfig, error) {
	cfg, err := s.load(ctx)
	cfg.IntervalSeconds = normalizeOfficialInterval(cfg.IntervalSeconds)
	cfg.OwnerID = 0
	if !admin {
		cfg.APIKeyID = 0
	}
	return cfg, err
}

func (s *ModelQualityOfficialService) Configure(ctx context.Context, owner, keyID int64, model string, enabled bool) error {
	return s.ConfigureWithInterval(ctx, owner, keyID, model, enabled, ModelQualityOfficialDefaultIntervalSeconds)
}

func (s *ModelQualityOfficialService) ConfigureWithInterval(ctx context.Context, owner, keyID int64, model string, enabled bool, intervalSeconds int) error {
	if intervalSeconds < ModelQualityOfficialMinIntervalSeconds || intervalSeconds > ModelQualityOfficialMaxIntervalSeconds {
		return errors.New("检测间隔需设置在 300 至 86400 秒之间")
	}
	cfg, err := s.load(ctx)
	if err != nil {
		return err
	}
	cfg.IntervalSeconds = intervalSeconds
	cfg.Enabled = enabled
	if enabled {
		key, err := s.keys.GetByID(ctx, keyID)
		if err != nil || key == nil || key.UserID != owner || !validOfficialQualityKey(key, model) {
			return errors.New("请选择本人可用的官 key 及其文本模型")
		}
		cfg.APIKeyID, cfg.Model, cfg.OwnerID = keyID, model, owner
	} else if cfg.OwnerID == 0 {
		cfg.OwnerID = owner
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.settings.Set(ctx, modelQualityOfficialSetting, string(raw))
}

func normalizeOfficialInterval(seconds int) int {
	if seconds < ModelQualityOfficialMinIntervalSeconds || seconds > ModelQualityOfficialMaxIntervalSeconds {
		return ModelQualityOfficialDefaultIntervalSeconds
	}
	return seconds
}

func validOfficialQualityKey(key *APIKey, model string) bool {
	return key != nil && key.IsActive() && !key.IsExpired() && !key.IsQuotaExhausted() && key.Group != nil && key.GroupID != nil && key.User != nil && key.User.IsActive() && key.User.IsAdmin() && IsModelQualityTextModel(model) && key.Group.ModelAllowlist.Allows(model)
}

func (s *ModelQualityOfficialService) Start() {
	s.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case now := <-ticker.C:
					s.runOnce(ctx, now)
				case <-ctx.Done():
					return
				}
			}
		}()
	})
}

func (s *ModelQualityOfficialService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
	s.wg.Wait()
	s.client.CloseIdleConnections()
}

func (s *ModelQualityOfficialService) runOnce(parent context.Context, now time.Time) {
	ctx, cancel := context.WithTimeout(parent, 8*time.Minute)
	defer cancel()
	cfg, err := s.load(ctx)
	if err != nil || !cfg.Enabled {
		return
	}
	key, err := s.keys.GetByID(ctx, cfg.APIKeyID)
	if err != nil || key == nil || key.UserID != cfg.OwnerID {
		return
	}
	interval := time.Duration(normalizeOfficialInterval(cfg.IntervalSeconds)) * time.Second
	slot := now.UTC().Truncate(interval)
	run := &ModelQualityRun{UserID: cfg.OwnerID, APIKeyID: key.ID, Model: cfg.Model, Theme: "official", Prompt: "hi", Status: "running", OfficialSlot: &slot}
	if key.User != nil {
		run.Author = key.User.Email
	}
	if key.Group != nil {
		run.GroupName = key.Group.Name
	}
	if err := s.repo.Create(ctx, run); err != nil {
		return
	}
	var content, requestID string
	status := "failed"
	defer func() {
		// 即使关机取消了 HTTP 请求，也尽量把已创建记录更新为明确终态。
		finalCtx, finalCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer finalCancel()
		if err := s.repo.Update(finalCtx, run.ID, run.UserID, status, content, requestID); err != nil {
			slog.Warn("官key检测记录终态保存失败", "run_id", run.ID)
		}
	}()
	if !validOfficialQualityKey(key, cfg.Model) {
		return
	}
	body, _ := json.Marshal(map[string]any{"model": cfg.Model, "messages": []map[string]string{{"role": "user", "content": "hi"}}, "stream": true, "max_tokens": 2048, "tool_choice": "none"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+key.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := s.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	requestID = resp.Header.Get("X-Client-Request-ID")
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return
	}
	lastSave := time.Time{}
	err = readModelQualityStream(ctx, resp.Body, func(delta string) error {
		if len(content)+len(delta) > ModelQualityMaxOutput {
			return errors.New("检测内容过大")
		}
		content += delta
		if time.Since(lastSave) >= time.Second {
			if err := s.repo.Update(ctx, run.ID, run.UserID, "running", content, requestID); err != nil {
				return err
			}
			lastSave = time.Now()
		}
		return nil
	})
	if err == nil && strings.TrimSpace(content) != "" {
		status = "completed"
	}
}

func readModelQualityStream(ctx context.Context, reader io.Reader, onText func(string) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), ModelQualityMaxOutput+16*1024)
	var lines []string
	finished, done := false, false
	process := func() error {
		data := strings.Join(lines, "\n")
		lines = nil
		if data == "" {
			return nil
		}
		if strings.TrimSpace(data) == "[DONE]" {
			done = true
			return nil
		}
		var payload struct {
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &payload) != nil || (len(payload.Error) > 0 && string(payload.Error) != "null") {
			return errors.New("检测流式响应无效")
		}
		if len(payload.Choices) > 0 {
			choice := payload.Choices[0]
			if choice.Delta.Content != "" {
				if err := onText(choice.Delta.Content); err != nil {
					return err
				}
			}
			if choice.FinishReason != nil {
				if *choice.FinishReason != "stop" {
					return errors.New("检测回答未完整结束")
				}
				finished = true
			}
		}
		return nil
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()
		if line == "" {
			if err := process(); err != nil {
				return err
			}
			if done {
				break
			}
		} else if strings.HasPrefix(line, "data:") {
			lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return errors.New("检测流已中断")
	}
	if len(lines) > 0 {
		if err := process(); err != nil {
			return err
		}
	}
	if !finished {
		return errors.New("检测回答未完整结束")
	}
	return ctx.Err()
}
