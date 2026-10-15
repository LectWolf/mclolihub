package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestQualityPreviewHandlerRejectsUnauthorizedGroupBeforeStorage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, group := range []string{"7", "0", "invalid"} {
		t.Run(group, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/quality-preview/id?group_id="+group, nil)
			c.Params = gin.Params{{Key: "resultID", Value: "12345678-1234-4234-8234-123456789abc"}}
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
			h := &ChannelMonitorV2Handler{apiKeyService: &channelMonitorV2GroupAuthorizerStub{groups: []service.Group{{ID: 3}}}}
			h.QualityPreview(c)
			if group == "7" {
				require.Equal(t, http.StatusNotFound, recorder.Code)
			} else {
				require.Equal(t, http.StatusBadRequest, recorder.Code)
			}
		})
	}
}

func TestQualityPreviewHandlerRejectsMalformedResultID(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/quality-preview/id?group_id=7", nil)
	c.Params = gin.Params{{Key: "resultID", Value: "invalid"}}
	h := &ChannelMonitorV2Handler{}
	h.QualityPreview(c)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestQualityRawOutputHandlerRejectsUnauthorizedAndInvalidScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, group := range []string{"7", "0", "invalid"} {
		t.Run(group, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/quality-raw-output/id?group_id="+group, nil)
			c.Params = gin.Params{{Key: "resultID", Value: "12345678-1234-4234-8234-123456789abc"}}
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
			h := &ChannelMonitorV2Handler{apiKeyService: &channelMonitorV2GroupAuthorizerStub{groups: []service.Group{{ID: 3}}}}
			h.QualityRawOutput(c)
			if group == "7" {
				require.Equal(t, http.StatusNotFound, recorder.Code)
			} else {
				require.Equal(t, http.StatusBadRequest, recorder.Code)
			}
		})
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/quality-raw-output/id?group_id=7", nil)
	c.Params = gin.Params{{Key: "resultID", Value: "invalid"}}
	(&ChannelMonitorV2Handler{}).QualityRawOutput(c)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}
