package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

type gatewayTokenRequestPricingAtCtxKey struct{}
type gatewayTokenRequestBillingGroupCtxKey struct{}

// WithGatewayTokenRequestPricing marks a shared-gateway request as token billed
// and freezes the downstream pricing instant for its whole lifetime. Media and
// metadata-only handlers deliberately do not call this helper.
func WithGatewayTokenRequestPricing(ctx context.Context) (context.Context, time.Time) {
	if ctx == nil {
		ctx = context.Background()
	}
	pricingAt := timezone.Now()
	ctx = context.WithValue(ctx, gatewayTokenRequestPricingAtCtxKey{}, pricingAt)
	if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(group) {
		ctx = BindGatewayTokenRequestBillingGroup(ctx, group)
	}
	return ctx, pricingAt
}

// BindGatewayTokenRequestBillingGroup points token billing at the group that
// actually handled the request (kiss-kedaya smart-route hydrate), not the key's
// original primary group.
func BindGatewayTokenRequestBillingGroup(ctx context.Context, group *Group) context.Context {
	if ctx == nil || !IsGroupContextValid(group) {
		return ctx
	}
	return context.WithValue(ctx, gatewayTokenRequestBillingGroupCtxKey{}, group)
}

func gatewayTokenRequestPricingAtFromContext(ctx context.Context) (time.Time, bool) {
	if ctx == nil {
		return time.Time{}, false
	}
	pricingAt, ok := ctx.Value(gatewayTokenRequestPricingAtCtxKey{}).(time.Time)
	return pricingAt, ok && !pricingAt.IsZero()
}

// GatewayTokenRequestPricingAtFromContext exposes the frozen instant to
// handlers before they detach asynchronous usage-recording work.
func GatewayTokenRequestPricingAtFromContext(ctx context.Context) time.Time {
	pricingAt, _ := gatewayTokenRequestPricingAtFromContext(ctx)
	return pricingAt
}

func gatewayTokenRequestBillingGroupFromContext(ctx context.Context) *Group {
	if ctx == nil {
		return nil
	}
	group, _ := ctx.Value(gatewayTokenRequestBillingGroupCtxKey{}).(*Group)
	if IsGroupContextValid(group) {
		return group
	}
	return nil
}
