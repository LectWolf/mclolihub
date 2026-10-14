package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestQualityManualHandlersValidateTargetBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &GroupHealthHandler{}
	for _, method := range []func(*gin.Context){h.RunQuality, h.QualityRun} {
		for _, id := range []string{"0", "-1", "invalid", "7"} {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/admin/groups/"+id+"/quality-check/run", nil)
			c.Params = gin.Params{{Key: "id", Value: id}}
			method(c)
			if id == "7" {
				require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			} else {
				require.Equal(t, http.StatusBadRequest, recorder.Code)
			}
		}
	}
}

func TestQualityManualHandlerMapsErrorsWithoutSensitiveDetails(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{service.ErrQualityRunBusy, http.StatusConflict},
		{service.ErrQualityRunDisabled, http.StatusBadRequest},
		{service.ErrQualityRunNoAccounts, http.StatusBadRequest},
		{service.ErrQualityRunUnavailable, http.StatusServiceUnavailable},
		{errors.New("private db password"), http.StatusInternalServerError},
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		writeQualityRunError(c, tc.err)
		require.Equal(t, tc.status, recorder.Code)
		require.NotContains(t, recorder.Body.String(), "private")
	}
}
