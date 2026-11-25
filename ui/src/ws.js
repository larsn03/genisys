// WS client with auto-reconnect + last_event_id resume (SPEC §21).
import { TOKEN } from "./api";
export class WSConn {
    session;
    onEvent;
    onStatus;
    ws = null;
    lastEvent = 0;
    closed = false;
    retry = 0;
    constructor(session, onEvent, onStatus) {
        this.session = session;
        this.onEvent = onEvent;
        this.onStatus = onStatus;
    }
    connect() {
        if (this.closed)
            return;
        const proto = location.protocol === "https:" ? "wss" : "ws";
        const url = `${proto}://${location.host}/ws?session=${this.session}&token=${TOKEN}&last_event_id=${this.lastEvent}`;
        this.ws = new WebSocket(url);
        this.ws.onopen = () => {
            this.retry = 0;
            this.onStatus(true);
        };
        this.ws.onmessage = (m) => {
            try {
                const ev = JSON.parse(m.data);
                if (ev.id > this.lastEvent)
                    this.lastEvent = ev.id;
                this.onEvent(ev);
            }
            catch {
                /* keep-alive junk */
            }
        };
        this.ws.onclose = () => {
            this.onStatus(false);
            if (this.closed)
                return;
            setTimeout(() => this.connect(), Math.min(5000, 300 * 2 ** this.retry++));
        };
    }
    send(cmd) {
        if (this.ws?.readyState === WebSocket.OPEN)
            this.ws.send(JSON.stringify(cmd));
    }
    close() {
        this.closed = true;
        this.ws?.close();
    }
}
