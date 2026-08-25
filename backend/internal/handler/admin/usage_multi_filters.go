package admin

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// parseCSVValues 兼容逗号分隔和重复查询参数，前端多选统一使用逗号分隔。
func parseCSVValues(c *gin.Context, name string) []string {
	rawValues := c.QueryArray(name)
	if len(rawValues) == 0 {
		rawValues = []string{c.Query(name)}
	}
	values := make([]string, 0)
	seen := make(map[string]struct{})
	for _, raw := range rawValues {
		for _, part := range strings.Split(raw, ",") {
			value := strings.TrimSpace(part)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			values = append(values, value)
		}
	}
	return values
}

func parseCSVInt64Values(c *gin.Context, name string) ([]int64, error) {
	values := parseCSVValues(c, name)
	result := make([]int64, 0, len(values))
	for _, value := range values {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid %s", name)
		}
		result = append(result, id)
	}
	return result, nil
}

func parseUsageMultiFilters(c *gin.Context) (usagestats.UsageLogFilters, error) {
	var filters usagestats.UsageLogFilters
	var err error
	if filters.UserIDs, err = parseCSVInt64Values(c, "user_ids"); err != nil {
		return filters, err
	}
	if filters.APIKeyIDs, err = parseCSVInt64Values(c, "api_key_ids"); err != nil {
		return filters, err
	}
	if filters.AccountIDs, err = parseCSVInt64Values(c, "account_ids"); err != nil {
		return filters, err
	}
	if filters.GroupIDs, err = parseCSVInt64Values(c, "group_ids"); err != nil {
		return filters, err
	}
	if filters.UpstreamSiteAccountIDs, err = parseCSVInt64Values(c, "upstream_site_account_ids"); err != nil {
		return filters, err
	}
	filters.Models = parseCSVValues(c, "models")
	for _, raw := range parseCSVValues(c, "request_types") {
		value, parseErr := service.ParseUsageRequestType(raw)
		if parseErr != nil {
			return filters, parseErr
		}
		filters.RequestTypes = append(filters.RequestTypes, int16(value))
	}
	for _, raw := range parseCSVValues(c, "billing_types") {
		value, parseErr := strconv.ParseInt(raw, 10, 8)
		if parseErr != nil {
			return filters, fmt.Errorf("invalid billing_types")
		}
		filters.BillingTypes = append(filters.BillingTypes, int8(value))
	}
	filters.BillingModes = parseCSVValues(c, "billing_modes")
	for _, raw := range parseCSVValues(c, "upstream_model_mismatches") {
		value, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			return filters, fmt.Errorf("invalid upstream_model_mismatches")
		}
		filters.UpstreamModelMismatches = append(filters.UpstreamModelMismatches, value)
	}
	return filters, nil
}
