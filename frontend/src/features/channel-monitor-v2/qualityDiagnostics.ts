import type { MonitorLatestQuality } from '@/api/channelMonitorV2'
import type { GroupQualityRun } from '@/api/admin/qualityCheck'

export function formatQualityDiagnostics(latest: MonitorLatestQuality, groupId?: number, run?: GroupQualityRun): string {
  const details = latest.diagnostics
  return JSON.stringify({
    format: 'aihub-quality-check-diagnostic-v1',
    group_id: groupId ?? null,
    result_id: latest.result_id,
    status: latest.status,
    model_id: latest.model_id,
    checked_at: latest.checked_at,
    reason: latest.reason,
    has_preview: latest.has_preview,
    has_artwork: latest.has_artwork ?? false,
    run: run ? {
      run_id: run.run_id, source: run.source ?? 'manual', status: run.status,
      checked_accounts: run.checked_accounts, total_accounts: run.total_accounts,
      started_at: run.started_at, finished_at: run.finished_at ?? null,
    } : null,
    diagnostics: details ? {
      stage: details.stage,
      configured_protocol: details.configured_protocol,
      total_duration_ms: details.total_duration_ms,
      generation: {
        duration_ms: details.generation_duration_ms,
        timeout_seconds: details.generation_timeout_seconds,
        received_text_bytes: details.received_text_bytes,
        received_events: details.received_events,
        last_event_type: details.last_event_type ?? null,
        completion_received: details.completion_received,
        capture_overflow: details.capture_overflow,
        failure_code: details.failure_code ?? null,
        deadline_source: details.deadline_source ?? null,
      },
      rendering: {
        artwork_available: latest.has_artwork ?? false,
        duration_ms: details.render_duration_ms ?? null,
        failure_code: details.render_failure_code ?? null,
      },
    } : null,
    diagnostic_note: details
      ? 'Only local event metadata is included; no upstream response body, keys or authentication headers. Generation statistics describe the artwork-generation request.'
      : 'This historical result has no detailed diagnostics. Duration and received events cannot be recovered; rerun after updating to collect them.',
  }, null, 2)
}
