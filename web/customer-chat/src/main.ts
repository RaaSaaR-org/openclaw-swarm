import './style.css';
import { GatewayClient } from './gateway';
import type { AgentIdentity } from './gateway';
import { marked } from 'marked';

// Configure marked for safe rendering
marked.setOptions({
  breaks: true,
  gfm: true,
});

// Parse URL: /chat/<slug>?token=<token>&host=<host>
const path = window.location.pathname;
const slugMatch = path.match(/\/chat\/([a-z0-9-]+)/);
const params = new URLSearchParams(window.location.search);

const slug = slugMatch?.[1] || 'demo';
const customerName = slug.split('-').map(w => w.charAt(0).toUpperCase() + w.slice(1)).join(' ');
const token = params.get('token') || '';
const wsHost = params.get('host') || `ws://${window.location.hostname}:18790`;

// Set page title
document.title = `EmAI Kai — ${customerName}`;

// State
let isStreaming = false;
let currentStreamEl: HTMLElement | null = null;
let streamedText = '';
let hasFatalError = false;

// Render app shell
document.querySelector<HTMLDivElement>('#app')!.innerHTML = `
  <div class="chat-container">
    <header class="chat-header">
      <div class="header-left">
        <div class="avatar">🤖</div>
        <div class="header-info">
          <h1 class="agent-name">Kai</h1>
          <span class="header-subtitle">${customerName} — Projekt-Assistent</span>
          <span class="status" id="status">Verbindet...</span>
        </div>
      </div>
      <div class="header-right">
        <button class="info-btn" id="info-btn" title="Info & Hilfe">?</button>
        <span class="brand">EmAI</span>
      </div>
    </header>
    <main class="messages" id="messages">
      <div class="welcome" id="welcome">
        <div class="welcome-emoji">🤖</div>
        <h2>Willkommen!</h2>
        <p>Schreibe eine Nachricht, um mit deinem Projekt-Assistenten zu starten.</p>
      </div>
    </main>
    <footer class="input-area">
      <form id="chat-form">
        <input type="text" id="input" placeholder="Nachricht schreiben..." autocomplete="off" disabled />
        <button type="submit" id="send-btn" disabled>
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="22" y1="2" x2="11" y2="13"/><polygon points="22 2 15 22 11 13 2 9 22 2"/></svg>
        </button>
      </form>
    </footer>
    <div class="page-footer">
      <a href="https://emai.dev" target="_blank">made with <span class="heart">&hearts;</span> and AI by emai.dev</a>
    </div>
  </div>
  <div class="modal-overlay" id="info-modal">
    <div class="modal">
      <div class="modal-header">
        <h2>Kai — Projekt-Assistent</h2>
        <button class="modal-close" id="modal-close">&times;</button>
      </div>
      <div class="modal-body">
        <p class="modal-intro">Kai ist dein persoenlicher KI-Assistent fuer die Zusammenarbeit mit <strong>EmAI</strong>.</p>

        <h3>Was kann Kai?</h3>
        <ul>
          <li><strong>Projektmanagement</strong> — Aufgaben erstellen, verfolgen und priorisieren</li>
          <li><strong>Termine</strong> — Meetings planen, dokumentieren und erinnern</li>
          <li><strong>Statusberichte</strong> — Projektfortschritt und Meilensteine zusammenfassen</li>
          <li><strong>Dokumentation</strong> — Forschungsnotizen und Ergebnisse festhalten</li>
        </ul>

        <h3>Tipps</h3>
        <ul>
          <li>Stelle Fragen auf Deutsch — Kai antwortet in deiner Sprache</li>
          <li>Sei konkret: <em>"Was sind die offenen Tasks?"</em> funktioniert besser als <em>"Was gibt es Neues?"</em></li>
          <li>Kai hat nur Zugriff auf dein Projekt — keine anderen Kundendaten</li>
        </ul>

        <h3>Datenschutz</h3>
        <p>Deine Nachrichten werden ausschliesslich fuer die Projektarbeit verwendet. Kai hat keinen Zugriff auf Daten anderer Kunden oder interne EmAI-Informationen.</p>

        <div class="modal-footer">
          <p>Powered by <strong>EmAI</strong> — Cognitive Robotics. Understand. Deploy.</p>
        </div>
      </div>
    </div>
  </div>
`;

const messagesEl = document.getElementById('messages')!;
const welcomeEl = document.getElementById('welcome')!;
const inputEl = document.getElementById('input') as HTMLInputElement;
const sendBtn = document.getElementById('send-btn') as HTMLButtonElement;
const statusEl = document.getElementById('status')!;
const agentNameEl = document.querySelector('.agent-name')!;
const avatarEl = document.querySelector('.avatar')!;
const form = document.getElementById('chat-form')!;

// Info modal
const infoModal = document.getElementById('info-modal')!;
document.getElementById('info-btn')!.addEventListener('click', () => {
  infoModal.classList.add('open');
});
document.getElementById('modal-close')!.addEventListener('click', () => {
  infoModal.classList.remove('open');
});
infoModal.addEventListener('click', (e) => {
  if (e.target === infoModal) infoModal.classList.remove('open');
});

// Token check
if (!token) {
  statusEl.textContent = 'Kein Zugangstoken';
  statusEl.classList.add('error');
  welcomeEl.style.display = 'none';
  inputEl.disabled = true;
  sendBtn.disabled = true;
  messagesEl.innerHTML = `
    <div class="error-page">
      <div class="error-icon">&#x1f517;</div>
      <h2>Kein Zugangstoken</h2>
      <p class="error-detail">Diese Seite kann nur ueber einen persoenlichen Zugangslink geoeffnet werden.</p>
      <div class="error-steps">
        <h3>Was du tun kannst:</h3>
        <ol>
          <li>Verwende den <strong>Zugangslink</strong>, den du von deinem EmAI-Team erhalten hast.</li>
          <li>Der Link sieht so aus: <code>…/chat/dein-projekt?token=…</code></li>
          <li>Falls du keinen Link hast, kontaktiere dein <strong>EmAI-Team</strong> fuer einen Zugang.</li>
        </ol>
      </div>
      <div class="error-contact">
        <p>Brauchst du Hilfe? Besuche <a href="https://emai.dev" target="_blank">emai.dev</a></p>
      </div>
    </div>
  `;
} else {
  // Gateway client
  const client = new GatewayClient(wsHost, token, {
    onConnected: async (identity: AgentIdentity | null) => {
      statusEl.textContent = 'Online';
      statusEl.classList.remove('error');
      statusEl.classList.add('online');
      inputEl.disabled = false;
      sendBtn.disabled = false;
      inputEl.focus();

      if (identity) {
        agentNameEl.textContent = identity.name;
        if (identity.emoji) avatarEl.textContent = identity.emoji;
      }

      // Load history
      const history = await client.loadHistory();
      if (history.length > 0) {
        welcomeEl.style.display = 'none';
        history.forEach((msg) => {
          const role: 'user' | 'agent' = msg.role === 'user' ? 'user' : 'agent';
          appendMessage(role, msg.text, role === 'agent');
        });
        scrollToBottom();
      }
    },

    onDisconnected: () => {
      if (hasFatalError) return; // don't overwrite error page
      statusEl.textContent = 'Verbindung unterbrochen...';
      statusEl.classList.remove('online');
      statusEl.classList.add('error');
      inputEl.disabled = true;
      sendBtn.disabled = true;
    },

    onChatDelta: (text: string, _runId: string) => {
      if (!isStreaming) {
        isStreaming = true;
        streamedText = '';
        currentStreamEl = appendMessage('agent', '');
        currentStreamEl.classList.add('streaming');
      }
      streamedText = text;
      if (currentStreamEl) {
        // Show plain text while streaming (faster)
        currentStreamEl.querySelector('.message-text')!.textContent = streamedText;
      }
      scrollToBottom();
    },

    onChatFinal: (text: string, _runId: string) => {
      const finalText = text || streamedText;
      if (currentStreamEl) {
        // Render markdown on final
        currentStreamEl.querySelector('.message-text')!.innerHTML = renderMarkdown(finalText);
        currentStreamEl.classList.remove('streaming');
      } else {
        appendMessage('agent', finalText, true);
      }
      isStreaming = false;
      currentStreamEl = null;
      streamedText = '';
      inputEl.disabled = false;
      sendBtn.disabled = false;
      inputEl.focus();
      scrollToBottom();
    },

    onChatError: (error: string) => {
      if (currentStreamEl) {
        currentStreamEl.querySelector('.message-text')!.textContent = `Fehler: ${error}`;
        currentStreamEl.classList.remove('streaming');
        currentStreamEl.classList.add('error');
      } else {
        appendMessage('agent', `Fehler: ${error}`);
      }
      isStreaming = false;
      currentStreamEl = null;
      inputEl.disabled = false;
      sendBtn.disabled = false;
    },

    onPairing: () => {
      statusEl.textContent = 'Gerät wird gekoppelt...';
      statusEl.classList.remove('online');
      statusEl.classList.add('error');
    },

    onError: (error: string, code?: string) => {
      console.error('[Chat] Error:', error, code);
      if (hasFatalError) return; // don't overwrite error page
      hasFatalError = true;
      statusEl.textContent = 'Verbindungsfehler';
      statusEl.classList.remove('online');
      statusEl.classList.add('error');
      inputEl.disabled = true;
      sendBtn.disabled = true;

      if (code === 'AUTH_FAILED') {
        showFullError(
          'Zugang verweigert',
          'Das Zugangstoken ist ungueltig oder abgelaufen.',
          [
            'Pruefe, ob du den <strong>richtigen Link</strong> verwendest — den du von EmAI erhalten hast.',
            'Stelle sicher, dass der Link <strong>vollstaendig kopiert</strong> wurde (keine fehlenden Zeichen oder Leerzeichen am Ende).',
            'Falls das Problem bestehen bleibt, kontaktiere dein EmAI-Team fuer einen <strong>neuen Zugangslink</strong>.',
          ]
        );
      } else {
        showFullError(
          'Verbindungsfehler',
          String(error),
          [
            'Pruefe deine <strong>Internetverbindung</strong>.',
            'Versuche die <strong>Seite neu zu laden</strong>.',
            'Falls das Problem bestehen bleibt, kontaktiere dein EmAI-Team.',
          ]
        );
      }
    },
  });

  // Send message
  form.addEventListener('submit', (e) => {
    e.preventDefault();
    const text = inputEl.value.trim();
    if (!text || isStreaming) return;

    welcomeEl.style.display = 'none';
    appendMessage('user', text);
    scrollToBottom();
    inputEl.value = '';
    inputEl.disabled = true;
    sendBtn.disabled = true;

    client.sendMessage(text);
  });

  // Connect
  client.connect();
}

// Helpers
function appendMessage(role: 'user' | 'agent', text: string, isMarkdown = false): HTMLElement {
  const el = document.createElement('div');
  el.className = `message ${role}`;
  const content = role === 'agent' && isMarkdown ? renderMarkdown(text) : escapeHtml(text);
  const sender = role === 'agent' ? '<div class="message-sender">Kai</div>' : '';
  el.innerHTML = `<div class="message-bubble">${sender}<div class="message-text">${content}</div></div>`;
  messagesEl.appendChild(el);
  return el;
}

function scrollToBottom() {
  requestAnimationFrame(() => {
    messagesEl.scrollTop = messagesEl.scrollHeight;
  });
}

function escapeHtml(text: string): string {
  const div = document.createElement('div');
  div.textContent = text;
  return div.innerHTML;
}

function showFullError(title: string, detail: string, steps: string[]) {
  welcomeEl.style.display = 'none';
  const stepsList = steps.map(s => `<li>${s}</li>`).join('');
  messagesEl.innerHTML = `
    <div class="error-page">
      <div class="error-icon">&#x1f512;</div>
      <h2>${title}</h2>
      <p class="error-detail">${detail}</p>
      <div class="error-steps">
        <h3>Was du tun kannst:</h3>
        <ol>${stepsList}</ol>
      </div>
      <div class="error-contact">
        <p>Brauchst du Hilfe? Schreibe an dein EmAI-Team oder besuche <a href="https://emai.dev" target="_blank">emai.dev</a></p>
      </div>
    </div>
  `;
}

function renderMarkdown(text: string): string {
  try {
    return marked.parse(text) as string;
  } catch {
    return escapeHtml(text);
  }
}
