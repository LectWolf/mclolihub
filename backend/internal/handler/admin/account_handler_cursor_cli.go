package admin

import (
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursorproxy"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) StartCursorCLILogin(c *gin.Context) {
	sess, err := cursorproxy.NewCLILoginSession()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"login_url": sess.LoginURL,
		"uuid":      sess.UUID,
		"verifier":  sess.Verifier,
	})
}

type pollCursorCLILoginRequest struct {
	UUID     string `json:"uuid" binding:"required"`
	Verifier string `json:"verifier" binding:"required"`
}

func (h *AccountHandler) PollCursorCLILogin(c *gin.Context) {
	var req pollCursorCLILoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	tok, err := cursorproxy.PollCLILogin(c.Request.Context(), nil, req.UUID, req.Verifier)
	if errors.Is(err, cursorproxy.ErrCLILoginPending) {
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
		"email":         tok.Email,
		"auth_id":       tok.AuthID,
	})
}
