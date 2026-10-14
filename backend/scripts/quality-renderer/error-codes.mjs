const codes = new Set([
  'renderer_root_forbidden', 'renderer_resource_unavailable', 'renderer_dependency_unavailable',
  'renderer_chromium_unavailable', 'renderer_sandbox_failed', 'renderer_timeout',
  'renderer_output_limit', 'renderer_artwork_rejected', 'renderer_failed',
]);

// Only fixed categories cross the process boundary; raw browser errors stay local.
export function rendererFailureCode(error) {
  const message = String(error?.message ?? '').toLowerCase();
  if (codes.has(message)) return message;
  if (message.includes('root rendering is forbidden')) return 'renderer_root_forbidden';
  if (error?.code === 'ERR_MODULE_NOT_FOUND') return 'renderer_dependency_unavailable';
  if (/executable doesn.t exist|executable.*not found|executable.*does not exist|enoent/.test(message)) return 'renderer_chromium_unavailable';
  if (/error while loading shared libraries|missing dependencies|host system is missing dependencies/.test(message)) return 'renderer_dependency_unavailable';
  if (/no usable sandbox|failed to move to new namespace|suid sandbox|sandbox.*(?:failed|not supported)/.test(message)) return 'renderer_sandbox_failed';
  if (/timeout|timed out|deadline/.test(message)) return 'renderer_timeout';
  if (/rendered output.*exceeds/.test(message)) return 'renderer_output_limit';
  if (/^(?:unsafe html|html |invalid svg artwork)/.test(message)) return 'renderer_artwork_rejected';
  return 'renderer_failed';
}
