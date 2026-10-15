package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
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
		{fmt.Errorf("private upstream artifact: %w", service.ErrQualityRunBusy), http.StatusConflict},
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

type qualityRunGroupRepository struct {
	service.GroupRepository
}

type qualityRunAccountRepository struct {
	service.AccountRepository
}

func (qualityRunGroupRepository) GetByID(_ context.Context, id int64) (*service.Group, error) {
	return &service.Group{ID: id}, nil
}

func TestQualityManualHandlerQueriesAutomaticRunForCurrentGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, source := range []string{"manual", "automatic", "absent"} {
		t.Run(source, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			rows := sqlmock.NewRows([]string{"run", "group", "status", "reason", "checked", "total", "started", "finished", "source"})
			if source != "absent" {
				rows.AddRow("12345678-1234-4234-8234-123456789abc", 7, "running", "", 0, 2, time.Now(), nil, source)
			}
			mock.ExpectQuery(`SELECT run_id, group_id, status, reason`).WithArgs(int64(7)).WillReturnRows(rows)
			health := service.NewGroupHealthService(nil, &qualityRunAccountRepository{}, nil, nil, db)
			h := NewGroupHealthHandler(health, nil, service.NewGroupService(qualityRunGroupRepository{}, nil))
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/admin/groups/7/quality-check/run", nil)
			c.Params = gin.Params{{Key: "id", Value: "7"}}
			h.QualityRun(c)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
			var body struct {
				Data *service.GroupQualityRun `json:"data"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
			if source == "absent" {
				require.Nil(t, body.Data)
			} else {
				require.Equal(t, source, body.Data.Source)
				require.EqualValues(t, 7, body.Data.GroupID)
				require.Equal(t, 2, body.Data.TotalAccounts)
				require.Equal(t, "running", body.Data.Status)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
