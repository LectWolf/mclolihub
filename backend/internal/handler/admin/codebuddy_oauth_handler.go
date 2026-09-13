package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
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

func (h *CodeBuddyOAuthHandler) CreateAccountFromOAuth(c *gin.Context) {
	var req struct {
		LoginID     string         `json:"login_id"`
		Credentials map[string]any `json:"credentials"`
		ProxyID     *int64         `json:"proxy_id"`
		Name        string         `json:"name"`
		Concurrency int            `json:"concurrency"`
		Priority    int            `json:"priority"`
		GroupIDs    []int64        `json:"group_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	var credentials map[string]any
	switch {
	case strings.TrimSpace(req.LoginID) != "":
		creds, err := h.oauthService.Take(req.LoginID)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		credentials = h.oauthService.BuildAccountCredentials(creds)
	case req.Credentials != nil:
		creds, err := h.oauthService.ImportCredentials(req.Credentials)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		credentials = h.oauthService.BuildAccountCredentials(creds)
	default:
		response.BadRequest(c, "login_id or credentials is required")
		return
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
	response.Success(c, dto.AccountFromService(account))
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
