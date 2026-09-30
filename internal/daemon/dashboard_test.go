package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func TestDashboardSSEUsesCachedStateAndReconnects(t *testing.T) {
	s := New(Options{AuditDir: t.TempDir()})
	d := s.runtime.document("w", &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"})
	d.Objects["x"] = &runtimeObject{Data: map[string]any{"kind": "component", "secretFullObject": "must-not-stream"}, StaleFields: map[string]bool{"pins": true}}
	s.runtime.emit("changed", map[string]any{"payload": "must-not-stream"})
	server := httptest.NewServer(s.routes(0))
	defer server.Close()
	read := func(cursor string) (string, map[string]any) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/runtime/events", nil)
		req.Header.Set("Last-Event-ID", cursor)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
			t.Fatal("SSE unavailable")
		}
		scanner := bufio.NewScanner(response.Body)
		var id string
		var frame map[string]any
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "id: ") {
				id = strings.TrimPrefix(line, "id: ")
			}
			if strings.HasPrefix(line, "data: ") {
				if strings.Contains(line, "must-not-stream") {
					t.Fatal("raw object or event payload leaked into console")
				}
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame); err != nil {
					t.Fatal(err)
				}
				break
			}
		}
		if frame == nil {
			t.Fatalf("no initial state: %v", scanner.Err())
		}
		return id, frame
	}
	cursor, first := read("")
	if first["reset"] != true || runtimeMap(first["state"])["edaReads"] != float64(0) {
		t.Fatal("first connection must synchronize cached state")
	}
	_, second := read(cursor)
	if second["reset"] != false || len(runtimeArray(second["events"])) != 0 {
		t.Fatal("same cursor replayed events")
	}
	if len(s.runtime.receipts) != 0 || s.runtime.sequence != 1 {
		t.Fatal("console or reconnect dispatched an EDA request")
	}
}

func TestDashboardReplayGapAndInstanceReset(t *testing.T) {
	s := newRuntimeStore()
	for i := 0; i < 260; i++ {
		s.emit("progress", map[string]any{"requestId": "r"})
	}
	frame, cursor, _ := s.dashboardFrame(s.instance + ":258")
	if frame["reset"] != false || len(runtimeArray(frame["events"])) != 2 {
		t.Fatal("retained events must replay exactly once")
	}
	for _, old := range []string{s.instance + ":1", "previous:260", s.instance + ":999", "invalid"} {
		frame, _, _ := s.dashboardFrame(old)
		if frame["reset"] != true {
			t.Fatalf("missing history must reset: %s", old)
		}
	}
	if cursor != s.instance+":260" || len(s.events) != 256 {
		t.Fatal("cursor or event bound incorrect")
	}
}

func TestDashboardControlsUseCommonActionDispatch(t *testing.T) {
	base, cleanup := startDaemon(t)
	defer cleanup()
	c := dialConnector(t, base, "dashboard-window")
	defer c.Close(websocket.StatusNormalClosure, "")
	go echoRequests(t.Context(), c)
	waitForWindow(t, base, "dashboard-window")
	req, _ := http.NewRequest("POST", "http://"+base+"/runtime/control", strings.NewReader(`{"operation":"pause","window":"dashboard-window"}`))
	req.Header.Set("X-Easyeda-Control", "1")
	req.Header.Set("Origin", "http://"+base)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result protocol.Response
	if json.NewDecoder(response.Body).Decode(&result) != nil || !result.OK || result.Result["echo"] != "debug.control" {
		t.Fatalf("UI control did not follow the existing action chain: %+v", result)
	}
	page, err := http.Get("http://" + base + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	defer page.Body.Close()
	html, _ := io.ReadAll(page.Body)
	if page.StatusCode != 200 || !strings.Contains(string(html), "fetch('/runtime/control'") || strings.Contains(string(html), "fetch('/action'") {
		t.Fatal("console did not expose the shared control entry point")
	}
	blocked := httptest.NewRecorder()
	foreign := httptest.NewRequest("GET", "/runtime/events", nil)
	foreign.Header.Set("Origin", "https://foreign.example")
	New(Options{AuditDir: t.TempDir()}).handleRuntimeEvents(blocked, foreign)
	if blocked.Code != http.StatusForbidden {
		t.Fatal("cross-origin event access allowed")
	}
}
