//go:build unit

package repository

import (
	"database/sql"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageLogFirstPacketPersistence(t *testing.T) {
	log := &service.UsageLog{}
	field := reflect.ValueOf(log).Elem().FieldByName("FirstPacketMs")
	require.True(t, field.IsValid(), "首包必须独立存储，不能覆盖真实首字")
	firstToken := 45000
	log.FirstTokenMs = &firstToken
	for _, packet := range []*int{nil, new(int), &firstToken} {
		field.Set(reflect.ValueOf(packet))
		prepared := prepareUsageLogInsert(log)
		columns := strings.Split(usageLogSelectColumns, ", ")
		idx := -1
		for i, column := range columns[1:] {
			if column == "first_packet_ms" {
				idx = i
			}
		}
		require.NotEqual(t, -1, idx)
		require.Len(t, prepared.args, len(columns)-1)
		arg, ok := prepared.args[idx].(sql.NullInt64)
		require.True(t, ok)
		require.Equal(t, packet != nil, arg.Valid)
		if packet != nil {
			require.Equal(t, int64(*packet), arg.Int64)
		}
		key := usageLogBatchKey("req", 1)
		batch, _ := buildUsageLogBatchInsertQuery([]string{key}, map[string]usageLogInsertPrepared{key: prepared})
		bestEffort, _ := buildUsageLogBestEffortInsertQuery([]usageLogInsertPrepared{prepared})
		require.Equal(t, 3, strings.Count(batch, "first_packet_ms"), "CTE、INSERT、SELECT 必须对应")
		require.Equal(t, 3, strings.Count(bestEffort, "first_packet_ms"))
		for _, query := range []string{batch, bestEffort} {
			for _, match := range regexp.MustCompile(`(?s)(?:WITH input \(|INSERT INTO usage_logs \(|SELECT\n\s+user_id,)(.*?)\n\s*(?:\)|FROM input)`).FindAllStringSubmatch(query, -1) {
				list := strings.Join(strings.Fields(match[1]), " ")
				list = strings.TrimPrefix(list, "input_idx, ")
				if !strings.HasPrefix(list, "user_id,") {
					list = "user_id, " + list
				}
				require.Equal(t, strings.Join(columns[1:], ", "), list, "CTE、INSERT、SELECT 列顺序必须与参数一致")
			}
		}
		require.Equal(t, 45000, *log.FirstTokenMs)
	}
}

type firstPacketScanner struct{ packet sql.NullInt64 }

func (s firstPacketScanner) Scan(dest ...any) error {
	for i, column := range strings.Split(usageLogSelectColumns, ", ") {
		value := reflect.ValueOf(dest[i]).Elem()
		value.Set(reflect.Zero(value.Type()))
		switch column {
		case "first_packet_ms":
			value.Set(reflect.ValueOf(s.packet))
		case "first_token_ms":
			value.Set(reflect.ValueOf(sql.NullInt64{Valid: true, Int64: 45000}))
		}
	}
	return nil
}

func TestScanUsageLogFirstPacketPreservesNullAndZero(t *testing.T) {
	for _, packet := range []sql.NullInt64{{}, {Valid: true}, {Valid: true, Int64: 1050}} {
		log, err := scanUsageLog(firstPacketScanner{packet: packet})
		require.NoError(t, err)
		require.Equal(t, packet.Valid, log.FirstPacketMs != nil)
		if packet.Valid {
			require.Equal(t, int(packet.Int64), *log.FirstPacketMs)
		}
		require.Equal(t, 45000, *log.FirstTokenMs)
	}
}
