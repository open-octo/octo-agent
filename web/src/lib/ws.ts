import { writable } from "svelte/store";

export const wsState = writable<"connecting" | "connected" | "disconnected">("disconnected");

// Reconnect telemetry for the disconnect banner: which attempt we're on and the
// wall-clock time the next attempt fires (so the banner can show a countdown).
export const wsReconnect = writable<{ attempt: number; nextAt: number } | null>(null);

type Handler = (event: Record<string, unknown>) => void;

const BACKOFF_STEPS = [1000, 2000, 4000, 8000, 16000, 30000];

export class WsManager {
  private ws: WebSocket | null = null;
  private handlers: Map<string, Set<Handler>> = new Map();
  private anyHandlers: Set<Handler> = new Set();
  private queue: string[] = [];
  private backoffIndex = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private intentionalClose = false;

  connect(): void {
    if (this.ws && (this.ws.readyState === WebSocket.CONNECTING || this.ws.readyState === WebSocket.OPEN)) {
      return;
    }
    this.intentionalClose = false;
    const protocol = location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${protocol}//${location.host}/ws`;
    wsState.set("connecting");
    this.ws = new WebSocket(url);

    this.ws.onopen = () => {
      this.backoffIndex = 0;
      wsReconnect.set(null);
      wsState.set("connected");
      const pending = this.queue.splice(0);
      for (const msg of pending) {
        this.ws!.send(msg);
      }
    };

    this.ws.onmessage = (ev: MessageEvent) => {
      let event: Record<string, unknown>;
      try {
        event = JSON.parse(ev.data as string) as Record<string, unknown>;
      } catch {
        return;
      }
      this.dispatch(event);
    };

    this.ws.onclose = () => {
      wsState.set("disconnected");
      if (!this.intentionalClose) {
        this.scheduleReconnect();
      }
    };

    this.ws.onerror = () => {
      wsState.set("disconnected");
      // onclose will also fire after onerror, so reconnect is handled there
    };
  }

  disconnect(): void {
    this.intentionalClose = true;
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
    wsReconnect.set(null);
    wsState.set("disconnected");
  }

  subscribe(sessionId: string): void {
    this.send({ type: "subscribe", session_id: sessionId });
  }

  unsubscribe(sessionId: string): void {
    this.send({ type: "unsubscribe", session_id: sessionId });
  }

  sendMessage(sessionId: string, content: string, files?: unknown[], force?: boolean): void {
    const payload: Record<string, unknown> = {
      type: "user_message",
      session_id: sessionId,
      content,
    };
    if (files !== undefined) {
      payload.files = files;
    }
    if (force) {
      payload.force = true;
    }
    this.send(payload);
  }

  interrupt(sessionId: string): void {
    this.send({ type: "interrupt", session_id: sessionId });
  }

  answerConfirmation(confId: string, result: unknown): void {
    this.send({ type: "confirmation", id: confId, result });
  }

  answerQuestion(
    questionId: string,
    choices: unknown[],
    custom?: string,
    cancelled?: boolean
  ): void {
    this.send({
      type: "user_question_answer",
      question_id: questionId,
      choices,
      custom,
      cancelled,
    });
  }

  promoteSyncTerminal(sessionId: string): void {
    this.send({ type: "promote_sync_terminal", session_id: sessionId });
  }

  promoteSyncSubAgent(sessionId: string): void {
    this.send({ type: "promote_sync_sub_agent", session_id: sessionId });
  }

  retry(sessionId: string): void {
    this.send({ type: "retry", session_id: sessionId });
  }

  rollback(sessionId: string): void {
    this.send({ type: "rollback", session_id: sessionId });
  }

  on(type: string, handler: Handler): () => void {
    if (!this.handlers.has(type)) {
      this.handlers.set(type, new Set());
    }
    this.handlers.get(type)!.add(handler);
    return () => {
      this.handlers.get(type)?.delete(handler);
    };
  }

  onAny(handler: Handler): () => void {
    this.anyHandlers.add(handler);
    return () => {
      this.anyHandlers.delete(handler);
    };
  }

  private send(data: Record<string, unknown>): void {
    const msg = JSON.stringify(data);
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(msg);
    } else {
      this.queue.push(msg);
    }
  }

  private dispatch(event: Record<string, unknown>): void {
    const type = event.type as string | undefined;
    if (type) {
      const set = this.handlers.get(type);
      if (set) {
        for (const h of set) {
          h(event);
        }
      }
    }
    for (const h of this.anyHandlers) {
      h(event);
    }
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer !== null) {
      return;
    }
    const delay = BACKOFF_STEPS[Math.min(this.backoffIndex, BACKOFF_STEPS.length - 1)];
    this.backoffIndex = Math.min(this.backoffIndex + 1, BACKOFF_STEPS.length - 1);
    wsReconnect.set({ attempt: this.backoffIndex, nextAt: Date.now() + delay });
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      if (!this.intentionalClose) {
        this.connect();
      }
    }, delay);
  }
}

export const ws = new WsManager();
