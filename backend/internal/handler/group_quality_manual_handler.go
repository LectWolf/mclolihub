package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// These methods are registered only under the administrator middleware.
func (h *GroupHealthHandler) RunQuality(c *gin.Context) {
	checker, id, ok := h.manualQualityTarget(c)
	if !ok {
		return
	}
	run, err := checker.StartManualRun(c.Request.Context(), id)
	if err != nil {
		writeQualityRunError(c, err)
		return
	}
	response.Accepted(c, run)
}

func (h *GroupHealthHandler) QualityRun(c *gin.Context) {
	checker, id, ok := h.manualQualityTarget(c)
	if !ok {
		return
	}
	run, err := checker.ManualRun(c.Request.Context(), id)
	if err != nil {
		writeQualityRunError(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	response.Success(c, run)
}

func (h *GroupHealthHandler) manualQualityTarget(c *gin.Context) (*service.GroupQualityChecker, int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid group id")
		return nil, 0, false
	}
	checker := h.quality()
	if checker == nil || h.groupService == nil {
		response.Error(c, http.StatusServiceUnavailable, service.ErrQualityRunUnavailable.Error())
		return nil, 0, false
	}
	if _, err := h.groupService.GetByID(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return nil, 0, false
	}
	return checker, id, true
}

func writeQualityRunError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrQualityRunBusy):
		response.Error(c, http.StatusConflict, service.ErrQualityRunBusy.Error())
	case errors.Is(err, service.ErrQualityRunDisabled):
		response.BadRequest(c, service.ErrQualityRunDisabled.Error())
	case errors.Is(err, service.ErrQualityRunNoAccounts):
		response.BadRequest(c, service.ErrQualityRunNoAccounts.Error())
	case errors.Is(err, service.ErrQualityRunModelNotAllowed):
		response.BadRequest(c, service.ErrQualityRunModelNotAllowed.Error())
	case errors.Is(err, service.ErrQualityRunUnavailable):
		response.Error(c, http.StatusServiceUnavailable, service.ErrQualityRunUnavailable.Error())
	default:
		response.InternalError(c, "quality check execution failed")
	}
}
