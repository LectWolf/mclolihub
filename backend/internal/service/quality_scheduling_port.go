package service

import "context"

type manualQualitySchedulingRepository interface {
	SetManualQualitySchedulable(context.Context, []int64, bool) error
}

// QualityAccountEligibilitySQL is shared by quality candidate scans and pause
// ownership reconciliation. The accounts table must use the alias a.
// Keep this aligned with accountAcceptsQualityPrompt; model eligibility is
// checked after applying the account's model mapping in Go.
const QualityAccountEligibilitySQL = `a.type = 'apikey'
    AND COALESCE(a.extra->'synthetic_ui_test', 'false'::jsonb) <> 'true'::jsonb
    AND (a.platform IN ('openai', 'gemini')
        OR (a.platform IN ('kimi', 'zhipu', 'deepseek', 'minimax')
            AND COALESCE(BTRIM(a.credentials->>'api_protocol'), '') <> 'anthropic'))`
