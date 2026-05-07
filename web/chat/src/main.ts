import './style.css';
import { GatewayClient } from './gateway';
import { marked } from 'marked';
import { loadBranding, applyBranding, DEFAULT_BRANDING, type Branding } from '@branding/loader';

marked.setOptions({ breaks: true, gfm: true });

// Parse URL: /chat/<slug>
const path = window.location.pathname;
const slugMatch = path.match(/\/chat\/([a-z0-9-]+)/);
const slug = slugMatch?.[1] || '';
const customerName = slug.split('-').filter(Boolean).map(w => w.charAt(0).toUpperCase() + w.slice(1)).join(' ');

let b: Branding = DEFAULT_BRANDING;

const app = document.querySelector<HTMLDivElement>('#app')!;

interface SessionInfo {
  email: string;
  slug: string;
}

interface SavedMessage { role: 'user' | 'agent'; text: string; ts: number; }
const STORAGE_KEY = `kai-chat-${slug}`;
const MAX_SAVED = 100;

function saveMessage(role: 'user' | 'agent', text: string) {
  try {
    const saved: SavedMessage[] = JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]');
    saved.push({ role, text, ts: Date.now() });
    if (saved.length > MAX_SAVED) saved.splice(0, saved.length - MAX_SAVED);
    localStorage.setItem(STORAGE_KEY, JSON.stringify(saved));
  } catch { /* storage full or unavailable */ }
}

function loadSavedMessages(): SavedMessage[] {
  try {
    return JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]');
  } catch { return []; }
}

async function checkSession(): Promise<SessionInfo | null> {
  if (!slug) return null;
  try {
    const res = await fetch(`/api/chat/${encodeURIComponent(slug)}/me`, { credentials: 'same-origin' });
    if (!res.ok) return null;
    return (await res.json()) as SessionInfo;
  } catch {
    return null;
  }
}

bootstrap();

async function bootstrap() {
  const [branding, session] = await Promise.all([loadBranding(), slug ? checkSession() : Promise.resolve(null)]);
  b = branding;
  applyBranding(b, { docTitle: customerName ? `${customerName} — ${b.copy.chat.docTitle}` : b.copy.chat.docTitle });
  if (!slug) {
    renderMissingSlug();
    return;
  }
  if (session) {
    renderChat(session);
  } else {
    renderLogin();
  }
}

function renderMissingSlug() {
  app.innerHTML = `
    <div class="page-error">
      <div class="error-card">
        <div class="login-eyebrow">${escapeHtml(b.name)}</div>
        <h2>${escapeHtml(b.copy.chat.missingSlugTitle)}</h2>
        <p>This page can only be opened through a project URL like <code>/chat/your-project</code>.</p>
        <p>${escapeHtml(b.copy.chat.missingSlugHelp)}</p>
      </div>
    </div>
  `;
}

function renderLogin(errorMsg?: string) {
  app.innerHTML = `
    <div class="login-shell">
      <header class="login-header">
        <a class="brand-lockup" href="/" aria-label="${escapeHtml(b.name)}">
          <img class="brand-lockup-mark" src="${escapeHtml(b.faviconUrl)}" alt="" />
          <span class="brand-lockup-word">${escapeHtml(b.name)}</span>
        </a>
      </header>
      <main class="login-main">
        <div class="login-card">
          <div class="login-eyebrow">${customerName ? escapeHtml(customerName) : 'Your project'}</div>
          <h1 class="login-title">${escapeHtml(b.copy.chat.signInTitle)}</h1>
          <p class="login-subtitle">${escapeHtml(b.copy.chat.signInSubtitle)}</p>
          ${errorMsg ? `<div class="login-error">${escapeHtml(errorMsg)}</div>` : ''}
          <form id="login-form" autocomplete="on">
            <label class="login-field">
              <span>Email</span>
              <input type="email" name="email" required autocomplete="username" autofocus placeholder="you@${escapeHtml(b.domain)}" />
            </label>
            <label class="login-field">
              <span>Password</span>
              <input type="password" name="password" required autocomplete="current-password" placeholder="••••••••" />
            </label>
            <button type="submit" class="login-submit">Sign in</button>
          </form>
          <p class="login-help">Forgot your password? Ask your project lead to reset it from the Workspace.</p>
        </div>
      </main>
      <footer class="login-footer">
        <a href="${escapeHtml(b.domainUrl)}" target="_blank" rel="noopener">${escapeHtml(b.domain)}</a>
      </footer>
    </div>
  `;

  const form = document.getElementById('login-form') as HTMLFormElement;
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const submit = form.querySelector('button[type="submit"]') as HTMLButtonElement;
    submit.disabled = true;
    submit.textContent = 'Signing in…';
    const email = (form.elements.namedItem('email') as HTMLInputElement).value.trim().toLowerCase();
    const password = (form.elements.namedItem('password') as HTMLInputElement).value;
    try {
      const res = await fetch(`/api/chat/${encodeURIComponent(slug)}/login`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email, password }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        renderLogin(body.error === 'invalid login' ? 'Email or password is incorrect.' : (body.error || `Sign-in failed (HTTP ${res.status}).`));
        return;
      }
      const session = (await res.json()) as SessionInfo;
      session.slug = slug;
      renderChat(session);
    } catch (err) {
      renderLogin(`Network error: ${String(err)}`);
    }
  });
}

function renderChat(session: SessionInfo) {
  app.innerHTML = `
    <div class="chat-container">
      <header class="chat-header">
        <div class="header-left">
          <a class="brand-lockup" href="/" aria-label="${escapeHtml(b.name)}">
            <img class="brand-lockup-mark" src="${escapeHtml(b.faviconUrl)}" alt="" />
            <span class="brand-lockup-word">${escapeHtml(b.name)}</span>
          </a>
          <span class="header-divider" aria-hidden="true">/</span>
          <div class="header-context">
            <span class="header-context-label">${customerName ? escapeHtml(customerName) : 'Project assistant'}</span>
            <span class="header-context-meta">${escapeHtml(b.agentName)}</span>
          </div>
        </div>
        <div class="header-right">
          <span class="live-badge"><span class="status connecting" id="status">Connecting…</span></span>
          <span class="signed-in-as" title="Signed in as">${escapeHtml(session.email)}</span>
          <button class="icon-btn" id="logout-btn" title="Sign out" aria-label="Sign out">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><polyline points="16 17 21 12 16 7"/><line x1="21" y1="12" x2="9" y2="12"/></svg>
          </button>
        </div>
      </header>
      <main class="messages" id="messages" aria-live="polite">
        <div class="welcome" id="welcome">
          <img class="welcome-mark" src="${escapeHtml(b.faviconUrl)}" alt="" />
          <h2>${session.email ? 'Welcome, ' + escapeHtml(session.email.split('@')[0]) + '.' : 'Welcome.'}</h2>
          <p>${escapeHtml(b.copy.chat.welcomeBody)}</p>
        </div>
      </main>
      <footer class="input-area">
        <form id="chat-form">
          <input type="text" id="input" placeholder="${escapeHtml(b.copy.chat.typingPlaceholder)}" autocomplete="off" disabled />
          <button type="submit" id="send-btn" disabled aria-label="Send">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><line x1="12" y1="19" x2="12" y2="5"/><polyline points="5 12 12 5 19 12"/></svg>
          </button>
        </form>
      </footer>
      <div class="page-footer">
        <a href="${escapeHtml(b.domainUrl)}" target="_blank" rel="noopener">${escapeHtml(b.footerCredit)}</a>
      </div>
    </div>
  `;

  const messagesEl = document.getElementById('messages')!;
  const welcomeEl = document.getElementById('welcome')!;
  const inputEl = document.getElementById('input') as HTMLInputElement;
  const sendBtn = document.getElementById('send-btn') as HTMLButtonElement;
  const statusEl = document.getElementById('status')!;
  const form = document.getElementById('chat-form')!;

  document.getElementById('logout-btn')!.addEventListener('click', async () => {
    try {
      await fetch(`/api/chat/${encodeURIComponent(slug)}/logout`, { method: 'POST', credentials: 'same-origin' });
    } catch { /* ignore */ }
    location.reload();
  });

  let isStreaming = false;
  let currentStreamEl: HTMLElement | null = null;
  let streamedText = '';
  let serverHistoryLoaded = false;

  function showTyping() {
    if (document.getElementById('typing-indicator')) return;
    const el = document.createElement('div');
    el.id = 'typing-indicator';
    el.className = 'message agent typing';
    el.innerHTML = `<div class="message-bubble"><div class="message-sender">${escapeHtml(b.agentName)}</div><div class="typing-dots"><span></span><span></span><span></span></div></div>`;
    messagesEl.appendChild(el);
    scrollToBottom(messagesEl);
  }

  function hideTyping() {
    document.getElementById('typing-indicator')?.remove();
  }

  const client = new GatewayClient(slug, {
    onReady: () => {
      statusEl.textContent = 'Online';
      statusEl.classList.remove('error', 'connecting');
      statusEl.classList.add('online');
      inputEl.disabled = false;
      sendBtn.disabled = false;
      inputEl.focus();
    },

    onHistory: (history) => {
      serverHistoryLoaded = true;
      if (history.length > 0) {
        welcomeEl.style.display = 'none';
        messagesEl.querySelectorAll('.message').forEach((el) => el.remove());
        history.forEach((msg) => {
          const role: 'user' | 'agent' = msg.role === 'user' ? 'user' : 'agent';
          appendMessage(messagesEl, role, msg.text, role === 'agent');
        });
        scrollToBottom(messagesEl);
      } else {
        // Fall back to local cache.
        const saved = loadSavedMessages();
        if (saved.length > 0) {
          welcomeEl.style.display = 'none';
          saved.forEach((msg) => appendMessage(messagesEl, msg.role, msg.text, msg.role === 'agent'));
          scrollToBottom(messagesEl);
        }
      }
    },

    onChatDelta: (text, _runId) => {
      if (!isStreaming) {
        isStreaming = true;
        streamedText = '';
        hideTyping();
        currentStreamEl = appendMessage(messagesEl, 'agent', '');
        currentStreamEl.classList.add('streaming');
      }
      streamedText += text;
      if (currentStreamEl) {
        currentStreamEl.querySelector('.message-text')!.textContent = streamedText;
      }
      scrollToBottom(messagesEl);
    },

    onChatFinal: (text, _runId) => {
      const finalText = text || streamedText;
      if (currentStreamEl) {
        currentStreamEl.querySelector('.message-text')!.innerHTML = renderMarkdown(finalText);
        currentStreamEl.classList.remove('streaming');
      } else {
        appendMessage(messagesEl, 'agent', finalText, true);
      }
      saveMessage('agent', finalText);
      isStreaming = false;
      currentStreamEl = null;
      streamedText = '';
      inputEl.disabled = false;
      sendBtn.disabled = false;
      inputEl.focus();
      scrollToBottom(messagesEl);
    },

    onChatError: (error) => {
      hideTyping();
      // Connection-level errors (e.g. upstream unreachable) come in before any
      // user message — show them in the status bar, not as a chat bubble, and
      // don't duplicate on reconnect.
      if (!isStreaming && !currentStreamEl) {
        statusEl.textContent = error;
        statusEl.classList.remove('online', 'connecting');
        statusEl.classList.add('error');
        return;
      }
      if (currentStreamEl) {
        currentStreamEl.querySelector('.message-text')!.textContent = `Error: ${error}`;
        currentStreamEl.classList.remove('streaming');
        currentStreamEl.classList.add('error');
      } else {
        appendMessage(messagesEl, 'agent', `Error: ${error}`);
      }
      isStreaming = false;
      currentStreamEl = null;
      inputEl.disabled = false;
      sendBtn.disabled = false;
    },

    onDisconnected: () => {
      statusEl.textContent = 'Reconnecting…';
      statusEl.classList.remove('online');
      statusEl.classList.add('error');
      inputEl.disabled = true;
      sendBtn.disabled = true;
    },

    onError: async (error) => {
      // If the WS was rejected because our session expired, fall back to login.
      const fresh = await checkSession();
      if (!fresh) {
        renderLogin('Your session has expired. Please sign in again.');
        return;
      }
      console.error('[chat] error:', error);
    },
  });

  form.addEventListener('submit', (e) => {
    e.preventDefault();
    const text = inputEl.value.trim();
    if (!text || isStreaming) return;
    welcomeEl.style.display = 'none';
    appendMessage(messagesEl, 'user', text);
    saveMessage('user', text);
    scrollToBottom(messagesEl);
    inputEl.value = '';
    inputEl.disabled = true;
    sendBtn.disabled = true;
    client.send(text);
    showTyping();
  });

  client.connect();

  // If history doesn't arrive within a few seconds (e.g., empty session), fall back to localStorage.
  setTimeout(() => {
    if (serverHistoryLoaded) return;
    const saved = loadSavedMessages();
    if (saved.length > 0 && messagesEl.querySelectorAll('.message').length === 0) {
      welcomeEl.style.display = 'none';
      saved.forEach((msg) => appendMessage(messagesEl, msg.role, msg.text, msg.role === 'agent'));
      scrollToBottom(messagesEl);
    }
  }, 1500);
}

function appendMessage(container: HTMLElement, role: 'user' | 'agent', text: string, isMarkdown = false): HTMLElement {
  const el = document.createElement('div');
  el.className = `message ${role}`;
  const content = role === 'agent' && isMarkdown ? renderMarkdown(text) : escapeHtml(text);
  const sender = role === 'agent' ? `<div class="message-sender">${escapeHtml(b.agentName)}</div>` : '';
  el.innerHTML = `<div class="message-bubble">${sender}<div class="message-text">${content}</div></div>`;
  container.appendChild(el);
  return el;
}

function scrollToBottom(container: HTMLElement) {
  requestAnimationFrame(() => {
    container.scrollTop = container.scrollHeight;
  });
}

function escapeHtml(text: string): string {
  const div = document.createElement('div');
  div.textContent = text ?? '';
  return div.innerHTML;
}

function renderMarkdown(text: string): string {
  try {
    let html = marked.parse(text) as string;
    html = html.replace(/<pre><code(.*?)>([\s\S]*?)<\/code><\/pre>/g,
      (_match, attrs, code) => {
        return `<div class="code-block"><button class="copy-btn" title="Copy" aria-label="Copy code">&#x1f4cb;</button><pre><code${attrs}>${code}</code></pre></div>`;
      });
    return html;
  } catch {
    return escapeHtml(text);
  }
}

document.addEventListener('click', (e) => {
  const btn = (e.target as HTMLElement).closest('.copy-btn');
  if (!btn) return;
  const code = btn.parentElement?.querySelector('code');
  if (code) {
    navigator.clipboard.writeText(code.textContent || '').then(() => {
      btn.textContent = '✓';
      setTimeout(() => { btn.textContent = '\u{1f4cb}'; }, 1500);
    });
  }
});
