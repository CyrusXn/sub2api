package service

import (
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestPricingAPIOnlyReloadsSharedCatalog(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		catalog := filepath.Join(dir, "model_pricing.json")
		override := filepath.Join(dir, "override.json")
		old := `{"gpt-5.4":{"input_cost_per_token":0.0000025,"output_cost_per_token":0.000015}}`
		latest := `{"gpt-6-sol":{"input_cost_per_token":0.000002,"output_cost_per_token":0.00001}}`
		require.NoError(t, os.WriteFile(catalog, []byte(old), 0600))
		require.NoError(t, os.WriteFile(override, []byte(`{}`), 0600))
		client := &countingPricingRemoteClient{}
		svc := NewPricingService(&config.Config{
			DeploymentRole: config.DeploymentRoleAPIOnly,
			Pricing: config.PricingConfig{
				DataDir: dir, OverrideFile: override,
				RemoteURL: "https://example.com/pricing.json", HashURL: "https://example.com/pricing.sha256",
			},
		}, client)
		require.NoError(t, svc.Initialize())
		defer svc.Stop()
		synctest.Wait()
		tick := func() {
			time.Sleep(time.Minute)
			synctest.Wait()
		}

		require.NoError(t, os.WriteFile(catalog, []byte(latest), 0600))
		tick()
		price := svc.GetIdentifiedModelPricing("gpt-6-sol")
		require.NotNil(t, price, "主节点更新共享目录后，API 节点必须自动加载新模型")
		require.Equal(t, 2e-6, price.InputCostPerToken)
		require.Equal(t, 10e-6, price.OutputCostPerToken)
		tick()
		require.Same(t, price, svc.GetIdentifiedModelPricing("gpt-6-sol"), "文件未变时不重建")

		for _, broken := range []string{`{"gpt-6-sol":`, `null`, `{}`} {
			require.NoError(t, os.WriteFile(catalog, []byte(broken), 0600))
			tick()
			require.Same(t, price, svc.GetIdentifiedModelPricing("gpt-6-sol"), "无效目录必须保留上次有效价格")
		}
		require.NoError(t, os.Remove(catalog))
		tick()
		require.Same(t, price, svc.GetIdentifiedModelPricing("gpt-6-sol"))
		require.NoError(t, os.WriteFile(catalog, []byte(latest), 0600))
		require.NoError(t, os.WriteFile(override, []byte(`{"gpt-6-sol":`), 0600))
		tick()
		require.Same(t, price, svc.GetIdentifiedModelPricing("gpt-6-sol"), "损坏的覆盖文件不能替换生效价格")
		require.NoError(t, os.WriteFile(override, []byte(`{"gpt-6-sol":{"input_cost_per_token":0.000003}}`), 0600))
		tick()
		require.Equal(t, 3e-6, svc.GetIdentifiedModelPricing("gpt-6-sol").InputCostPerToken)
		require.NoError(t, os.Remove(override))
		tick()
		require.Equal(t, 2e-6, svc.GetIdentifiedModelPricing("gpt-6-sol").InputCostPerToken)
		require.Zero(t, client.calls, "API 节点不得下载或写入共享价格目录")
		body, err := os.ReadFile(catalog)
		require.NoError(t, err)
		require.Equal(t, latest, string(body))
	})
}

func TestPricingAPIOnlyFallbackDoesNotWriteSharedCatalog(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		fallback := filepath.Join(t.TempDir(), "fallback.json")
		require.NoError(t, os.WriteFile(fallback, []byte(hotReloadCatalogJSON), 0600))
		svc := NewPricingService(&config.Config{
			DeploymentRole: config.DeploymentRoleAPIOnly,
			Pricing:        config.PricingConfig{DataDir: dir, FallbackFile: fallback},
		}, &countingPricingRemoteClient{})
		require.NoError(t, svc.Initialize())
		defer svc.Stop()
		require.NotNil(t, svc.GetIdentifiedModelPricing("remote-model"))
		_, err := os.Stat(filepath.Join(dir, "model_pricing.json"))
		require.True(t, os.IsNotExist(err), "API 节点兜底启动不得覆盖主节点共享目录")
		synctest.Wait()
		require.NoError(t, os.WriteFile(svc.getPricingFilePath(), []byte(`{"new-model":{"input_cost_per_token":0.000002}}`), 0600))
		time.Sleep(time.Minute)
		synctest.Wait()
		require.NotNil(t, svc.GetIdentifiedModelPricing("new-model"), "仅配置本地文件也必须启动共享目录监听")
	})
}
