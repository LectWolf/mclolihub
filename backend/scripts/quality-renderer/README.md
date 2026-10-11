# Quality renderer runtime

Docker images include the Node.js, Chromium and font dependencies. Binary archives include `scripts/quality-renderer` next to the server executable; install its dependencies before enabling group quality checks.

Run the server as a non-root user. Node.js 20.19 or newer is required.

From the directory containing the server executable:

```sh
npm ci --prefix scripts/quality-renderer --omit=dev --ignore-scripts
npx --prefix scripts/quality-renderer playwright install chromium
node scripts/quality-renderer/smoke.mjs
```

On Linux, install Chromium system dependencies using your distribution's package manager (or the Playwright `install-deps chromium` command with administrator privileges). Install fonts including Noto/DejaVu for stable SVG rendering.

If using a distribution-provided browser, set `QUALITY_CHROMIUM_EXECUTABLE_PATH` to its absolute executable path instead of downloading Chromium. Keep the browser accessible to the account running the server. To select a script outside the archive, set `SUB2API_QUALITY_RENDERER_SCRIPT` to the absolute path of `render.mjs`.

The Playwright download command uses the default browser cache for the current account. Install and run the renderer with the same account, or set `PLAYWRIGHT_BROWSERS_PATH` consistently for both commands and the server process.

Keep `scripts/quality-renderer`, `package.json`, `package-lock.json`, `LICENSE` and `NOTICE` with the executable when upgrading. The renderer blocks generated JavaScript and external network requests; its smoke command exercises the Chromium installation without calling any model provider.
