// WS client with auto-reconnect + last_event_id resume (SPEC §21).

import type { WSEvent } from "./types";
import { TOKEN } from "./api";

export type Command =
  | { type: "prompt"; text: string; mode?: string }
  | { type: "stop" }
  | { type: "permission"; id: number; granted: boolean }
  | { type: "withdraw"; id_str: string }
  | { type: "promote"; id_str: string };

export class WSConn {
  private ws: WebSocket | null = null;
  private lastEvent = 0;
  private closed = false;
  private retry = 0;

  constructor(
    private session: string,
    private onEvent: (ev: WSEvent) => void,
    private onStatus: (up: boolean) => void,
  ) {}

  connect() {
    if (this.closed) return;
    const proto = location.protocol === "https:" ? "wss" : "ws";
    const url = `${proto}://${location.host}/ws?session=${this.session}&token=${TOKEN}&last_event_id=${this.lastEvent}`;
    this.ws = new WebSocket(url);
    this.ws.onopen = () => {
      this.retry = 0;
      this.onStatus(true);
    };
    this.ws.onmessage = (m) => {
      try {
        const ev = JSON.parse(m.data) as WSEvent;
        if (ev.id > this.lastEvent) this.lastEvent = ev.id;
        this.onEvent(ev);
      } catch {
        /* keep-alive junk */
      }
    };
    this.ws.onclose = () => {
      this.onStatus(false);
      if (this.closed) return;
      setTimeout(() => this.connect(), Math.min(5000, 300 * 2 ** this.retry++));
    };
  }

  send(cmd: Command) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(cmd));
  }

  close() {
    this.closed = true;
    this.ws?.close();
  }
}
