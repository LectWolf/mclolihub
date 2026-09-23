package admin

import (
	"strconv"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// GetQoderQuota returns the Qoder credits of one account. refresh=false
// serves the snapshot stored on the account while it is fresh; the default
// queries Qoder and stores the answer for the account list.
func (h *AccountHandler) GetQoderQuota(c *gin.Context) {
	if h.qoderGateway == nil {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("QODER_UNAVAILABLE", "Qoder service is not configured"))
		return
	}
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if account == nil || !account.IsQoder() {
		response.BadRequest(c, "not a qoder account")
		return
	}
	refresh, parseErr := strconv.ParseBool(c.DefaultQuery("refresh", "true"))
	if parseErr != nil {
		refresh = true
	}
	snapshot, err := h.qoderGateway.QoderQuota(c.Request.Context(), account, refresh)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, snapshot)
}
