package handler

import (
	"database/sql"
	"errors"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Registered exclusively under the admin-authenticated groups router.
func (h *GroupHealthHandler) QualityDiagnostics(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid group id")
		return
	}
	resultID := c.Param("resultID")
	if _, err := uuid.Parse(resultID); err != nil {
		response.BadRequest(c, "invalid result id")
		return
	}
	checker := h.quality()
	if checker == nil {
		response.Error(c, 503, "quality check unavailable")
		return
	}
	result, err := checker.AdminDiagnostics(c.Request.Context(), id, resultID)
	if errors.Is(err, sql.ErrNoRows) {
		response.NotFound(c, "quality result not found")
		return
	}
	if err != nil {
		response.InternalError(c, "failed to load quality diagnostics")
		return
	}
	response.Success(c, result)
}
