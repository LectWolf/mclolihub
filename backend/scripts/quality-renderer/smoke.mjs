import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';

const html = '<style>@keyframes move{from{transform:translateX(0)}to{transform:translateX(600px)}}.moving{animation:move 2s linear infinite}</style><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 960 640"><rect width="960" height="640" fill="white"/><rect class="moving" x="40" y="80" width="80" height="80" fill="red"/><circle cy="300" r="40" fill="blue"><animate attributeName="cx" values="80;700" dur="2s" repeatCount="indefinite"/></circle></svg>';
const child = spawn(process.execPath, [fileURLToPath(new URL('./render.mjs', import.meta.url))], { stdio: ['pipe', 'pipe', 'inherit'] });
const chunks = [];
child.stdout.on('data', chunk => chunks.push(chunk));
child.stdin.on('error', () => {});
const exited = once(child, 'exit');
child.stdin.end(html);
const [code] = await exited;
assert.equal(code, 0, 'renderer must launch Chromium as a non-root user');
const frames = JSON.parse(Buffer.concat(chunks).toString());
assert.equal(frames.length, 4);
assert.equal(new Set(frames.map(frame => frame.png)).size, 4, 'CSS/SMIL frames must move');
for (const frame of frames) {
  const png = Buffer.from(frame.png, 'base64');
  assert.ok(png.length <= 2 * 1024 * 1024);
  assert.equal(png.subarray(0, 8).toString('hex'), '89504e470d0a1a0a');
  assert.equal(png.readUInt32BE(16), 960);
  assert.equal(png.readUInt32BE(20), 640);
}
process.stdout.write('Quality renderer Chromium smoke check passed\n');
