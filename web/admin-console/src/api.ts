export interface InstanceSummary {
  name: string;
  tenantName: string;
  projectName: string;
  tenantSlug: string;
  model?: string;
  phase: string;
  ready: boolean;
  suspended: boolean;
  gatewayURL?: string;
  externalURL?: string;
  creationTimestamp: string;
}

const TOKEN_KEY = 'admin-console-token';

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
      ...(init.headers || {}),
      Authorization: `Bearer ${getToken()}`,
    },
  });
  if (res.status === 401) {
    throw new AuthError('Unauthorized');
  }
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`${res.status}: ${body}`);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

export class AuthError extends Error {}

export const api = {
  list: () => request<InstanceSummary[]>('/api/instances'),
  get: (name: string) => request<Record<string, unknown>>(`/api/instances/${encodeURIComponent(name)}`),
  suspend: (name: string) =>
    request<{ name: string; suspended: boolean }>(
      `/api/instances/${encodeURIComponent(name)}/suspend`,
      { method: 'POST' },
    ),
  resume: (name: string) =>
    request<{ name: string; suspended: boolean }>(
      `/api/instances/${encodeURIComponent(name)}/resume`,
      { method: 'POST' },
    ),
};
