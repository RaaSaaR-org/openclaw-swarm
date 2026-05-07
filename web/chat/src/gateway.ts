// Thin WebSocket client. Talks to the chat Go backend on the same origin.
// The backend handles OpenClaw device pairing, signing, and session bookkeeping.

export interface ChatHistoryMessage {
  role: 'user' | 'assistant';
  text: string;
}

export interface GatewayCallbacks {
  onReady: (email: string) => void;
  onHistory: (messages: ChatHistoryMessage[]) => void;
  onChatDelta: (text: string, runId: string) => void;
  onChatFinal: (text: string, runId: string) => void;
  onChatError: (error: string) => void;
  onDisconnected: () => void;
  onError: (error: string) => void;
}

export class GatewayClient {
  private ws: WebSocket | null = null;
  private reconnectTimer: number | null = null;
  private reconnectAttempts = 0;
  private maxReconnectAttempts = 5;
  private reconnectDelay = 1000;
  private url: string;
  private callbacks: GatewayCallbacks;

  constructor(slug: string, callbacks: GatewayCallbacks) {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    this.url = `${proto}//${window.location.host}/chat/${encodeURIComponent(slug)}/ws`;
    this.callbacks = callbacks;
  }

  connect() {
    if (this.ws) {
      this.ws.close();
    }
    this.ws = new WebSocket(this.url);

    this.ws.onopen = () => {
      this.reconnectAttempts = 0;
      this.reconnectDelay = 1000;
    };

    this.ws.onmessage = (event) => {
      let msg: any;
      try {
        msg = JSON.parse(event.data);
      } catch {
        return;
      }
      switch (msg.type) {
        case 'ready':
          this.callbacks.onReady(msg.email || '');
          return;
        case 'history':
          this.callbacks.onHistory((msg.messages || []) as ChatHistoryMessage[]);
          return;
        case 'delta':
          this.callbacks.onChatDelta(msg.text || '', msg.runId || '');
          return;
        case 'final':
          this.callbacks.onChatFinal(msg.text || '', msg.runId || '');
          return;
        case 'error':
          this.callbacks.onChatError(msg.message || 'Unknown error');
          return;
      }
    };

    this.ws.onclose = () => {
      this.callbacks.onDisconnected();
      this.scheduleReconnect();
    };

    this.ws.onerror = () => {
      this.callbacks.onError('Connection error');
    };
  }

  disconnect() {
    this.stopReconnecting();
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
  }

  send(text: string) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: 'send', text }));
    }
  }

  requestHistory() {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: 'history' }));
    }
  }

  private stopReconnecting() {
    this.reconnectAttempts = this.maxReconnectAttempts;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  private scheduleReconnect() {
    if (this.reconnectTimer) return;
    if (this.reconnectAttempts >= this.maxReconnectAttempts) return;
    this.reconnectAttempts++;
    const jitter = this.reconnectDelay * 0.3 * Math.random();
    const delay = this.reconnectDelay + jitter;
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null;
      this.reconnectDelay = Math.min(this.reconnectDelay * 2, 30000);
      this.connect();
    }, delay);
  }
}
