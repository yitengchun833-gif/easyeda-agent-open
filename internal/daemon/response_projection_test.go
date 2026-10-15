package daemon

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"
	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func TestPublicResponseProjectsBeforeTransportWithoutChangingEvidence(t *testing.T) {
	resp := protocol.Response{OK: true, Result: map[string]any{
		"partial": true, "_baseline": strings.Repeat("x", 33<<20),
		"results":       []any{map[string]any{"_readback": []any{"proof"}, "_scene": map[string]any{"sequence": 1}, "ok": false}},
		"otherProperty": map[string]any{"_scene": "user metadata"},
	}}
	request := httptest.NewRequest("POST", "/action", nil)
	raw := httptest.NewRecorder()
	writeActionResponse(raw, request, 200, &resp)
	request.Header.Set("X-Easyeda-Response", "public-v1")
	compact := httptest.NewRecorder()
	writeActionResponse(compact, request, 200, &resp)
	if raw.Body.Len() <= 32<<20 || compact.Body.Len() > 1024 {
		t.Fatalf("projection bytes raw=%d public=%d", raw.Body.Len(), compact.Body.Len())
	}
	if !strings.Contains(compact.Body.String(), `"partial":true`) || !strings.Contains(compact.Body.String(), "user metadata") || strings.Contains(compact.Body.String(), "proof") {
		t.Fatal("public result lost outcomes or leaked private proof")
	}
	if len(resp.Result["_baseline"].(string)) != 33<<20 || runtimeMap(runtimeArray(resp.Result["results"])[0])["_readback"] == nil {
		t.Fatal("shared recovery evidence was mutated")
	}
	t.Logf("HTTP body: raw %d bytes -> public %d bytes (synthetic private 33 MiB)", raw.Body.Len(), compact.Body.Len())
}

func TestPublicDispatchRetainsAuditAndRuntimeReadback(t *testing.T) {
	s := New(Options{AuditDir: t.TempDir()})
	server := httptest.NewServer(s.routes(0))
	defer server.Close()
	base := strings.TrimPrefix(server.URL, "http://")
	connector := dialConnector(t, base, "w")
	defer connector.CloseNow()
	waitForWindow(t, base, "w")
	native, _ := s.hub.target("w")
	native.applyResponseContext(protocol.Context{ProjectUUID: "p", DocumentUUID: "d", DocumentType: "schematic"}, time.Now())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		for {
			var request protocol.Request
			if wsjson.Read(ctx, connector, &request) != nil {
				return
			}
			if request.Type != protocol.TypeRequest {
				continue
			}
			_ = wsjson.Write(ctx, connector, protocol.Response{Envelope: protocol.Envelope{ID: request.ID, Type: protocol.TypeResponse}, OK: true, Context: &protocol.Context{ProjectUUID: "p", DocumentUUID: "d", DocumentType: "schematic"}, Result: map[string]any{
				"partial": true, "completed": 1, "_baseline": map[string]any{"marker": "private-proof"},
				"_readback": []any{map[string]any{"action": "schematic.components.list", "result": map[string]any{"components": []any{map[string]any{"primitiveId": "r", "x": 25}}, "readScope": map[string]any{"complete": false}}}},
			}})
		}
	}()
	r := httptest.NewRequest("POST", "/action", strings.NewReader(`{"action":"document.current","windowId":"w"}`))
	r.Header.Set("X-Easyeda-Response", "public-v1")
	w := httptest.NewRecorder()
	s.handleAction(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-proof") || !strings.Contains(w.Body.String(), `"partial":true`) {
		t.Fatalf("dispatch: %s", w.Body.String())
	}
	d := s.runtime.documents[s.runtime.current["w"]]
	if d == nil || d.Objects["r"] == nil {
		t.Fatal("runtime lost actual readback")
	}
	files, _ := filepath.Glob(filepath.Join(s.audit.Dir(), "*.jsonl"))
	if len(files) != 1 {
		t.Fatal("audit missing")
	}
	data, err := os.ReadFile(files[0])
	if err != nil || !strings.Contains(string(data), "private-proof") || !strings.Contains(string(data), "_readback") {
		t.Fatal("audit lost private evidence")
	}
}

func BenchmarkAuditAppend64KiB(b *testing.B) {
	a := newAuditWriter(b.TempDir())
	entry := auditEntry{Timestamp: time.Now(), Action: "read", Result: map[string]any{"_readback": strings.Repeat("x", 64<<10)}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Append(entry)
	}
}

func BenchmarkCheckpointEncoding1000Objects(b *testing.B) {
	s := newRuntimeStore()
	d := s.document("w", &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"})
	for i := 0; i < 1000; i++ {
		id := strings.Repeat("r", 1) + time.Unix(int64(i), 0).String()
		d.Objects[id] = &runtimeObject{Data: map[string]any{"primitiveId": id, "x": i, "pins": []any{map[string]any{"pinNumber": "1", "net": "GND"}}}, StaleFields: map[string]bool{}}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.mu.Lock()
		_, err := json.Marshal(runtimeCheckpoint{1, time.Now(), s.documents, s.current, s.receipts})
		s.mu.Unlock()
		if err != nil {
			b.Fatal(err)
		}
	}
}
