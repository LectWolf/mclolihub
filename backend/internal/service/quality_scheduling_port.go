package service

import "context"

type manualQualitySchedulingRepository interface {
	SetManualQualitySchedulable(context.Context, []int64, bool) error
}

// QualityAccountEligibilitySQL excludes only synthetic UI fixtures, which never
// call an upstream. Account state/expiry rules remain in the candidate queries.
// Platform, credential type and model names do not establish model capability.
const QualityAccountEligibilitySQL = `COALESCE(a.extra->'synthetic_ui_test', 'false'::jsonb) <> 'true'::jsonb`
