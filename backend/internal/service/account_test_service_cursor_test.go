//go:build unit

package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountTestService_CursorDoesNotRequireGenericAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name     string
		account  *Account
		gateway  *CursorGatewayService
		contains string
		absent   string
	}{
		{
			name: "sand missing credentials",
			account: &Account{
				ID: 41, Platform: PlatformCursorSand, Type: AccountTypeAPIKey,
				Credentials: map[string]any{},
			},
			gateway:  NewCursorGatewayService(),
			contains: "SAND_INFERENCE_RENEWAL_CREDENTIAL",
			absent:   "No API key available",
		},
		{
			name: "ide missing session",
			account: &Account{
				ID: 42, Platform: PlatformCursor, Type: AccountTypeAPIKey,
				Credentials: map[string]any{},
			},
			gateway:  NewCursorGatewayService(),
			contains: "Cursor CLI session token",
			absent:   "No API key available",
		},
		{
			name: "sand token reaches cursor gateway",
			account: &Account{
				ID: 43, Platform: PlatformCursorSand, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"grok_bot_token": "gb-test"},
			},
			contains: "Cursor proxy service is not configured",
			absent:   "No API key available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &AccountTestService{
				accountRepo: &mockAccountRepoForGemini{
					accountsByID: map[int64]*Account{tt.account.ID: tt.account},
				},
				cursorGatewayService: tt.gateway,
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/admin/accounts/test", nil)

			err := svc.TestAccountConnection(c, tt.account.ID, "grok-4.6", "ok", "")
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.contains)
			require.NotContains(t, err.Error(), tt.absent)
		})
	}
}
