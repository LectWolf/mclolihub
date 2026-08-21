package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type openAIDynamicRoutingGroupRepo struct {
	service.GroupRepository
	groups []service.Group
}

func (r *openAIDynamicRoutingGroupRepo) ListActive(context.Context) ([]service.Group, error) {
	return append([]service.Group(nil), r.groups...), nil
}

func TestOpenAIDynamicRoutingSelectsCheapestGroupAndUpdatesRequestAttribution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &openAIDynamicRoutingGroupRepo{groups: []service.Group{
		{ID: 20, Name: "A2-Pro", Platform: service.PlatformOpenAI, RateMultiplier: 0.185, Status: service.StatusActive},
		{ID: 30, Name: "A3-0.11x", Platform: service.PlatformOpenAI, RateMultiplier: 0.11, Status: service.StatusActive},
	}}
	apiKeyService := service.NewAPIKeyService(nil, nil, repo, nil, nil, nil, nil)
	h := &OpenAIGatewayHandler{apiKeyService: apiKeyService}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	boundGroup := repo.groups[0]
	boundGroupID := boundGroup.ID
	key := &service.APIKey{
		ID:            7,
		UserID:        42,
		GroupID:       &boundGroupID,
		Group:         &boundGroup,
		RouteMode:     service.RouteModeCheapest,
		RoutePlatform: service.RoutePlatformOpenAI,
		User:          &service.User{ID: 42, Status: service.StatusActive},
	}
	c.Set(string(middleware2.ContextKeyAPIKey), key)

	selected, state, err := h.resolveDynamicRouting(c, key, "gpt-5.6-sol")
	require.NoError(t, err)
	require.NotNil(t, state)
	require.Equal(t, int64(30), *selected.GroupID)
	require.Equal(t, "A3-0.11x", selected.Group.Name)
	fromContext, ok := middleware2.GetAPIKeyFromContext(c)
	require.True(t, ok)
	require.Equal(t, int64(30), *fromContext.GroupID)

	// The authenticated API key is immutable; only the request-scoped clone is
	// changed, so a later request will resolve against fresh active groups again.
	require.Equal(t, int64(20), *key.GroupID)

	selected, ok = state.next(c, selected, "gpt-5.6-sol")
	require.True(t, ok)
	require.Equal(t, int64(20), *selected.GroupID)
	fromContext, ok = middleware2.GetAPIKeyFromContext(c)
	require.True(t, ok)
	require.Equal(t, int64(20), *fromContext.GroupID)
}
