package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseNodeExporterSampleExtractsHostMetrics(t *testing.T) {
	raw := strings.NewReader(`# HELP node_cpu_seconds_total Seconds the CPUs spent in each mode.
node_cpu_seconds_total{cpu="0",mode="idle"} 100
node_cpu_seconds_total{cpu="0",mode="user"} 20
node_cpu_seconds_total{cpu="1",mode="idle"} 200
node_cpu_seconds_total{cpu="1",mode="system"} 40
node_cpu_seconds_total{cpu="1",mode="guest"} 50
node_memory_MemTotal_bytes 8589934592
node_memory_MemAvailable_bytes 3221225472
node_network_receive_bytes_total{device="eth0"} 1200000
node_network_receive_bytes_total{device="lo"} 999999
node_network_receive_bytes_total{device="veth123"} 5000000
node_network_transmit_bytes_total{device="eth0"} 800000
node_network_transmit_bytes_total{device="docker0"} 6000000
node_filesystem_size_bytes{device="/dev/vda1",fstype="ext4",mountpoint="/"} 107374182400
node_filesystem_avail_bytes{device="/dev/vda1",fstype="ext4",mountpoint="/"} 42949672960
`)

	sample, err := parseNodeExporterSample(raw, time.Unix(100, 0))
	require.NoError(t, err)
	require.Equal(t, float64(360), sample.cpuTotalSeconds)
	require.Equal(t, float64(300), sample.cpuIdleSeconds)
	require.Equal(t, uint64(8*1024*1024*1024), sample.memoryTotalBytes)
	require.Equal(t, uint64(3*1024*1024*1024), sample.memoryAvailableBytes)
	require.Equal(t, float64(1200000), sample.networkReceiveBytes)
	require.Equal(t, float64(800000), sample.networkTransmitBytes)
	require.Equal(t, uint64(100*1024*1024*1024), sample.diskTotalBytes)
	require.Equal(t, uint64(40*1024*1024*1024), sample.diskAvailableBytes)
}

func TestDeriveNodeExporterStatsUsesCounterDeltas(t *testing.T) {
	previous := &nodeExporterSample{
		sampledAt:            time.Unix(100, 0),
		hasNetwork:           true,
		cpuTotalSeconds:      100,
		cpuIdleSeconds:       80,
		networkReceiveBytes:  1000,
		networkTransmitBytes: 500,
	}
	current := &nodeExporterSample{
		sampledAt:            time.Unix(110, 0),
		hasNetwork:           true,
		cpuTotalSeconds:      120,
		cpuIdleSeconds:       85,
		memoryTotalBytes:     1000,
		memoryAvailableBytes: 400,
		networkReceiveBytes:  3000,
		networkTransmitBytes: 1500,
		diskTotalBytes:       2000,
		diskAvailableBytes:   500,
	}

	stats := deriveNodeExporterStats(previous, current)
	require.NotNil(t, stats.cpuUsagePercent)
	require.InDelta(t, 75, *stats.cpuUsagePercent, 0.01)
	require.NotNil(t, stats.networkReceiveBytesPerSecond)
	require.InDelta(t, 200, *stats.networkReceiveBytesPerSecond, 0.01)
	require.NotNil(t, stats.networkTransmitBytesPerSecond)
	require.InDelta(t, 100, *stats.networkTransmitBytesPerSecond, 0.01)
	require.Equal(t, int64(2000), *stats.networkReceiveBytes)
	require.Equal(t, int64(1000), *stats.networkTransmitBytes)
	require.Equal(t, "host", stats.resourceSource)
	require.Equal(t, int64(1500), *stats.diskUsedBytes)
	require.InDelta(t, 75, *stats.diskUsagePercent, 0.01)
}

func TestReadHostNetworkTotalsExcludesVirtualDevices(t *testing.T) {
	sysfsRoot := t.TempDir()
	writeCounter := func(device, name, value string) {
		path := filepath.Join(sysfsRoot, "class", "net", device, "statistics")
		require.NoError(t, os.MkdirAll(path, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(path, name), []byte(value), 0o644))
	}
	for _, sample := range []struct {
		device string
		rx     string
		tx     string
	}{
		{device: "eth0", rx: "1200", tx: "800"},
		{device: "ens3", rx: "300", tx: "200"},
		{device: "lo", rx: "9999", tx: "9999"},
		{device: "docker0", rx: "8888", tx: "8888"},
	} {
		writeCounter(sample.device, "rx_bytes", sample.rx)
		writeCounter(sample.device, "tx_bytes", sample.tx)
	}

	receive, transmit, err := readHostNetworkTotals(sysfsRoot)
	require.NoError(t, err)
	require.Equal(t, float64(1500), receive)
	require.Equal(t, float64(1000), transmit)
}

func TestParseNodeExporterSampleRejectsEmptyOrUnrelatedResponse(t *testing.T) {
	for _, raw := range []string{"", "<html>ok</html>", "# 只有注释\nunknown_metric 1\n"} {
		_, err := parseNodeExporterSample(strings.NewReader(raw), time.Now())
		require.ErrorContains(t, err, "没有可用的主机指标")
	}
}
