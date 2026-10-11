package service

import "context"

type manualQualitySchedulingRepository interface {
	SetManualQualitySchedulable(context.Context, []int64, bool) error
}
