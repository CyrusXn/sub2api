package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type balanceCenterLiandongRepositoryStub struct {
	BalanceCenterRepository
	sessionCipher string
	orders        []BalanceCenterLiandongOrder
	automatic     int
}

func (r *balanceCenterLiandongRepositoryStub) SaveBalanceCenterLiandongSession(_ context.Context, cipher string) error {
	r.sessionCipher = cipher
	return nil
}

func (r *balanceCenterLiandongRepositoryStub) GetBalanceCenterLiandongSession(context.Context) (string, error) {
	if r.sessionCipher == "" {
		return "", ErrBalanceCenterLiandongSessionNotFound
	}
	return r.sessionCipher, nil
}

func (r *balanceCenterLiandongRepositoryStub) UpsertBalanceCenterLiandongOrders(_ context.Context, orders []BalanceCenterLiandongOrder) (int, error) {
	r.orders = append([]BalanceCenterLiandongOrder(nil), orders...)
	return len(orders), nil
}

func (r *balanceCenterLiandongRepositoryStub) SyncBalanceCenterAutomaticRecords(context.Context) (int, error) {
	return r.automatic, nil
}

type balanceCenterLiandongEncryptorStub struct {
	plain string
}

func (e *balanceCenterLiandongEncryptorStub) Encrypt(value string) (string, error) {
	e.plain = value
	return "opaque-ciphertext", nil
}

func (e *balanceCenterLiandongEncryptorStub) Decrypt(value string) (string, error) {
	if value != "opaque-ciphertext" || e.plain == "" {
		return "", errors.New("密文无效")
	}
	return e.plain, nil
}

type balanceCenterRoundTripFunc func(*http.Request) (*http.Response, error)

func (f balanceCenterRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestBalanceCenterSaveLiandongSessionEncryptsSensitiveRequest(t *testing.T) {
	repository := &balanceCenterLiandongRepositoryStub{}
	service := NewBalanceCenterService(repository, nil)
	encryptor := &balanceCenterLiandongEncryptorStub{}
	service.SetLiandongDependencies(encryptor, nil)
	raw := `curl 'https://pay.ldxp.cn/shopApi/Order/list' -H 'cookie: session=secret-cookie' --data-raw '{"keywords":"13800000000"}'`

	result, err := service.SaveLiandongSession(context.Background(), raw)

	require.NoError(t, err)
	require.Equal(t, "13800000000", result.Keywords)
	require.NotContains(t, repository.sessionCipher, "secret-cookie")
	require.Equal(t, "opaque-ciphertext", repository.sessionCipher)
	require.Contains(t, encryptor.plain, "secret-cookie")
}

func TestBalanceCenterSyncLiandongStoresPaidOrdersUsingActualAmount(t *testing.T) {
	repository := &balanceCenterLiandongRepositoryStub{}
	service := NewBalanceCenterService(repository, nil)
	client := &http.Client{Transport: balanceCenterRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, "pay.ldxp.cn", request.URL.Hostname())
		require.Equal(t, "/shopApi/Order/list", request.URL.Path)
		require.Equal(t, "session=secret-cookie", request.Header.Get("cookie"))
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), `"status":1`)
		payload := `{"code":1,"data":{"pages":1,"list":[
			{"trade_no":"paid-1","goods_name":"Yigpt 60元兑换码","total_amount":"55.50","quantity":1,"status":1,"create_time":1786500000},
			{"trade_no":"unpaid-1","goods_name":"Yigpt 60元兑换码","total_amount":60,"quantity":1,"status":0,"create_time":1786500001}
		]}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(payload)),
			Request:    request,
		}, nil
	})}
	encryptor := &balanceCenterLiandongEncryptorStub{}
	service.SetLiandongDependencies(encryptor, client)
	request := &BalanceCenterLiandongRequest{
		URL:      "https://pay.ldxp.cn/shopApi/Order/list",
		Headers:  map[string]string{"cookie": "session=secret-cookie"},
		Body:     map[string]any{"keywords": "13800000000"},
		Keywords: "13800000000",
	}
	plain, err := json.Marshal(request)
	require.NoError(t, err)
	encryptor.plain = string(plain)
	repository.sessionCipher = "opaque-ciphertext"

	result, err := service.SyncLiandong(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, result.Synced)
	require.Len(t, repository.orders, 1)
	require.Equal(t, "paid-1", repository.orders[0].TransactionNo)
	require.Equal(t, 55.5, repository.orders[0].PaidAmount)
	require.Equal(t, time.Unix(1786500000, 0).UTC(), repository.orders[0].PaidAt)
}

func TestBalanceCenterSyncAutomaticRecordsDelegatesToRepository(t *testing.T) {
	repository := &balanceCenterLiandongRepositoryStub{automatic: 7}
	service := NewBalanceCenterService(repository, nil)

	result, err := service.SyncAutomaticRecords(context.Background())

	require.NoError(t, err)
	require.Equal(t, 7, result.Synced)
}
