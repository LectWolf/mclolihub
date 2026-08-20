package handler

import (
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type GroupHealthHandler struct {
	health        *service.GroupHealthService
	apiKeyService *service.APIKeyService
	groupService  *service.GroupService
}

func NewGroupHealthHandler(health *service.GroupHealthService, apiKeyService *service.APIKeyService, groupService *service.GroupService) *GroupHealthHandler {
	return &GroupHealthHandler{health: health, apiKeyService: apiKeyService, groupService: groupService}
}

type groupHealthResponse struct {
	GroupID              int64                            `json:"group_id"`
	Name                 string                           `json:"name"`
	Platform             string                           `json:"platform"`
	RateMultiplier       float64                          `json:"rate_multiplier"`
	ProbeEnabled         bool                             `json:"probe_enabled"`
	ProbeModel           string                           `json:"probe_model"`
	ProbeIntervalSeconds int                              `json:"probe_interval_seconds"`
	Status               string                           `json:"status"`
	Reason               string                           `json:"reason"`
	LastProbeAt          *time.Time                       `json:"last_probe_at"`
	LastSuccessAt        *time.Time                       `json:"last_success_at"`
	NextProbeAt          *time.Time                       `json:"next_probe_at"`
	ProbeTTFTMS          int                              `json:"probe_ttft_ms"`
	ProbeTTFTAvgMS       int                              `json:"probe_ttft_avg_ms"`
	ProbeTTFTP95MS       int                              `json:"probe_ttft_p95_ms"`
	ProbeSamples         int                              `json:"probe_samples"`
	ProbeAvailability6h  float64                          `json:"probe_availability_6h"`
	RealTTFTP50MS        int                              `json:"real_ttft_p50_ms"`
	RealTTFTAvgMS        int                              `json:"real_ttft_avg_ms"`
	RealTTFTP95MS        int                              `json:"real_ttft_p95_ms"`
	RealTTFTSamples      int                              `json:"real_ttft_samples"`
	RealTotalAvgMS       int                              `json:"real_total_avg_ms"`
	RealAvailability6h   float64                          `json:"real_availability_6h"`
	Trend                []service.GroupHealthTrendBucket `json:"trend"`
}

func (h *GroupHealthHandler) UserList(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	groups, err := h.apiKeyService.GetAvailableGroups(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	rates, _ := h.apiKeyService.GetUserGroupRates(c.Request.Context(), subject.UserID)
	h.writeGroups(c, groups, rates, true)
}

func (h *GroupHealthHandler) AdminList(c *gin.Context) {
	groups, err := h.groupService.ListActive(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.writeGroups(c, groups, nil, true)
}

func (h *GroupHealthHandler) AdminGet(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid group id")
		return
	}
	group, err := h.groupService.GetByID(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.writeGroups(c, []service.Group{*group}, nil, true)
}

func (h *GroupHealthHandler) AdminRefresh(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 { response.BadRequest(c, "invalid group id"); return }
	if err := h.health.ProbeNow(c.Request.Context(), id); err != nil { response.ErrorFrom(c, err); return }
	group, err := h.groupService.GetByID(c.Request.Context(), id); if err != nil { response.ErrorFrom(c, err); return }
	h.writeGroups(c, []service.Group{*group}, nil, true)
}

func (h *GroupHealthHandler) RestoreBalance(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid account id")
		return
	}
	if err := h.health.RestoreBalance(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"account_id": id, "status": service.StatusActive})
}

func (h *GroupHealthHandler) writeGroups(c *gin.Context, groups []service.Group, rates map[int64]float64, includeTrend bool) {
	ids := make([]int64, 0, len(groups))
	for i := range groups {
		ids = append(ids, groups[i].ID)
	}
	metrics, err := h.health.LoadMetrics(c.Request.Context(), ids)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	trends := make(map[int64][]service.GroupHealthTrendBucket)
	if includeTrend {
		end := time.Now()
		trends, err = h.health.LoadTrend(c.Request.Context(), ids, end.Add(-6*time.Hour), end)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}
	items := make([]groupHealthResponse, 0, len(groups))
	for i := range groups {
		group := groups[i]
		snapshot := metrics[group.ID]
		status := snapshot.Status
		reason := snapshot.Reason
		if !group.ProbeEnabled {
			status = "not_enabled"
			reason = "probe_not_enabled"
		} else if status == "" || status == service.GroupHealthUnknown {
			status = service.GroupHealthUnavailable
			reason = "no_successful_probe"
		} else if status == service.GroupHealthHealthy && (snapshot.LastSuccessAt == nil || snapshot.LastSuccessAt.Before(time.Now().Add(-service.ImmediateProbeCooldown))) {
			status = service.GroupHealthUnavailable
			reason = "probe_stale"
		}
		rate := group.RateMultiplier
		if custom, ok := rates[group.ID]; ok {
			rate = custom
		}
		items = append(items, groupHealthResponse{
			GroupID: group.ID, Name: group.Name, Platform: group.Platform, RateMultiplier: rate,
			ProbeEnabled: group.ProbeEnabled, ProbeModel: group.ProbeModel, ProbeIntervalSeconds: group.ProbeIntervalSeconds,
			Status: status, Reason: reason, LastProbeAt: snapshot.LastProbeAt, LastSuccessAt: snapshot.LastSuccessAt, NextProbeAt: snapshot.NextProbeAt,
			ProbeTTFTMS: snapshot.ProbeTTFTMS, ProbeTTFTAvgMS: snapshot.ProbeTTFTAvgMS, ProbeTTFTP95MS: snapshot.ProbeTTFTP95MS,
			ProbeSamples: snapshot.ProbeSamples, ProbeAvailability6h: snapshot.ProbeAvailability6h,
			RealTTFTP50MS: snapshot.RealTTFTP50MS, RealTTFTAvgMS: snapshot.RealTTFTAvgMS, RealTTFTP95MS: snapshot.RealTTFTP95MS,
			RealTTFTSamples: snapshot.RealTTFTSamples, RealTotalAvgMS: snapshot.RealTotalAvgMS, RealAvailability6h: snapshot.RealAvailability6h,
			Trend: trends[group.ID],
		})
	}
	response.Success(c, gin.H{"items": items, "window_hours": 6, "bucket_minutes": 10})
}
