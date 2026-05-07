import './style.css';
import { loadBranding, applyBranding, DEFAULT_BRANDING, type Branding } from '@branding/loader';

let b: Branding = DEFAULT_BRANDING;

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

bootstrap();

async function bootstrap() {
  b = await loadBranding();
  applyBranding(b);
  if (!slug || !token) {
    renderError(
      'Status link required',
      `${b.copy.status.missingSlugBody} The URL should look like <code>/status/&lt;your-project&gt;?token=&hellip;</code>`,
    );
    return;
  }
  renderShell();
  await refresh(true);
  refreshTimer = window.setInterval(() => refresh(false), REFRESH_MS);
}

function renderShell() {
  document.title = `${b.copy.status.docTitlePrefix}${humanize(slug)}${b.copy.status.docTitleSuffix}`;
  app.innerHTML = `
    <div class="status-shell">
      <header class="status-header">
        <a class="brand-lockup" href="/" aria-label="${escapeHtml(b.name)}">
          <img class="brand-lockup-mark" src="${escapeHtml(b.faviconUrl)}" alt="" />
          <span class="brand-lockup-word">${escapeHtml(b.name)}</span>
        </a>
        <span class="brand-tagline">Status</span>
      </header>
      <main class="status-main" id="status-main">
        ${loadingHTML()}
      </main>
      <footer class="status-footer">
        <span id="last-checked-line">Checking…</span>
        <a href="${escapeHtml(b.domainUrl)}" target="_blank" rel="noopener">${escapeHtml(b.domain)}</a>
      </footer>
    </div>
  `;
}

// The Kai brand mark — three nodes in triangle inside a faint circle.
// At display scale this becomes the live status indicator. brand.md §5.
function indicatorMarkSVG(): string {
  return `
    <svg viewBox="0 0 100 100" class="indicator-mark" aria-hidden="true">
      <circle class="ring" cx="50" cy="50" r="42" />
      <circle class="node n1" cx="50" cy="22" r="7" />
      <circle class="node n2" cx="24" cy="64" r="7" />
      <circle class="node n3" cx="76" cy="64" r="7" />
    </svg>
  `;
}

function loadingHTML(): string {
  return `
    <div class="loading" role="status" aria-live="polite">
      <svg viewBox="0 0 100 100" class="loading-mark" aria-hidden="true">
        <circle class="ring" cx="50" cy="50" r="42" />
        <circle class="node n1" cx="50" cy="22" r="7" />
        <circle class="node n2" cx="24" cy="64" r="7" />
        <circle class="node n3" cx="76" cy="64" r="7" />
      </svg>
      <span>Checking signal</span>
    </div>
  `;
}

function alertIconSVG(): string {
  return `
    <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0Z"/>
      <line x1="12" y1="9" x2="12" y2="13"/>
      <line x1="12" y1="17" x2="12.01" y2="17"/>
    </svg>
  `;
}

async function refresh(showSpinner: boolean) {
  const main = document.getElementById('status-main');
  if (!main) return;
  if (showSpinner) main.innerHTML = loadingHTML();

  try {
    const res = await fetch(`/api/status/${encodeURIComponent(slug)}?token=${encodeURIComponent(token)}`);
    if (res.status === 401) {
      renderError('Status link not valid', b.copy.status.expiredLinkBody);
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
        ${indicatorMarkSVG()}
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
          <span class="meta-val">${escapeHtml(formatTime(s.lastUpdate))}</span>
        </div>
      </div>
    </div>
  `;
}

function renderError(title: string, message: string) {
  const main = document.getElementById('status-main') || app;
  document.title = `${b.copy.status.docTitlePrefix.replace(/\s·\s$/, '')}${b.copy.status.docTitleSuffix}`;
  if (main === app) {
    app.innerHTML = `
      <div class="status-shell">
        <header class="status-header">
          <a class="brand-lockup" href="/" aria-label="${escapeHtml(b.name)}">
            <img class="brand-lockup-mark" src="${escapeHtml(b.faviconUrl)}" alt="" />
            <span class="brand-lockup-word">${escapeHtml(b.name)}</span>
          </a>
          <span class="brand-tagline">Status</span>
        </header>
        <main class="status-main">${errorBlock(title, message)}</main>
        <footer class="status-footer"><a href="${escapeHtml(b.domainUrl)}" target="_blank" rel="noopener">${escapeHtml(b.domain)}</a></footer>
      </div>
    `;
  } else {
    main.innerHTML = errorBlock(title, message);
  }
}

function errorBlock(title: string, message: string): string {
  return `
    <div class="status-card error">
      <div class="error-icon" aria-hidden="true">${alertIconSVG()}</div>
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
