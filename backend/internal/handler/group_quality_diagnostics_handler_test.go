package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestQualityDiagnosticsHandlerValidatesAndScopesResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const resultID = "12345678-1234-4234-8234-123456789abc"
	for _, tc := range []struct {
		name, id, resultID, reason string
		dbError                    error
		status                     int
	}{
		{name: "invalid-group", id: "0", resultID: resultID, status: 400},
		{name: "invalid-result", id: "7", resultID: "bad", status: 400},
		{name: "absent", id: "7", resultID: resultID, status: 404},
		{name: "legacy", id: "7", resultID: resultID, reason: "old reason", status: 200},
		{name: "database-error", id: "7", resultID: resultID, dbError: errors.New("private database secret"), status: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			if tc.status != 400 {
				query := mock.ExpectQuery(`SELECT h.group_id, COALESCE\(g.name, ''\), h.reason FROM group_quality_check_history h LEFT JOIN groups g ON g.id = h.group_id WHERE h.group_id = \$1 AND h.result_id = \$2`).WithArgs(int64(7), resultID)
				if tc.dbError != nil {
					query.WillReturnError(tc.dbError)
				} else {
					rows := sqlmock.NewRows([]string{"group_id", "group_name", "reason"})
					if tc.reason != "" {
						rows.AddRow(7, "诊断分组", tc.reason)
					}
					query.WillReturnRows(rows)
				}
			}
			health := service.NewGroupHealthService(nil, &qualityRunAccountRepository{}, nil, nil, db)
			h := NewGroupHealthHandler(health, nil, nil)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/admin/groups/"+tc.id+"/quality-check/diagnostics/"+tc.resultID, nil)
			c.Params = gin.Params{{Key: "id", Value: tc.id}, {Key: "resultID", Value: tc.resultID}}
			h.QualityDiagnostics(c)
			require.Equal(t, tc.status, recorder.Code)
			require.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
			require.NotContains(t, recorder.Body.String(), "private database")
			if tc.status == 200 {
				require.Contains(t, recorder.Body.String(), `"attempts":[]`)
				require.Contains(t, recorder.Body.String(), "old reason")
				require.Contains(t, recorder.Body.String(), `"group_id":7`)
				require.Contains(t, recorder.Body.String(), `"group_name":"诊断分组"`)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
