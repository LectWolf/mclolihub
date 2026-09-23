package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// qoderQuotaHandlerUpstream answers the PAT exchange and the quota query.
type qoderQuotaHandlerUpstream struct{}

func (qoderQuotaHandlerUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body := ""
	switch {
	case strings.HasSuffix(req.URL.Path, "/jobToken/exchange"):
		body = `{"token":"jt-1","expires_in":3600}`
	case strings.HasSuffix(req.URL.Path, "/userinfo"):
		body = `{"id":"user-1"}`
	case strings.HasSuffix(req.URL.Path, "/quota/usage"):
		body = `{"userType":"pro","userQuota":{"total":100,"used":40,"unit":"credits"}}`
	default:
		return nil, fmt.Errorf("unexpected upstream call %s", req.URL)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func (u qoderQuotaHandlerUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func serveQoderQuota(t *testing.T, handler *AccountHandler, target string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/admin/accounts/:id/qoder-quota", handler.GetQoderQuota)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return recorder
}

func TestGetQoderQuotaRequiresConfiguredGateway(t *testing.T) {
	handler := NewAccountHandler(newStubAdminService(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	recorder := serveQoderQuota(t, handler, "/api/v1/admin/accounts/5/qoder-quota")
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

func TestGetQoderQuotaRejectsOtherPlatforms(t *testing.T) {
	adminSvc := newStubAdminService()
	adminSvc.getAccountResult = &service.Account{ID: 5, Platform: service.PlatformOpenAI}
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.SetQoderGatewayService(service.NewQoderGatewayService(nil, qoderQuotaHandlerUpstream{}, nil, nil, nil))

	recorder := serveQoderQuota(t, handler, "/api/v1/admin/accounts/5/qoder-quota")
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestGetQoderQuotaReturnsSnapshot(t *testing.T) {
	adminSvc := newStubAdminService()
	adminSvc.getAccountResult = &service.Account{
		ID:          5,
		Platform:    service.PlatformQoder,
		Type:        service.AccountTypeAPIKey,
		Credentials: map[string]any{"personal_token": "pt-1", "qoder_region": "global"},
	}
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.SetQoderGatewayService(service.NewQoderGatewayService(nil, qoderQuotaHandlerUpstream{}, nil, nil, nil))

	recorder := serveQoderQuota(t, handler, "/api/v1/admin/accounts/5/qoder-quota?refresh=true")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var payload struct {
		Code int                        `json:"code"`
		Data service.QoderQuotaSnapshot `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, "pro", payload.Data.PlanType)
	require.Equal(t, service.QoderQuotaCredentialPAT, payload.Data.Credential)
	require.Len(t, payload.Data.Pools, 1)
	require.Equal(t, 60.0, payload.Data.Pools[0].Remaining)
}
