package service

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type nodeExporterSample struct {
	sampledAt            time.Time
	hasCPU               bool
	hasMemory            bool
	hasNetwork           bool
	hasDisk              bool
	cpuTotalSeconds      float64
	cpuIdleSeconds       float64
	memoryTotalBytes     uint64
	memoryAvailableBytes uint64
	networkReceiveBytes  float64
	networkTransmitBytes float64
	diskTotalBytes       uint64
	diskAvailableBytes   uint64
}

type nodeExporterStats struct {
	resourceSource                string
	cpuUsagePercent               *float64
	memoryUsedMB                  *int64
	memoryTotalMB                 *int64
	memoryUsagePercent            *float64
	networkReceiveBytesPerSecond  *float64
	networkTransmitBytesPerSecond *float64
	networkReceiveBytes           *int64
	networkTransmitBytes          *int64
	diskUsedBytes                 *int64
	diskTotalBytes                *int64
	diskUsagePercent              *float64
}

func parseNodeExporterSample(reader io.Reader, sampledAt time.Time) (*nodeExporterSample, error) {
	sample := &nodeExporterSample{sampledAt: sampledAt}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		metric := fields[0]
		switch {
		case strings.HasPrefix(metric, "node_cpu_seconds_total{"):
			mode := metricLabel(metric, "mode")
			if mode == "guest" || mode == "guest_nice" {
				continue
			}
			sample.cpuTotalSeconds += value
			sample.hasCPU = true
			if mode == "idle" {
				sample.cpuIdleSeconds += value
			}
		case metric == "node_memory_MemTotal_bytes":
			sample.memoryTotalBytes = positiveUint64(value)
			sample.hasMemory = true
		case metric == "node_memory_MemAvailable_bytes":
			sample.memoryAvailableBytes = positiveUint64(value)
			sample.hasMemory = true
		case strings.HasPrefix(metric, "node_network_receive_bytes_total{") && isHostTrafficDevice(metricLabel(metric, "device")):
			sample.networkReceiveBytes += value
			sample.hasNetwork = true
		case strings.HasPrefix(metric, "node_network_transmit_bytes_total{") && isHostTrafficDevice(metricLabel(metric, "device")):
			sample.networkTransmitBytes += value
			sample.hasNetwork = true
		case strings.HasPrefix(metric, "node_filesystem_size_bytes{") && metricLabel(metric, "mountpoint") == "/":
			sample.diskTotalBytes = positiveUint64(value)
			sample.hasDisk = true
		case strings.HasPrefix(metric, "node_filesystem_avail_bytes{") && metricLabel(metric, "mountpoint") == "/":
			sample.diskAvailableBytes = positiveUint64(value)
			sample.hasDisk = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取 Node Exporter 指标失败: %w", err)
	}
	if !sample.hasCPU && !sample.hasMemory && !sample.hasNetwork && !sample.hasDisk {
		return nil, fmt.Errorf("Node Exporter 响应中没有可用的主机指标")
	}
	return sample, nil
}

func isHostTrafficDevice(device string) bool {
	if device == "" || device == "lo" {
		return false
	}
	for _, prefix := range []string{"veth", "docker", "br-", "virbr", "cni", "flannel"} {
		if strings.HasPrefix(device, prefix) {
			return false
		}
	}
	return true
}

func readHostNetworkTotals(sysfsRoot string) (float64, float64, error) {
	networkRoot := filepath.Join(strings.TrimSpace(sysfsRoot), "class", "net")
	entries, err := os.ReadDir(networkRoot)
	if err != nil {
		return 0, 0, fmt.Errorf("读取宿主机网卡目录失败: %w", err)
	}

	var receive, transmit float64
	physicalDevices := 0
	for _, entry := range entries {
		device := entry.Name()
		if !isHostTrafficDevice(device) {
			continue
		}
		readCounter := func(name string) (uint64, error) {
			raw, readErr := os.ReadFile(filepath.Join(networkRoot, device, "statistics", name))
			if readErr != nil {
				return 0, readErr
			}
			return strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
		}
		rx, rxErr := readCounter("rx_bytes")
		tx, txErr := readCounter("tx_bytes")
		if rxErr != nil || txErr != nil {
			return 0, 0, fmt.Errorf("读取宿主机网卡 %s 计数器失败", device)
		}
		receive += float64(rx)
		transmit += float64(tx)
		physicalDevices++
	}
	if physicalDevices == 0 {
		return 0, 0, fmt.Errorf("宿主机 sysfs 中没有可统计的物理网卡")
	}
	return receive, transmit, nil
}

func metricLabel(metric, name string) string {
	needle := name + "=\""
	start := strings.Index(metric, needle)
	if start < 0 {
		return ""
	}
	start += len(needle)
	end := strings.IndexByte(metric[start:], '"')
	if end < 0 {
		return ""
	}
	return metric[start : start+end]
}

func positiveUint64(value float64) uint64 {
	if value <= 0 || value > math.MaxUint64 {
		return 0
	}
	return uint64(value)
}

func deriveNodeExporterStats(previous, current *nodeExporterSample) nodeExporterStats {
	stats := nodeExporterStats{resourceSource: "host"}
	if current == nil {
		return stats
	}
	if current.memoryTotalBytes > 0 && current.memoryAvailableBytes <= current.memoryTotalBytes {
		used := current.memoryTotalBytes - current.memoryAvailableBytes
		usedMB, totalMB := int64(used/(1024*1024)), int64(current.memoryTotalBytes/(1024*1024))
		usage := float64(used) / float64(current.memoryTotalBytes) * 100
		stats.memoryUsedMB, stats.memoryTotalMB, stats.memoryUsagePercent = &usedMB, &totalMB, &usage
	}
	if current.diskTotalBytes > 0 && current.diskAvailableBytes <= current.diskTotalBytes {
		used := int64(current.diskTotalBytes - current.diskAvailableBytes)
		total := int64(current.diskTotalBytes)
		usage := float64(used) / float64(total) * 100
		stats.diskUsedBytes, stats.diskTotalBytes, stats.diskUsagePercent = &used, &total, &usage
	}
	if previous == nil {
		return stats
	}
	elapsed := current.sampledAt.Sub(previous.sampledAt).Seconds()
	cpuTotalDelta := current.cpuTotalSeconds - previous.cpuTotalSeconds
	cpuIdleDelta := current.cpuIdleSeconds - previous.cpuIdleSeconds
	if cpuTotalDelta > 0 && cpuIdleDelta >= 0 && cpuIdleDelta <= cpuTotalDelta {
		usage := (1 - cpuIdleDelta/cpuTotalDelta) * 100
		stats.cpuUsagePercent = &usage
	}
	if elapsed > 0 && previous.hasNetwork && current.hasNetwork {
		if delta := current.networkReceiveBytes - previous.networkReceiveBytes; delta >= 0 {
			value := delta / elapsed
			stats.networkReceiveBytesPerSecond = &value
			bytes := int64(math.Round(delta))
			stats.networkReceiveBytes = &bytes
		}
		if delta := current.networkTransmitBytes - previous.networkTransmitBytes; delta >= 0 {
			value := delta / elapsed
			stats.networkTransmitBytesPerSecond = &value
			bytes := int64(math.Round(delta))
			stats.networkTransmitBytes = &bytes
		}
	}
	return stats
}
