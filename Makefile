.PHONY: build build-backend build-frontend test test-backend test-backend-critical test-frontend test-frontend-critical

FRONTEND_CRITICAL_VITEST := \
	src/i18n/__tests__/localeKeyCompleteness.spec.ts \
	src/api/__tests__/client.spec.ts \
	src/api/__tests__/tokenRefresh.spec.ts \
	src/constants/__tests__/platforms.spec.ts \
	src/api/__tests__/admin.qualityCheck.spec.ts \
	src/features/channel-monitor-v2/__tests__/MonitorSettingsPanel.quality.spec.ts \
	src/components/user/monitor/__tests__/ChannelMonitorV3QualityPreview.spec.ts

BACKEND_CRITICAL_PACKAGES := ./internal/service ./internal/handler ./internal/repository ./internal/server
BACKEND_CRITICAL_TESTS := ^(TestQuality|TestPelican|TestGroupHealthManual|TestAccountTestService_ResponsesStreamCompatibility|TestUsageLogRepositoryGetStatsWithFiltersRequestTypePriority|TestGatewayService_AnthropicAPIKeyPassthrough_ForwardDirect_NonStreamingSuccess|TestOpenAIGatewayServiceRecordUsage_(UsesUserSpecificGroupRate|ClampsActualInputTokensToZero|GPT56SeparatesCacheWriteForBillingAndStats)|TestAPIContracts)

# Default checks stay short. Full suites remain available through backend/Makefile
# and pnpm --dir frontend test:run when a change needs broader coverage.
test: test-backend test-frontend

test-backend: test-backend-critical

test-backend-critical:
	@cd backend && go test -tags=unit -timeout=3m $(BACKEND_CRITICAL_PACKAGES) -run '$(BACKEND_CRITICAL_TESTS)'

test-frontend:
	@pnpm --dir frontend run typecheck
	@$(MAKE) test-frontend-critical

test-frontend-critical:
	@pnpm --dir frontend exec vitest run $(FRONTEND_CRITICAL_VITEST)

build: build-frontend build-backend

build-backend:
	@$(MAKE) -C backend build

build-frontend:
	@pnpm --dir frontend run build
