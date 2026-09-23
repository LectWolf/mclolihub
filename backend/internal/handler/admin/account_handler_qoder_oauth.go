package admin

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/qoderproxy"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

const qoderOAuthHTTPTimeout = 30 * time.Second

var errQoderOAuthProxyNotFound = errors.New("QODER_OAUTH_PROXY_NOT_FOUND: proxy not found")

type startQoderOAuthRequest struct {
	Region    string `json:"region"`
	MachineID string `json:"machine_id"`
}

// StartQoderOAuth builds a Qoder device-login URL (PKCE). It makes no
// upstream call; the browser opens the URL and PollQoderOAuth collects the
// token.
func (h *AccountHandler) StartQoderOAuth(c *gin.Context) {
	var req startQoderOAuthRequest
	_ = c.ShouldBindJSON(&req)
	sess, err := qoderproxy.StartLogin(req.Region, req.MachineID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"login_url":  sess.LoginURL,
		"nonce":      sess.Nonce,
		"verifier":   sess.Verifier,
		"machine_id": sess.MachineID,
		"region":     string(sess.Region),
	})
}

type pollQoderOAuthRequest struct {
	Region   string `json:"region"`
	Nonce    string `json:"nonce" binding:"required"`
	Verifier string `json:"verifier" binding:"required"`
	// ProxyID routes the poll through the proxy chosen for the new account.
	ProxyID *int64 `json:"proxy_id"`
}

// PollQoderOAuth polls a device login once. "pending" means the browser login
// has not finished; a 400 means Qoder refused the login session and the
// client should stop polling.
func (h *AccountHandler) PollQoderOAuth(c *gin.Context) {
	var req pollQoderOAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	ctx := c.Request.Context()
	doer, err := h.qoderOAuthDoer(ctx, req.ProxyID)
	if err != nil {
		if errors.Is(err, errQoderOAuthProxyNotFound) {
			response.BadRequest(c, err.Error())
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	region := qoderproxy.NormalizeRegion(req.Region)
	tok, err := qoderproxy.PollLogin(ctx, doer, string(region), req.Nonce, req.Verifier)
	if errors.Is(err, qoderproxy.ErrLoginPending) {
		response.Success(c, gin.H{"status": "pending"})
		return
	}
	if err != nil {
		if qoderproxy.IsRejected(err) {
			response.BadRequest(c, "Qoder login was rejected, start the login again: "+err.Error())
			return
		}
		response.ErrorFrom(c, err)
		return
	}

	// Best effort: the profile is shown in the form and signs COSY requests;
	// the gateway fetches it again later if this call fails.
	userID := tok.UserID
	var name, email string
	if profile, profileErr := qoderproxy.FetchProfile(ctx, doer, region, tok.AccessToken); profileErr == nil {
		if userID == "" {
			userID = profile.UserID
		}
		name, email = profile.Name, profile.Email
	}
	expiresAt := ""
	if !tok.ExpiresAt.IsZero() {
		expiresAt = tok.ExpiresAt.UTC().Format(time.RFC3339)
	}
	response.Success(c, gin.H{
		"status":        "ok",
		"access_token":  tok.AccessToken,
		"refresh_token": tok.RefreshToken,
		"user_id":       userID,
		"name":          name,
		"email":         email,
		"expires_at":    expiresAt,
		"region":        string(region),
	})
}

func (h *AccountHandler) qoderOAuthDoer(ctx context.Context, proxyID *int64) (qoderproxy.Doer, error) {
	opts := httpclient.Options{Timeout: qoderOAuthHTTPTimeout}
	if proxyID != nil && *proxyID > 0 {
		proxy, err := h.adminService.GetProxy(ctx, *proxyID)
		if err != nil {
			return nil, err
		}
		if proxy == nil {
			return nil, errQoderOAuthProxyNotFound
		}
		opts.ProxyURL = proxy.URL()
	}
	return httpclient.GetClient(opts)
}
