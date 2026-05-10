export interface ProvisionRequest {
  customerName: string;
  projectName: string;
  customerSlug: string;
  model?: string;
  telegramSecretRef?: string;
  externalAccess?: boolean;
}

export interface ProvisionResponse {
  name: string;
  namespace: string;
  customerSlug: string;
  gatewayToken: string;
}

const TOKEN_KEY = 'onboarding-token';

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) || '';
}

export function setToken(token: string) {
  localStorage.setItem(TOKEN_KEY, token);
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY);
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(init.headers || {}),
      Authorization: `Bearer ${getToken()}`,
    },
  });
  if (res.status === 401) throw new AuthError('Unauthorized');
  if (!res.ok) {
    let body: unknown;
    try {
      body = await res.json();
    } catch {
      body = { error: await res.text() };
    }
    const msg = (body as { error?: string }).error || `HTTP ${res.status}`;
    throw new ApiError(msg, res.status);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

export class AuthError extends Error {}
export class ApiError extends Error {
  constructor(message: string, public status: number) {
    super(message);
  }
}

export interface SignupRequest {
  email: string;
  password: string;
  app?: string;
  language?: 'de' | 'en';
  captchaToken?: string;
}

export interface SignupResponse {
  status: 'verification_sent';
}

export interface OnboardingConfig {
  signupEnabled: boolean;
  turnstileSiteKey?: string;
}

export interface VerifyResponse {
  status: 'verified';
  workspace?: string;
}

async function publicRequest<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...(init.headers || {}) },
  });
  if (!res.ok) {
    let body: unknown;
    try { body = await res.json(); } catch { body = { error: await res.text() }; }
    const errBody = body as { error?: string; message?: string };
    throw new ApiError(errBody.message || errBody.error || `HTTP ${res.status}`, res.status);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

export const api = {
  checkAuth: () => request<{ namespace: string }>('/api/auth'),
  provision: (req: ProvisionRequest) =>
    request<ProvisionResponse>('/api/instances', {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  signup: (req: SignupRequest) =>
    publicRequest<SignupResponse>('/api/signup', {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  config: () => publicRequest<OnboardingConfig>('/api/onboarding/config'),
  verify: (id: string, token: string) => {
    const qs = new URLSearchParams({ id, token }).toString();
    return publicRequest<VerifyResponse>(`/api/signup/verify?${qs}`);
  },
};

export interface CatalogApp {
  slug: string;
  name: string;
  shortDescription: string;
}

export const catalogApps: CatalogApp[] = [
  { slug: 'personal-assistant', name: 'Personal Assistant', shortDescription: 'Plans your day, drafts replies, keeps notes.' },
  { slug: 'coding-helper', name: 'Coding Helper', shortDescription: 'Reviews diffs, explains code, runs scripts.' },
  { slug: 'writing-coach', name: 'Writing Coach', shortDescription: 'Edits drafts, sharpens style, clarifies structure.' },
  { slug: 'language-tutor', name: 'Language Tutor', shortDescription: 'Practices conversation, corrects grammar, explains nuance.' },
  { slug: 'study-buddy', name: 'Study Buddy', shortDescription: 'Quizzes you, summarizes notes, plans review sessions.' },
  { slug: 'productivity-companion', name: 'Productivity Companion', shortDescription: 'Tracks goals, batches tasks, blocks deep work.' },
];

export function slugify(input: string): string {
  return input
    .toLowerCase()
    .normalize('NFKD')
    .replace(/[̀-ͯ]/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 63);
}

export function isValidSlug(slug: string): boolean {
  return /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/.test(slug) && slug.length <= 63;
}

export function renderYaml(req: ProvisionRequest, namespace: string): string {
  const indent = (n: number) => ' '.repeat(n);
  const name = `kai-${req.customerSlug || '<slug>'}`;
  const lines: string[] = [
    'apiVersion: swarm.emai.io/v1alpha2',
    'kind: KaiInstance',
    'metadata:',
    `${indent(2)}name: ${name}`,
    `${indent(2)}namespace: ${namespace}`,
    'spec:',
    `${indent(2)}customerName: ${yamlString(req.customerName)}`,
    `${indent(2)}projectName: ${yamlString(req.projectName)}`,
    `${indent(2)}customerSlug: ${yamlString(req.customerSlug)}`,
    `${indent(2)}gatewayAuth:`,
    `${indent(4)}mode: token`,
    `${indent(4)}token: <generated-on-create>`,
  ];
  if (req.model) {
    lines.push(`${indent(2)}model: ${yamlString(req.model)}`);
  }
  if (req.telegramSecretRef) {
    lines.push(`${indent(2)}telegram:`);
    lines.push(`${indent(4)}botTokenSecretRef: ${yamlString(req.telegramSecretRef)}`);
  }
  if (req.externalAccess === false) {
    lines.push(`${indent(2)}externalAccess: false`);
  }
  return lines.join('\n');
}

function yamlString(v: string): string {
  if (!v) return '""';
  if (/^[A-Za-z0-9_./:-]+$/.test(v)) return v;
  return `"${v.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`;
}
