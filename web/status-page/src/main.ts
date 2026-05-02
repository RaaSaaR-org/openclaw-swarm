import './style.css';

interface PublicStatus {
  customerName: string;
  projectName: string;
  slug: string;
  status: 'online' | 'setting-up' | 'maintenance' | 'issue' | 'unknown';
  ready: boolean;
  message: string;
  lastUpdate: string;
  gatewayURL?: string;
}

const REFRESH_MS = 15000;

const path = window.location.pathname;
const slugMatch = path.match(/\/status\/([a-z0-9-]+)/);
const params = new URLSearchParams(window.location.search);

const slug = slugMatch?.[1] || '';
const token = params.get('token') || '';

const app = document.querySelector<HTMLDivElement>('#app')!;
let refreshTimer: number | undefined;
let lastFetched = 0;

if (!slug || !token) {
  renderError(
    'Status link required',
    'Open the personal status link sent by your EmAI team. The URL should look like ' +
      '<code>/status/&lt;your-project&gt;?token=&hellip;</code>',
  );
} else {
  bootstrap();
}

async function bootstrap() {
  renderShell();
  await refresh(true);
  refreshTimer = window.setInterval(() => refresh(false), REFRESH_MS);
}

function renderShell() {
  document.title = `Status — ${humanize(slug)} · EmAI`;
  app.innerHTML = `
    <div class="status-shell">
      <header class="status-header">
        <span class="brand-mark">EmAI</span>
        <span class="brand-divider">·</span>
        <span class="brand-tagline">Status</span>
      </header>
      <main class="status-main" id="status-main">
        <div class="loading">Checking…</div>
      </main>
      <footer class="status-footer">
        <span id="last-checked-line">Checking…</span>
        <span class="dot-sep">·</span>
        <a href="https://emai.dev" target="_blank" rel="noopener">emai.dev</a>
      </footer>
    </div>
  `;
}

async function refresh(showSpinner: boolean) {
  const main = document.getElementById('status-main');
  if (!main) return;
  if (showSpinner) main.innerHTML = '<div class="loading">Checking…</div>';

  try {
    const res = await fetch(`/api/status/${encodeURIComponent(slug)}?token=${encodeURIComponent(token)}`);
    if (res.status === 401) {
      renderError(
        'Status link not valid',
        'This status link is missing, expired, or wrong. Please use the link sent by your EmAI team.',
      );
      stopRefresh();
      return;
    }
    if (!res.ok) {
      renderError('Status temporarily unavailable', `We couldn't reach the status backend (HTTP ${res.status}). It will retry automatically.`);
      return;
    }
    const data = (await res.json()) as PublicStatus;
    renderStatus(data);
    lastFetched = Date.now();
    updateLastChecked();
  } catch (err) {
    renderError('Status temporarily unavailable', `Network error: ${String(err)}. It will retry automatically.`);
  }
}

function renderStatus(s: PublicStatus) {
  const main = document.getElementById('status-main');
  if (!main) return;

  const statusLabel: Record<PublicStatus['status'], string> = {
    online: 'Online',
    'setting-up': 'Setting up',
    maintenance: 'Paused',
    issue: 'Issue detected',
    unknown: 'Unknown',
  };
  const customer = s.customerName || humanize(s.slug);
  const project = s.projectName || '';

  main.innerHTML = `
    <div class="status-card">
      <div class="status-indicator status-${escapeHtml(s.status)}" aria-hidden="true">
        <div class="indicator-dot"></div>
      </div>
      <div class="status-headline">${escapeHtml(statusLabel[s.status] || s.status)}</div>
      <p class="status-message">${escapeHtml(s.message)}</p>
      <div class="status-meta">
        <div class="meta-row">
          <span class="meta-key">Customer</span>
          <span class="meta-val">${escapeHtml(customer)}</span>
        </div>
        ${project ? `
          <div class="meta-row">
            <span class="meta-key">Project</span>
            <span class="meta-val">${escapeHtml(project)}</span>
          </div>
        ` : ''}
        <div class="meta-row">
          <span class="meta-key">Last update</span>
          <span class="meta-val">${formatTime(s.lastUpdate)}</span>
        </div>
      </div>
    </div>
  `;
}

function renderError(title: string, message: string) {
  const main = document.getElementById('status-main') || app;
  document.title = `Status — EmAI`;
  if (main === app) {
    app.innerHTML = `
      <div class="status-shell">
        <header class="status-header">
          <span class="brand-mark">EmAI</span>
          <span class="brand-divider">·</span>
          <span class="brand-tagline">Status</span>
        </header>
        <main class="status-main">${errorBlock(title, message)}</main>
        <footer class="status-footer"><a href="https://emai.dev" target="_blank" rel="noopener">emai.dev</a></footer>
      </div>
    `;
  } else {
    main.innerHTML = errorBlock(title, message);
  }
}

function errorBlock(title: string, message: string): string {
  return `
    <div class="status-card error">
      <div class="status-indicator status-issue" aria-hidden="true">
        <div class="indicator-dot"></div>
      </div>
      <div class="status-headline">${escapeHtml(title)}</div>
      <p class="status-message">${message}</p>
    </div>
  `;
}

function stopRefresh() {
  if (refreshTimer) {
    clearInterval(refreshTimer);
    refreshTimer = undefined;
  }
}

function updateLastChecked() {
  const el = document.getElementById('last-checked-line');
  if (!el) return;
  const fmt = () => {
    const sec = Math.max(0, Math.floor((Date.now() - lastFetched) / 1000));
    if (sec < 5) return 'Checked just now';
    if (sec < 60) return `Checked ${sec}s ago`;
    const m = Math.floor(sec / 60);
    return `Checked ${m}m ago`;
  };
  el.textContent = fmt();
  setTimeout(() => updateLastChecked(), 5000);
}

function humanize(slug: string): string {
  return slug
    .split('-')
    .filter(Boolean)
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(' ');
}

function formatTime(iso: string): string {
  if (!iso) return '—';
  const t = new Date(iso).getTime();
  if (isNaN(t)) return '—';
  const sec = Math.max(0, Math.floor((Date.now() - t) / 1000));
  if (sec < 60) return 'just now';
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m} min ago`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h} h ago`;
  const d = Math.floor(h / 24);
  return `${d} d ago`;
}

function escapeHtml(text: string): string {
  const div = document.createElement('div');
  div.textContent = text ?? '';
  return div.innerHTML;
}
