package handler

import (
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *ChannelMonitorV2Handler) PassiveCards(c *gin.Context) {
	days := 7
	switch c.DefaultQuery("range", "7d") {
	case "7d":
	case "15d":
		days = 15
	case "30d":
		days = 30
	default:
		response.Error(c, http.StatusBadRequest, "invalid monitor range")
		return
	}
	end := time.Now().UTC().Truncate(time.Minute)
	filter := service.ChannelMonitorV2Filter{Start: end.AddDate(0, 0, -days), End: end, Platforms: queryList(c, "platform")}
	if !h.scopeFilter(c, &filter, channelMonitorV2IsAdmin(c)) {
		return
	}
	items, err := h.service.PassiveCards(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items})
}
