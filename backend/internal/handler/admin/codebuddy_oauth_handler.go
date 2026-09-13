package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type CodeBuddyOAuthHandler struct {
	oauthService *service.CodeBuddyOAuthService
	adminService service.AdminService
}

func NewCodeBuddyOAuthHandler(oauthService *service.CodeBuddyOAuthService, adminService service.AdminService) *CodeBuddyOAuthHandler {
	return &CodeBuddyOAuthHandler{oauthService: oauthService, adminService: adminService}
}

func (h *CodeBuddyOAuthHandler) Start(c *gin.Context) {
	var req struct {
		Site    string `json:"site"`
		ProxyID *int64 `json:"proxy_id"`
	}
	_ = c.ShouldBindJSON(&req)
	if strings.TrimSpace(req.Site) == "" {
		req.Site = "cn"
	}
	result, err := h.oauthService.Start(c.Request.Context(), req.Site, req.ProxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *CodeBuddyOAuthHandler) Poll(c *gin.Context) {
	loginID := strings.TrimSpace(c.Query("login_id"))
	if loginID == "" {
		var req struct {
			LoginID string `json:"login_id"`
		}
		_ = c.ShouldBindJSON(&req)
		loginID = strings.TrimSpace(req.LoginID)
	}
	if loginID == "" {
		response.BadRequest(c, "login_id is required")
		return
	}
	result, err := h.oauthService.Poll(c.Request.Context(), loginID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// resolveCredentials turns either a completed QR login or a pasted official
// .info payload into stored credentials. It writes the error response itself and
// reports false when the caller should stop.
func (h *CodeBuddyOAuthHandler) resolveCredentials(c *gin.Context, loginID string, raw map[string]any) (map[string]any, bool) {
	switch {
	case strings.TrimSpace(loginID) != "":
		creds, err := h.oauthService.Take(loginID)
		if err != nil {
			response.ErrorFrom(c, err)
			return nil, false
		}
		return h.oauthService.BuildAccountCredentials(creds), true
	case raw != nil:
		creds, err := h.oauthService.ImportCredentials(raw)
		if err != nil {
			response.ErrorFrom(c, err)
			return nil, false
		}
		return h.oauthService.BuildAccountCredentials(creds), true
	default:
		response.BadRequest(c, "login_id or credentials is required")
		return nil, false
	}
}

func (h *CodeBuddyOAuthHandler) CreateAccountFromOAuth(c *gin.Context) {
	var req struct {
		LoginID      string         `json:"login_id"`
		Credentials  map[string]any `json:"credentials"`
		CreditPolicy string         `json:"credit_policy"`
		ModelMapping map[string]any `json:"model_mapping"`
		ProxyID      *int64         `json:"proxy_id"`
		Name         string         `json:"name"`
		Concurrency  int            `json:"concurrency"`
		Priority     int            `json:"priority"`
		GroupIDs     []int64        `json:"group_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	credentials, ok := h.resolveCredentials(c, req.LoginID, req.Credentials)
	if !ok {
		return
	}
	if policy := strings.TrimSpace(req.CreditPolicy); policy != "" {
		credentials["credit_policy"] = codebuddy.NormalizeCreditPolicy(policy)
	}
	if len(req.ModelMapping) > 0 {
		credentials["model_mapping"] = req.ModelMapping
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		if nickname, _ := credentials["nickname"].(string); strings.TrimSpace(nickname) != "" {
			name = strings.TrimSpace(nickname)
		} else if uid, _ := credentials["uid"].(string); uid != "" {
			name = "CodeBuddy " + uid
		} else {
			name = "CodeBuddy OAuth Account"
		}
	}

	account, err := h.adminService.CreateAccount(c.Request.Context(), &service.CreateAccountInput{
		Name:        name,
		Platform:    service.PlatformCodeBuddy,
		Type:        service.AccountTypeOAuth,
		Credentials: credentials,
		ProxyID:     req.ProxyID,
		Concurrency: req.Concurrency,
		Priority:    req.Priority,
		GroupIDs:    req.GroupIDs,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if models, catalogErr := h.oauthService.FetchCatalog(c.Request.Context(), account); catalogErr == nil {
		_ = h.oauthService.PersistCatalog(c.Request.Context(), account, models)
	}
	_, _ = h.oauthService.QueryCredits(c.Request.Context(), account)
	response.Success(c, dto.AccountFromService(account))
}

func (h *CodeBuddyOAuthHandler) QueryCredits(c *gin.Context) {
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
	if account == nil || !account.IsCodeBuddy() {
		response.BadRequest(c, "not a codebuddy account")
		return
	}
	refresh, _ := strconv.ParseBool(c.DefaultQuery("refresh", "true"))
	report, err := h.oauthService.CreditsReport(c.Request.Context(), account, refresh)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, report)
}

// ReAuthAccount swaps in credentials from a fresh QR login so an expired account
// keeps its groups, priority and usage history instead of being recreated.
func (h *CodeBuddyOAuthHandler) ReAuthAccount(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	var req struct {
		LoginID     string         `json:"login_id"`
		Credentials map[string]any `json:"credentials"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if account == nil || !account.IsCodeBuddy() {
		response.BadRequest(c, "not a codebuddy account")
		return
	}

	credentials, ok := h.resolveCredentials(c, req.LoginID, req.Credentials)
	if !ok {
		return
	}
	// Re-authorizing must not silently repoint the account at a different
	// CodeBuddy identity: the usage history and credit tally would stop meaning
	// anything. Creating a new account is the correct action for a new identity.
	previousUID, _ := account.Credentials["uid"].(string)
	newUID, _ := credentials["uid"].(string)
	if strings.TrimSpace(previousUID) != "" && strings.TrimSpace(newUID) != strings.TrimSpace(previousUID) {
		response.BadRequest(c, "the authorized CodeBuddy account does not match this account's uid")
		return
	}
	// Admin-configured fields live in credentials next to the tokens, so carry
	// them across rather than resetting them on every re-authorization.
	for _, key := range []string{"credit_policy", "model_mapping"} {
		if value, exists := account.Credentials[key]; exists {
			credentials[key] = value
		}
	}

	updated, err := h.adminService.UpdateAccount(c.Request.Context(), accountID, &service.UpdateAccountInput{
		Credentials: credentials,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if models, catalogErr := h.oauthService.FetchCatalog(c.Request.Context(), updated); catalogErr == nil {
		_ = h.oauthService.PersistCatalog(c.Request.Context(), updated, models)
	}
	response.Success(c, dto.AccountFromService(updated))
}

// QueryRequestUsage returns CodeBuddy's own billed consumption for the account,
// broken down by day and model.
func (h *CodeBuddyOAuthHandler) QueryRequestUsage(c *gin.Context) {
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
	if account == nil || !account.IsCodeBuddy() {
		response.BadRequest(c, "not a codebuddy account")
		return
	}
	// Out-of-range values fall back to the upstream maximum rather than erroring,
	// since the upstream itself silently empties a too-wide window.
	days, _ := strconv.Atoi(c.DefaultQuery("days", strconv.Itoa(codebuddy.RequestUsageMaxDays)))
	usage, err := h.oauthService.QueryRequestUsage(c.Request.Context(), account, days)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, usage)
}

func (h *CodeBuddyOAuthHandler) RefreshAccountToken(c *gin.Context) {
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
	if account == nil || !account.IsCodeBuddyOAuth() {
		response.BadRequest(c, "not a codebuddy oauth account")
		return
	}
	result, err := h.oauthService.RefreshAccountToken(c.Request.Context(), account)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	newCredentials := h.oauthService.ApplyRefresh(account, result)
	updated, err := h.adminService.UpdateAccount(c.Request.Context(), accountID, &service.UpdateAccountInput{
		Credentials: newCredentials,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.AccountFromService(updated))
}
