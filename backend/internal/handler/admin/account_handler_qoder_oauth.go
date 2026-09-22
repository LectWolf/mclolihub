package admin

import (
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/pkg/qoderproxy"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type startQoderOAuthRequest struct {
	Region    string `json:"region"`
	MachineID string `json:"machine_id"`
}

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
}

func (h *AccountHandler) PollQoderOAuth(c *gin.Context) {
	var req pollQoderOAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	tok, err := qoderproxy.PollLogin(c.Request.Context(), nil, req.Region, req.Nonce, req.Verifier)
	if errors.Is(err, qoderproxy.ErrLoginPending) {
		response.Success(c, gin.H{"status": "pending"})
		return
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"status":        "ok",
		"access_token":  tok.AccessToken,
		"refresh_token": tok.RefreshToken,
		"user_id":       tok.UserID,
		"expires_at":    tok.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
		"region":        string(qoderproxy.NormalizeRegion(req.Region)),
	})
}
