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

const app = document.querySelector<HTMLDivElement>('#app')!;
let namespace = 'emai-swarm';

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
          <span class="brand-mark">EmAI</span>
          <span class="brand-divider">·</span>
          <span class="brand-tagline">Onboarding · Admin</span>
        </div>
        <h1>Sign in</h1>
        <p class="login-hint">Enter the admin token to provision a new Kai instance.</p>
        <label for="token-input" class="visually-hidden">Admin token</label>
        <input id="token-input" type="password" placeholder="Admin token" autocomplete="off" required autofocus />
        ${error ? `<p class="login-error">${escapeHtml(error)}</p>` : ''}
        <button type="submit">Continue</button>
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
}

const signupState: SignupState = {
  email: '',
  password: '',
  app: 'personal-assistant',
  language: 'de',
  submitting: false,
};

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
          <span class="brand-mark">EmAI</span>
          <span class="brand-divider">·</span>
          <span class="brand-tagline">Create your workspace</span>
        </div>
        <h1>Sign up for Kai</h1>
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

  try {
    await api.signup({
      email,
      password: signupState.password,
      app: signupState.app,
      language: signupState.language,
    });
    renderCheckInbox(email);
  } catch (err) {
    const msg = err instanceof ApiError ? err.message : String(err);
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
          <span class="brand-mark">EmAI</span>
          <span class="brand-divider">·</span>
          <span class="brand-tagline">Almost there</span>
        </div>
        <h1>Check your inbox</h1>
        <p class="check-inbox-lead">We sent a verification link to <code>${escapeHtml(email)}</code>. Click it to provision your workspace — usually under a minute.</p>
        <ul class="check-inbox-tips">
          <li>Link expires in 24 hours.</li>
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
      <div class="signup-card">
        <div class="login-brand">
          <span class="brand-mark">EmAI</span>
          <span class="brand-divider">·</span>
          <span class="brand-tagline">Verifying…</span>
        </div>
        <h1>Provisioning your workspace</h1>
        <p class="check-inbox-lead">One moment — we're confirming your email and creating your private agent.</p>
        <div class="spinner" aria-label="Working"></div>
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
      <div class="signup-card">
        <div class="login-brand">
          <span class="brand-mark">EmAI</span>
          <span class="brand-divider">·</span>
          <span class="brand-tagline">Workspace ready</span>
        </div>
        <h1>You're in.</h1>
        <p class="check-inbox-lead">Your workspace <code>${escapeHtml(workspace || '(unknown)')}</code> is provisioning. The agent typically starts answering within a minute.</p>
        <div class="verified-actions">
          ${chatPath ? `<a class="primary-btn link-btn" href="${escapeHtml(chatPath)}">Open chat</a>` : ''}
          ${centerPath ? `<a class="ghost-btn link-btn" href="${escapeHtml(centerPath)}">Manage workspace</a>` : ''}
        </div>
        <ul class="check-inbox-tips">
          <li>You'll need to log in to chat with the email + password you just used.</li>
          <li>Bookmark these URLs — they're your front door.</li>
        </ul>
      </div>
    </div>
  `;
}

function renderVerifyError(msg: string) {
  app.innerHTML = `
    <div class="signup-shell">
      <div class="signup-card">
        <div class="login-brand">
          <span class="brand-mark">EmAI</span>
          <span class="brand-divider">·</span>
          <span class="brand-tagline">Verification failed</span>
        </div>
        <h1>Something went wrong</h1>
        <p class="login-error">${escapeHtml(msg)}</p>
        <ul class="check-inbox-tips">
          <li>Links expire after 24 hours — request a new one by signing up again.</li>
          <li>If this keeps happening, the team is reachable at <a href="mailto:hello@emai.dev">hello@emai.dev</a>.</li>
        </ul>
        <a href="/" class="ghost-btn link-btn">Back to signup</a>
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
          <span class="brand-mark">EmAI</span>
          <span class="brand-divider">·</span>
          <span class="brand-tagline">Swarm Onboarding</span>
        </div>
        <div class="header-right">
          <span class="namespace-badge">ns: ${escapeHtml(namespace)}</span>
          <button class="ghost-btn" id="logout-btn">Sign out</button>
        </div>
      </header>
      <main class="onboard-main">
        <section class="panel panel-form">
          <div class="panel-header"><h2>Provision a Kai instance</h2></div>
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
              <p class="field-hint" id="slug-hint">DNS-safe identifier. Auto-derived from customer name.</p>
            </div>
            <div class="field">
              <label for="model">Model <span class="field-optional">(optional)</span></label>
              <input id="model" name="model" type="text" placeholder="openrouter/anthropic/claude-sonnet-4-6"
                autocomplete="off" />
              <p class="field-hint">Leave empty to use the operator default.</p>
            </div>
            <div class="field">
              <label for="telegramSecretRef">Telegram bot secret <span class="field-optional">(optional)</span></label>
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
      <p class="field-hint">Append this path to the customer-chat host the customer should reach.</p>
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

route();
