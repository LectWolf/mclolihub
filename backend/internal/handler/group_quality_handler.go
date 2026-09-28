package handler

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *GroupHealthHandler) ListQuality(c *gin.Context) {
	checker := h.quality()
	if checker == nil {
		response.Success(c, []service.GroupQualityStatus{})
		return
	}
	items, err := checker.List(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

func (h *GroupHealthHandler) SetQuality(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid group id")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, "invalid request")
		return
	}
	if _, err := h.groupService.GetByID(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	checker := h.quality()
	if checker == nil {
		response.InternalError(c, "quality check unavailable")
		return
	}
	status, err := checker.SetEnabled(c.Request.Context(), id, body.Enabled)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

func (h *GroupHealthHandler) quality() *service.GroupQualityChecker {
	if h == nil || h.health == nil {
		return nil
	}
	return h.health.Quality()
}
