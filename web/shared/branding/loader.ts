import defaultBranding from './branding.json';

export interface Branding {
  name: string;
  agentName: string;
  domain: string;
  domainUrl: string;
  supportEmail: string;
  fontFamily: string;
  fontHref?: string;
  footerCredit: string;
  poweredBy?: { label: string; url: string };
  logoUrl: string;
  faviconUrl: string;
  copy: {
    chat: {
      docTitle: string;
      missingSlugTitle: string;
      missingSlugHelp: string;
      signInTitle: string;
      signInSubtitle: string;
      welcomeBody: string;
      typingPlaceholder: string;
    };
    workspace: {
      docTitleSuffix: string;
      missingSlugBody: string;
      briefingsTitle: string;
      askLabel: string;
      whatItDoesTitle: string;
      whereToReachTitle: string;
      teamPlaceholder: string;
      hubDocTitle: string;
    };
    admin: {
      docTitle: string;
      loginHint: string;
      instancesHeading: string;
    };
    onboarding: {
      docTitle: string;
      loginHint: string;
      signupTitle: string;
      provisionTitle: string;
      provisionHelp: string;
      genericErrorContact: string;
    };
    status: {
      docTitlePrefix: string;
      docTitleSuffix: string;
      missingSlugBody: string;
      expiredLinkBody: string;
    };
  };
}

export const DEFAULT_BRANDING = defaultBranding as Branding;

export async function loadBranding(): Promise<Branding> {
  try {
    const res = await fetch('/branding/branding.json', { cache: 'no-cache' });
    if (!res.ok) return DEFAULT_BRANDING;
    const ct = res.headers.get('content-type') || '';
    if (!ct.includes('json')) return DEFAULT_BRANDING;
    const data = (await res.json()) as Branding;
    return mergeWithDefaults(data);
  } catch {
    return DEFAULT_BRANDING;
  }
}

function mergeWithDefaults(b: Partial<Branding>): Branding {
  return {
    ...DEFAULT_BRANDING,
    ...b,
    poweredBy: b.poweredBy ?? DEFAULT_BRANDING.poweredBy,
    copy: {
      chat: { ...DEFAULT_BRANDING.copy.chat, ...(b.copy?.chat ?? {}) },
      workspace: { ...DEFAULT_BRANDING.copy.workspace, ...(b.copy?.workspace ?? {}) },
      admin: { ...DEFAULT_BRANDING.copy.admin, ...(b.copy?.admin ?? {}) },
      onboarding: { ...DEFAULT_BRANDING.copy.onboarding, ...(b.copy?.onboarding ?? {}) },
      status: { ...DEFAULT_BRANDING.copy.status, ...(b.copy?.status ?? {}) },
    },
  };
}

export function applyBranding(b: Branding, opts: { docTitle?: string } = {}): void {
  if (opts.docTitle !== undefined) document.title = opts.docTitle;
  const favicon = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
  if (favicon && favicon.getAttribute('href') !== b.faviconUrl) {
    favicon.setAttribute('href', b.faviconUrl);
  }
  (window as unknown as { __brand: Branding }).__brand = b;
}

export function brandMarkHTML(b: Branding): string {
  return `<img src="${escapeAttr(b.logoUrl)}" alt="${escapeAttr(b.name)}" class="brand-mark" />`;
}

function escapeAttr(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c] as string));
}
