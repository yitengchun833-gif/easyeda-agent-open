package daemon

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket/wsjson"
	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRuntimeScopedReadAndMutation(t *testing.T) {
	s := newRuntimeStore()
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
	observe := func(result map[string]any) {
		s.observe(auditEntry{WindowID: "w", Context: ctx, Action: "schematic.components.list", OK: true, Result: runtimeMap(runtimeClone(result))})
	}
	observe(map[string]any{"components": []any{map[string]any{"primitiveId": "r1", "x": 10, "pinsAvailable": true, "netlistAvailable": true}, map[string]any{"primitiveId": "r2", "x": 20}}, "readScope": map[string]any{"complete": true}})
	d := s.documents[s.current["w"]]
	d.invalidate("schematic.component.modify", runtimeMap(runtimeClone(map[string]any{"primitiveId": "r1"})))
	if !d.Objects["r1"].StaleFields["geometry"] || d.Objects["r2"].StaleFields["geometry"] {
		t.Fatal("scope invalidation expanded geometry")
	}
	observe(map[string]any{"components": []any{map[string]any{"primitiveId": "r1", "x": 30}}, "readScope": map[string]any{"complete": false}})
	if len(d.Objects) != 2 || d.Objects["r1"].Data["x"] != float64(30) || !d.Objects["r1"].StaleFields["pins"] {
		t.Fatal("partial merge lost unaffected data or reused stale pin nets")
	}
	if len(runtimeArray(s.snapshot("w", map[string]bool{"r1": true})["documents"])) != 1 {
		t.Fatal("projection missing")
	}
}
func TestRuntimeCacheIsNotLiveReadOrSave(t *testing.T) {
	s := New(Options{})
	rr := httptest.NewRecorder()
	s.handleRuntimeState(rr, httptest.NewRequest("GET", "/runtime/state", nil))
	if rr.Code != 200 || s.runtime.sequence != 0 || len(s.runtime.receipts) != 0 {
		t.Fatal("dashboard state must not dispatch EDA reads")
	}
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
	d := s.runtime.document("w", ctx)
	d.ChangesSinceSave = true
	projection := runtimeMap(runtimeArray(s.runtime.snapshot("w", nil)["documents"])[0])
	if projection["freshness"] != "external_unconfirmed" || projection["requiresScopedReadback"] != true {
		t.Fatal("silent Beta events must not certify cached external freshness")
	}
	s.runtime.observe(auditEntry{WindowID: "w", Context: ctx, Action: "schematic.save", OK: true, Result: map[string]any{"ok": true}})
	if !d.ChangesSinceSave || d.SavedAt != nil {
		t.Fatal("action OK is not a save receipt")
	}
}
func TestRuntimePageIsolationAndEventGap(t *testing.T) {
	s := newRuntimeStore()
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
	d := s.document("w", ctx)
	d.Objects["r"] = &runtimeObject{Data: map[string]any{"primitiveId": "r"}, StaleFields: map[string]bool{}}
	d.SceneInstance = "first"
	d.Coverage["components"] = true
	s.observe(auditEntry{WindowID: "w", Context: ctx, OK: true, Action: "document.current", Result: map[string]any{"_scene": map[string]any{"instance": "second", "sequence": float64(1)}}})
	if !d.Objects["r"].StaleFields["pins"] || d.Coverage["components"] {
		t.Fatal("connector reload must invalidate old evidence")
	}
	other := s.document("w", &protocol.Context{ProjectUUID: "p", DocumentUUID: "other"})
	if other == d || len(other.Objects) != 0 {
		t.Fatal("page data leaked")
	}
}

func TestRuntimeRejectsForeignReadSchemas(t *testing.T) {
	s := newRuntimeStore()
	d := s.document("w", &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"})
	d.read("schematic.components.list", runtimeMap(runtimeClone(map[string]any{"components": []any{map[string]any{"primitiveId": "r", "pins": []any{map[string]any{"pinNumber": "1", "net": "N"}}}}, "readScope": map[string]any{"complete": true}})))
	for _, action := range []string{"schematic.read", "schematic.components.list"} {
		d.read(action, map[string]any{"components": []any{map[string]any{"primitiveId": "other-page"}}, "readScope": map[string]any{"allPages": true, "complete": true}})
	}
	if len(d.Objects) != 1 || d.Objects["r"] == nil {
		t.Fatal("multi-page or legacy response replaced the active-page baseline")
	}
}
func TestRuntimeDisplayScopeAndPrivateBaseline(t *testing.T) {
	s := newRuntimeStore()
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
	d := s.document("w", ctx)
	put := func(id string, data map[string]any) {
		data["primitiveId"] = id
		d.Objects[id] = &runtimeObject{Data: runtimeMap(runtimeClone(data)), StaleFields: map[string]bool{}}
	}
	box := func(x float64) map[string]any {
		return map[string]any{"minX": x, "maxX": x + 10, "minY": 0, "maxY": 10}
	}
	put("text", map[string]any{"kind": "text", "parentId": "wire", "x": 0, "y": 0, "bbox": box(0)})
	put("wire", map[string]any{"kind": "wire", "net": "GND", "x0": 0, "x1": 30, "y0": 0, "y1": 0})
	put("local", map[string]any{"kind": "component", "pins": []any{map[string]any{"x": 0, "y": 0, "net": "GND"}}, "bbox": box(0)})
	put("near", map[string]any{"kind": "component", "bbox": box(45)})
	put("far", map[string]any{"kind": "component", "pins": []any{map[string]any{"x": 500, "y": 0, "net": "GND"}}, "bbox": box(500)})
	for i := 0; i < 20; i++ { // map traversal order must never change the scope
		req := protocol.Request{Envelope: protocol.Envelope{WindowID: "w"}, Action: "schematic.attribute.modify", Payload: runtimeMap(runtimeClone(map[string]any{"primitiveId": "text", "props": map[string]any{"x": 50, "bold": true}, "_resumeBaseline": map[string]any{"fake": true}, "_edit": map[string]any{"baseline": map[string]any{"fake": true}}}))}
		s.prepare(&req, ctx)
		ids := req.Payload["_scope"].([]string)
		if len(ids) != 2 || !idsIn(ids, "local") || !idsIn(ids, "near") {
			t.Fatalf("scope widened to unrelated net or missed measured neighbour: %v", ids)
		}
		if req.Payload["_displayOnly"] != true || req.Payload["_resumeBaseline"] != nil || runtimeMap(req.Payload["_edit"])["baseline"] != nil {
			t.Fatal("caller metadata trusted or display edit misclassified")
		}
		d.invalidate(req.Action, req.Payload)
		if d.Objects["far"].StaleFields["pins"] {
			t.Fatal("display-only edit invalidated unrelated electrical facts")
		}
		d.Objects["text"].StaleFields = map[string]bool{}
		s.prepare(&req, ctx, runtimeDispatchProof{Baseline: map[string]any{"trusted": true}})
		if runtimeMap(req.Payload["_resumeBaseline"])["trusted"] != true {
			t.Fatal("private continuation baseline lost")
		}
	}
	if runtimeDisplayOnly("schematic.attribute.modify", map[string]any{"props": map[string]any{"value": "renamed"}}) {
		t.Fatal("electrical label rename treated as display-only")
	}
}
func runtimeTestControl(s *Server, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "http://local/runtime/control", strings.NewReader(body))
	r.Header.Set("X-Easyeda-Control", "1")
	w := httptest.NewRecorder()
	s.handleRuntimeControl(w, r)
	return w
}
func TestRuntimeFinishRequiresCompleteChecksAndExplicitDrc(t *testing.T) {
	s := New(Options{})
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d", DocumentType: "schematic"}
	d := s.runtime.document("w", ctx)
	d.Generation = 3
	r := &runtimeReceipt{RequestID: "task", WindowID: "w", ProjectUUID: "p", DocumentUUID: "d", Generation: 3, Status: "needs_readback", Checks: map[string]any{"status": "pass", "visualReview": map[string]any{"generation": uint64(3)}}}
	s.runtime.receipts = append(s.runtime.receipts, r)
	finish := func() *httptest.ResponseRecorder {
		return runtimeTestControl(s, `{"operation":"finish","window":"w","requestId":"task"}`)
	}
	if w := finish(); w.Code != 409 || len(s.runtime.receipts) != 1 {
		t.Fatal("partial task dispatched a save")
	}
	r.Status = "action_finished"
	r.ObservedFacts = map[string]any{"components": []any{map[string]any{"primitiveId": "r"}}, "readScope": map[string]any{"netRead": "not-requested"}}
	r.RequiredChecks = []string{"drc"}
	if w := finish(); w.Code != 409 {
		t.Fatal("missing declared DRC accepted")
	}
	s.runtime.observe(auditEntry{RequestID: "drc", WindowID: "w", Context: ctx, Action: "schematic.drc.check", OK: true, Result: map[string]any{"passed": true, "countsAvailable": false}})
	if w := finish(); w.Code != 409 {
		t.Fatal("unknown DRC counts accepted")
	}
	s.runtime.observe(auditEntry{RequestID: "drc-malformed", WindowID: "w", Context: ctx, Action: "schematic.drc.check", OK: true, Result: map[string]any{"countsAvailable": true, "summary": map[string]any{"error": "unknown", "fatal": 0, "unknown": 0}}})
	if w := finish(); w.Code != 409 {
		t.Fatal("non-numeric DRC count accepted")
	}
	s.runtime.observe(auditEntry{RequestID: "drc-good", WindowID: "w", Context: ctx, Action: "schematic.drc.check", OK: true, Result: map[string]any{"countsAvailable": true, "summary": map[string]any{"error": 0, "fatal": 0, "unknown": 0, "warn": 10}}})
	if w := finish(); w.Code != 503 {
		t.Fatalf("verified DRC with WARN should reach common dispatch (no connector fixture), got %d: %s", w.Code, w.Body.String())
	}
	d.Generation++
	if w := finish(); w.Code != 409 {
		t.Fatal("stale checks accepted")
	}
	if w := runtimeTestControl(s, `{"operation":"visual_review","window":"w","requestId":"task","source":"fiction"}`); w.Code != 400 {
		t.Fatal("unrecognized visual source accepted")
	}
}
func TestRuntimeEventGapAndContextInvalidateEvidence(t *testing.T) {
	s := newRuntimeStore()
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
	d := s.document("w", ctx)
	d.SceneInstance = "i"
	d.SceneSequence = 4
	d.Objects["unmentioned"] = &runtimeObject{Data: map[string]any{"primitiveId": "unmentioned"}, StaleFields: map[string]bool{}}
	s.connectorEvent("w", map[string]any{"kind": "scene_changed", "stamp": map[string]any{"instance": "i", "sequence": 6, "oldestSequence": 1, "delta": true, "changes": []any{map[string]any{"sequence": 6, "primitiveIds": []any{"other"}}}}})
	if !d.Objects["unmentioned"].StaleFields["geometry"] {
		t.Fatal("missed delta did not invalidate unmentioned object")
	}
	before := d.Generation
	s.connectionChanged("w", "context", &protocol.Context{ProjectUUID: "p", DocumentUUID: "else"})
	if s.documents[s.current["w"]] == d || d.Generation <= before {
		t.Fatal("page switch retained old acceptance generation")
	}
	s.connectionChanged("w", "context", &protocol.Context{})
	if s.current["w"] != "" {
		t.Fatal("empty active document retained former page")
	}
}
func TestRuntimeConcurrentResumeDoesNotReplaySteps(t *testing.T) {
	s := New(Options{})
	server := httptest.NewServer(s.routes(0))
	defer server.Close()
	base := strings.TrimPrefix(server.URL, "http://")
	connector := dialConnector(t, base, "w")
	defer connector.CloseNow()
	waitForWindow(t, base, "w")
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d", DocumentType: "schematic"}
	native, _ := s.hub.target("w")
	native.applyResponseContext(*ctx, time.Now())
	s.runtime.mu.Lock()
	d := s.runtime.document("w", ctx)
	s.runtime.receipts = append(s.runtime.receipts, &runtimeReceipt{RequestID: "paused", WindowID: "w", ProjectUUID: "p", DocumentUUID: "d", Generation: d.Generation, Status: "paused", Progress: map[string]any{"completed": 1, "results": []any{map[string]any{"ok": true}}}})
	s.runtime.requests["paused"] = protocol.Request{Envelope: protocol.Envelope{ID: "paused", WindowID: "w"}, Action: "debug.batch", Payload: map[string]any{"steps": []any{map[string]any{"action": "schematic.component.modify", "payload": map[string]any{"primitiveId": "done"}}, map[string]any{"action": "schematic.component.modify", "payload": map[string]any{"primitiveId": "remaining"}}}, "_scope": []any{"done", "remaining"}}}
	s.runtime.baselines["paused"] = map[string]any{"marker": "private-original"}
	s.runtime.receipts[len(s.runtime.receipts)-1].ObservedFacts = map[string]any{"marker": "private-observed"}
	s.runtime.mu.Unlock()
	callCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resumeSeen, release, resumed := make(chan struct{}), make(chan struct{}), make(chan protocol.Request, 1)
	go func() {
		for {
			var req protocol.Request
			if wsjson.Read(callCtx, connector, &req) != nil {
				return
			}
			if req.Type != protocol.TypeRequest {
				continue
			}
			if req.Action == "debug.control" {
				close(resumeSeen)
				select {
				case <-release:
				case <-callCtx.Done():
					return
				}
			} else {
				resumed <- req
			}
			_ = wsjson.Write(callCtx, connector, protocol.Response{Envelope: protocol.Envelope{ID: req.ID, Type: protocol.TypeResponse}, OK: true, Context: ctx, Result: map[string]any{"paused": false}})
		}
	}()
	body := `{"operation":"resume","window":"w","requestId":"paused"}`
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- runtimeTestControl(s, body) }()
	select {
	case <-resumeSeen:
	case <-callCtx.Done():
		t.Fatal("queue resume was not dispatched")
	}
	if second := runtimeTestControl(s, body); second.Code != 409 {
		t.Fatalf("concurrent resume accepted: %d", second.Code)
	}
	close(release)
	select {
	case req := <-resumed:
		steps := runtimeArray(req.Payload["steps"])
		if len(steps) != 1 || runtimeMap(runtimeMap(steps[0])["payload"])["primitiveId"] != "remaining" {
			t.Fatalf("completed action replayed: %v", steps)
		}
		if runtimeMap(req.Payload["_resumeBaseline"])["marker"] != "private-original" {
			t.Fatal("private baseline not propagated")
		}
		if runtimeMap(req.Payload["_expectedObserved"])["marker"] != "private-observed" {
			t.Fatal("private paused scope observation not propagated")
		}
	case <-callCtx.Done():
		t.Fatal("continuation not dispatched")
	}
	select {
	case response := <-first:
		if response.Code != 200 {
			t.Fatalf("resume response: %s", response.Body.String())
		}
		var result protocol.Response
		if json.Unmarshal(response.Body.Bytes(), &result) != nil || !result.OK {
			t.Fatal("invalid continuation result")
		}
	case <-callCtx.Done():
		t.Fatal("continuation did not finish")
	}
	if again := runtimeTestControl(s, body); again.Code != 409 {
		t.Fatal("consumed continuation replayed")
	}
}

func TestRuntimeChangedCaptureDoesNotCertifyVisualReview(t *testing.T) {
	s := newRuntimeStore()
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
	s.observe(auditEntry{RequestID: "image", WindowID: "w", Context: ctx, Action: "view.capture", OK: true, Result: map[string]any{"value": map[string]any{"base64": "image-data"}, "_scene": map[string]any{"instance": "one", "sequence": 1, "changedDuringAction": true}}})
	if s.receipts[0].ImageAvailable {
		t.Fatal("image from changing scene certified as current visual evidence")
	}
}

func TestRuntimeGeometryReadCannotKeepUnknownNetsFresh(t *testing.T) {
	s := newRuntimeStore()
	d := s.document("w", &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"})
	d.read("schematic.components.list", map[string]any{"components": []any{map[string]any{"primitiveId": "r", "pinsAvailable": true, "netlistAvailable": true, "pins": []any{map[string]any{"pinNumber": "1", "net": "GND"}}}}})
	if d.Objects["r"].StaleFields["pins"] {
		t.Fatal("confirmed net did not become fresh")
	}
	d.read("schematic.components.list", map[string]any{"components": []any{map[string]any{"primitiveId": "r", "pinsAvailable": true, "netlistAvailable": false, "pins": []any{map[string]any{"pinNumber": "1", "net": nil}}}}})
	if !d.Objects["r"].StaleFields["pins"] {
		t.Fatal("geometry-only null net silently retained fresh pin status")
	}
}
func TestRuntimeVisualReviewUpdatesDocumentProjection(t *testing.T) {
	s := New(Options{})
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
	d := s.runtime.document("w", ctx)
	d.Generation = 8
	s.runtime.receipts = []*runtimeReceipt{{RequestID: "task", WindowID: "w", ProjectUUID: "p", DocumentUUID: "d", Generation: 8, Checks: map[string]any{"status": "pass"}}, {RequestID: "image", Action: "view.capture", WindowID: "w", ProjectUUID: "p", DocumentUUID: "d", Generation: 8, ImageAvailable: true}}
	w := runtimeTestControl(s, `{"operation":"visual_review","window":"w","requestId":"task","generation":8,"screenshotRequestId":"image","source":"ai"}`)
	if w.Code != 200 || runtimeMap(runtimeMap(runtimeMap(d.Checks)["result"])["visualReview"])["source"] != "ai" {
		t.Fatal("visual review missing from document projection")
	}
}

func TestRuntimeGeometryReadDropsUnmeasuredCachedBBox(t *testing.T) {
	s := newRuntimeStore()
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
	d := s.document("w", ctx)
	d.read("schematic.components.list", map[string]any{"components": []any{map[string]any{"primitiveId": "r", "x": 0, "bbox": map[string]any{"minX": 0, "maxX": 10, "minY": 0, "maxY": 10}}}})
	d.invalidate("schematic.component.modify", map[string]any{"primitiveId": "r", "x": 200})
	d.read("schematic.components.list", map[string]any{"components": []any{map[string]any{"primitiveId": "r", "x": 200}}})
	if d.Objects["r"].Data["bbox"] != nil {
		t.Fatal("old bbox survived a geometry-only refresh at a new position")
	}
	d.read("schematic.components.list", map[string]any{"components": []any{map[string]any{"primitiveId": "r", "pinsAvailable": true, "netlistAvailable": true, "pins": []any{map[string]any{"pinNumber": "1", "net": nil}}}}})
	if !d.Objects["r"].StaleFields["pins"] {
		t.Fatal("null net accepted as fresh electrical evidence")
	}
}

func TestRuntimeConcurrentWriteCannotRefreshOlderEvidence(t *testing.T) {
	s := newRuntimeStore()
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d", DocumentType: "schematic"}
	d := s.document("w", ctx)
	write := func(id string) protocol.Request {
		return protocol.Request{Envelope: protocol.Envelope{ID: id, WindowID: "w"}, Action: "schematic.component.modify", Payload: map[string]any{"primitiveId": id}}
	}
	a, b := write("a"), write("b")
	if err := s.start(a, ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.start(b, ctx); err != nil {
		t.Fatal(err)
	}
	s.observe(auditEntry{RequestID: a.ID, WindowID: "w", Context: ctx, Action: a.Action, Payload: a.Payload, OK: true, Result: map[string]any{"rules": map[string]any{"status": "pass"}}})
	if runtimeMap(s.receipts[0].Checks)["status"] != "unknown" {
		t.Fatal("A accepted while B remains queued")
	}
	before := d.Generation
	s.observe(auditEntry{RequestID: b.ID, WindowID: "w", Context: ctx, Action: b.Action, Payload: b.Payload, OK: true, Result: map[string]any{}})
	if d.Generation <= before {
		t.Fatal("write completion did not advance observation boundary without native events")
	}
	capture := protocol.Request{Envelope: protocol.Envelope{ID: "image", WindowID: "w"}, Action: "view.capture"}
	if err := s.start(capture, ctx); err != nil {
		t.Fatal(err)
	}
	c := write("c")
	if err := s.start(c, ctx); err != nil {
		t.Fatal(err)
	}
	s.observe(auditEntry{RequestID: c.ID, WindowID: "w", Context: ctx, Action: c.Action, Payload: c.Payload, OK: true, Result: map[string]any{}})
	s.observe(auditEntry{RequestID: capture.ID, WindowID: "w", Context: ctx, Action: capture.Action, OK: true, Result: map[string]any{"value": map[string]any{"base64": "older-image"}}})
	if s.receipts[2].ImageAvailable || s.receipts[2].Generation == d.Generation {
		t.Fatal("older capture borrowed newest write generation")
	}
}
func TestRuntimeFinishProofRejectsStateChangeBeforeForward(t *testing.T) {
	s := newRuntimeStore()
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d", DocumentType: "schematic"}
	d := s.document("w", ctx)
	d.Generation = 4
	s.receipts = append(s.receipts, &runtimeReceipt{RequestID: "task", WindowID: "w", Status: "saving", Generation: 4})
	generation := uint64(4)
	save := protocol.Request{Envelope: protocol.Envelope{ID: "save", WindowID: "w"}, Action: "schematic.save", Payload: map[string]any{"_finishGeneration": 999, "_taskParent": "fake"}}
	s.prepare(&save, ctx, runtimeDispatchProof{ParentID: "task", FinishGeneration: &generation})
	d.Generation++
	if err := s.start(save, ctx); err == nil {
		t.Fatal("stale finish proof sent to editor")
	}
	if len(s.receipts) != 1 {
		t.Fatal("refused save created a forwarded receipt")
	}
	s.prepare(&save, ctx)
	if save.Payload["_finishGeneration"] != nil || save.Payload["_taskParent"] != nil {
		t.Fatal("external JSON forged internal finish proof")
	}
}
func TestRuntimeStateSummaryOmitsCachedObjectBodies(t *testing.T) {
	s := New(Options{})
	d := s.runtime.document("w", &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"})
	d.Objects["r"] = &runtimeObject{Data: map[string]any{"kind": "component", "privateLargePayload": "must-not-expand"}, StaleFields: map[string]bool{}}
	w := httptest.NewRecorder()
	s.handleRuntimeState(w, httptest.NewRequest(http.MethodGet, "/runtime/state?summary=1&window=w", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), "must-not-expand") || len(s.runtime.receipts) != 0 {
		t.Fatal("summary expanded object payload or dispatched a native read")
	}
}
