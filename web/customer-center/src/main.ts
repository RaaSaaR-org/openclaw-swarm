import './style.css';
import { marked } from 'marked';

marked.setOptions({ breaks: true, gfm: true });

interface AppLink {
  label: string;
  url: string;
  description?: string;
  icon?: string;
  external?: boolean;
}

interface Briefing {
  id: string;
  title: string;
  date?: string;
  excerpt?: string;
  body?: string;
}

interface Channel {
  kind: 'webchat' | 'telegram' | string;
  label: string;
  hint?: string;
}

interface TeamMember {
  name: string;
  role?: string;
  company?: string;
  email?: string;
  phone?: string;
  timezone?: string;
  avatar?: string;
}

type StatusKind = 'online' | 'setting-up' | 'paused' | 'issue' | 'unknown';

interface CenterData {
  customerName: string;
  projectName: string;
  slug: string;
  status: StatusKind;
  statusLabel: string;
  links: AppLink[];
  channels: Channel[];
  team: TeamMember[];
  scope?: string;
  heartbeat?: string;
  briefings: Briefing[];
}

interface AccessUser {
  email: string;
  createdAt: string;
  passwordUpdatedAt: string;
}

const SEARCH_THRESHOLD = 5;

const path = window.location.pathname;
const slugMatch = path.match(/\/center\/([a-z0-9-]+)/);
const slug = slugMatch?.[1] || '';

const app = document.querySelector<HTMLDivElement>('#app')!;

if (!slug) {
  renderError(
    'Project link required',
    'Open the project hub link sent by your EmAI team. The URL should look like ' +
      '<code>/center/&lt;your-project&gt;</code>',
  );
} else {
  bootstrap();
}

let currentData: CenterData | null = null;
let currentEmail: string | null = null;
let briefingFilter = '';
let accessUsers: AccessUser[] | null = null;
let accessError: string | null = null;

interface AuthInfo {
  authenticated: boolean;
  email?: string;
  needsSetup: boolean;
}

async function bootstrap() {
  renderLoading();
  let auth: AuthInfo;
  try {
    const res = await fetch(`/api/center/${encodeURIComponent(slug)}/auth`, { credentials: 'same-origin' });
    if (!res.ok) {
      renderError('Hub temporarily unavailable', `We couldn't reach the hub backend (HTTP ${res.status}). Please try again shortly.`);
      return;
    }
    auth = (await res.json()) as AuthInfo;
  } catch (err) {
    renderError('Hub temporarily unavailable', `Network error: ${String(err)}.`);
    return;
  }

  if (!auth.authenticated) {
    renderLogin(auth.needsSetup);
    return;
  }
  currentEmail = auth.email || null;
  await loadHubData();
}

async function loadHubData() {
  renderLoading();
  try {
    const res = await fetch(`/api/center/${encodeURIComponent(slug)}`, { credentials: 'same-origin' });
    if (res.status === 401) {
      currentEmail = null;
      renderLogin(false);
      return;
    }
    if (!res.ok) {
      renderError('Hub temporarily unavailable', `We couldn't reach the hub backend (HTTP ${res.status}). Please try again shortly.`);
      return;
    }
    currentData = (await res.json()) as CenterData;
    currentData.team = currentData.team || [];
    currentData.briefings = currentData.briefings || [];
    currentData.links = currentData.links || [];
    currentData.channels = currentData.channels || [];
    renderHub(currentData);
    void loadAccessUsers();
  } catch (err) {
    renderError('Hub temporarily unavailable', `Network error: ${String(err)}.`);
  }
}

function renderLogin(needsSetup: boolean, errorMsg?: string, prefilledEmail = '') {
  const customerName = humanize(slug);
  document.title = `${customerName} · EmAI`;
  app.innerHTML = `
    <div class="login-shell">
      <header class="login-header"><span class="brand-mark">EmAI</span></header>
      <main class="login-main">
        <div class="login-card">
          <div class="login-eyebrow">${escapeHtml(customerName)} · Customer Center</div>
          <h1 class="login-title">${needsSetup ? 'Set up your customer center' : 'Sign in'}</h1>
          <p class="login-subtitle">${needsSetup
            ? 'No team members have been added yet. The first person to sign in becomes the admin and can add others.'
            : 'Use the email and password your project lead shared with you.'}</p>
          ${errorMsg ? `<div class="login-error">${escapeHtml(errorMsg)}</div>` : ''}
          <form id="login-form" autocomplete="on">
            <label class="login-field">
              <span>Email</span>
              <input type="email" name="email" required autocomplete="username" value="${escapeAttr(prefilledEmail)}" ${prefilledEmail ? '' : 'autofocus'} />
            </label>
            <label class="login-field">
              <span>${needsSetup ? 'Choose a password (min 8 chars)' : 'Password'}</span>
              <input type="password" name="password" required minlength="8" autocomplete="${needsSetup ? 'new-password' : 'current-password'}" ${prefilledEmail ? 'autofocus' : ''} />
            </label>
            <button type="submit" class="login-submit">${needsSetup ? 'Create admin account →' : 'Sign in →'}</button>
          </form>
          ${needsSetup ? '' : '<p class="login-help">Forgot your password? Ask your project lead to reset it.</p>'}
        </div>
      </main>
      <footer class="login-footer"><a href="https://emai.dev" target="_blank">emai.dev</a></footer>
    </div>
  `;

  const form = document.getElementById('login-form') as HTMLFormElement;
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const submit = form.querySelector('button[type="submit"]') as HTMLButtonElement;
    const original = submit.textContent;
    submit.disabled = true;
    submit.textContent = needsSetup ? 'Creating…' : 'Signing in…';
    const email = (form.elements.namedItem('email') as HTMLInputElement).value.trim().toLowerCase();
    const password = (form.elements.namedItem('password') as HTMLInputElement).value;
    try {
      const res = await fetch(`/api/center/${encodeURIComponent(slug)}/login`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email, password }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        renderLogin(needsSetup, body.error === 'invalid login' ? 'Email or password is incorrect.' : (body.error || `Sign-in failed (HTTP ${res.status}).`), email);
        return;
      }
      const result = await res.json().catch(() => ({}));
      currentEmail = result.email || email;
      await loadHubData();
    } catch (err) {
      submit.disabled = false;
      submit.textContent = original;
      renderLogin(needsSetup, `Network error: ${String(err)}`, email);
    }
  });
}

async function logout() {
  try {
    await fetch(`/api/center/${encodeURIComponent(slug)}/logout`, { method: 'POST', credentials: 'same-origin' });
  } catch { /* ignore */ }
  currentEmail = null;
  currentData = null;
  history.replaceState(null, '', location.pathname);
  bootstrap();
}

window.addEventListener('hashchange', handleRouteChange);
window.addEventListener('popstate', handleRouteChange);

type Route = 'overview' | 'briefings' | 'team' | 'apps' | 'team-access';
const VALID_ROUTES: Route[] = ['overview', 'briefings', 'team', 'apps', 'team-access'];

interface ParsedRoute { route: Route; param?: string; }

function parseRoute(): ParsedRoute {
  const raw = location.hash.replace(/^#\/?/, '');
  if (!raw) return { route: 'overview' };

  // Legacy briefing deep-link: #briefing-<id>
  const legacy = raw.match(/^briefing-(.+)$/);
  if (legacy) return { route: 'briefings', param: legacy[1] };

  const parts = raw.split('/').filter(Boolean);
  const r = parts[0];
  if (VALID_ROUTES.includes(r as Route)) {
    return { route: r as Route, param: parts[1] };
  }
  return { route: 'overview' };
}

function buildRouteHash(route: Route, param?: string): string {
  return param ? `#/${route}/${param}` : `#/${route}`;
}

function navigateTo(route: Route, param?: string) {
  const target = buildRouteHash(route, param);
  if (location.hash !== target) {
    history.pushState(null, '', target);
  }
  renderCurrentRoute();
}

function handleRouteChange() {
  renderCurrentRoute();
}

function renderCurrentRoute() {
  if (!currentData) return;
  const { route, param } = parseRoute();
  setActiveNav(route);
  renderPage(currentData, route, param);
  window.scrollTo({ top: 0, behavior: 'auto' });
}

function renderLoading() {
  app.innerHTML = `
    <div class="hub-shell">
      <main class="hub-main"><div class="loading">Loading…</div></main>
    </div>
  `;
}

interface NavItem { id: string; label: string; icon: string; }

function navItems(data: CenterData): NavItem[] {
  const items: NavItem[] = [
    { id: 'overview', label: 'Overview', icon: '·' },
    { id: 'briefings', label: 'Briefings', icon: '·' },
  ];
  if (data.team && data.team.length) items.push({ id: 'team', label: 'Project team', icon: '·' });
  items.push({ id: 'apps', label: 'Apps & channels', icon: '·' });
  items.push({ id: 'team-access', label: 'Team access', icon: '·' });
  return items;
}

function renderHub(data: CenterData) {
  document.title = `${data.customerName || humanize(slug)} · EmAI`;

  app.innerHTML = `
    <div class="hub-shell">
      ${sidebarHTML(data)}
      <main class="hub-main" id="hub-main">
        <div id="page-content" class="page-content"></div>
        <footer class="hub-footer">
          <a href="https://emai.dev" target="_blank" rel="noopener">emai.dev</a>
        </footer>
      </main>
    </div>
  `;

  bindSidebarNav();
  renderCurrentRoute();
}

function renderPage(data: CenterData, route: Route, param?: string) {
  const container = document.getElementById('page-content');
  if (!container) return;

  switch (route) {
    case 'overview':
      container.innerHTML = overviewPageHTML(data);
      bindTeaserHandler();
      bindOverviewHandlers();
      break;
    case 'briefings':
      container.innerHTML = briefingsPageHTML(data);
      bindBriefingHandlers();
      bindSearchHandler();
      if (param) {
        // Deep-link: open and scroll to the requested briefing.
        requestAnimationFrame(() => focusBriefing(param));
      }
      break;
    case 'team':
      container.innerHTML = teamPageHTML(data);
      break;
    case 'apps':
      container.innerHTML = appsPageHTML(data);
      break;
    case 'team-access':
      container.innerHTML = accessPageHTML();
      bindAccessHandlers();
      break;
  }
}

// ---------- Page bodies ----------

function pageHeaderHTML(eyebrow: string, title: string, subtitle?: string): string {
  return `
    <div class="page-header">
      <p class="page-eyebrow">${escapeHtml(eyebrow)}</p>
      <h1 class="page-title">${escapeHtml(title)}</h1>
      ${subtitle ? `<p class="page-subtitle">${escapeHtml(subtitle)}</p>` : ''}
    </div>
  `;
}

function overviewPageHTML(data: CenterData): string {
  const briefingCount = data.briefings.length;
  const unread = data.briefings.filter((b) => !isRead(b.id)).length;
  const teamCount = data.team.length;
  const accessCount = accessUsers ? accessUsers.length : null;

  const stats: Array<{ label: string; value: string; hint?: string; tone?: string }> = [
    { label: 'Status', value: data.statusLabel || 'Unknown', tone: data.status },
    {
      label: 'Briefings',
      value: String(briefingCount),
      hint: unread > 0 ? `${unread} unread` : briefingCount > 0 ? 'all read' : '—',
    },
    { label: 'Project team', value: String(teamCount), hint: teamCount === 0 ? '—' : undefined },
    {
      label: 'Chat access',
      value: accessCount === null ? '…' : String(accessCount),
      hint: accessCount === null ? 'loading' : accessCount === 0 ? 'no users yet' : `user${accessCount === 1 ? '' : 's'}`,
    },
  ];

  const statCards = stats
    .map(
      (s) => `
        <article class="stat-card">
          <div class="stat-label">${escapeHtml(s.label)}</div>
          <div class="stat-value ${s.tone ? `tone-${escapeAttr(s.tone)}` : ''}">${escapeHtml(s.value)}</div>
          ${s.hint ? `<div class="stat-hint">${escapeHtml(s.hint)}</div>` : ''}
        </article>
      `,
    )
    .join('');

  const greeting = greetingFor(new Date());

  return `
    ${pageHeaderHTML(greeting, `Welcome back to ${data.customerName || humanize(data.slug)}`, data.projectName || undefined)}

    <section class="stat-grid">${statCards}</section>

    ${teaserHTML(data)}

    ${scopeAndRhythmsHTML(data)}

    <section class="quick-actions-card">
      <h3 class="quick-actions-title">Quick actions</h3>
      <div class="quick-actions">
        ${data.links
          .filter((l) => !l.external)
          .map(
            (l) => `<a class="quick-action" href="${escapeAttr(l.url)}">
              <span class="quick-action-icon">${escapeHtml(l.icon || '↗')}</span>
              <span>${escapeHtml(l.label)}</span>
              <span class="quick-action-arrow">→</span>
            </a>`,
          )
          .join('')}
        <button type="button" class="quick-action" data-action="goto-access">
          <span class="quick-action-icon">👥</span>
          <span>Manage team access</span>
          <span class="quick-action-arrow">→</span>
        </button>
        ${
          briefingCount > 0
            ? `<button type="button" class="quick-action" data-action="goto-latest-briefing">
                <span class="quick-action-icon">📄</span>
                <span>Read latest briefing</span>
                <span class="quick-action-arrow">→</span>
              </button>`
            : ''
        }
      </div>
    </section>
  `;
}

function bindOverviewHandlers() {
  document.querySelector<HTMLButtonElement>('button[data-action="goto-access"]')?.addEventListener('click', () => {
    navigateTo('team-access');
  });
  document.querySelector<HTMLButtonElement>('button[data-action="goto-latest-briefing"]')?.addEventListener('click', () => {
    if (currentData && currentData.briefings.length > 0) {
      navigateTo('briefings', currentData.briefings[0].id);
    }
  });
}

function briefingsPageHTML(data: CenterData): string {
  const unread = data.briefings.filter((b) => !isRead(b.id)).length;
  let subtitle: string;
  if (data.briefings.length === 0) {
    subtitle = 'Nothing posted yet.';
  } else if (unread > 0) {
    subtitle = `${data.briefings.length} total · ${unread} unread`;
  } else {
    subtitle = `${data.briefings.length} total · all read`;
  }
  return `
    ${pageHeaderHTML('Updates from Kai', 'Briefings', subtitle)}
    ${briefingsSectionHTML(data)}
  `;
}

function teamPageHTML(data: CenterData): string {
  if (!data.team.length) {
    return `
      ${pageHeaderHTML('Project contacts', 'Project team', 'No contacts have been published yet for this project.')}
      <div class="empty-state">
        <p class="dim">When your EmAI lead populates <code>team.json</code> in the customer profile, contacts will show up here.</p>
      </div>
    `;
  }
  return `
    ${pageHeaderHTML('Project contacts', 'Project team', `${data.team.length} ${data.team.length === 1 ? 'contact' : 'contacts'}`)}
    ${teamSectionHTML(data)}
  `;
}

function appsPageHTML(data: CenterData): string {
  const total = data.links.length + data.channels.length;
  return `
    ${pageHeaderHTML('Connected surfaces', 'Apps & channels', `${total} ${total === 1 ? 'item' : 'items'}`)}
    ${appsSectionHTML(data)}
    ${channelsSectionHTML(data)}
  `;
}

function accessPageHTML(): string {
  return `
    ${pageHeaderHTML('Chat login management', 'Team access', 'Add or remove who can sign in to the chat for this project.')}
    ${accessSectionHTML()}
  `;
}

function focusBriefing(id: string) {
  const det = document.getElementById(`briefing-${id}`) as HTMLDetailsElement | null;
  if (!det) return;
  det.open = true;
  det.scrollIntoView({ behavior: 'smooth', block: 'start' });
  det.classList.add('flash');
  setTimeout(() => det.classList.remove('flash'), 1200);
}

function sidebarHTML(data: CenterData): string {
  const initials = customerInitials(data.customerName || data.slug);
  const chatLink = data.links.find((l) => l.label === 'Chat with Kai')?.url || '#';
  const items = navItems(data);
  const active = parseRoute().route;
  const navHTML = items
    .map(
      (n) => {
        const unreadBadge =
          n.id === 'briefings' && data.briefings
            ? data.briefings.filter((b) => !isRead(b.id)).length
            : 0;
        return `
        <a class="nav-item${n.id === active ? ' is-active' : ''}" href="${buildRouteHash(n.id as Route)}" data-target="${n.id}">
          <span class="nav-dot"></span>
          <span class="nav-label">${escapeHtml(n.label)}</span>
          ${unreadBadge > 0 ? `<span class="nav-badge">${unreadBadge}</span>` : ''}
        </a>
      `;
      },
    )
    .join('');
  return `
    <aside class="sidebar" aria-label="Navigation">
      <div class="sidebar-inner">
        <div class="sidebar-brand">
          <span class="brand-mark">EmAI</span>
          <span class="brand-divider">·</span>
          <span>Customer Center</span>
        </div>

        <div class="sidebar-customer">
          <div class="customer-avatar">${escapeHtml(initials)}</div>
          <div class="customer-info">
            <div class="customer-name" title="${escapeAttr(data.customerName || '')}">${escapeHtml(data.customerName || humanize(data.slug))}</div>
            ${data.projectName ? `<div class="customer-project" title="${escapeAttr(data.projectName)}">${escapeHtml(data.projectName)}</div>` : ''}
            <div class="customer-status">
              <span class="status-pill status-${escapeAttr(data.status)}">
                <span class="status-dot"></span>
                <span>${escapeHtml(data.statusLabel || 'Status unknown')}</span>
              </span>
            </div>
          </div>
        </div>

        <nav class="sidebar-nav" id="sidebar-nav" aria-label="Sections">
          ${navHTML}
        </nav>

        <a class="sidebar-cta" href="${escapeAttr(chatLink)}">
          <span>Ask Kai a question</span>
          <span class="cta-arrow">→</span>
        </a>

        ${currentEmail ? `
          <div class="sidebar-account">
            <div class="account-row">
              <span class="account-email" title="${escapeAttr(currentEmail)}">${escapeHtml(currentEmail)}</span>
              <button type="button" class="account-logout" id="sidebar-logout" title="Sign out" aria-label="Sign out">↩</button>
            </div>
          </div>
        ` : ''}

        <div class="sidebar-footer">
          <a href="https://emai.dev" target="_blank" rel="noopener">emai.dev</a>
        </div>
      </div>
    </aside>
  `;
}

function greetingFor(d: Date): string {
  const h = d.getHours();
  if (h < 5) return 'Good night';
  if (h < 12) return 'Good morning';
  if (h < 18) return 'Good afternoon';
  return 'Good evening';
}

function customerInitials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return '?';
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}

function bindSidebarNav() {
  document.querySelectorAll<HTMLAnchorElement>('.sidebar-nav .nav-item').forEach((link) => {
    link.addEventListener('click', (e) => {
      e.preventDefault();
      const id = link.dataset.target as Route | undefined;
      if (!id) return;
      navigateTo(id);
    });
  });
  document.getElementById('sidebar-logout')?.addEventListener('click', () => {
    void logout();
  });
}

function setActiveNav(id: string) {
  document.querySelectorAll<HTMLAnchorElement>('.sidebar-nav .nav-item').forEach((link) => {
    link.classList.toggle('is-active', link.dataset.target === id);
  });
}


function headerHTML(): string {
  return `
    <header class="hub-header">
      <span class="brand-mark">EmAI</span>
      <span class="brand-divider">·</span>
      <span class="brand-tagline">Customer Center</span>
    </header>
  `;
}

function teaserHTML(data: CenterData): string {
  if (data.briefings.length === 0) return '';
  const latest = data.briefings[0];
  const isUnread = !isRead(latest.id);
  return `
    <section class="teaser-section" data-teaser-id="${escapeAttr(latest.id)}">
      <div class="teaser-card">
        <div class="teaser-eyebrow">
          What's new
          ${isUnread ? '<span class="new-badge">NEW</span>' : ''}
          <span class="teaser-date">${formatRelative(latest.date)}</span>
        </div>
        <h2 class="teaser-title">${escapeHtml(latest.title)}</h2>
        ${latest.excerpt ? `<p class="teaser-excerpt">${escapeHtml(latest.excerpt)}</p>` : ''}
        <button type="button" class="teaser-link" data-action="open-latest">Read full briefing →</button>
      </div>
    </section>
  `;
}

function appsSectionHTML(data: CenterData): string {
  const tiles = data.links.map(linkTileHTML).join('');
  return `
    <section class="apps-section">
      <h2 class="section-heading">Your apps</h2>
      <div class="apps-grid">${tiles}</div>
    </section>
  `;
}

function linkTileHTML(l: AppLink): string {
  return `
    <a class="app-tile" href="${escapeAttr(l.url)}" ${l.external ? 'target="_blank" rel="noopener"' : ''}>
      <div class="app-tile-icon">${escapeHtml(l.icon || '↗')}</div>
      <div class="app-tile-label">${escapeHtml(l.label)}</div>
      ${l.description ? `<div class="app-tile-desc">${escapeHtml(l.description)}</div>` : ''}
      ${l.external ? '<div class="app-tile-external">External ↗</div>' : ''}
    </a>
  `;
}

function teamSectionHTML(data: CenterData): string {
  if (!data.team || data.team.length === 0) return '';
  const cards = data.team.map(teamCardHTML).join('');
  return `
    <section class="team-section">
      <h2 class="section-heading">Your project team</h2>
      <div class="team-grid">${cards}</div>
    </section>
  `;
}

function teamCardHTML(m: TeamMember): string {
  const avatar = m.avatar || initials(m.name);
  const meta = [m.company, m.timezone].filter((v): v is string => !!v).map(escapeHtml).join(' · ');
  const contacts: string[] = [];
  if (m.email) {
    contacts.push(
      `<a class="team-contact" href="mailto:${escapeAttr(m.email)}" title="Email"><span class="team-contact-icon">✉</span>${escapeHtml(m.email)}</a>`,
    );
  }
  if (m.phone) {
    contacts.push(
      `<a class="team-contact" href="tel:${escapeAttr(m.phone)}" title="Phone"><span class="team-contact-icon">☎</span>${escapeHtml(m.phone)}</a>`,
    );
  }
  return `
    <div class="team-card">
      <div class="team-avatar">${escapeHtml(avatar)}</div>
      <div class="team-body">
        <div class="team-name">${escapeHtml(m.name)}</div>
        ${m.role ? `<div class="team-role">${escapeHtml(m.role)}</div>` : ''}
        ${meta ? `<div class="team-meta">${meta}</div>` : ''}
        ${contacts.length ? `<div class="team-contacts">${contacts.join('')}</div>` : ''}
      </div>
    </div>
  `;
}

function initials(name: string): string {
  const parts = name.trim().split(/\s+/);
  if (parts.length === 0) return '?';
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}

function scopeAndRhythmsHTML(data: CenterData): string {
  const hasScope = !!(data.scope && data.scope.trim());
  const hasRhythm = !!(data.heartbeat && data.heartbeat.trim());
  if (!hasScope && !hasRhythm) return '';

  const scopeCard = hasScope
    ? `
      <article class="info-card">
        <div class="info-card-header">
          <span class="info-card-icon">🧭</span>
          <h3>What Kai does for you</h3>
        </div>
        <div class="info-card-body">${renderMarkdown(data.scope!)}</div>
      </article>
    `
    : '';
  const rhythmCard = hasRhythm
    ? `
      <article class="info-card">
        <div class="info-card-header">
          <span class="info-card-icon">🔁</span>
          <h3>Recurring rhythms</h3>
        </div>
        <div class="info-card-body">${renderMarkdown(data.heartbeat!)}</div>
      </article>
    `
    : '';

  return `
    <section class="info-section">
      <div class="info-grid">${scopeCard}${rhythmCard}</div>
    </section>
  `;
}

function channelsSectionHTML(data: CenterData): string {
  if (!data.channels || data.channels.length === 0) return '';
  const items = data.channels
    .map(
      (c) => `
      <li class="channel-item">
        <span class="channel-icon">${escapeHtml(channelIcon(c.kind))}</span>
        <div class="channel-text">
          <div class="channel-label">${escapeHtml(c.label)}</div>
          ${c.hint ? `<div class="channel-hint">${escapeHtml(c.hint)}</div>` : ''}
        </div>
      </li>
    `,
    )
    .join('');
  return `
    <section class="channels-section">
      <h2 class="section-heading">Where you can reach Kai</h2>
      <ul class="channels-list">${items}</ul>
    </section>
  `;
}

function channelIcon(kind: string): string {
  switch (kind) {
    case 'webchat': return '💬';
    case 'telegram': return '✈️';
    case 'email': return '📧';
    case 'voice': return '📞';
    default: return '↗';
  }
}

function briefingsSectionHTML(data: CenterData): string {
  const filtered = filterBriefings(data.briefings, briefingFilter);
  const showSearch = data.briefings.length >= SEARCH_THRESHOLD;
  const allRead = data.briefings.length > 0 && data.briefings.every((b) => isRead(b.id));

  const search = showSearch
    ? `
      <div class="briefings-toolbar">
        <input
          type="search"
          class="briefings-search"
          id="briefings-search"
          placeholder="Search briefings…"
          value="${escapeAttr(briefingFilter)}"
        />
        ${
          allRead
            ? ''
            : '<button type="button" class="ghost-btn small" data-action="mark-all-read">Mark all as read</button>'
        }
      </div>
    `
    : !allRead && data.briefings.length > 0
      ? `
        <div class="briefings-toolbar align-end">
          <button type="button" class="ghost-btn small" data-action="mark-all-read">Mark all as read</button>
        </div>
      `
      : '';

  const list = filtered.length
    ? filtered.map((b, i) => briefingHTML(b, i === 0 && briefingFilter === '' && i === 0)).join('')
    : briefingFilter
      ? `<div class="briefings-empty"><p>No briefings match "${escapeHtml(briefingFilter)}".</p></div>`
      : `
          <div class="briefings-empty">
            <p>No briefings yet.</p>
            <p class="dim">Your assistant will share weekly summaries and meeting notes here.</p>
          </div>
        `;

  return `
    <section class="briefings-section" id="briefings">
      <h2 class="section-heading">Briefings</h2>
      ${search}
      <div class="briefings-list">${list}</div>
    </section>
  `;
}

function briefingHTML(b: Briefing, openByDefault: boolean): string {
  const unread = !isRead(b.id);
  const open = openByDefault || hashMatchesBriefing(b.id);
  return `
    <details class="briefing${unread ? ' unread' : ''}" ${open ? 'open' : ''} data-id="${escapeAttr(b.id)}" id="briefing-${escapeAttr(b.id)}">
      <summary>
        <div class="briefing-meta">
          <span class="briefing-date">${formatRelative(b.date)}</span>
          <span class="briefing-title">${escapeHtml(b.title)}</span>
          ${unread ? '<span class="new-badge">NEW</span>' : ''}
        </div>
        ${b.excerpt ? `<p class="briefing-excerpt">${escapeHtml(b.excerpt)}</p>` : ''}
        <div class="briefing-actions">
          <button type="button" class="link-btn" data-action="copy-link" data-id="${escapeAttr(b.id)}">Copy link</button>
        </div>
      </summary>
      <div class="briefing-body">${b.body ? renderMarkdown(b.body) : '<em>No additional content.</em>'}</div>
    </details>
  `;
}

function bindBriefingHandlers() {
  // Mark-as-read when expanded.
  document.querySelectorAll<HTMLDetailsElement>('details.briefing').forEach((det) => {
    det.addEventListener('toggle', () => {
      if (det.open) {
        const id = det.dataset.id!;
        markRead(id);
        det.classList.remove('unread');
        det.querySelector('.new-badge')?.remove();
        // Refresh teaser badge if this was the latest one.
        const teaserSection = document.querySelector<HTMLElement>('.teaser-section');
        if (teaserSection?.dataset.teaserId === id) {
          teaserSection.querySelector('.teaser-eyebrow .new-badge')?.remove();
        }
      }
    });
  });

  // Copy-link buttons.
  document.querySelectorAll<HTMLButtonElement>('.link-btn[data-action="copy-link"]').forEach((btn) => {
    btn.addEventListener('click', async (e) => {
      e.preventDefault();
      e.stopPropagation();
      const id = btn.dataset.id!;
      const url = `${location.origin}${location.pathname}${location.search}#/briefings/${id}`;
      try {
        await navigator.clipboard.writeText(url);
        const orig = btn.textContent;
        btn.textContent = 'Copied!';
        setTimeout(() => { btn.textContent = orig; }, 1500);
      } catch { /* ignore */ }
    });
  });

  // Mark-all-read.
  document.querySelectorAll<HTMLButtonElement>('button[data-action="mark-all-read"]').forEach((btn) => {
    btn.addEventListener('click', () => {
      if (!currentData) return;
      currentData.briefings.forEach((b) => markRead(b.id));
      renderHub(currentData);
    });
  });
}

function bindSearchHandler() {
  const input = document.getElementById('briefings-search') as HTMLInputElement | null;
  if (!input) return;
  input.addEventListener('input', () => {
    briefingFilter = input.value.trim();
    if (currentData) {
      const section = document.getElementById('briefings');
      if (section) {
        section.outerHTML = briefingsSectionHTML(currentData);
        bindBriefingHandlers();
        bindSearchHandler();
        // Restore focus + caret.
        const newInput = document.getElementById('briefings-search') as HTMLInputElement | null;
        if (newInput) {
          newInput.focus();
          newInput.setSelectionRange(briefingFilter.length, briefingFilter.length);
        }
      }
    }
  });
}

function bindTeaserHandler() {
  document.querySelector('button[data-action="open-latest"]')?.addEventListener('click', () => {
    if (!currentData || currentData.briefings.length === 0) return;
    navigateTo('briefings', currentData.briefings[0].id);
  });
}

function hashMatchesBriefing(id: string): boolean {
  const r = parseRoute();
  return r.route === 'briefings' && r.param === id;
}

function filterBriefings(list: Briefing[], q: string): Briefing[] {
  if (!q) return list;
  const lc = q.toLowerCase();
  return list.filter(
    (b) =>
      b.title.toLowerCase().includes(lc) ||
      (b.excerpt || '').toLowerCase().includes(lc) ||
      (b.body || '').toLowerCase().includes(lc),
  );
}

// ---------- Read state ----------

function readKey(): string {
  return `customer-center:read:${slug}`;
}
function readSet(): Set<string> {
  try {
    return new Set(JSON.parse(localStorage.getItem(readKey()) || '[]'));
  } catch {
    return new Set();
  }
}
function isRead(id: string): boolean {
  return readSet().has(id);
}
function markRead(id: string) {
  const s = readSet();
  s.add(id);
  try {
    localStorage.setItem(readKey(), JSON.stringify([...s]));
  } catch { /* ignore */ }
  // Sidebar shows the unread count — refresh the badge.
  refreshSidebarNavState();
}

function refreshSidebarNavState() {
  if (!currentData) return;
  const sidebar = document.querySelector('.sidebar');
  if (!sidebar) return;
  // Only the nav counter and unread badge need updating; rebuild that subtree.
  const nav = sidebar.querySelector('.sidebar-nav');
  if (!nav) return;
  const items = navItems(currentData);
  const active = parseRoute().route;
  nav.innerHTML = items
    .map((n) => {
      const unread =
        n.id === 'briefings' && currentData!.briefings
          ? currentData!.briefings.filter((b) => !isRead(b.id)).length
          : 0;
      return `
        <a class="nav-item${n.id === active ? ' is-active' : ''}" href="${buildRouteHash(n.id as Route)}" data-target="${n.id}">
          <span class="nav-dot"></span>
          <span class="nav-label">${escapeHtml(n.label)}</span>
          ${unread > 0 ? `<span class="nav-badge">${unread}</span>` : ''}
        </a>
      `;
    })
    .join('');
  bindSidebarNav();
}

// ---------- Helpers ----------

function renderError(title: string, message: string) {
  document.title = 'Hub — EmAI';
  app.innerHTML = `
    <div class="hub-shell">
      ${headerHTML()}
      <main class="hub-main">
        <div class="error-card">
          <div class="error-icon" aria-hidden="true">!</div>
          <h2>${escapeHtml(title)}</h2>
          <p>${message}</p>
        </div>
      </main>
      <footer class="hub-footer"><a href="https://emai.dev" target="_blank" rel="noopener">emai.dev</a></footer>
    </div>
  `;
}

function humanize(s: string): string {
  return s.split('-').filter(Boolean).map((w) => w.charAt(0).toUpperCase() + w.slice(1)).join(' ');
}

function formatRelative(iso?: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '';
  const diffMs = Date.now() - d.getTime();
  const sec = Math.floor(diffMs / 1000);
  if (sec < 60) return 'just now';
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} min ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} h ago`;
  const day = Math.floor(hr / 24);
  if (day < 7) return `${day} day${day === 1 ? '' : 's'} ago`;
  const wk = Math.floor(day / 7);
  if (wk < 5) return `${wk} week${wk === 1 ? '' : 's'} ago`;
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

function renderMarkdown(text: string): string {
  try {
    return marked.parse(text) as string;
  } catch {
    return escapeHtml(text);
  }
}

function escapeHtml(text: string): string {
  const div = document.createElement('div');
  div.textContent = text ?? '';
  return div.innerHTML;
}

function escapeAttr(text: string): string {
  return escapeHtml(text).replace(/"/g, '&quot;');
}

// ---------- Team access (chat login management) ----------

function accessApiBase(): string {
  return `/api/center/${encodeURIComponent(slug)}/users`;
}

function accessUserUrl(email: string, suffix = ''): string {
  return `/api/center/${encodeURIComponent(slug)}/users/${encodeURIComponent(email)}${suffix}`;
}

async function loadAccessUsers() {
  accessError = null;
  try {
    const res = await fetch(accessApiBase(), { credentials: 'same-origin' });
    if (!res.ok) {
      accessError = `Could not load access list (HTTP ${res.status}).`;
      accessUsers = [];
    } else {
      accessUsers = (await res.json()) as AccessUser[];
    }
  } catch (e) {
    accessError = `Network error: ${String(e)}.`;
    accessUsers = [];
  }
  refreshAccessSection();
  // The overview stats and sidebar badges depend on the access count.
  if (currentData && parseRoute().route === 'overview') {
    const container = document.getElementById('page-content');
    if (container) {
      container.innerHTML = overviewPageHTML(currentData);
      bindTeaserHandler();
      bindOverviewHandlers();
    }
  }
}

function refreshAccessSection() {
  const section = document.querySelector<HTMLElement>('#team-access');
  if (!section) return;
  section.outerHTML = accessSectionHTML();
  bindAccessHandlers();
}

function accessSectionHTML(): string {
  const list = (() => {
    if (accessUsers === null) return '<div class="dim">Loading…</div>';
    if (accessError) return `<div class="access-error">${escapeHtml(accessError)}</div>`;
    if (accessUsers.length === 0) {
      return '<div class="dim">No one has access yet. Add the first email below.</div>';
    }
    return `
      <ul class="access-list">
        ${accessUsers.map(accessRowHTML).join('')}
      </ul>
    `;
  })();

  return `
    <section class="access-section" id="team-access">
      <h2 class="section-heading">Team access</h2>
      <p class="section-sub">Anyone in this list can log in to chat with their email and password. Share initial passwords with each person directly — passwords are never sent by email.</p>
      ${list}
      <form class="access-form" id="access-add-form" autocomplete="off">
        <div class="access-form-row">
          <input type="email" name="email" placeholder="name@company.com" required class="access-input" autocomplete="off" />
          <input type="text" name="password" placeholder="initial password (min 8)" required minlength="8" class="access-input" autocomplete="new-password" />
          <button type="button" class="ghost-btn small" data-action="generate-password">Generate</button>
          <button type="submit" class="primary-btn small">Add user</button>
        </div>
        <div class="access-form-hint dim" id="access-form-hint"></div>
      </form>
    </section>
  `;
}

function accessRowHTML(u: AccessUser): string {
  const updated = u.passwordUpdatedAt ? formatRelative(u.passwordUpdatedAt) : '';
  return `
    <li class="access-row" data-email="${escapeAttr(u.email)}">
      <div class="access-row-main">
        <span class="access-email">${escapeHtml(u.email)}</span>
        ${updated ? `<span class="access-updated dim">password updated ${escapeHtml(updated)}</span>` : ''}
      </div>
      <div class="access-row-actions">
        <button type="button" class="link-btn" data-action="reset" data-email="${escapeAttr(u.email)}">Reset password</button>
        <button type="button" class="link-btn danger" data-action="remove" data-email="${escapeAttr(u.email)}">Remove</button>
      </div>
    </li>
  `;
}

function bindAccessHandlers() {
  const form = document.getElementById('access-add-form') as HTMLFormElement | null;
  if (form) {
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const email = (form.elements.namedItem('email') as HTMLInputElement).value.trim().toLowerCase();
      const password = (form.elements.namedItem('password') as HTMLInputElement).value;
      const hint = document.getElementById('access-form-hint');
      if (hint) hint.textContent = 'Adding…';
      try {
        const res = await fetch(accessApiBase(), {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ email, password }),
        });
        if (!res.ok) {
          const body = await res.json().catch(() => ({}));
          if (hint) hint.textContent = body.error ? `Error: ${body.error}` : `Failed (HTTP ${res.status}).`;
          return;
        }
        form.reset();
        if (hint) hint.textContent = `Added ${email}. Share the password with them.`;
        await loadAccessUsers();
      } catch (err) {
        if (hint) hint.textContent = `Network error: ${String(err)}.`;
      }
    });

    form.querySelector<HTMLButtonElement>('button[data-action="generate-password"]')?.addEventListener('click', () => {
      const passInput = form.elements.namedItem('password') as HTMLInputElement;
      passInput.value = generatePassword();
      passInput.type = 'text';
    });
  }

  document.querySelectorAll<HTMLButtonElement>('button[data-action="reset"]').forEach((btn) => {
    btn.addEventListener('click', async () => {
      const email = btn.dataset.email!;
      const newPass = window.prompt(`New password for ${email} (min 8 chars):`, generatePassword());
      if (!newPass) return;
      if (newPass.length < 8) {
        window.alert('Password must be at least 8 characters.');
        return;
      }
      try {
        const res = await fetch(accessUserUrl(email, '/password'), {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ password: newPass }),
        });
        if (!res.ok) {
          const body = await res.json().catch(() => ({}));
          window.alert(body.error ? `Error: ${body.error}` : `Failed (HTTP ${res.status}).`);
          return;
        }
        window.alert(`Password reset for ${email}. New password:\n\n${newPass}\n\nShare this with them — it won't be shown again.`);
        await loadAccessUsers();
      } catch (err) {
        window.alert(`Network error: ${String(err)}`);
      }
    });
  });

  document.querySelectorAll<HTMLButtonElement>('button[data-action="remove"]').forEach((btn) => {
    btn.addEventListener('click', async () => {
      const email = btn.dataset.email!;
      if (!window.confirm(`Remove ${email} from the access list? They will be logged out at the end of their current session.`)) return;
      try {
        const res = await fetch(accessUserUrl(email), { method: 'DELETE', credentials: 'same-origin' });
        if (!res.ok && res.status !== 404) {
          const body = await res.json().catch(() => ({}));
          window.alert(body.error ? `Error: ${body.error}` : `Failed (HTTP ${res.status}).`);
          return;
        }
        await loadAccessUsers();
      } catch (err) {
        window.alert(`Network error: ${String(err)}`);
      }
    });
  });
}

function generatePassword(): string {
  // 16-char alphanumeric, decent entropy, easy to share verbally if needed.
  const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789';
  const buf = new Uint8Array(16);
  crypto.getRandomValues(buf);
  let out = '';
  for (let i = 0; i < buf.length; i++) {
    out += alphabet[buf[i] % alphabet.length];
  }
  return out;
}
