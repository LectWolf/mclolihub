package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *ChannelMonitorV2Handler) qualityAssetScope(c *gin.Context, requireResultID bool) (int64, string, bool) {
	groupID, err := strconv.ParseInt(c.Query("group_id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "invalid group id")
		return 0, "", false
	}
	resultID := c.Param("resultID")
	if requireResultID {
		if _, err := uuid.Parse(resultID); err != nil {
			response.BadRequest(c, "invalid result id")
			return 0, "", false
		}
	}
	filter := service.ChannelMonitorV2Filter{}
	if !h.scopeFilter(c, &filter, channelMonitorV2IsAdmin(c)) {
		return 0, "", false
	}
	if filter.RestrictGroups {
		allowed := false
		for _, id := range filter.AllowedGroupIDs {
			if id == groupID {
				allowed = true
				break
			}
		}
		if !allowed {
			response.Error(c, http.StatusNotFound, "preview unavailable")
			return 0, "", false
		}
	}
	return groupID, resultID, true
}

func (h *ChannelMonitorV2Handler) QualityPreview(c *gin.Context) {
	groupID, resultID, ok := h.qualityAssetScope(c, true)
	if !ok {
		return
	}
	data, err := h.service.QualityPreview(c.Request.Context(), groupID, resultID)
	if errors.Is(err, sql.ErrNoRows) {
		response.Error(c, http.StatusNotFound, "preview unavailable")
		return
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "image/png", data)
}

func (h *ChannelMonitorV2Handler) QualityArtwork(c *gin.Context) {
	groupID, resultID, ok := h.qualityAssetScope(c, true)
	if !ok {
		return
	}
	document, err := h.service.QualityArtwork(c.Request.Context(), groupID, resultID)
	if errors.Is(err, sql.ErrNoRows) {
		response.Error(c, http.StatusNotFound, "artwork unavailable")
		return
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "sandbox; default-src 'none'; frame-ancestors 'none'")
	// HTML is transported as data and rendered only by the isolated client preview.
	response.Success(c, gin.H{"html": document})
}

func (h *ChannelMonitorV2Handler) QualityHistory(c *gin.Context) {
	groupID, _, ok := h.qualityAssetScope(c, false)
	if !ok {
		return
	}
	items, err := h.service.QualityHistory(c.Request.Context(), groupID)
	if errors.Is(err, sql.ErrNoRows) {
		response.Error(c, http.StatusNotFound, "history unavailable")
		return
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	response.Success(c, gin.H{"items": items})
}
