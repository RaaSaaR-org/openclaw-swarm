import './style.css';
import { api, AuthError, clearToken, getToken, setToken, type InstanceSummary } from './api';

const REFRESH_MS = 5000;

const app = document.querySelector<HTMLDivElement>('#app')!;

if (!getToken()) {
  renderLogin();
} else {
  renderConsole();
}

function renderLogin(error?: string) {
  app.innerHTML = `
    <div class="login-shell">
      <form class="login-card" id="login-form">
        <div class="login-brand">
          <span class="brand-mark">EmAI</span>
          <span class="brand-divider">·</span>
          <span class="brand-tagline">Swarm Admin</span>
        </div>
        <h1>Sign in</h1>
        <p class="login-hint">Enter the admin token to manage Kai instances.</p>
        <label for="token-input" class="visually-hidden">Admin token</label>
        <input id="token-input" type="password" placeholder="Admin token" autocomplete="off" required autofocus />
        ${error ? `<p class="login-error">${escapeHtml(error)}</p>` : ''}
        <button type="submit">Continue</button>
      </form>
    </div>
  `;

  const form = document.getElementById('login-form') as HTMLFormElement;
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const input = document.getElementById('token-input') as HTMLInputElement;
    const token = input.value.trim();
    if (!token) return;
    setToken(token);
    try {
      await api.list();
    } catch (err) {
      if (err instanceof AuthError) {
        clearToken();
        renderLogin('Invalid token.');
        return;
      }
      // Token is accepted (or backend unreachable for non-auth reason) —
      // enter the console; errors will surface in the table.
    }
    renderConsole();
  });
}

interface ConsoleState {
  instances: InstanceSummary[];
  loading: boolean;
  error?: string;
  selected?: string;
  detail?: Record<string, unknown>;
}

const state: ConsoleState = { instances: [], loading: true };
let refreshTimer: number | undefined;

function renderConsole() {
  app.innerHTML = `
    <div class="admin-shell">
      <header class="admin-header">
        <div class="header-left">
          <span class="brand-mark">EmAI</span>
          <span class="brand-divider">·</span>
          <span class="brand-tagline">Swarm Admin</span>
        </div>
        <div class="header-right">
          <span class="refresh-indicator" id="refresh-indicator" title="Auto-refresh"></span>
          <button class="ghost-btn" id="logout-btn">Sign out</button>
        </div>
      </header>
      <main class="admin-main">
        <section class="panel panel-list">
          <div class="panel-header">
            <h2>Kai Instances</h2>
            <button class="ghost-btn" id="refresh-btn" title="Refresh">↻</button>
          </div>
          <div id="instance-table"></div>
        </section>
        <aside class="panel panel-detail" id="detail-panel">
          <div class="panel-header">
            <h2>Details</h2>
            <button class="ghost-btn" id="close-detail-btn" hidden>×</button>
          </div>
          <div id="detail-body" class="detail-empty">Select an instance to view details.</div>
        </aside>
      </main>
    </div>
    <div class="modal-overlay" id="confirm-modal">
      <div class="modal" role="dialog" aria-modal="true">
        <div class="modal-header"><h2 id="confirm-title">Confirm</h2></div>
        <div class="modal-body">
          <p id="confirm-message"></p>
          <div class="modal-actions">
            <button class="ghost-btn" id="confirm-cancel">Cancel</button>
            <button class="primary-btn" id="confirm-ok">Confirm</button>
          </div>
        </div>
      </div>
    </div>
  `;

  document.getElementById('logout-btn')!.addEventListener('click', () => {
    clearToken();
    if (refreshTimer) clearInterval(refreshTimer);
    renderLogin();
  });
  document.getElementById('refresh-btn')!.addEventListener('click', () => loadInstances(true));
  document.getElementById('close-detail-btn')!.addEventListener('click', () => {
    state.selected = undefined;
    state.detail = undefined;
    renderDetail();
  });

  loadInstances(true);
  refreshTimer = window.setInterval(() => loadInstances(false), REFRESH_MS);
}

async function loadInstances(showSpinner: boolean) {
  if (showSpinner) state.loading = true;
  pulseRefresh();
  try {
    state.instances = await api.list();
    state.error = undefined;
  } catch (err) {
    if (err instanceof AuthError) {
      clearToken();
      if (refreshTimer) clearInterval(refreshTimer);
      renderLogin('Session expired. Sign in again.');
      return;
    }
    state.error = String(err);
  } finally {
    state.loading = false;
  }
  renderTable();
  if (state.selected) loadDetail(state.selected);
}

function renderTable() {
  const el = document.getElementById('instance-table');
  if (!el) return;

  if (state.loading) {
    el.innerHTML = `<div class="status-msg">Loading…</div>`;
    return;
  }
  if (state.error) {
    el.innerHTML = `<div class="status-msg error">${escapeHtml(state.error)}</div>`;
    return;
  }
  if (state.instances.length === 0) {
    el.innerHTML = `<div class="status-msg">No instances. Provision one with <code>swarm-ctl provision</code>.</div>`;
    return;
  }

  const rows = state.instances
    .slice()
    .sort((a, b) => a.customerName.localeCompare(b.customerName))
    .map((i) => rowHtml(i))
    .join('');

  el.innerHTML = `
    <table class="instance-table">
      <thead>
        <tr>
          <th>Customer</th>
          <th>Project</th>
          <th>Phase</th>
          <th>Ready</th>
          <th>Age</th>
          <th class="actions-col">Actions</th>
        </tr>
      </thead>
      <tbody>${rows}</tbody>
    </table>
  `;

  el.querySelectorAll<HTMLElement>('tr[data-name]').forEach((tr) => {
    tr.addEventListener('click', (e) => {
      if ((e.target as HTMLElement).closest('button')) return;
      const name = tr.dataset.name!;
      state.selected = state.selected === name ? undefined : name;
      renderTable();
      if (state.selected) loadDetail(state.selected);
      else renderDetail();
    });
  });
  el.querySelectorAll<HTMLButtonElement>('button[data-action]').forEach((btn) => {
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      const name = btn.dataset.name!;
      const action = btn.dataset.action as 'suspend' | 'resume';
      const inst = state.instances.find((i) => i.name === name);
      if (!inst) return;
      confirmAction(
        action === 'suspend' ? 'Suspend instance?' : 'Resume instance?',
        action === 'suspend'
          ? `Scale ${inst.customerName} (${inst.name}) to zero replicas? Data is preserved.`
          : `Resume ${inst.customerName} (${inst.name})?`,
        async () => {
          if (action === 'suspend') await api.suspend(name);
          else await api.resume(name);
          await loadInstances(false);
        },
      );
    });
  });
}

function rowHtml(i: InstanceSummary): string {
  const phaseClass = `phase-${i.phase.toLowerCase() || 'unknown'}`;
  const readyDot = i.ready ? '<span class="dot ok"></span>' : '<span class="dot off"></span>';
  const isSelected = state.selected === i.name ? ' selected' : '';
  const age = relativeAge(i.creationTimestamp);
  const action = i.suspended
    ? `<button class="primary-btn small" data-action="resume" data-name="${i.name}">Resume</button>`
    : `<button class="ghost-btn small" data-action="suspend" data-name="${i.name}">Suspend</button>`;
  return `
    <tr data-name="${i.name}" class="${isSelected.trim()}">
      <td>
        <div class="customer-cell">
          <div class="customer-name">${escapeHtml(i.customerName)}</div>
          <div class="customer-slug">${escapeHtml(i.customerSlug || i.name)}</div>
        </div>
      </td>
      <td>${escapeHtml(i.projectName)}</td>
      <td><span class="phase-badge ${phaseClass}">${escapeHtml(i.phase || '—')}</span></td>
      <td>${readyDot}</td>
      <td class="age-cell">${age}</td>
      <td class="actions-col">${action}</td>
    </tr>
  `;
}

async function loadDetail(name: string) {
  try {
    state.detail = await api.get(name);
    renderDetail();
  } catch (err) {
    if (err instanceof AuthError) {
      clearToken();
      if (refreshTimer) clearInterval(refreshTimer);
      renderLogin('Session expired. Sign in again.');
      return;
    }
    state.detail = { error: String(err) };
    renderDetail();
  }
}

function renderDetail() {
  const body = document.getElementById('detail-body');
  const closeBtn = document.getElementById('close-detail-btn') as HTMLButtonElement | null;
  if (!body) return;

  if (!state.selected || !state.detail) {
    body.className = 'detail-empty';
    body.textContent = 'Select an instance to view details.';
    if (closeBtn) closeBtn.hidden = true;
    return;
  }
  if (closeBtn) closeBtn.hidden = false;

  const inst = state.instances.find((i) => i.name === state.selected);
  const detail = state.detail as any;
  const conditions = (detail?.status?.conditions || []) as Array<Record<string, string>>;

  body.className = '';
  body.innerHTML = `
    <div class="detail-section">
      <h3>${escapeHtml(inst?.customerName || state.selected)}</h3>
      <div class="kv-grid">
        <div class="kv-key">Name</div><div class="kv-val mono">${escapeHtml(state.selected)}</div>
        <div class="kv-key">Slug</div><div class="kv-val mono">${escapeHtml(inst?.customerSlug || '—')}</div>
        <div class="kv-key">Project</div><div class="kv-val">${escapeHtml(inst?.projectName || '—')}</div>
        <div class="kv-key">Phase</div><div class="kv-val">${escapeHtml(inst?.phase || '—')}</div>
        <div class="kv-key">Ready</div><div class="kv-val">${inst?.ready ? 'yes' : 'no'}</div>
        <div class="kv-key">Suspended</div><div class="kv-val">${inst?.suspended ? 'yes' : 'no'}</div>
        <div class="kv-key">Model</div><div class="kv-val mono">${escapeHtml(inst?.model || 'default')}</div>
        <div class="kv-key">Gateway</div><div class="kv-val mono">${escapeHtml(inst?.gatewayURL || '—')}</div>
        <div class="kv-key">External</div><div class="kv-val mono">${escapeHtml(inst?.externalURL || '—')}</div>
        <div class="kv-key">Created</div><div class="kv-val">${escapeHtml(inst?.creationTimestamp || '—')}</div>
      </div>
    </div>
    ${conditions.length > 0 ? `
      <div class="detail-section">
        <h3>Conditions</h3>
        <table class="conditions-table">
          <thead><tr><th>Type</th><th>Status</th><th>Reason</th></tr></thead>
          <tbody>
            ${conditions.map((c) => `
              <tr>
                <td>${escapeHtml(c.type || '')}</td>
                <td>${c.status === 'True' ? '<span class="dot ok"></span>' : '<span class="dot off"></span>'} ${escapeHtml(c.status || '')}</td>
                <td>${escapeHtml(c.reason || '')}</td>
              </tr>
            `).join('')}
          </tbody>
        </table>
      </div>
    ` : ''}
    <div class="detail-section">
      <h3>Raw</h3>
      <pre class="raw-yaml">${escapeHtml(JSON.stringify(detail, null, 2))}</pre>
    </div>
  `;
}

function confirmAction(title: string, message: string, onConfirm: () => Promise<void>) {
  const modal = document.getElementById('confirm-modal')!;
  document.getElementById('confirm-title')!.textContent = title;
  document.getElementById('confirm-message')!.textContent = message;
  modal.classList.add('open');

  const ok = document.getElementById('confirm-ok')! as HTMLButtonElement;
  const cancel = document.getElementById('confirm-cancel')! as HTMLButtonElement;

  const close = () => modal.classList.remove('open');
  const cleanup = () => {
    ok.replaceWith(ok.cloneNode(true));
    cancel.replaceWith(cancel.cloneNode(true));
  };

  document.getElementById('confirm-ok')!.addEventListener('click', async () => {
    close();
    cleanup();
    try {
      await onConfirm();
    } catch (err) {
      alert(String(err));
    }
  });
  document.getElementById('confirm-cancel')!.addEventListener('click', () => {
    close();
    cleanup();
  });
}

function pulseRefresh() {
  const el = document.getElementById('refresh-indicator');
  if (!el) return;
  el.classList.add('pulse');
  setTimeout(() => el.classList.remove('pulse'), 600);
}

function relativeAge(iso: string): string {
  if (!iso) return '—';
  const t = new Date(iso).getTime();
  if (isNaN(t)) return '—';
  const s = Math.max(0, Math.floor((Date.now() - t) / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h}h`;
  const d = Math.floor(h / 24);
  return `${d}d`;
}

function escapeHtml(text: string): string {
  const div = document.createElement('div');
  div.textContent = text ?? '';
  return div.innerHTML;
}
