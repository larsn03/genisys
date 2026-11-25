// ws.go — the live wire (SPEC §21): one connection per session; server→
// client event frames replayed gap-free from the ledger via
// last_event_id, client→server command frames (prompt/stop/permission/
// interjection ops).

package api

import (
	"net/http"
	"strconv"
	"time"

	"context"

	"github.com/gorilla/websocket"
)

// clientFrame is one command from the UI.
type clientFrame struct {
	Type string `json:"type"` // prompt|stop|permission|withdraw|promote

	Text  string `json:"text,omitempty"`
	Mode  string `json:"mode,omitempty"`   // queue|interrupt (prompt)
	ID    int64  `json:"id,omitempty"`     // permission id
	IDStr string `json:"id_str,omitempty"` // interjection id
	Grant *bool  `json:"granted,omitempty"`
}

// WSEvent is one event frame to the UI.
type WSEvent struct {
	Type    string         `json:"type"`
	ID      int64          `json:"id"`
	Session string         `json:"session_id"`
	Step    string         `json:"step_id,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
}

func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		writeErr(w, http.StatusBadRequest, "?session=<id> required")
		return
	}
	if _, err := s.App.DB.GetSession(sessionID); err != nil {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	wk, err := s.Sup.Worker(sessionID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	lastEvent, _ := strconv.ParseInt(r.URL.Query().Get("last_event_id"), 10, 64)

	// 1) replay from the ledger (gap-free reconnect, SPEC §21)
	if evs, err := s.App.DB.ListEventsAfter(sessionID, lastEvent); err == nil {
		for _, e := range evs {
			lastEvent = e.ID
			f := WSEvent{Type: e.Type, ID: e.ID, Session: sessionID, Step: e.StepID}
			writeEvent(conn, f)
		}
	}

	// 2) live fan-out (skip ledger ids already sent)
	subID, ch := wk.Hub.Subscribe()
	defer wk.Hub.Unsubscribe(subID)

	// writer goroutine: hub → conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		ping := time.NewTicker(30 * time.Second)
		defer ping.Stop()
		for {
			select {
			case e, ok := <-ch:
				if !ok {
					return
				}
				if e.ID != 0 && e.ID <= lastEvent {
					continue // already replayed
				}
				if e.ID != 0 {
					lastEvent = e.ID
				}
				writeEvent(conn, WSEvent{Type: e.Type, ID: e.ID, Session: sessionID, Step: e.StepID, Payload: e.Payload})
			case <-ping.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					return
				}
			}
		}
	}()

	// reader goroutine: conn → commands
	conn.SetReadLimit(1 << 20)
	go func() { // unblock reader on close
		<-done
		conn.Close()
	}()
	for {
		var f clientFrame
		if err := conn.ReadJSON(&f); err != nil {
			break
		}
		switch f.Type {
		case "prompt":
			go wk.Prompt(context.Background(), f.Text, f.Mode)
		case "stop":
			wk.Stop()
		case "permission":
			if f.Grant != nil {
				wk.AnswerPermission(f.ID, *f.Grant)
			}
		case "withdraw":
			wk.Interjections().Withdraw(f.IDStr)
		case "promote":
			wk.Interjections().Promote(f.IDStr)
		}
	}
}

func writeEvent(conn *websocket.Conn, f WSEvent) {
	conn.WriteJSON(f)
}
