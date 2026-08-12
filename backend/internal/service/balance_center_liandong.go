package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	balanceCenterLiandongHost    = "pay.ldxp.cn"
	balanceCenterLiandongPath    = "/shopApi/Order/list"
	balanceCenterLiandongTimeout = 15 * time.Second
)

var ErrBalanceCenterLiandongSessionNotFound = errors.New("尚未配置联动小铺 curl 请求")

type BalanceCenterLiandongRequest struct {
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
	Body     map[string]any    `json:"body"`
	Keywords string            `json:"keywords"`
}

type BalanceCenterLiandongSessionResult struct {
	Keywords string `json:"keywords"`
}

type BalanceCenterLiandongSyncResult struct {
	Synced int `json:"synced"`
}

type BalanceCenterLiandongOrder struct {
	TransactionNo string
	GoodsName     string
	PaidAmount    float64
	Quantity      int
	Status        string
	PaidAt        time.Time
}

type BalanceCenterLiandongRepository interface {
	SaveBalanceCenterLiandongSession(context.Context, string) error
	GetBalanceCenterLiandongSession(context.Context) (string, error)
	UpsertBalanceCenterLiandongOrders(context.Context, []BalanceCenterLiandongOrder) (int, error)
	SyncBalanceCenterAutomaticRecords(context.Context) (int, error)
}

func (s *BalanceCenterService) SetLiandongDependencies(encryptor SecretEncryptor, client *http.Client) {
	if s == nil {
		return
	}
	s.liandongEncryptor = encryptor
	if client == nil {
		client = &http.Client{Timeout: balanceCenterLiandongTimeout}
	}
	s.liandongClient = client
}

func (s *BalanceCenterService) SaveLiandongSession(ctx context.Context, rawCurl string) (*BalanceCenterLiandongSessionResult, error) {
	repository, err := s.liandongRepository()
	if err != nil {
		return nil, err
	}
	if s.liandongEncryptor == nil {
		return nil, errors.New("联动小铺会话加密器不可用")
	}
	request, err := ParseBalanceCenterLiandongCurl(rawCurl)
	if err != nil {
		return nil, err
	}
	plain, err := json.Marshal(request)
	if err != nil {
		return nil, errors.New("联动小铺请求配置无法序列化")
	}
	ciphertext, err := s.liandongEncryptor.Encrypt(string(plain))
	if err != nil {
		return nil, errors.New("联动小铺请求配置加密失败")
	}
	if err := repository.SaveBalanceCenterLiandongSession(ctx, ciphertext); err != nil {
		return nil, err
	}
	return &BalanceCenterLiandongSessionResult{Keywords: request.Keywords}, nil
}

func (s *BalanceCenterService) SyncLiandong(ctx context.Context) (*BalanceCenterLiandongSyncResult, error) {
	repository, err := s.liandongRepository()
	if err != nil {
		return nil, err
	}
	if s.liandongEncryptor == nil {
		return nil, errors.New("联动小铺会话加密器不可用")
	}
	ciphertext, err := repository.GetBalanceCenterLiandongSession(ctx)
	if err != nil {
		return nil, err
	}
	plain, err := s.liandongEncryptor.Decrypt(ciphertext)
	if err != nil {
		return nil, errors.New("联动小铺 curl 配置无法解密，请重新粘贴")
	}
	var request BalanceCenterLiandongRequest
	if err := json.Unmarshal([]byte(plain), &request); err != nil {
		return nil, errors.New("联动小铺 curl 配置无法识别，请重新粘贴")
	}
	orders, err := s.fetchBalanceCenterLiandongOrders(ctx, &request)
	if err != nil {
		return nil, err
	}
	synced, err := repository.UpsertBalanceCenterLiandongOrders(ctx, orders)
	if err != nil {
		return nil, err
	}
	return &BalanceCenterLiandongSyncResult{Synced: synced}, nil
}

func (s *BalanceCenterService) SyncAutomaticRecords(ctx context.Context) (*BalanceCenterLiandongSyncResult, error) {
	repository, err := s.liandongRepository()
	if err != nil {
		return nil, err
	}
	synced, err := repository.SyncBalanceCenterAutomaticRecords(ctx)
	if err != nil {
		return nil, err
	}
	return &BalanceCenterLiandongSyncResult{Synced: synced}, nil
}

func (s *BalanceCenterService) liandongRepository() (BalanceCenterLiandongRepository, error) {
	if s == nil {
		return nil, errors.New("余额中心服务不可用")
	}
	repository, ok := s.repository.(BalanceCenterLiandongRepository)
	if !ok {
		return nil, errors.New("联动小铺仓储不可用")
	}
	return repository, nil
}

// ParseBalanceCenterLiandongCurl 只解析浏览器复制的 curl 文本，不调用 shell，避免 Cookie 被执行或写入日志。
func ParseBalanceCenterLiandongCurl(raw string) (*BalanceCenterLiandongRequest, error) {
	tokens := tokenizeBalanceCenterCurl(raw)
	if len(tokens) == 0 || strings.ToLower(tokens[0]) != "curl" {
		return nil, errors.New("请粘贴浏览器复制的 curl 请求")
	}
	endpoint := ""
	bodyText := ""
	headers := map[string]string{}
	for i := 1; i < len(tokens); i++ {
		token := tokens[i]
		switch token {
		case "--url":
			i++
			endpoint = tokenAt(tokens, i)
		case "-H", "--header":
			i++
			addBalanceCenterLiandongHeader(headers, tokenAt(tokens, i))
		case "-b", "--cookie":
			i++
			if value := strings.TrimSpace(tokenAt(tokens, i)); value != "" {
				headers["cookie"] = value
			}
		case "--data-raw", "--data", "--data-binary", "-d":
			i++
			bodyText = tokenAt(tokens, i)
		default:
			if endpoint == "" && strings.HasPrefix(strings.ToLower(token), "http") {
				endpoint = token
			}
		}
	}
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != balanceCenterLiandongHost || parsed.Path != balanceCenterLiandongPath {
		return nil, errors.New("只允许联动小铺订单接口")
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(bodyText), &body); err != nil || body == nil {
		return nil, errors.New("curl 请求体不是有效 JSON")
	}
	keywords, _ := body["keywords"].(string)
	keywords = strings.TrimSpace(keywords)
	if keywords == "" {
		return nil, errors.New("curl 请求体缺少查询手机号")
	}
	for _, name := range []string{"host", "content-length", "connection", "transfer-encoding"} {
		delete(headers, name)
	}
	body["status"] = float64(1)
	body["current"] = float64(1)
	body["pageSize"] = float64(999999)
	body["total"] = float64(0)
	body["keywords"] = keywords
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return &BalanceCenterLiandongRequest{URL: parsed.String(), Headers: headers, Body: body, Keywords: keywords}, nil
}

func (s *BalanceCenterService) fetchBalanceCenterLiandongOrders(ctx context.Context, request *BalanceCenterLiandongRequest) ([]BalanceCenterLiandongOrder, error) {
	if err := validateBalanceCenterLiandongRequest(request); err != nil {
		return nil, err
	}
	client := s.liandongClient
	if client == nil {
		client = &http.Client{Timeout: balanceCenterLiandongTimeout}
	}
	orders := make(map[string]BalanceCenterLiandongOrder)
	for current, pages := 1, 1; current <= pages; current++ {
		body := make(map[string]any, len(request.Body)+5)
		for key, value := range request.Body {
			body[key] = value
		}
		body["status"], body["current"], body["pageSize"], body["total"], body["keywords"] = 1, current, 999999, 0, request.Keywords
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, errors.New("联动小铺请求体无效")
		}
		httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, request.URL, bytes.NewReader(payload))
		if err != nil {
			return nil, errors.New("联动小铺请求无法创建")
		}
		for name, value := range request.Headers {
			httpRequest.Header.Set(name, value)
		}
		if httpRequest.Header.Get("Accept") == "" {
			httpRequest.Header.Set("Accept", "application/json")
		}
		httpRequest.Header.Set("Content-Type", "application/json")
		response, err := client.Do(httpRequest)
		if err != nil {
			return nil, errors.New("服务器无法连接联动小铺，可能被安全验证拦截或网络超时")
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		_ = response.Body.Close()
		if readErr != nil {
			return nil, errors.New("联动小铺返回数据读取失败")
		}
		contentType := strings.ToLower(response.Header.Get("Content-Type"))
		if strings.Contains(contentType, "text/html") || regexp.MustCompile(`(?i)Aliyun|CF_APP_WAF|安全验证|人机验证`).Match(responseBody) {
			return nil, errors.New("服务器直连已被联动小铺安全验证拦截，请重新从已验证的浏览器复制 curl")
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("联动小铺请求失败（HTTP %d）", response.StatusCode)
		}
		parsed, nextPages, err := parseBalanceCenterLiandongResponse(responseBody)
		if err != nil {
			return nil, err
		}
		if nextPages > pages {
			pages = nextPages
		}
		for _, order := range parsed {
			orders[order.TransactionNo] = order
		}
	}
	result := make([]BalanceCenterLiandongOrder, 0, len(orders))
	for _, order := range orders {
		result = append(result, order)
	}
	return result, nil
}

func validateBalanceCenterLiandongRequest(request *BalanceCenterLiandongRequest) error {
	if request == nil || strings.TrimSpace(request.Keywords) == "" {
		return ErrBalanceCenterLiandongSessionNotFound
	}
	parsed, err := url.Parse(request.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != balanceCenterLiandongHost || parsed.Path != balanceCenterLiandongPath {
		return errors.New("只允许联动小铺订单接口")
	}
	return nil
}

func parseBalanceCenterLiandongResponse(payload []byte) ([]BalanceCenterLiandongOrder, int, error) {
	var root struct {
		Code any    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Pages int               `json:"pages"`
			List  []json.RawMessage `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, 0, errors.New("联动小铺返回数据格式无法识别")
	}
	if numberFromBalanceCenterAny(root.Code) != 1 {
		if regexp.MustCompile(`(?i)验证码|人机|ticket|验证失败|登录|未授权|cookie`).MatchString(root.Msg) {
			return nil, 0, errors.New("联动登录状态已失效，请重新从浏览器复制最新 curl")
		}
		if strings.TrimSpace(root.Msg) != "" {
			return nil, 0, errors.New(strings.TrimSpace(root.Msg))
		}
		return nil, 0, errors.New("联动小铺查询失败")
	}
	orders := make([]BalanceCenterLiandongOrder, 0, len(root.Data.List))
	for _, raw := range root.Data.List {
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, 0, errors.New("联动小铺返回数据格式无法识别")
		}
		if int(numberFromBalanceCenterAny(value["status"])) != 1 {
			continue
		}
		transactionNo := stringFromBalanceCenterAny(value["trade_no"])
		goodsName := stringFromBalanceCenterAny(value["goods_name"])
		paidAmount := numberFromBalanceCenterAny(value["total_amount"])
		createdAt := int64(numberFromBalanceCenterAny(value["create_time"]))
		if transactionNo == "" || goodsName == "" || !isFiniteLegacyNumber(paidAmount) || paidAmount < 0 || createdAt <= 0 {
			return nil, 0, errors.New("联动小铺返回数据格式无法识别")
		}
		quantity := int(numberFromBalanceCenterAny(value["quantity"]))
		if quantity < 1 {
			quantity = 1
		}
		orders = append(orders, BalanceCenterLiandongOrder{
			TransactionNo: transactionNo,
			GoodsName:     goodsName,
			PaidAmount:    roundLegacyMoney(paidAmount),
			Quantity:      quantity,
			Status:        "paid",
			PaidAt:        time.Unix(createdAt, 0).UTC(),
		})
	}
	if root.Data.Pages < 1 {
		root.Data.Pages = 1
	}
	return orders, root.Data.Pages, nil
}

func stringFromBalanceCenterAny(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func numberFromBalanceCenterAny(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case json.Number:
		result, _ := typed.Float64()
		return result
	case string:
		result, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return result
	default:
		return math.NaN()
	}
}

func tokenAt(tokens []string, index int) string {
	if index < 0 || index >= len(tokens) {
		return ""
	}
	return tokens[index]
}

func addBalanceCenterLiandongHeader(headers map[string]string, raw string) {
	name, value, ok := strings.Cut(raw, ":")
	if !ok {
		return
	}
	name = strings.ToLower(strings.TrimSpace(name))
	value = strings.TrimSpace(value)
	if name != "" && value != "" {
		headers[name] = value
	}
}

func tokenizeBalanceCenterCurl(raw string) []string {
	input := regexp.MustCompile(`\\\r?\n`).ReplaceAllString(raw, " ")
	tokens := make([]string, 0)
	var current strings.Builder
	var quote rune
	for _, char := range input {
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				current.WriteRune(char)
			}
			continue
		}
		switch {
		case char == '\'' || char == '"':
			quote = char
		case char == ' ' || char == '\t' || char == '\n' || char == '\r':
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(char)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}
