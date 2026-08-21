package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKeyPrefix(t *testing.T) {
	if got := keyPrefix("sk-3f2a9c7e", 8); got != "sk-3f2a9" {
		t.Errorf("keyPrefix=%q want %q", got, "sk-3f2a9")
	}
	if got := keyPrefix("abc", 8); got != "abc" {
		t.Errorf("short key should be returned as-is, got %q", got)
	}
}

func TestDynamicGroupRequestContextDrivesOpsAttribution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)

	authGroup := &service.Group{ID: 10, Platform: service.PlatformOpenAI}
	selectedGroup := &service.Group{ID: 20, Platform: service.PlatformAnthropic}
	authKey := &service.APIKey{ID: 1, GroupID: &authGroup.ID, Group: authGroup}
	c.Set(string(middleware.ContextKeyAPIKey), authKey)

	applyDynamicGroupRequestContext(c, &service.APIKey{ID: 1, GroupID: &selectedGroup.ID, Group: selectedGroup}, "gpt-5.6-sol")

	opsKey := getOpsAPIKey(c)
	require.NotNil(t, opsKey)
	require.Equal(t, int64(20), *opsKey.GroupID)
	require.Same(t, selectedGroup, opsKey.Group)
}
