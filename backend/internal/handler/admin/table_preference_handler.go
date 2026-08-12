package admin

import (
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type TablePreferenceHandler struct {
	service *service.TablePreferenceService
}

func NewTablePreferenceHandler(preferenceService *service.TablePreferenceService) *TablePreferenceHandler {
	return &TablePreferenceHandler{service: preferenceService}
}

func (h *TablePreferenceHandler) Get(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	preference, err := h.service.Get(c.Request.Context(), subject.UserID, c.Param("table_key"))
	if err != nil {
		if errors.Is(err, service.ErrTablePreferenceInvalid) {
			response.BadRequest(c, "Invalid table preference")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, preference)
}

func (h *TablePreferenceHandler) Save(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var preference service.TablePreference
	if err := c.ShouldBindJSON(&preference); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	preference.TableKey = c.Param("table_key")
	saved, err := h.service.Save(c.Request.Context(), subject.UserID, &preference)
	if err != nil {
		if errors.Is(err, service.ErrTablePreferenceInvalid) {
			response.BadRequest(c, "Invalid table preference")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, saved)
}
