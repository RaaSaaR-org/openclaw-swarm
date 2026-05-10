import './style.css';
import {
  api,
  AuthError,
  ApiError,
  clearToken,
  getToken,
  setToken,
  slugify,
  isValidSlug,
  renderYaml,
  catalogApps,
  type ProvisionRequest,
  type ProvisionResponse,
} from './api';
import { loadBranding, applyBranding, DEFAULT_BRANDING, type Branding } from '@branding/loader';

const app = document.querySelector<HTMLDivElement>('#app')!;
let namespace = 'swarm-system';

let b: Branding = DEFAULT_BRANDING;

bootstrap();

async function bootstrap() {
  b = await loadBranding();
  applyBranding(b, { docTitle: b.copy.onboarding.docTitle });
  route();
}

function route() {
  const path = location.pathname.replace(/\/+$/, '') || '/';
  if (path === '/admin' || path.startsWith('/admin/')) {
    if (!getToken()) renderLogin();
    else bootstrapAdmin();
    return;
  }
  if (path === '/verify') {
    handleVerify();
    return;
  }
  renderSignup();
}

async function bootstrapAdmin() {
  try {
    const auth = await api.checkAuth();
    namespace = auth.namespace;
    renderForm();
  } catch (err) {
    if (err instanceof AuthError) {
      clearToken();
      renderLogin('Session expired. Sign in again.');
      return;
    }
    renderForm();
  }
}

function renderLogin(error?: string) {
  app.innerHTML = `
    <div class="login-shell">
      <form class="login-card" id="login-form">
        <div class="login-brand">
          <a class="brand-lockup" href="/" aria-label="${escapeHtml(b.name)}">
            <img class="brand-lockup-mark" src="${escapeHtml(b.faviconUrl)}" alt="" />
            <span class="brand-lockup-word">${escapeHtml(b.name)}</span>
          </a>
          <span class="brand-tagline">Operator console</span>
        </div>
        <h1>Sign in</h1>
        <p class="login-hint">${escapeHtml(b.copy.onboarding.loginHint)}</p>
        <label for="token-input" class="visually-hidden">Admin token</label>
        <input id="token-input" type="password" placeholder="Admin token" autocomplete="off" required autofocus />
        ${error ? `<p class="login-error">${escapeHtml(error)}</p>` : ''}
        <button type="submit">Sign in</button>
      </form>
    </div>
  `;

  document.getElementById('login-form')!.addEventListener('submit', async (e) => {
    e.preventDefault();
    const input = document.getElementById('token-input') as HTMLInputElement;
    const token = input.value.trim();
    if (!token) return;
    setToken(token);
    try {
      await api.checkAuth();
    } catch (err) {
      if (err instanceof AuthError) {
        clearToken();
        renderLogin('Invalid token.');
        return;
      }
    }
    bootstrapAdmin();
  });
}

interface SignupState {
  email: string;
  password: string;
  app: string;
  language: 'de' | 'en';
  submitting: boolean;
  error?: string;
  /** Cloudflare-issued site key fetched from /api/onboarding/config; empty string when no CAPTCHA is configured. */
  turnstileSiteKey: string;
  /** Token produced by the Turnstile widget (cf-turnstile callback). Refreshed on every successful challenge. */
  turnstileToken: string;
  /** Tracks whether we've already kicked off the config fetch so renderSignup() can be called multiple times safely. */
  configLoaded: boolean;
}

const signupState: SignupState = {
  email: '',
  password: '',
  app: 'personal-assistant',
  language: 'de',
  submitting: false,
  turnstileSiteKey: '',
  turnstileToken: '',
  configLoaded: false,
};

// Turnstile's global API surface. The script in index.html attaches
// `window.turnstile` once it's done loading; we feature-detect at use
// time so the SPA still works in environments where the Cloudflare
// challenges domain is blocked (the back end will reject the signup
// with a CAPTCHA failure, which is the right outcome).
declare global {
  interface Window {
    turnstile?: {
      render: (selector: string | HTMLElement, opts: {
        sitekey: string;
        callback?: (token: string) => void;
        'error-callback'?: () => void;
        'expired-callback'?: () => void;
      }) => string;
      reset: (widgetID?: string) => void;
    };
  }
}

function renderSignup(error?: string) {
  signupState.error = error;
  const appOptions = catalogApps
    .map((a) => `
      <label class="app-card${signupState.app === a.slug ? ' selected' : ''}">
        <input type="radio" name="app" value="${escapeHtml(a.slug)}" ${signupState.app === a.slug ? 'checked' : ''} />
        <span class="app-name">${escapeHtml(a.name)}</span>
        <span class="app-desc">${escapeHtml(a.shortDescription)}</span>
      </label>
    `)
    .join('');

  app.innerHTML = `
    <div class="signup-shell">
      <form class="signup-card" id="signup-form" novalidate>
        <div class="login-brand">
          <a class="brand-lockup" href="/" aria-label="${escapeHtml(b.name)}">
            <img class="brand-lockup-mark" src="${escapeHtml(b.faviconUrl)}" alt="" />
            <span class="brand-lockup-word">${escapeHtml(b.name)}</span>
          </a>
          <span class="brand-tagline">Create your workspace</span>
        </div>
        <h1>${escapeHtml(b.copy.onboarding.signupTitle)}</h1>
        <p class="login-hint">Email + password. We mail you a link, you click it, your private agent is ready in about a minute.</p>

        <div class="signup-grid">
          <div class="field">
            <label for="email-input">Email</label>
            <input id="email-input" type="email" placeholder="you@company.com" autocomplete="email" required value="${escapeHtml(signupState.email)}" />
          </div>
          <div class="field">
            <label for="password-input">Password</label>
            <input id="password-input" type="password" placeholder="at least 8 characters" autocomplete="new-password" minlength="8" required />
            <p class="field-hint">Min 8 characters. We hash with argon2id; the plaintext never leaves the server.</p>
          </div>
          <div class="field">
            <label>Language</label>
            <div class="lang-toggle" role="radiogroup" aria-label="Language">
              <button type="button" class="lang-btn${signupState.language === 'de' ? ' selected' : ''}" data-lang="de" role="radio" aria-checked="${signupState.language === 'de'}">DE — Deutsch</button>
              <button type="button" class="lang-btn${signupState.language === 'en' ? ' selected' : ''}" data-lang="en" role="radio" aria-checked="${signupState.language === 'en'}">EN — English</button>
            </div>
          </div>
        </div>

        <div class="field">
          <label>Pick your starting agent</label>
          <p class="field-hint">You can switch or add more later. Each is a working configuration — persona, recommended model, starter prompts.</p>
          <div class="app-grid">${appOptions}</div>
        </div>

        ${signupState.error ? `<p class="login-error" id="signup-error">${escapeHtml(signupState.error)}</p>` : '<p class="login-error" id="signup-error" hidden></p>'}

        ${signupState.turnstileSiteKey ? `<div class="cf-turnstile" data-sitekey="${escapeHtml(signupState.turnstileSiteKey)}" data-theme="dark" id="turnstile-widget"></div>` : ''}

        <button type="submit" class="primary-btn" id="signup-submit">Create my workspace</button>
        <p class="signup-fineprint">By signing up you agree to the <a href="https://kai.example.org/terms">terms of service</a> and <a href="https://kai.example.org/privacy">privacy policy</a>. <a href="/admin" class="muted-link">Admin?</a></p>
      </form>
    </div>
  `;

  const form = document.getElementById('signup-form') as HTMLFormElement;
  const emailEl = document.getElementById('email-input') as HTMLInputElement;
  const passwordEl = document.getElementById('password-input') as HTMLInputElement;
  emailEl.addEventListener('input', () => { signupState.email = emailEl.value; });
  passwordEl.addEventListener('input', () => { signupState.password = passwordEl.value; });
  document.querySelectorAll<HTMLButtonElement>('.lang-btn').forEach((btn) => {
    btn.addEventListener('click', () => {
      signupState.language = btn.dataset.lang === 'en' ? 'en' : 'de';
      document.querySelectorAll<HTMLButtonElement>('.lang-btn').forEach((b) => {
        const sel = b.dataset.lang === signupState.language;
        b.classList.toggle('selected', sel);
        b.setAttribute('aria-checked', String(sel));
      });
    });
  });
  document.querySelectorAll<HTMLInputElement>('input[name="app"]').forEach((radio) => {
    radio.addEventListener('change', () => {
      signupState.app = radio.value;
      document.querySelectorAll<HTMLLabelElement>('.app-card').forEach((c) => {
        const input = c.querySelector('input') as HTMLInputElement;
        c.classList.toggle('selected', input.value === signupState.app);
      });
    });
  });

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    if (signupState.submitting) return;
    await submitSignup();
  });

  // Fetch the runtime config + mount the Turnstile widget. Re-renders the
  // form once the config is in so the widget div appears with the right
  // sitekey. The fetch only happens once per page load.
  if (!signupState.configLoaded) {
    void loadOnboardingConfigAndRender();
  } else if (signupState.turnstileSiteKey) {
    mountTurnstileWidget();
  }
}

async function loadOnboardingConfigAndRender() {
  signupState.configLoaded = true;
  try {
    const cfg = await api.config();
    if (cfg.turnstileSiteKey) {
      signupState.turnstileSiteKey = cfg.turnstileSiteKey;
      renderSignup();
    }
  } catch {
    // Best-effort — if the config endpoint is down, the form still works
    // with whatever CAPTCHA the server is running (a noopCaptcha in dev,
    // or a Turnstile verifier that will reject the empty token in prod).
  }
}

function mountTurnstileWidget() {
  if (!signupState.turnstileSiteKey) return;
  if (!window.turnstile) {
    // Turnstile script hasn't finished loading yet — retry once. The
    // widget div is already in the DOM; turnstile.render fills it in.
    setTimeout(mountTurnstileWidget, 200);
    return;
  }
  const target = document.getElementById('turnstile-widget');
  if (!target) return;
  window.turnstile.render(target, {
    sitekey: signupState.turnstileSiteKey,
    callback: (token: string) => {
      signupState.turnstileToken = token;
    },
    'expired-callback': () => {
      signupState.turnstileToken = '';
    },
    'error-callback': () => {
      signupState.turnstileToken = '';
    },
  });
}

async function submitSignup() {
  const errEl = document.getElementById('signup-error')!;
  errEl.hidden = true;
  errEl.textContent = '';

  const email = signupState.email.trim();
  if (!email || !email.includes('@')) return showSignupError('Enter a valid email address.');
  if (signupState.password.length < 8) return showSignupError('Password must be at least 8 characters.');

  const submitBtn = document.getElementById('signup-submit') as HTMLButtonElement;
  signupState.submitting = true;
  submitBtn.disabled = true;
  submitBtn.textContent = 'Creating…';

  // CAPTCHA is configured (site key was returned by /api/onboarding/config)
  // but the user hasn't solved the challenge yet — block the submit with
  // a helpful message instead of POSTing an empty token and getting
  // rejected by the back end.
  if (signupState.turnstileSiteKey && !signupState.turnstileToken) {
    signupState.submitting = false;
    submitBtn.disabled = false;
    submitBtn.textContent = 'Create my workspace';
    return showSignupError('Please complete the CAPTCHA before submitting.');
  }

  try {
    await api.signup({
      email,
      password: signupState.password,
      app: signupState.app,
      language: signupState.language,
      captchaToken: signupState.turnstileToken || undefined,
    });
    renderCheckInbox(email);
  } catch (err) {
    const msg = err instanceof ApiError ? err.message : String(err);
    // Turnstile tokens are single-use — reset the widget so the user can
    // retry submitting without a page refresh.
    signupState.turnstileToken = '';
    if (signupState.turnstileSiteKey && window.turnstile) {
      window.turnstile.reset();
    }
    showSignupError(msg);
  } finally {
    signupState.submitting = false;
    submitBtn.disabled = false;
    submitBtn.textContent = 'Create my workspace';
  }
}

function showSignupError(msg: string) {
  signupState.error = msg;
  const el = document.getElementById('signup-error')!;
  el.textContent = msg;
  el.hidden = false;
}

function renderCheckInbox(email: string) {
  app.innerHTML = `
    <div class="signup-shell">
      <div class="signup-card">
        <div class="login-brand">
          <a class="brand-lockup" href="/" aria-label="${escapeHtml(b.name)}">
            <img class="brand-lockup-mark" src="${escapeHtml(b.faviconUrl)}" alt="" />
            <span class="brand-lockup-word">${escapeHtml(b.name)}</span>
          </a>
          <span class="brand-tagline">Email sent</span>
        </div>
        <h1>Check your inbox</h1>
        <p class="check-inbox-lead">We sent a verification link to <code>${escapeHtml(email)}</code>. Click it to provision your workspace — usually under a minute.</p>
        <ul class="check-inbox-tips">
          <li>The link expires in 24 hours.</li>
          <li>No mail? Check spam, then <a href="/" class="muted-link">try a different email</a>.</li>
        </ul>
      </div>
    </div>
  `;
}

async function handleVerify() {
  const params = new URLSearchParams(location.search);
  const id = params.get('id') || '';
  const token = params.get('token') || '';
  if (!id || !token) {
    renderVerifyError('Missing id or token in URL.');
    return;
  }
  app.innerHTML = `
    <div class="signup-shell">
      <div class="signup-card loading">
        <div class="login-brand">
          <a class="brand-lockup" href="/" aria-label="${escapeHtml(b.name)}">
            <img class="brand-lockup-mark" src="${escapeHtml(b.faviconUrl)}" alt="" />
            <span class="brand-lockup-word">${escapeHtml(b.name)}</span>
          </a>
          <span class="brand-tagline">Verifying</span>
        </div>
        <div class="indicator loading-state" aria-hidden="true">${indicatorMarkSVG()}</div>
        <h1>Provisioning your workspace</h1>
        <p class="check-inbox-lead">One moment — we're confirming your email and creating your private agent.</p>
      </div>
    </div>
  `;
  try {
    const res = await api.verify(id, token);
    renderVerified(res.workspace || '');
  } catch (err) {
    const msg = err instanceof ApiError ? err.message : String(err);
    renderVerifyError(msg);
  }
}

function renderVerified(workspace: string) {
  const chatPath = workspace ? `/chat/${encodeURIComponent(workspace)}` : '';
  const centerPath = workspace ? `/center/${encodeURIComponent(workspace)}` : '';
  app.innerHTML = `
    <div class="signup-shell">
      <div class="signup-card verified">
        <div class="login-brand" style="align-self:stretch;">
          <a class="brand-lockup" href="/" aria-label="${escapeHtml(b.name)}">
            <img class="brand-lockup-mark" src="${escapeHtml(b.faviconUrl)}" alt="" />
            <span class="brand-lockup-word">${escapeHtml(b.name)}</span>
          </a>
          <span class="brand-tagline">Workspace ready</span>
        </div>
        <div class="indicator lg active" aria-hidden="true">${indicatorMarkSVG()}</div>
        <h1>Your workspace is ready.</h1>
        <p class="check-inbox-lead">Workspace <code>${escapeHtml(workspace || '(unknown)')}</code> is provisioning. The agent typically starts answering within a minute.</p>
        <div class="verified-actions">
          ${chatPath ? `<a class="primary-btn link-btn" href="${escapeHtml(chatPath)}">Open chat</a>` : ''}
          ${centerPath ? `<a class="ghost-btn link-btn" href="${escapeHtml(centerPath)}">Manage workspace</a>` : ''}
        </div>
        <ul class="check-inbox-tips">
          <li>Sign in to the chat with the email and password you just used.</li>
          <li>Bookmark these URLs — they're your front door.</li>
        </ul>
      </div>
    </div>
  `;
}

function renderVerifyError(msg: string) {
  app.innerHTML = `
    <div class="signup-shell">
      <div class="signup-card verify-error">
        <div class="login-brand">
          <a class="brand-lockup" href="/" aria-label="${escapeHtml(b.name)}">
            <img class="brand-lockup-mark" src="${escapeHtml(b.faviconUrl)}" alt="" />
            <span class="brand-lockup-word">${escapeHtml(b.name)}</span>
          </a>
          <span class="brand-tagline">Verification failed</span>
        </div>
        <h1>Something went wrong</h1>
        <p class="login-error">${escapeHtml(msg)}</p>
        <ul class="check-inbox-tips">
          <li>Links expire after 24 hours — request a new one by signing up again.</li>
          <li>If this keeps happening, the team is reachable at <a href="mailto:${escapeHtml(b.supportEmail)}">${escapeHtml(b.supportEmail)}</a>.</li>
        </ul>
        <div class="verified-actions" style="justify-content:flex-start;">
          <a href="/" class="ghost-btn link-btn">Back to signup</a>
        </div>
      </div>
    </div>
  `;
}

interface FormState {
  customerName: string;
  projectName: string;
  customerSlug: string;
  slugManuallyEdited: boolean;
  model: string;
  telegramSecretRef: string;
  externalAccess: boolean;
  submitting: boolean;
  error?: string;
  result?: ProvisionResponse;
}

const state: FormState = {
  customerName: '',
  projectName: '',
  customerSlug: '',
  slugManuallyEdited: false,
  model: '',
  telegramSecretRef: '',
  externalAccess: true,
  submitting: false,
};

function renderForm() {
  app.innerHTML = `
    <div class="onboard-shell">
      <header class="onboard-header">
        <div class="header-left">
          <a class="brand-lockup" href="/" aria-label="${escapeHtml(b.name)}">
            <img class="brand-lockup-mark" src="${escapeHtml(b.faviconUrl)}" alt="" />
            <span class="brand-lockup-word">${escapeHtml(b.name)}</span>
          </a>
          <span class="brand-tagline">Operator console</span>
        </div>
        <div class="header-right">
          <span class="namespace-badge">ns · ${escapeHtml(namespace)}</span>
          <button class="ghost-btn small" id="logout-btn">Sign out</button>
        </div>
      </header>
      <main class="onboard-main">
        <section class="panel panel-form">
          <div class="panel-header"><h2>${escapeHtml(b.copy.onboarding.provisionTitle)}</h2></div>
          <form id="provision-form" class="provision-form" novalidate>
            <div class="field">
              <label for="customerName">Customer name</label>
              <input id="customerName" name="customerName" type="text" required maxlength="100"
                placeholder="Acme GmbH" autocomplete="off" />
              <p class="field-hint">Display name shown in the agent's identity and chat.</p>
            </div>
            <div class="field">
              <label for="projectName">Project name</label>
              <input id="projectName" name="projectName" type="text" required maxlength="200"
                placeholder="Robot Pilot" autocomplete="off" />
              <p class="field-hint">Brief project context for the agent.</p>
            </div>
            <div class="field">
              <label for="customerSlug">Slug</label>
              <input id="customerSlug" name="customerSlug" type="text" required maxlength="63"
                placeholder="acme-gmbh" autocomplete="off" pattern="^[a-z0-9]([a-z0-9-]*[a-z0-9])?$" />
              <p class="field-hint" id="slug-hint">DNS-safe identifier · auto-derived from customer name.</p>
            </div>
            <div class="field">
              <label for="model">Model <span class="field-optional">optional</span></label>
              <input id="model" name="model" type="text" placeholder="openrouter/anthropic/claude-sonnet-4-6"
                autocomplete="off" />
              <p class="field-hint">Leave empty to use the operator default.</p>
            </div>
            <div class="field">
              <label for="telegramSecretRef">Telegram bot secret <span class="field-optional">optional</span></label>
              <input id="telegramSecretRef" name="telegramSecretRef" type="text" placeholder="kai-acme-telegram"
                autocomplete="off" />
              <p class="field-hint">Name of an existing Secret with key <code>bot-token</code>. Skip to disable Telegram.</p>
            </div>
            <div class="field field-checkbox">
              <label>
                <input id="externalAccess" name="externalAccess" type="checkbox" checked />
                <span>Create Ingress for external access</span>
              </label>
              <p class="field-hint">Disable for in-cluster only.</p>
            </div>
            <div class="form-error" id="form-error" hidden></div>
            <div class="form-actions">
              <button type="submit" class="primary-btn" id="submit-btn">Provision instance</button>
            </div>
          </form>
        </section>
        <aside class="panel panel-preview">
          <div class="panel-header">
            <h2>YAML preview</h2>
            <button class="ghost-btn small" id="copy-yaml-btn" type="button" title="Copy YAML">Copy</button>
          </div>
          <pre class="yaml-preview" id="yaml-preview"></pre>
        </aside>
      </main>
      <footer class="onboard-footer">
        Generates a <code>KaiInstance</code> CR with a fresh gateway token. The operator reconciles it into a Deployment, Service, ConfigMap, PVC, and NetworkPolicy.
      </footer>
    </div>
    <div class="modal-overlay" id="success-modal">
      <div class="modal" role="dialog" aria-modal="true">
        <div class="modal-header"><h2>Instance provisioned</h2></div>
        <div class="modal-body" id="success-body"></div>
      </div>
    </div>
  `;

  document.getElementById('logout-btn')!.addEventListener('click', () => {
    clearToken();
    renderLogin();
  });

  bindForm();
  updatePreview();
}

function bindForm() {
  const form = document.getElementById('provision-form') as HTMLFormElement;
  const customerEl = document.getElementById('customerName') as HTMLInputElement;
  const projectEl = document.getElementById('projectName') as HTMLInputElement;
  const slugEl = document.getElementById('customerSlug') as HTMLInputElement;
  const modelEl = document.getElementById('model') as HTMLInputElement;
  const telegramEl = document.getElementById('telegramSecretRef') as HTMLInputElement;
  const externalEl = document.getElementById('externalAccess') as HTMLInputElement;
  const slugHint = document.getElementById('slug-hint')!;

  customerEl.addEventListener('input', () => {
    state.customerName = customerEl.value;
    if (!state.slugManuallyEdited) {
      state.customerSlug = slugify(customerEl.value);
      slugEl.value = state.customerSlug;
    }
    updatePreview();
    updateSlugHint(slugHint);
  });
  projectEl.addEventListener('input', () => {
    state.projectName = projectEl.value;
    updatePreview();
  });
  slugEl.addEventListener('input', () => {
    state.customerSlug = slugEl.value;
    state.slugManuallyEdited = true;
    updatePreview();
    updateSlugHint(slugHint);
  });
  modelEl.addEventListener('input', () => {
    state.model = modelEl.value;
    updatePreview();
  });
  telegramEl.addEventListener('input', () => {
    state.telegramSecretRef = telegramEl.value;
    updatePreview();
  });
  externalEl.addEventListener('change', () => {
    state.externalAccess = externalEl.checked;
    updatePreview();
  });

  document.getElementById('copy-yaml-btn')!.addEventListener('click', async () => {
    const text = document.getElementById('yaml-preview')!.textContent || '';
    try {
      await navigator.clipboard.writeText(text);
      const btn = document.getElementById('copy-yaml-btn')!;
      const orig = btn.textContent;
      btn.textContent = 'Copied';
      setTimeout(() => { btn.textContent = orig; }, 1500);
    } catch { /* no-op */ }
  });

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    if (state.submitting) return;
    await submitForm();
  });
}

function updateSlugHint(el: HTMLElement) {
  const slug = state.customerSlug;
  if (!slug) {
    el.textContent = 'DNS-safe identifier. Auto-derived from customer name.';
    el.className = 'field-hint';
  } else if (!isValidSlug(slug)) {
    el.textContent = 'Invalid: lowercase letters/digits/hyphens, must start and end with a letter or digit, max 63 chars.';
    el.className = 'field-hint error';
  } else {
    el.textContent = `Resource will be named "kai-${slug}".`;
    el.className = 'field-hint ok';
  }
}

function updatePreview() {
  const el = document.getElementById('yaml-preview');
  if (!el) return;
  const req: ProvisionRequest = {
    customerName: state.customerName,
    projectName: state.projectName,
    customerSlug: state.customerSlug,
    model: state.model || undefined,
    telegramSecretRef: state.telegramSecretRef || undefined,
    externalAccess: state.externalAccess,
  };
  el.textContent = renderYaml(req, namespace);
}

async function submitForm() {
  const errorEl = document.getElementById('form-error')!;
  errorEl.hidden = true;
  errorEl.textContent = '';

  if (!state.customerName.trim()) return showFormError('Customer name is required.');
  if (!state.projectName.trim()) return showFormError('Project name is required.');
  if (!state.customerSlug.trim()) return showFormError('Slug is required.');
  if (!isValidSlug(state.customerSlug)) return showFormError('Slug is not DNS-safe.');

  const submitBtn = document.getElementById('submit-btn') as HTMLButtonElement;
  state.submitting = true;
  submitBtn.disabled = true;
  submitBtn.textContent = 'Provisioning…';

  try {
    const result = await api.provision({
      customerName: state.customerName.trim(),
      projectName: state.projectName.trim(),
      customerSlug: state.customerSlug.trim(),
      model: state.model.trim() || undefined,
      telegramSecretRef: state.telegramSecretRef.trim() || undefined,
      externalAccess: state.externalAccess,
    });
    state.result = result;
    showSuccess(result);
  } catch (err) {
    if (err instanceof AuthError) {
      clearToken();
      renderLogin('Session expired. Sign in again.');
      return;
    }
    const msg = err instanceof ApiError ? err.message : String(err);
    showFormError(msg);
  } finally {
    state.submitting = false;
    submitBtn.disabled = false;
    submitBtn.textContent = 'Provision instance';
  }
}

function showFormError(msg: string) {
  const el = document.getElementById('form-error')!;
  el.textContent = msg;
  el.hidden = false;
}

function showSuccess(result: ProvisionResponse) {
  const modal = document.getElementById('success-modal')!;
  const body = document.getElementById('success-body')!;
  const chatPath = `/chat/${encodeURIComponent(result.customerSlug)}?token=${encodeURIComponent(result.gatewayToken)}`;
  body.innerHTML = `
    <p class="success-lead">
      <code>${escapeHtml(result.name)}</code> created in namespace <code>${escapeHtml(result.namespace)}</code>.
    </p>
    <div class="success-section">
      <h3>Gateway token</h3>
      <p class="warn-line">Shown only once. Copy it now.</p>
      <div class="token-row">
        <code id="success-token" class="token-value">${escapeHtml(result.gatewayToken)}</code>
        <button class="ghost-btn small" id="copy-token-btn" type="button">Copy</button>
      </div>
    </div>
    <div class="success-section">
      <h3>Customer chat URL</h3>
      <code class="token-value">${escapeHtml(chatPath)}</code>
      <p class="field-hint">Append this path to the chat host the customer should reach.</p>
    </div>
    <div class="success-section">
      <h3>Next steps</h3>
      <ul class="next-steps">
        <li>Watch reconciliation: <code>kubectl get kaiinstance ${escapeHtml(result.name)} -n ${escapeHtml(result.namespace)} -w</code></li>
        <li>Edit USER.md inside the running pod once it is Running.</li>
        <li>Manage from the Admin Console (suspend/resume, view conditions).</li>
      </ul>
    </div>
    <div class="modal-actions">
      <button class="ghost-btn" id="success-close" type="button">Close</button>
      <button class="primary-btn" id="success-another" type="button">Provision another</button>
    </div>
  `;
  modal.classList.add('open');

  document.getElementById('copy-token-btn')!.addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(result.gatewayToken);
      const btn = document.getElementById('copy-token-btn')!;
      btn.textContent = 'Copied';
      setTimeout(() => { btn.textContent = 'Copy'; }, 1500);
    } catch { /* no-op */ }
  });
  document.getElementById('success-close')!.addEventListener('click', () => {
    modal.classList.remove('open');
  });
  document.getElementById('success-another')!.addEventListener('click', () => {
    modal.classList.remove('open');
    resetForm();
  });
}

function resetForm() {
  state.customerName = '';
  state.projectName = '';
  state.customerSlug = '';
  state.slugManuallyEdited = false;
  state.model = '';
  state.telegramSecretRef = '';
  state.externalAccess = true;
  state.result = undefined;
  renderForm();
}

function escapeHtml(text: string): string {
  const div = document.createElement('div');
  div.textContent = text ?? '';
  return div.innerHTML;
}

// The Mind Swarm brand mark — three nodes in triangle inside a faint circle.
// Lifted from status-page so the verified/loading states feel continuous
// with the rest of the product. Per brand.md §5.
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
