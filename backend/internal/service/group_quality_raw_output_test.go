package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestPelicanRawOutputSurvivesParseFailure(t *testing.T) {
	for _, tail := range []string{"data: invalid\n\n", "data: {\"type\":\"error\"}\n\n"} {
		raw := "data: {\"type\":\"content\",\"text\":\"<html>partial\"}\n\n" + tail
		out, err := parsePelicanTestEvents([]byte(raw), pelicanDocumentLimit)
		require.Error(t, err)
		require.Equal(t, "<html>partial", out.Text)
	}
}

func TestPelicanRawOutputRetainsBoundedUTF8Prefix(t *testing.T) {
	event, err := json.Marshal(TestEvent{Type: "content", Text: "abc中文"})
	require.NoError(t, err)
	out, err := parsePelicanTestEvents(append([]byte("data: "), event...), 7)
	require.Error(t, err)
	require.Equal(t, "abc中", out.Text)
	require.True(t, utf8.ValidString(out.Text))
	require.True(t, out.CaptureOverflow)
}

func TestQualityRawOutputReadIsScopedAndPreservesText(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	reader := NewGroupQualityReader(db)
	_, err = reader.RawOutput(context.Background(), 7, "invalid")
	require.ErrorIs(t, err, sql.ErrNoRows)
	const id = "12345678-1234-4234-8234-123456789abc"
	const output = "  <svg>fragment</svg>\n  "
	query := `(?s)SELECT l.raw_output FROM group_quality_check_history l.*s.enabled.*a.deleted_at IS NULL.*ag.group_id = l.group_id.*l.group_id = \$1 AND l.result_id = \$2`
	mock.ExpectQuery(query).WithArgs(int64(7), id).WillReturnRows(sqlmock.NewRows([]string{"raw_output"}).AddRow(output))
	actual, err := reader.RawOutput(context.Background(), 7, id)
	require.NoError(t, err)
	require.Equal(t, output, actual)
	mock.ExpectQuery(query).WithArgs(int64(8), id).WillReturnRows(sqlmock.NewRows([]string{"raw_output"}))
	_, err = reader.RawOutput(context.Background(), 8, id)
	require.ErrorIs(t, err, sql.ErrNoRows)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Nil(t, qualityRawOutputValue(""))
	require.Nil(t, qualityRawOutputValue(strings.Repeat("x", pelicanDocumentLimit+1)))
	require.Equal(t, output, qualityRawOutputValue(output))
}

func TestQualityRawOutputHistoryFlag(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	mock.ExpectQuery(`(?s)SELECT s.group_id, h.result_id.*h.raw_output.*group_quality_check_history`).
		WillReturnRows(sqlmock.NewRows([]string{"group", "id", "status", "reason", "model", "checked", "artwork", "raw", "score"}).
			AddRow(7, "id", "failed", "no HTML", "model", now, false, true, nil))
	history, err := NewGroupQualityReader(db).History(context.Background(), []int64{7})
	require.NoError(t, err)
	require.True(t, history[7][0].HasRawOutput)
	require.False(t, history[7][0].HasArtwork)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQualityRawOutputSavedForFailedGeneration(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	candidate := qualityCandidate{groupID: 7, id: 11, version: 2, modelID: "text-model"}
	const output = "  <svg>unfinished fragment</svg>\n"
	expectQualitySave(mock, now, candidate, "failed", 0, false, nil, output)
	checker := newGroupQualityChecker(db, nil, nil, nil, "test")
	err = checker.finishCheck(context.Background(), candidate, now, qualityProbeResult{Status: "failed", Reason: "safe reason", ModelID: candidate.modelID, RawOutput: output})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
