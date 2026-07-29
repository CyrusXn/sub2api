package admin

import (
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type upstreamSiteCredentialRequest struct {
	BaseURL       string `json:"base_url" binding:"required,max=1000"`
	LoginUsername string `json:"login_username" binding:"required,max=320"`
	LoginPassword string `json:"login_password" binding:"max=2000"`
}

func (h *AccountHandler) ListUpstreamSites(c *gin.Context) {
	if h.upstreamSiteCredentials == nil {
		response.ErrorFrom(c, service.ErrUpstreamSiteCredentialUnavailable)
		return
	}
	sites, err := h.upstreamSiteCredentials.List(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": sites})
}

func (h *AccountHandler) UpsertUpstreamSiteCredential(c *gin.Context) {
	if h.upstreamSiteCredentials == nil {
		response.ErrorFrom(c, service.ErrUpstreamSiteCredentialUnavailable)
		return
	}
	var req upstreamSiteCredentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	site, err := h.upstreamSiteCredentials.Upsert(c.Request.Context(), service.UpstreamSiteCredentialInput{
		BaseURL:  req.BaseURL,
		Username: req.LoginUsername,
		Password: req.LoginPassword,
	})
	if errors.Is(err, service.ErrUpstreamSiteCredentialInvalid) {
		response.BadRequest(c, err.Error())
		return
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, site)
}

func (h *AccountHandler) DeleteUpstreamSiteCredential(c *gin.Context) {
	if h.upstreamSiteCredentials == nil {
		response.ErrorFrom(c, service.ErrUpstreamSiteCredentialUnavailable)
		return
	}
	host := strings.TrimSpace(c.Param("host"))
	if host == "" {
		response.BadRequest(c, "Invalid upstream site host")
		return
	}
	if err := h.upstreamSiteCredentials.Delete(c.Request.Context(), "https://"+host); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"host": strings.ToLower(host), "deleted": true})
}
