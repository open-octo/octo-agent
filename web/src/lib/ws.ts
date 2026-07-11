import { writable } from "svelte/store";
import { isUnauthorized, reauth } from "./auth";

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
  // Session IDs we're subscribed to. Subscriptions are per-connection on the
  // server, so a reconnect lands on a fresh wsConn with an empty subscriber
  // set. We replay these on every (re)open, otherwise turn events broadcast to
  // the session's subscribers never reach us and the composer hangs forever.
  private subscriptions: Set<string> = new Set();
  private backoffIndex = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private intentionalClose = false;
  // False until the first successful open. Distinguishes a genuine reconnect
  // (where subscribed sessions need a history resync below) from the very
  // first connect of the app's lifetime, where each view's own mount effect
  // already loads history and a synthetic resync would just be a redundant
  // fetch.
  private hasConnectedOnce = false;

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
      const isReconnect = this.hasConnectedOnce;
      this.hasConnectedOnce = true;
      this.backoffIndex = 0;
      wsReconnect.set(null);
      wsState.set("connected");
      // Re-subscribe first so the server has registered our sessions before it
      // processes any queued user_message below (else the turn broadcasts to a
      // subscriber set we're not yet in).
      for (const sessionId of this.subscriptions) {
        this.ws!.send(JSON.stringify({ type: "subscribe", session_id: sessionId }));
      }
      const pending = this.queue.splice(0);
      for (const msg of pending) {
        this.ws!.send(msg);
      }
      // The socket carries no backlog: any turn output broadcast to a
      // subscribed session while we were down (laptop sleep, a network blip)
      // is otherwise a permanent hole in the rendered transcript — missing
      // text, tool cards stuck "running". Synthesize the same event ChatView
      // already handles for /clear and /compact to force a re-fetch.
      if (isReconnect) {
        for (const sessionId of this.subscriptions) {
          this.dispatch({ type: "history_reload", session_id: sessionId });
        }
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
        void this.handleUnexpectedClose();
      }
    };

    this.ws.onerror = () => {
      wsState.set("disconnected");
      // onclose will also fire after onerror, so reconnect is handled there
    };
  }

  // A revoked/expired access key fails the WS upgrade at the HTTP layer
  // before any frame ever opens, so the browser's close event carries no
  // status the client can read — it looks identical to a dropped connection.
  // Probe a real authenticated endpoint to tell the two apart before
  // committing to a backoff cycle that would otherwise retry the same
  // rejection forever with no way out.
  private async handleUnexpectedClose(): Promise<void> {
    const unauthorized = await isUnauthorized();
    // The probe is async — disconnect() (e.g. an unmount/HMR teardown) may
    // have run while it was in flight. Its contract is to stop all future
    // reconnect attempts, so re-check here before arming anything: even a
    // timer whose own callback later no-ops still resurrects the
    // "reconnecting" banner via wsReconnect.set() the moment it's scheduled.
    if (this.intentionalClose) return;
    if (!unauthorized) {
      this.scheduleReconnect();
      return;
    }
    const ok = await reauth();
    if (this.intentionalClose) return;
    if (ok) {
      this.connect();
    }
    // ok === false: the user cancelled or exhausted their retries — stay
    // disconnected rather than looping on a rejection that will not resolve
    // itself. AuthGate's own retry limit already gave them the chance.
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
    this.subscriptions.add(sessionId);
    this.send({ type: "subscribe", session_id: sessionId });
  }

  unsubscribe(sessionId: string): void {
    this.subscriptions.delete(sessionId);
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

  // Retract a mid-turn steer message the running turn hasn't consumed yet. The
  // server answers with steer_retracted (pulled back — reload it for editing) or
  // steer_retract_failed (already drained — keep the bubble).
  retractSteer(sessionId: string, pendingId: string, text: string): void {
    this.send({
      type: "retract_steer",
      session_id: sessionId,
      pending_id: pendingId,
      text,
    });
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
