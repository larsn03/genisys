package model

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

// SSEEvent is one dispatched Server-Sent Events record.
type SSEEvent struct {
	Event string // "message" when unspecified
	Data  string
	ID    string
	Retry *time.Duration
}

// ParseSSE implements the client side of the SSE wire format (RFC-style):
// LF/CRLF tolerant, multi-line data joined with \n, comments (":...") and
// unknown fields ignored, dispatch on blank line and at EOF.
//
// onEvent returning an error aborts parsing (error propagated).
func ParseSSE(r io.Reader, onEvent func(SSEEvent) error) error {
	br := bufio.NewReader(r)
	var (
		data  strings.Builder
		ev    SSEEvent
		lines int
	)
	dispatch := func() error {
		if data.Len() == 0 && ev.Event == "" {
			return nil
		}
		e := ev
		e.Data = strings.TrimSuffix(data.String(), "\n")
		if e.Event == "" {
			e.Event = "message"
		}
		data.Reset()
		ev = SSEEvent{}
		if lines > 0 {
			lines = 0
		}
		return onEvent(e)
	}
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			switch {
			case line == "":
				if derr := dispatch(); derr != nil {
					return derr
				}
			case strings.HasPrefix(line, ":"): // comment / keep-alive
			default:
				field, value, _ := strings.Cut(line, ":")
				value = strings.TrimPrefix(value, " ")
				switch field {
				case "data":
					data.WriteString(value)
					data.WriteString("\n")
				case "event":
					ev.Event = value
				case "id":
					ev.ID = value
				case "retry":
					if ms, cerr := strconv.Atoi(value); cerr == nil {
						d := time.Duration(ms) * time.Millisecond
						ev.Retry = &d
					}
				default: // unknown field: ignore per spec
				}
			}
			lines++
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return dispatch() // flush trailing record without blank line
			}
			return err
		}
	}
}
