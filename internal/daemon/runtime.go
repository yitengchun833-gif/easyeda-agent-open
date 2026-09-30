package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// This is a projection of existing receipts, not a second execution queue.
type runtimeObject struct {
	Data        map[string]any  `json:"data"`
	ObservedAt  time.Time       `json:"observedAt"`
	StaleFields map[string]bool `json:"staleFields"`
}
type runtimeDocument struct {
	Recovery         any                       `json:"recovery,omitempty"`
	Task             *runtimeTask              `json:"task,omitempty"`
	Restored         bool                      `json:"restored,omitempty"`
	WindowID         string                    `json:"windowId"`
	Context          protocol.Context          `json:"context"`
	Generation       uint64                    `json:"generation"`
	Objects          map[string]*runtimeObject `json:"objects"`
	Coverage         map[string]bool           `json:"coverage"`
	ChangesSinceSave bool                      `json:"mutationAttemptedSinceConfirmedSave"`
	Checks           any                       `json:"checks,omitempty"`
	SavedAt          *time.Time                `json:"savedAt,omitempty"`
	SceneInstance    string                    `json:"sceneInstance,omitempty"`
	SceneSequence    int64                     `json:"sceneSequence"`
	EventCoverage    string                    `json:"eventCoverage"`
}
type runtimeEvent struct {
	Sequence uint64         `json:"sequence"`
	Type     string         `json:"type"`
	At       time.Time      `json:"at"`
	Data     map[string]any `json:"data"`
}
type runtimeReceipt struct {
	TaskRevision      uint64 `json:"taskRevision,omitempty"`
	Historical        bool   `json:"historical,omitempty"`
	started           bool
	StartedGeneration uint64    `json:"startedGeneration"`
	ContentWrite      bool      `json:"contentWrite,omitempty"`
	DocumentUUID      string    `json:"documentUuid,omitempty"`
	ProjectUUID       string    `json:"projectUuid,omitempty"`
	Generation        uint64    `json:"generation"`
	ImageAvailable    bool      `json:"imageAvailable,omitempty"`
	ImageScope        []string  `json:"imageScope,omitempty"`
	DRCPassed         bool      `json:"drcPassed,omitempty"`
	RequiredChecks    []string  `json:"requiredChecks,omitempty"`
	RequestID         string    `json:"requestId"`
	WindowID          string    `json:"windowId"`
	Action            string    `json:"action"`
	Status            string    `json:"status"`
	StartedAt         time.Time `json:"startedAt"`
	DurationMs        int64     `json:"durationMs"`
	ErrorCode         string    `json:"errorCode,omitempty"`
	Error             string    `json:"error,omitempty"`
	Progress          any       `json:"progress,omitempty"`
	Checks            any       `json:"checks,omitempty"`
	ObservedFacts     any       `json:"-"` // Private scoped readback for resume/finish, never cached freshness proof.
}
type runtimeStore struct {
	persistMu       sync.Mutex
	checkpointPath  string
	checkpointError string
	checkpointAt    *time.Time
	dirty           chan struct{}
	mu              sync.Mutex
	instance        string
	sequence        uint64
	documents       map[string]*runtimeDocument
	current         map[string]string
	receipts        []*runtimeReceipt
	events          []runtimeEvent
	notify          chan struct{}
	requests        map[string]protocol.Request
	baselines       map[string]any
}

func newRuntimeStore() *runtimeStore {
	return &runtimeStore{instance: fmt.Sprintf("%d", time.Now().UnixNano()), documents: map[string]*runtimeDocument{}, current: map[string]string{}, notify: make(chan struct{}), requests: map[string]protocol.Request{}, baselines: map[string]any{}}
}
func runtimeMap(value any) map[string]any { m, _ := value.(map[string]any); return m }
func runtimeArray(value any) []any        { a, _ := value.([]any); return a }
func runtimeString(value any) string      { s, _ := value.(string); return s }
func runtimeNumber(value any) int64 {
	switch n := value.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	case uint64:
		return int64(n)
	}
	return 0
}
func runtimeZero(value any) bool {
	switch n := value.(type) {
	case float64:
		return n == 0
	case int:
		return n == 0
	case int64:
		return n == 0
	case uint64:
		return n == 0
	}
	return false
}
func runtimeClone(value any) any {
	b, _ := json.Marshal(value)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}
func (s *runtimeStore) emit(kind string, data map[string]any) {
	s.sequence++
	s.events = append(s.events, runtimeEvent{s.sequence, kind, time.Now().UTC(), data})
	if len(s.events) > 256 {
		s.events = s.events[len(s.events)-256:]
	}
	close(s.notify)
	s.notify = make(chan struct{})
	select {
	case s.dirty <- struct{}{}:
	default:
	}
}
func (s *runtimeStore) document(window string, ctx *protocol.Context) *runtimeDocument {
	if ctx == nil || ctx.ProjectUUID == "" || ctx.DocumentUUID == "" {
		return s.documents[s.current[window]]
	}
	key := window + "|" + ctx.ProjectUUID + "|" + ctx.DocumentUUID
	d := s.documents[key]

	if d == nil {
		// ponytail: retain at most eight observed documents; evicted pages require a new baseline.
		if len(s.documents) >= 8 {
			for old := range s.documents {
				if old != s.current[window] {
					delete(s.documents, old)
					break
				}
			}
		}
		d = &runtimeDocument{WindowID: window, Context: *ctx, Objects: map[string]*runtimeObject{}, Coverage: map[string]bool{}, EventCoverage: "unknown"}
		s.documents[key] = d
	}
	s.current[window] = key
	d.Restored = false
	return d
}
func affectedIDs(payload map[string]any) map[string]bool {
	ids := map[string]bool{}
	for _, key := range []string{"primitiveId", "componentPrimitiveId", "parentPrimitiveId", "id"} {
		if id := runtimeString(payload[key]); id != "" {
			ids[id] = true
		}
	}
	for _, key := range []string{"primitiveIds", "ids"} {
		for _, id := range runtimeArray(payload[key]) {
			if v := runtimeString(id); v != "" {
				ids[v] = true
			}
		}
	}
	return ids
}
func runtimeContentWrite(action string, payload map[string]any) bool {
	if payload["dryRun"] == true {
		return false
	}
	if action == "debug.batch" {
		for _, value := range runtimeArray(payload["steps"]) {
			step := runtimeMap(value)
			if runtimeContentWrite(runtimeString(step["action"]), runtimeMap(step["payload"])) {
				return true
			}
		}
		return false
	}
	if action == "debug.control" || strings.HasSuffix(action, ".save") || strings.HasPrefix(action, "view.") {
		return false
	}
	return mutatesAction[action]
}
func (d *runtimeDocument) invalidate(action string, payload map[string]any) {
	if action == "debug.batch" {
		for _, value := range runtimeArray(payload["steps"]) {
			step := runtimeMap(value)
			d.invalidate(runtimeString(step["action"]), runtimeMap(step["payload"]))
		}
		return
	}
	if !runtimeContentWrite(action, payload) {
		return
	}
	d.Generation++
	d.ChangesSinceSave = true
	ids := affectedIDs(payload)
	for id, object := range d.Objects {
		// Net compilation is document-wide. A scoped move can disconnect pins;
		// do not preserve old pin-net freshness merely because geometry is local.
		if !runtimeDisplayOnly(action, payload) {
			object.StaleFields["pins"] = true
		}
		if len(ids) == 0 || ids[id] || ids[runtimeString(object.Data["parentId"])] {
			object.StaleFields["geometry"] = true
			object.StaleFields["identity"] = true
		}
	}
	if len(ids) == 0 {
		d.Coverage = map[string]bool{}
	}
}
func (s *runtimeStore) start(req protocol.Request, ctx *protocol.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.document(req.WindowID, ctx)
	if expected, continuation := req.Payload["_taskRevision"]; continuation {
		revision := uint64(0)
		if d != nil && d.Task != nil {
			revision = d.Task.Revision
		}
		if uint64(runtimeNumber(expected)) != revision {
			return fmt.Errorf("task intent changed before continuation dispatch")
		}
	}
	if expected, finish := req.Payload["_finishGeneration"]; finish {
		if d == nil || uint64(runtimeNumber(expected)) != d.Generation || s.pendingWrite(req.WindowID, "") {
			return fmt.Errorf("finish evidence changed or a content write is still pending")
		}
		parentValid := false
		for _, receipt := range s.receipts {
			if receipt.RequestID == runtimeString(req.Payload["_taskParent"]) && receipt.WindowID == req.WindowID && receipt.Status == "saving" && receipt.Generation == d.Generation {
				parentValid = true
			}
		}
		if !parentValid {
			return fmt.Errorf("finish task claim is no longer current")
		}
	}
	receipt := &runtimeReceipt{started: true, RequestID: req.ID, WindowID: req.WindowID, Action: req.Action, Status: "queued", StartedAt: time.Now().UTC(), ContentWrite: runtimeContentWrite(req.Action, req.Payload)}
	if d != nil {
		d.invalidate(req.Action, req.Payload)
		if d.Task != nil {
			receipt.TaskRevision = d.Task.Revision
		}
		receipt.StartedGeneration = d.Generation
		receipt.ProjectUUID = d.Context.ProjectUUID
		receipt.DocumentUUID = d.Context.DocumentUUID
	}
	s.receipts = append(s.receipts, receipt)
	if req.Action == "debug.batch" {
		// ponytail: only sixteen resumable batches; older ones remain in the existing audit.
		if len(s.requests) >= 16 {
			for id := range s.requests {
				delete(s.requests, id)
				delete(s.baselines, id)
				break
			}
		}
		var saved protocol.Request
		b, _ := json.Marshal(req)
		_ = json.Unmarshal(b, &saved)
		s.requests[req.ID] = saved
	}
	if len(s.receipts) > 256 {
		s.receipts = s.receipts[len(s.receipts)-256:]
	}
	s.emit("action_started", map[string]any{"requestId": req.ID, "windowId": req.WindowID, "action": req.Action})
	return nil
}
func (s *runtimeStore) pendingWrite(window, except string) bool {
	for _, receipt := range s.receipts {
		if receipt.WindowID == window && receipt.RequestID != except && receipt.ContentWrite && (receipt.Status == "queued" || receipt.Status == "running" || receipt.Status == "resuming") {
			return true
		}
	}
	return false
}

// Display edits cannot change identity, text content or connectivity. Classify
// the actual actions here rather than trusting caller-supplied metadata.
func runtimeDisplayOnly(action string, payload map[string]any) bool {
	if action == "debug.batch" {
		writes := false
		for _, value := range runtimeArray(payload["steps"]) {
			step := runtimeMap(value)
			a, p := runtimeString(step["action"]), runtimeMap(step["payload"])
			if runtimeContentWrite(a, p) {
				writes = true
				if !runtimeDisplayOnly(a, p) {
					return false
				}
			}
		}
		return writes
	}
	if action != "schematic.attribute.modify" {
		return false
	}
	props := runtimeMap(payload["props"])
	if len(props) == 0 {
		return false
	}
	for key := range props {
		switch key {
		case "x", "y", "rotation", "fontName", "fontSize", "bold", "italic", "underLine", "fillColor", "color", "alignMode", "valueVisible", "keyVisible":
		default:
			return false
		}
	}
	return true
}

// Derive scope from cached parents, contacts and net members without editor I/O.
// The optional baseline comes only from an internal HTTP request context.
func (s *runtimeStore) prepare(req *protocol.Request, ctx *protocol.Context, baseline ...any) {
	if req.Payload == nil {
		req.Payload = map[string]any{}
	}
	delete(req.Payload, "_resumeBaseline")
	delete(req.Payload, "_expectedObserved")
	delete(req.Payload, "_scope")
	delete(req.Payload, "_finishGeneration")
	delete(req.Payload, "_taskParent")
	delete(req.Payload, "_taskRevision")
	edit := runtimeMap(req.Payload["_edit"])
	delete(edit, "baseline")
	if len(baseline) != 0 && baseline[0] != nil {
		if proof, internal := baseline[0].(runtimeDispatchProof); internal {
			if proof.Observed != nil {
				req.Payload["_expectedObserved"] = runtimeClone(proof.Observed)
			}
			if proof.Baseline != nil {
				req.Payload["_resumeBaseline"] = runtimeClone(proof.Baseline)
			}
			if proof.ParentID != "" {
				req.Payload["_taskParent"] = proof.ParentID
			}
			if proof.FinishGeneration != nil {
				req.Payload["_finishGeneration"] = *proof.FinishGeneration
			}
			if proof.TaskRevision != nil {
				req.Payload["_taskRevision"] = *proof.TaskRevision
			}
		}
	}
	req.Payload["_mutation"] = runtimeContentWrite(req.Action, req.Payload)
	req.Payload["_displayOnly"] = runtimeDisplayOnly(req.Action, req.Payload)
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.document(req.WindowID, ctx)
	ids, nets, direct := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, id := range runtimeArray(edit["primitiveIds"]) {
		if v := runtimeString(id); v != "" {
			ids[v], direct[v] = true, true
		}
	}
	networkChange := false
	positions := map[string]map[string]any{}
	var collect func(string, map[string]any)
	collect = func(action string, payload map[string]any) {
		if action == "debug.batch" {
			for _, value := range runtimeArray(payload["steps"]) {
				step := runtimeMap(value)
				collect(runtimeString(step["action"]), runtimeMap(step["payload"]))
			}
			return
		}
		if !runtimeContentWrite(action, payload) {
			return
		}
		if strings.HasPrefix(action, "schematic.wire.") || strings.HasPrefix(action, "schematic.netflag.") {
			networkChange = true
		}
		for id := range affectedIDs(payload) {
			ids[id] = true
		}
		if id := runtimeString(payload["primitiveId"]); id != "" {
			if props := runtimeMap(payload["props"]); props != nil {
				positions[id] = props
			}
		}
		if strings.HasPrefix(action, "schematic.component.") {
			if id := runtimeString(payload["primitiveId"]); id != "" {
				direct[id] = true
			}
		}
		if id := runtimeString(payload["componentPrimitiveId"]); id != "" {
			direct[id] = true
		}
		if net := runtimeString(payload["net"]); net != "" {
			nets[net] = true
		}
	}
	collect(req.Action, req.Payload)
	components := []string{}
	if d != nil {
		// Parent chains are short, but map iteration order is undefined. Follow
		// to a fixed point so text -> wire/component works in either order.
		for changed := true; changed; {
			changed = false
			for key, obj := range d.Objects {
				data := obj.Data
				if !ids[key] && !ids[runtimeString(data["primitiveId"])] {
					continue
				}
				if parent := runtimeString(data["parentId"]); parent != "" && !ids[parent] {
					ids[parent], changed = true, true
				}
				if networkChange {
					if net := runtimeString(data["net"]); net != "" {
						nets[net] = true
					}
					for _, p := range runtimeArray(data["pins"]) {
						if net := runtimeString(runtimeMap(p)["net"]); net != "" {
							nets[net] = true
						}
					}
				}
			}
		}
		// Use measured boxes only to choose nearby readback targets. Predicted
		// translation is not an observed result; post-write native reads remain
		// authoritative. Missing/stale boxes do not imply collision-free layout.
		boxes := [][4]float64{}
		for key, object := range d.Objects {
			id := runtimeString(object.Data["primitiveId"])
			if object.StaleFields["geometry"] || (!ids[key] && !ids[id]) {
				continue
			}
			if box, ok := runtimeBBox(object.Data["bbox"]); ok {
				boxes = append(boxes, box)
				if props := positions[id]; props != nil {
					dx, dy, valid := 0.0, 0.0, true
					if x, given := props["x"]; given {
						to, a := x.(float64)
						from, b := object.Data["x"].(float64)
						valid = valid && a && b
						dx = to - from
					}
					if y, given := props["y"]; given {
						to, a := y.(float64)
						from, b := object.Data["y"].(float64)
						valid = valid && a && b
						dy = to - from
					}
					if valid && (dx != 0 || dy != 0) {
						boxes = append(boxes, [4]float64{box[0] + dx, box[1] + dy, box[2] + dx, box[3] + dy})
					}
				}
			}
		}
		for id, obj := range d.Objects {
			if obj.Data["kind"] != "component" {
				continue
			}
			selected := ids[id] || direct[id]
			if !selected && !obj.StaleFields["geometry"] && (obj.Data["componentType"] == nil || obj.Data["componentType"] == "part") {
				if box, ok := runtimeBBox(obj.Data["bbox"]); ok {
					for _, near := range boxes {
						if box[0] < near[2] && box[2] > near[0] && box[1] < near[3] && box[3] > near[1] {
							selected = true
							break
						}
					}
				}
			}
			for _, p := range runtimeArray(obj.Data["pins"]) {
				pin := runtimeMap(p)
				if nets[runtimeString(pin["net"])] {
					selected = true
				}
				// A wire label needs its local endpoint components, not every
				// same-name GND member elsewhere on the page.
				if !networkChange && !selected {
					for key, wire := range d.Objects {
						if wire.Data["kind"] == "wire" && (ids[key] || ids[runtimeString(wire.Data["primitiveId"])]) && runtimePinOnWire(pin, wire.Data) {
							selected = true
							break
						}
					}
				}
			}
			if selected {
				components = append(components, id)
			}
		}
	}
	for id := range direct {
		if !idsIn(components, id) {
			components = append(components, id)
		}
	}
	sort.Strings(components)
	req.Payload["_scope"] = components
}
func runtimeBBox(value any) ([4]float64, bool) {
	box := runtimeMap(value)
	x0, a := box["minX"].(float64)
	y0, b := box["minY"].(float64)
	x1, c := box["maxX"].(float64)
	y1, d := box["maxY"].(float64)
	return [4]float64{x0, y0, x1, y1}, a && b && c && d && x1 > x0 && y1 > y0
}
func runtimePinOnWire(pin, wire map[string]any) bool {
	x, xok := pin["x"].(float64)
	y, yok := pin["y"].(float64)
	x0, a := wire["x0"].(float64)
	y0, b := wire["y0"].(float64)
	x1, c := wire["x1"].(float64)
	y1, d := wire["y1"].(float64)
	if !xok || !yok || !a || !b || !c || !d {
		return false
	}
	return (x-x0)*(y1-y0) == (y-y0)*(x1-x0) && x >= min(x0, x1) && x <= max(x0, x1) && y >= min(y0, y1) && y <= max(y0, y1)
}
func idsIn(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}
func (d *runtimeDocument) read(action string, result map[string]any) {
	if action == "debug.batch" {
		for _, item := range runtimeArray(result["results"]) {
			r := runtimeMap(item)
			if r["ok"] == true {
				d.read(runtimeString(r["action"]), runtimeMap(r["result"]))
			}
		}
	}
	if observations := runtimeArray(result["_readback"]); observations != nil {
		for _, item := range observations {
			r := runtimeMap(item)
			d.read(runtimeString(r["action"]), runtimeMap(r["result"]))
		}
	}
	if action != "schematic.components.list" {
		return
	}
	components, exists := result["components"]
	if !exists {
		return
	}
	scope := runtimeMap(result["readScope"])
	// Multi-page responses and legacy schemas are not observations of this page.
	if scope["allPages"] == true {
		return
	}
	complete := scope["complete"] == true
	if scope == nil {
		complete = false
	} // unscoped legacy reads do not prove whole-page coverage
	if complete {
		for id, obj := range d.Objects {
			if obj.Data["kind"] == "component" {
				delete(d.Objects, id)
			}
		}
		d.Coverage["components"] = true
	}
	for _, item := range runtimeArray(components) {
		data := runtimeMap(runtimeClone(item))
		id := runtimeString(data["primitiveId"])
		if id == "" {
			continue
		}
		old := d.Objects[id]
		if old == nil {
			old = &runtimeObject{Data: map[string]any{}, StaleFields: map[string]bool{"pins": true}}
			d.Objects[id] = old
		}
		// A geometry-only refresh cannot silently reuse an older measured box.
		if _, measured := data["bbox"]; !measured {
			delete(old.Data, "bbox")
		}
		for field, value := range data {
			old.Data[field] = value
		}
		old.Data["netlistAvailable"] = data["netlistAvailable"] == true
		old.Data["kind"] = "component"
		old.ObservedAt = time.Now().UTC()
		delete(old.StaleFields, "identity")
		delete(old.StaleFields, "geometry")
		pinsFresh := data["pinsAvailable"] == true && data["netlistAvailable"] == true && data["netAmbiguous"] != true
		if pins, supplied := data["pins"].([]any); supplied {
			for _, value := range pins {
				if _, known := runtimeMap(value)["net"].(string); !known {
					pinsFresh = false
				}
			}
		} else {
			pinsFresh = false
		}
		if pinsFresh {
			delete(old.StaleFields, "pins")
		} else {
			old.StaleFields["pins"] = true
		}
	}
	for _, value := range runtimeArray(scope["missingIds"]) {
		if object := d.Objects[runtimeString(value)]; object != nil {
			object.StaleFields["identity"] = true
			object.StaleFields["geometry"] = true
			object.StaleFields["pins"] = true
		}
	}
	for _, value := range runtimeArray(result["texts"]) {
		data := runtimeMap(runtimeClone(value))
		id := runtimeString(data["primitiveId"])
		if id != "" {
			data["kind"] = "text"
			d.Objects[id] = &runtimeObject{Data: data, ObservedAt: time.Now().UTC(), StaleFields: map[string]bool{}}
		}
	}
	if scope["concurrentChange"] == true {
		for _, item := range runtimeArray(components) {
			id := runtimeString(runtimeMap(item)["primitiveId"])
			if object := d.Objects[id]; object != nil {
				object.StaleFields["geometry"] = true
				object.StaleFields["pins"] = true
			}
		}
	}
	if result["wiresAvailable"] == true {
		for id, object := range d.Objects {
			if object.Data["kind"] == "wire" {
				delete(d.Objects, id)
			}
		}
		for _, item := range runtimeArray(result["wires"]) {
			data := runtimeMap(runtimeClone(item))
			id := runtimeString(data["primitiveId"])
			if id == "" {
				continue
			}
			key := id + ":" + fmt.Sprint(data["segmentIndex"])
			data["kind"] = "wire"
			d.Objects[key] = &runtimeObject{Data: data, ObservedAt: time.Now().UTC(), StaleFields: map[string]bool{}}
		}
		d.Coverage["wires"] = true
	}
}
func (s *runtimeStore) observe(entry auditEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var receipt *runtimeReceipt
	for i := len(s.receipts) - 1; i >= 0; i-- {
		if s.receipts[i].RequestID == entry.RequestID {
			receipt = s.receipts[i]
			break
		}
	}
	d := s.document(entry.WindowID, entry.Context)
	adopted := false
	if entry.OK && !runtimeContentWrite(entry.Action, entry.Payload) && d != nil {
		if instance := runtimeString(runtimeMap(entry.Result["_scene"])["instance"]); instance != "" {
			d, adopted = s.restoreObservedDocument(d, instance)
		}
	}
	tracked := receipt != nil && receipt.started
	if adopted && tracked {
		receipt.StartedGeneration = d.Generation
	}

	if receipt == nil {
		receipt = &runtimeReceipt{RequestID: entry.RequestID, WindowID: entry.WindowID, Action: entry.Action, StartedAt: entry.Timestamp}
		s.receipts = append(s.receipts, receipt)
		if len(s.receipts) > 256 {
			s.receipts = s.receipts[len(s.receipts)-256:]
		}
	}
	contentWrite := runtimeContentWrite(entry.Action, entry.Payload)
	observationCurrent := !s.pendingWrite(entry.WindowID, entry.RequestID)
	if d != nil {
		if tracked && !contentWrite && (receipt.StartedGeneration != d.Generation || receipt.DocumentUUID != d.Context.DocumentUUID || receipt.ProjectUUID != d.Context.ProjectUUID) {
			observationCurrent = false
		}
		d.applyScene(runtimeMap(entry.Result["_scene"]))
		if tracked && !contentWrite && receipt.StartedGeneration != d.Generation {
			observationCurrent = false
		}
		// Settling a write is a new observation boundary even without Beta
		// events. A prior screenshot/check cannot borrow a later queued write's
		// generation and remain current when that write eventually completes.
		if tracked && contentWrite {
			d.invalidate(entry.Action, entry.Payload)
		}
		if runtimeMap(entry.Result["_scene"])["changedDuringAction"] == true && !contentWrite {
			observationCurrent = false
		}
		if entry.OK && observationCurrent {
			d.read(entry.Action, entry.Result)
		}
		if entry.OK && observationCurrent && entry.Result["partial"] != true && entry.Result["saved"] == true && strings.HasSuffix(entry.Action, ".save") {
			now := time.Now().UTC()
			d.SavedAt = &now
			d.ChangesSinceSave = false
		}
	}

	receipt.Status = "action_finished"
	if !entry.OK || entry.Result["partial"] == true || entry.Result["ok"] == false {
		receipt.Status = "needs_readback"
	}
	receipt.DurationMs = entry.DurationMs
	receipt.ErrorCode = entry.ErrorCode
	receipt.Error = entry.ErrorMsg
	if entry.Action == "debug.batch" {
		steps := []any{}
		for _, item := range runtimeArray(entry.Result["results"]) {
			step := runtimeMap(item)
			steps = append(steps, map[string]any{"index": step["index"], "action": step["action"], "ok": step["ok"], "error": step["error"]})
		}
		receipt.Progress = map[string]any{"completed": entry.Result["completed"], "total": entry.Result["total"], "results": steps}
	}
	receipt.Checks = runtimeClone(entry.Result["rules"])
	if !observationCurrent {
		if checks := runtimeMap(receipt.Checks); checks != nil {
			checks["status"] = "unknown"
			checks["staleReason"] = "Another content write or context change overlapped this observation"
		}
	}
	if d != nil {
		receipt.DocumentUUID = d.Context.DocumentUUID
		receipt.ProjectUUID = d.Context.ProjectUUID
		receipt.Generation = d.Generation
		if !observationCurrent && tracked {
			receipt.Generation = receipt.StartedGeneration
		}
	}
	receipt.RequiredChecks = nil
	for _, check := range runtimeArray(runtimeMap(entry.Payload["_edit"])["requiredChecks"]) {
		if name := runtimeString(check); name != "" {
			receipt.RequiredChecks = append(receipt.RequiredChecks, name)
		}
	}
	receipt.observeEvidence(entry, observationCurrent)
	if parent := runtimeString(entry.Payload["_taskParent"]); parent != "" && strings.HasSuffix(entry.Action, ".save") {
		for _, r := range s.receipts {
			if r.RequestID == parent && r.WindowID == entry.WindowID && r.Status == "saving" {
				r.Status = "needs_readback"
				if observationCurrent && entry.OK && entry.Result["saved"] == true && entry.Result["partial"] != true {
					r.Status = "saved"
				}
			}
		}
	}
	if d != nil && receipt.Checks != nil {
		d.Checks = map[string]any{"generation": receipt.Generation, "documentUuid": d.Context.DocumentUUID, "checkedAt": time.Now().UTC(), "result": receipt.Checks}
	}
	if baseline := entry.Result["_baseline"]; baseline != nil && entry.Result["paused"] == true {
		if _, retained := s.requests[entry.RequestID]; retained {
			s.baselines[entry.RequestID] = runtimeClone(baseline)
		}
	}
	for _, item := range runtimeArray(entry.Result["_readback"]) {
		readback := runtimeMap(item)
		if readback["action"] == "schematic.components.list" {
			receipt.ObservedFacts = runtimeClone(readback["result"])
		}
	}
	if rules := runtimeMap(receipt.Checks); receipt.Status == "action_finished" && (rules["status"] == "fail" || rules["status"] == "unknown") {
		receipt.Status = "needs_review"
	}
	if entry.Result["paused"] == true {
		receipt.Status = "paused"
	}
	s.emit("action_finished", map[string]any{"requestId": entry.RequestID, "windowId": entry.WindowID, "action": entry.Action, "status": receipt.Status, "durationMs": entry.DurationMs, "error": entry.ErrorMsg})
}
func (s *runtimeStore) snapshot(window string, ids map[string]bool) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	documents := []any{}
	for _, d := range s.documents {
		if window != "" && (d.WindowID != window || d != s.documents[s.current[window]]) {
			continue
		}
		projection := *d
		if len(ids) > 0 {
			projection.Objects = map[string]*runtimeObject{}
			for key, object := range d.Objects {
				data := object.Data
				if ids[key] || ids[runtimeString(data["primitiveId"])] || ids[runtimeString(data["parentId"])] {
					projection.Objects[key] = object
				}
			}
		}
		copy := runtimeMap(runtimeClone(projection))
		copy["freshness"] = "external_unconfirmed"
		copy["requiresScopedReadback"] = true
		copy["note"] = "Cached observations, not a live EDA read. Beta events do not prove external-edit coverage."
		documents = append(documents, copy)
	}
	receipts := []any{}
	for _, r := range s.receipts {
		if s.receiptMatchesWindow(r, window) {
			receipts = append(receipts, runtimeClone(r))
		}
	}
	return map[string]any{"instance": s.instance, "sequence": s.sequence, "documents": documents, "receipts": receipts, "source": "daemon-receipts", "edaReads": 0, "persistence": s.persistenceStatus()}
}

func (s *runtimeStore) receiptMatchesWindow(r *runtimeReceipt, window string) bool {
	if window == "" || r.WindowID == window {
		return true
	}
	d := s.documents[s.current[window]]
	return r.Historical && d != nil && r.ProjectUUID == d.Context.ProjectUUID && r.DocumentUUID == d.Context.DocumentUUID
}
func (s *Server) handleRuntimeState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", 405)
		return
	}
	ids := map[string]bool{}
	for _, id := range strings.Split(r.URL.Query().Get("primitiveIds"), ",") {
		if id != "" {
			ids[id] = true
		}
	}
	if r.URL.Query().Get("summary") == "1" && len(ids) == 0 {
		s.runtime.mu.Lock()
		snapshot := s.runtime.dashboardSnapshotLocked()
		s.runtime.mu.Unlock()
		if window := r.URL.Query().Get("window"); window != "" {
			var target map[string]any
			for _, item := range runtimeArray(snapshot["documents"]) {
				if runtimeString(runtimeMap(item)["windowId"]) == window {
					target = runtimeMap(runtimeMap(item)["context"])
					break
				}
			}
			for _, field := range []string{"documents", "receipts"} {
				selected := []any{}
				for _, item := range runtimeArray(snapshot[field]) {
					value := runtimeMap(item)
					if runtimeString(value["windowId"]) == window || (field == "receipts" && target != nil && value["historical"] == true && value["projectUuid"] == target["projectUuid"] && value["documentUuid"] == target["documentUuid"]) {
						selected = append(selected, item)
					}
				}
				snapshot[field] = selected
			}
			snapshot["metricsScope"] = "retained requests across all windows"
		}
		receipts := runtimeArray(snapshot["receipts"])
		if len(receipts) > 12 {
			snapshot["receipts"] = receipts[len(receipts)-12:]
		}
		writeJSON(w, http.StatusOK, snapshot)
		return
	}
	writeJSON(w, http.StatusOK, s.runtime.snapshot(r.URL.Query().Get("window"), ids))
}
