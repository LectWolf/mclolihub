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

func (h *ChannelMonitorV2Handler) QualityPreview(c *gin.Context) {
	groupID, err := strconv.ParseInt(c.Query("group_id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "invalid group id")
		return
	}
	resultID := c.Param("resultID")
	if _, err := uuid.Parse(resultID); err != nil {
		response.BadRequest(c, "invalid result id")
		return
	}
	filter := service.ChannelMonitorV2Filter{}
	if !h.scopeFilter(c, &filter, channelMonitorV2IsAdmin(c)) {
		return
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
			return
		}
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
