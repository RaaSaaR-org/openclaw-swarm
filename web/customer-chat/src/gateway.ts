// OpenClaw WebSocket Gateway Client

import { getOrCreateDevice, signChallenge, DeviceIdentity } from './device';

export type ChatState = 'delta' | 'final' | 'error' | 'aborted';

export interface ChatEvent {
  state: ChatState;
  sessionKey: string;
  runId?: string;
  message?: {
    content: Array<{ type: string; text?: string; thinking?: string }>;
  };
  errorMessage?: string;
}

export interface AgentIdentity {
  name: string;
  emoji: string;
  agentId: string;
}

export interface GatewayCallbacks {
  onConnected: (identity: AgentIdentity | null) => void;
  onDisconnected: () => void;
  onChatDelta: (text: string, runId: string) => void;
  onChatFinal: (text: string, runId: string) => void;
  onChatError: (error: string) => void;
  onError: (error: string, code?: string) => void;
  onPairing: () => void;
}

const CLIENT_ID = 'webchat';
const CLIENT_MODE = 'webchat';
const ROLE = 'operator';
const SCOPES = ['operator.admin', 'operator.read', 'operator.write', 'operator.approvals', 'operator.pairing'];

export class GatewayClient {
  private ws: WebSocket | null = null;
  private callbacks: GatewayCallbacks;
  private pendingRequests = new Map<string, (res: any) => void>();
  private reqCounter = 0;
  private sessionKey: string | null = null;
  private reconnectTimer: number | null = null;
  private reconnectDelay = 1000;
  private reconnectAttempts = 0;
  private maxReconnectAttempts = 5;
  private url: string;
  private token: string;
  private device: DeviceIdentity | null = null;
  private connectNonce: string | null = null;

  constructor(url: string, token: string, callbacks: GatewayCallbacks) {
    this.url = url;
    this.token = token;
    this.callbacks = callbacks;
  }

  async connect() {
    // Ensure we have a device identity
    if (!this.device) {
      try {
        this.device = await getOrCreateDevice();
      } catch (e) {
        console.error('[GW] Failed to create device identity:', e);
        this.callbacks.onError('Geräteschlüssel konnte nicht erstellt werden');
        return;
      }
    }

    if (this.ws) {
      this.ws.close();
    }

    console.log('[GW] Connecting to', this.url);
    this.ws = new WebSocket(this.url);

    this.ws.onopen = () => {
      console.log('[GW] WebSocket connected to', this.url);
      this.reconnectDelay = 1000;
      this.reconnectAttempts = 0;
    };

    this.ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data);
        console.log('[GW] <<<', msg.type, msg.event || msg.method || '', JSON.stringify(msg).slice(0, 200));
        this.handleMessage(msg);
      } catch (e) {
        console.error('[GW] Failed to parse message:', event.data, e);
      }
    };

    this.ws.onclose = (event) => {
      console.log('[GW] WebSocket closed:', event.code, event.reason);
      this.callbacks.onDisconnected();
      this.scheduleReconnect();
    };

    this.ws.onerror = (event) => {
      console.error('[GW] WebSocket error:', event);
      this.callbacks.onError('Verbindungsfehler');
    };
  }

  disconnect() {
    this.stopReconnecting();
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
  }

  private stopReconnecting() {
    this.reconnectAttempts = this.maxReconnectAttempts; // prevent further attempts
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  private scheduleReconnect() {
    if (this.reconnectTimer) return;
    if (this.reconnectAttempts >= this.maxReconnectAttempts) {
      console.log('[GW] Max reconnect attempts reached, giving up');
      this.callbacks.onError('Verbindung fehlgeschlagen — bitte Seite neu laden');
      return;
    }
    this.reconnectAttempts++;
    const jitter = this.reconnectDelay * 0.3 * Math.random();
    const delay = this.reconnectDelay + jitter;
    console.log(`[GW] Reconnecting in ${Math.round(delay)}ms (attempt ${this.reconnectAttempts}/${this.maxReconnectAttempts})`);
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null;
      this.reconnectDelay = Math.min(this.reconnectDelay * 2, 30000);
      this.connect();
    }, delay);
  }

  private handleMessage(msg: any) {
    if (msg.type === 'event' && msg.event === 'connect.challenge') {
      this.connectNonce = msg.payload?.nonce || null;
      console.log('[GW] Got challenge nonce:', this.connectNonce);
      this.sendConnect();
      return;
    }

    if (msg.type === 'res') {
      const resolver = this.pendingRequests.get(msg.id);
      if (resolver) {
        this.pendingRequests.delete(msg.id);
        resolver(msg);
      }
      return;
    }

    if (msg.type === 'event' && msg.event === 'chat') {
      this.handleChatEvent(msg.payload);
      return;
    }
  }

  private handleChatEvent(payload: ChatEvent) {
    const runId = payload.runId || '';

    if (payload.state === 'delta' && payload.message) {
      const text = payload.message.content
        .filter((c) => c.type === 'text' && c.text)
        .map((c) => c.text!)
        .join('');
      if (text) {
        this.callbacks.onChatDelta(text, runId);
      }
      return;
    }

    if (payload.state === 'final' && payload.message) {
      const text = payload.message.content
        .filter((c) => c.type === 'text' && c.text)
        .map((c) => c.text!)
        .join('');
      this.callbacks.onChatFinal(text, runId);
      return;
    }

    if (payload.state === 'error') {
      this.callbacks.onChatError(payload.errorMessage || 'Unbekannter Fehler');
      return;
    }
  }

  private async sendConnect() {
    if (!this.device) {
      this.callbacks.onError('Kein Geräteschlüssel');
      return;
    }

    // Sign the challenge nonce with device key
    let deviceBlock: any = {
      id: this.device.id,
      publicKey: this.device.publicKeyRaw,
    };

    if (this.connectNonce) {
      try {
        const { signature, signedAt } = await signChallenge(
          this.device,
          this.connectNonce,
          CLIENT_ID,
          CLIENT_MODE,
          ROLE,
          SCOPES,
          this.token,
        );
        deviceBlock.signature = signature;
        deviceBlock.signedAt = signedAt;
        deviceBlock.nonce = this.connectNonce;
      } catch (e) {
        console.error('[GW] Failed to sign challenge:', e);
      }
    }

    const id = this.nextId('connect');
    const res = await this.request(id, 'connect', {
      minProtocol: 3,
      maxProtocol: 3,
      client: {
        id: CLIENT_ID,
        version: '1.0.0',
        platform: 'web',
        mode: CLIENT_MODE,
      },
      role: ROLE,
      scopes: SCOPES,
      caps: [],
      commands: [],
      permissions: {},
      auth: { token: this.token },
      device: deviceBlock,
      locale: 'de',
      userAgent: navigator.userAgent,
    });

    if (!res.ok) {
      const errMsg = typeof res.error === 'string' ? res.error : res.error?.message || 'Verbindung fehlgeschlagen';
      const errCode = typeof res.error === 'object' ? res.error?.code : '';
      console.error('[GW] Connect failed:', res.error);

      if (errCode === 'NOT_PAIRED') {
        console.log('[GW] Device not paired — waiting for approval');
        this.callbacks.onPairing();
        // Don't disconnect — wait for server to approve
        return;
      }

      // Stop reconnecting on auth failures — retrying won't help
      if (errCode === 'UNAUTHORIZED' || errCode === 'AUTH_FAILED' || errCode === 'TOKEN_INVALID'
          || errCode === 'INVALID_REQUEST'
          || errMsg.includes('unauthorized') || errMsg.includes('auth')) {
        this.stopReconnecting();
        this.callbacks.onError(errMsg, 'AUTH_FAILED');
        return;
      }

      this.callbacks.onError(errMsg, errCode);
      return;
    }

    console.log('[GW] Connected successfully! Payload:', JSON.stringify(res.payload).slice(0, 300));

    // Get or create session
    await this.initSession();

    // Get agent identity
    let identity: AgentIdentity | null = null;
    try {
      const idRes = await this.request(this.nextId('identity'), 'agent.identity.get', {
        sessionKey: this.sessionKey,
      });
      if (idRes.ok && idRes.payload) {
        identity = idRes.payload as AgentIdentity;
      }
    } catch {
      // Identity fetch is optional
    }

    this.callbacks.onConnected(identity);
  }

  private async initSession() {
    // List existing sessions
    const listRes = await this.request(this.nextId('sessions'), 'sessions.list', {});
    console.log('[GW] Sessions list:', JSON.stringify(listRes).slice(0, 200));
    if (listRes.ok && listRes.payload?.sessions?.length > 0) {
      this.sessionKey = listRes.payload.sessions[0].key;
      console.log('[GW] Using existing session:', this.sessionKey);
      return;
    }

    // Create new session
    const createRes = await this.request(this.nextId('session-create'), 'sessions.create', {
      channel: 'webchat',
    });
    console.log('[GW] Session create:', JSON.stringify(createRes).slice(0, 200));
    if (createRes.ok && createRes.payload?.key) {
      this.sessionKey = createRes.payload.key;
    } else {
      // Fallback
      this.sessionKey = 'agent:main:main';
    }
    console.log('[GW] Session key:', this.sessionKey);
  }

  async sendMessage(text: string) {
    if (!this.ws || !this.sessionKey) return;

    const id = this.nextId('chat');
    const res = await this.request(id, 'chat.send', {
      sessionKey: this.sessionKey,
      message: text,
      deliver: false,
      idempotencyKey: `msg-${Date.now()}-${Math.random().toString(36).slice(2)}`,
    });
    console.log('[GW] chat.send response:', JSON.stringify(res).slice(0, 200));
  }

  async loadHistory(): Promise<Array<{ role: string; text: string }>> {
    if (!this.sessionKey) return [];
    const res = await this.request(this.nextId('history'), 'chat.history', {
      sessionKey: this.sessionKey,
      limit: 50,
    });
    if (!res.ok || !res.payload?.messages) return [];

    // Only show user and assistant messages with actual text content
    // Filter out: thinking blocks, tool_use, tool_result, raw JSON
    return res.payload.messages
      .filter((m: any) => m.role === 'user' || m.role === 'assistant')
      .map((m: any) => {
        const textParts = (m.content || [])
          .filter((c: any) => c.type === 'text' && c.text && !c.text.startsWith('{'))
          .map((c: any) => c.text);
        return {
          role: m.role,
          text: textParts.join('\n'),
        };
      })
      .filter((m: any) => m.text.trim().length > 0);
  }

  private request(id: string, method: string, params: any): Promise<any> {
    return new Promise((resolve) => {
      const timeout = setTimeout(() => {
        this.pendingRequests.delete(id);
        resolve({ ok: false, error: 'Zeitüberschreitung' });
      }, 30000);

      this.pendingRequests.set(id, (res) => {
        clearTimeout(timeout);
        resolve(res);
      });

      this.send({ type: 'req', id, method, params });
    });
  }

  private send(data: any) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      console.log('[GW] >>>', data.type, data.method || '', data.id || '');
      this.ws.send(JSON.stringify(data));
    } else {
      console.warn('[GW] Cannot send, ws state:', this.ws?.readyState);
    }
  }

  private nextId(prefix: string): string {
    return `${prefix}-${++this.reqCounter}`;
  }
}
