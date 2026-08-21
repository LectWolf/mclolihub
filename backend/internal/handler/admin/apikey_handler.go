package admin

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AdminAPIKeyHandler handles admin API key management
type AdminAPIKeyHandler struct {
	adminService service.AdminService
}

// NewAdminAPIKeyHandler creates a new admin API key handler
func NewAdminAPIKeyHandler(adminService service.AdminService) *AdminAPIKeyHandler {
	return &AdminAPIKeyHandler{
		adminService: adminService,
	}
}

// AdminUpdateAPIKeyGroupRequest represents the request to update an API key.
type AdminUpdateAPIKeyGroupRequest struct {
	GroupID             *int64 `json:"group_id"`               // nil=不修改, 0=解绑, >0=绑定到目标分组
	ResetRateLimitUsage *bool  `json:"reset_rate_limit_usage"` // true=重置 5h/1d/7d 限速用量
	RouteMode           *string `json:"route_mode"`
	RoutePlatform       *string `json:"route_platform"`
	MaxRateMultiplier   *float64 `json:"max_rate_multiplier"`
	DisabledGroupIDs    *[]int64 `json:"disabled_group_ids"`
	CustomGroupIDs      *[]int64 `json:"custom_group_ids"`
}

// UpdateGroup handles updating an API key's admin-managed fields.
// PUT /api/v1/admin/api-keys/:id
func (h *AdminAPIKeyHandler) UpdateGroup(c *gin.Context) {
	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid API key ID")
		return
	}

	var req AdminUpdateAPIKeyGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	var resetKey *service.APIKey
	if req.ResetRateLimitUsage != nil && *req.ResetRateLimitUsage {
		resetKey, err = h.adminService.AdminResetAPIKeyRateLimitUsage(c.Request.Context(), keyID)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}

	result, err := h.adminService.AdminUpdateAPIKeyGroupID(c.Request.Context(), keyID, req.GroupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if resetKey != nil && req.GroupID == nil {
		result.APIKey = resetKey
	}
	if req.RouteMode != nil || req.RoutePlatform != nil || req.MaxRateMultiplier != nil || req.DisabledGroupIDs != nil || req.CustomGroupIDs != nil {
		updater, ok := h.adminService.(interface {
			AdminUpdateAPIKeyRouting(context.Context, int64, service.AdminUpdateAPIKeyRoutingInput) (*service.APIKey, error)
		})
		if !ok { response.Error(c, http.StatusNotImplemented, "admin API key routing update is unavailable"); return }
		updated, updateErr := updater.AdminUpdateAPIKeyRouting(c.Request.Context(), keyID, service.AdminUpdateAPIKeyRoutingInput{
			RouteMode: req.RouteMode, RoutePlatform: req.RoutePlatform, MaxRateMultiplier: req.MaxRateMultiplier,
			DisabledGroupIDs: req.DisabledGroupIDs, CustomGroupIDs: req.CustomGroupIDs,
		})
		if updateErr != nil { response.ErrorFrom(c, updateErr); return }
		result.APIKey = updated
	}

	resp := struct {
		APIKey                 *dto.APIKey `json:"api_key"`
		AutoGrantedGroupAccess bool        `json:"auto_granted_group_access"`
		GrantedGroupID         *int64      `json:"granted_group_id,omitempty"`
		GrantedGroupName       string      `json:"granted_group_name,omitempty"`
	}{
		APIKey:                 dto.APIKeyFromService(result.APIKey),
		AutoGrantedGroupAccess: result.AutoGrantedGroupAccess,
		GrantedGroupID:         result.GrantedGroupID,
		GrantedGroupName:       result.GrantedGroupName,
	}
	response.Success(c, resp)
}
