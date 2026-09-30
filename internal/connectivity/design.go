package connectivity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// DesignChange uses stable IDs in JSON-pointer paths, never inventory offsets.
// Before/After are present even for additions/removals, represented by null.
type DesignChange struct {
	Domain string `json:"domain"`
	ID     string `json:"id"`
	Path   string `json:"path"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

type DesignUnverified struct {
	Side   string `json:"side"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type DesignCoverage struct {
	Scope             string             `json:"scope"`
	CanonicalComplete bool               `json:"canonicalComplete"`
	DrawingCompared   bool               `json:"drawingCompared"`
	Unverified        []DesignUnverified `json:"unverified"`
}

// Synced means equality only within Coverage.Scope. Canonical data cannot prove
// the actual editor's wires, frames or rendered text, even with complete XY.
type DesignDiff struct {
	Status           string         `json:"status"`
	ExpectedRevision string         `json:"expectedRevision"`
	ActualRevision   string         `json:"actualRevision"`
	Coverage         DesignCoverage `json:"coverage"`
	Changes          []DesignChange `json:"changes"`
}

type IncompleteDesignError struct{ Reason string }

func (e *IncompleteDesignError) Error() string { return "incomplete canonical evidence: " + e.Reason }

// DesignEvidence preserves coordinate presence lost by the IR's omitempty
// numeric fields. Callers cannot accidentally turn an omitted measurement into
// an observed zero. Document itself remains the existing electrical IR.
type DesignEvidence struct {
	Document Document
	state    map[string]any
	missing  []DesignUnverified
}

// DecodeStrictDesignJSON rejects duplicate keys, aliases, unknown fields,
// trailing documents, nonfinite numbers and truncated fixed-length arrays.
// It is also used by the app's explicitly typed compose-plan adapter.
func DecodeStrictDesignJSON(raw []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := checkDesignJSON(dec, nil); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("requires exactly one JSON document")
	}
	var value any
	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return err
	}
	t := reflect.TypeOf(target)
	if t == nil || t.Kind() != reflect.Pointer {
		return fmt.Errorf("decode target must be a pointer")
	}
	if err := checkDesignFields(value, t.Elem(), nil); err != nil {
		return err
	}
	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(target)
}

func checkDesignJSON(dec *json.Decoder, path []string) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	if n, ok := token.(json.Number); ok {
		v, err := n.Float64()
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("nonfinite number at %s", designPointer(path))
		}
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for index := 0; dec.More(); index++ {
		key := fmt.Sprint(index)
		if delim == '{' {
			token, err := dec.Token()
			if err != nil {
				return err
			}
			key = token.(string)
			if seen[key] {
				return fmt.Errorf("duplicate JSON key at %s", designPointer(append(path, key)))
			}
			seen[key] = true
		}
		if err := checkDesignJSON(dec, append(path, key)); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

func checkDesignFields(value any, t reflect.Type, path []string) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if value == nil {
		switch t.Kind() {
		case reflect.Slice, reflect.Map:
			return nil
		}
		return &IncompleteDesignError{Reason: designPointer(path) + " cannot be null"}
	}
	switch t.Kind() {
	case reflect.Struct:
		obj, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("expected object at %s", designPointer(path))
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name != "" && name != "-" {
				fields[name] = f.Type
			}
		}
		for k, v := range obj {
			ft, ok := fields[k]
			if !ok {
				return fmt.Errorf("unknown JSON field %s (field names are case-sensitive)", designPointer(append(path, k)))
			}
			if err := checkDesignFields(v, ft, append(path, k)); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		list, ok := value.([]any)
		if !ok {
			return fmt.Errorf("expected array at %s", designPointer(path))
		}
		if t.Kind() == reflect.Array && len(list) != t.Len() {
			return fmt.Errorf("%s requires exactly %d array items", designPointer(path), t.Len())
		}
		for i, v := range list {
			if err := checkDesignFields(v, t.Elem(), append(path, fmt.Sprint(i))); err != nil {
				return err
			}
		}
	case reflect.Map:
		obj, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("expected object at %s", designPointer(path))
		}
		for k, v := range obj {
			if err := checkDesignFields(v, t.Elem(), append(path, k)); err != nil {
				return err
			}
		}
	}
	return nil
}

// DecodeDesign is retained for IR-only callers. Evidence-sensitive comparison
// must retain DecodeDesignEvidence's result rather than round-trip Document.
func DecodeDesign(raw []byte) (Document, error) {
	e, err := DecodeDesignEvidence(raw)
	return e.Document, err
}

func DecodeDesignEvidence(raw []byte) (DesignEvidence, error) {
	var e DesignEvidence
	if err := DecodeStrictDesignJSON(raw, &e.Document); err != nil {
		return e, err
	}
	var source map[string]any
	if err := json.Unmarshal(raw, &source); err != nil {
		return e, err
	}
	if source == nil {
		return e, &IncompleteDesignError{Reason: "document must be an object"}
	}
	if err := rejectDesignNull(source, nil); err != nil {
		return e, err
	}
	state, err := canonicalDesignState(e.Document)
	if err != nil {
		return e, err
	}
	e.state = state
	// Placement x/y and bbox coordinates have no zero default. Pin x/y use
	// omitempty in existing exports, so omission remains ambiguous evidence.
	components, _ := source["components"].([]any)
	for _, item := range components {
		c := item.(map[string]any)
		id := c["id"].(string)
		path := []string{"components", id}
		target := state["components"].(map[string]any)[id].(map[string]any)
		placement, ok := c["placement"].(map[string]any)
		if !ok {
			e.missing = append(e.missing, DesignUnverified{Path: designPointer(append(path, "placement")), Reason: "placement was not supplied"})
		} else {
			dest := target["placement"].(map[string]any)
			e.checkCoordinates(placement, dest, append(path, "placement"), []string{"x", "y"})
			if box, ok := placement["bbox"].(map[string]any); ok {
				e.checkCoordinates(box, dest["bbox"].(map[string]any), append(path, "placement", "bbox"), []string{"minX", "minY", "maxX", "maxY"})
			} else {
				e.missing = append(e.missing, DesignUnverified{Path: designPointer(append(path, "placement", "bbox")), Reason: "rendered component bounds were not supplied"})
			}
		}
		for _, item := range c["pins"].([]any) {
			pin := item.(map[string]any)
			number := pin["number"].(string)
			e.checkCoordinates(pin, target["pins"].(map[string]any)[number].(map[string]any), append(path, "pins", number), []string{"x", "y"})
		}
	}
	e.state = normalizeDesignNumbers(e.state).(map[string]any)
	return e, nil
}

func rejectDesignNull(value any, path []string) error {
	if len(path) > 0 && path[0] == "issues" {
		return nil
	}
	if value == nil {
		return &IncompleteDesignError{Reason: designPointer(path) + " is null; provide known values or omit an optional field"}
	}
	switch v := value.(type) {
	case map[string]any:
		for k, item := range v {
			if err := rejectDesignNull(item, append(path, k)); err != nil {
				return err
			}
		}
	case []any:
		for i, item := range v {
			if err := rejectDesignNull(item, append(path, fmt.Sprint(i))); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *DesignEvidence) checkCoordinates(source, target map[string]any, path []string, keys []string) {
	for _, k := range keys {
		if v, ok := source[k]; ok {
			target[k] = v
		} else {
			delete(target, k)
			e.missing = append(e.missing, DesignUnverified{Path: designPointer(append(path, k)), Reason: "coordinate omitted; zero and unavailable evidence cannot be distinguished"})
		}
	}
}

// DesignRevision includes all normalized canonical fields; callers with raw
// JSON should use DesignEvidence.Revision to also retain measured-zero evidence.
func DesignRevision(d Document) (string, error) {
	state, err := canonicalDesignState(d)
	if err != nil {
		return "", err
	}
	return designStateRevision(state)
}
func (e DesignEvidence) Revision() (string, error) { return designStateRevision(e.state) }

func CompareDesign(expected, actual Document) (DesignDiff, error) {
	a, err := json.Marshal(expected)
	if err != nil {
		return DesignDiff{}, err
	}
	b, err := json.Marshal(actual)
	if err != nil {
		return DesignDiff{}, err
	}
	ea, err := DecodeDesignEvidence(a)
	if err != nil {
		return DesignDiff{}, fmt.Errorf("expected: %w", err)
	}
	eb, err := DecodeDesignEvidence(b)
	if err != nil {
		return DesignDiff{}, fmt.Errorf("actual: %w", err)
	}
	return CompareDesignEvidence(ea, eb)
}
func CompareDesignEvidence(expected, actual DesignEvidence) (DesignDiff, error) {
	result := DesignDiff{Status: "synced", Changes: []DesignChange{}, Coverage: DesignCoverage{Scope: "canonical", CanonicalComplete: len(expected.missing)+len(actual.missing) == 0, Unverified: []DesignUnverified{{Side: "both", Path: "/drawing", Reason: "canonical IR does not include EDA wires, flags, frames or rendered text"}}}}
	if expected.state == nil || actual.state == nil {
		return result, fmt.Errorf("comparison requires decoded design evidence")
	}
	var err error
	if result.ExpectedRevision, err = expected.Revision(); err != nil {
		return result, err
	}
	if result.ActualRevision, err = actual.Revision(); err != nil {
		return result, err
	}
	for _, side := range []struct {
		name string
		e    DesignEvidence
	}{{"expected", expected}, {"actual", actual}} {
		for _, u := range side.e.missing {
			u.Side = side.name
			result.Coverage.Unverified = append(result.Coverage.Unverified, u)
		}
	}
	sort.Slice(result.Coverage.Unverified, func(i, j int) bool {
		a, b := result.Coverage.Unverified[i], result.Coverage.Unverified[j]
		if (a.Path == "/drawing") != (b.Path == "/drawing") {
			return a.Path == "/drawing"
		}
		if a.Side != b.Side {
			return a.Side < b.Side
		}
		return a.Path < b.Path
	})
	AppendDesignChanges(nil, expected.state, actual.state, &result.Changes)
	SortDesignChanges(result.Changes)
	if len(result.Changes) > 0 {
		result.Status = "different"
	}
	if !result.Coverage.CanonicalComplete {
		result.Status = "incomplete"
	}
	if expected.Document.ProjectID != actual.Document.ProjectID || expected.Document.DocumentID != actual.Document.DocumentID {
		result.Status = "wrong-target"
	}
	return result, nil
}

func canonicalDesignState(input Document) (map[string]any, error) {
	d := cloneDesignatorDocument(input)
	d.Issues = nil
	if d.Components == nil || d.Nets == nil || d.Connections == nil {
		return nil, &IncompleteDesignError{Reason: "components, nets and connections must be explicit arrays"}
	}
	if d.DocumentID != "" && strings.TrimSpace(d.DocumentID) == "" {
		return nil, fmt.Errorf("documentId must not be blank")
	}
	if strings.TrimSpace(d.ProjectID) == "" {
		return nil, &IncompleteDesignError{Reason: "projectId is required"}
	}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	d.Issues = nil
	pins, components, nets := map[string]bool{}, map[string]bool{}, map[string]bool{}
	ambiguousPinRefs := map[string]bool{}
	nc := map[[2]string]bool{}
	unconnected := map[[2]string]bool{}
	connected := map[[2]string]string{}
	for i := range d.Components {
		c := &d.Components[i]
		if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.Ref) == "" {
			return nil, fmt.Errorf("component id/ref must not be blank")
		}
		if len(c.Pins) == 0 {
			return nil, &IncompleteDesignError{Reason: c.ID + " requires a nonempty complete pins inventory"}
		}
		if strings.TrimSpace(c.Device.LibraryUUID) == "" || len(c.Device.UUID) != 32 {
			return nil, &IncompleteDesignError{Reason: c.ID + " requires libraryUuid and a 32-character deviceUuid"}
		}
		if _, err := hex.DecodeString(c.Device.UUID); err != nil {
			return nil, fmt.Errorf("%s has an invalid deviceUuid", c.ID)
		}
		if d.DocumentID == "" && strings.TrimSpace(c.PageID) == "" {
			return nil, &IncompleteDesignError{Reason: c.ID + " requires pageId in an all-pages document"}
		}
		if d.DocumentID != "" && c.PageID != "" && c.PageID != d.DocumentID {
			return nil, fmt.Errorf("%s pageId disagrees with documentId", c.ID)
		}
		// A single-page snapshot's document identity is sufficient evidence for
		// its omitted component page. Never infer page membership without it.
		if c.PageID == "" && d.DocumentID != "" {
			c.PageID = d.DocumentID
		}
		if c.Placement != nil {
			p := c.Placement
			if !designFinite(p.X, p.Y, p.Rotation) {
				return nil, fmt.Errorf("%s has nonfinite placement", c.ID)
			}
			if b := p.BBox; b != nil && (!designFinite(b.MinX, b.MinY, b.MaxX, b.MaxY) || b.MinX >= b.MaxX || b.MinY >= b.MaxY) {
				return nil, fmt.Errorf("%s has invalid bbox", c.ID)
			}
		}
		components[c.ID] = true
		for _, p := range c.Pins {
			if strings.TrimSpace(p.Number) == "" || !designFinite(p.X, p.Y) {
				return nil, fmt.Errorf("%s has invalid pin number/coordinates", c.ID)
			}
			if pins[c.ID+"."+p.Number] {
				ambiguousPinRefs[c.ID+"."+p.Number] = true
			}
			pins[c.ID+"."+p.Number] = true
			nc[[2]string{c.ID, p.Number}] = p.NoConnected
			unconnected[[2]string{c.ID, p.Number}] = p.ConnectionState == "unconnected"
		}
	}
	for _, n := range d.Nets {
		if strings.TrimSpace(n.ID) == "" {
			return nil, fmt.Errorf("blank net id")
		}
		if n.Scope != "" && n.Scope != "local" && n.Scope != "global" {
			return nil, fmt.Errorf("net %s has unsupported scope %q", n.ID, n.Scope)
		}
		nets[n.ID] = true
	}
	for _, c := range d.Connections {
		k := [2]string{c.ComponentID, c.PinNumber}
		if nc[k] {
			return nil, fmt.Errorf("%s.%s cannot be both connected and NC", c.ComponentID, c.PinNumber)
		}
		switch c.Kind {
		case "", "pin_net", "netlist", "wire", "netflag", "netport", "netlabel",
			"power", "ground", "net_port_in", "net_port_out", "net_port_bi":
		default:
			return nil, fmt.Errorf("connection %s.%s has unsupported kind %q", c.ComponentID, c.PinNumber, c.Kind)
		}
		connected[k] = c.NetID
	}
	for key, isNC := range nc {
		if connected[key] == "" && !isNC && !unconnected[key] {
			return nil, &IncompleteDesignError{Reason: key[0] + "." + key[1] + " has neither a net nor explicit NC"}
		}
	}
	moduleIDs, membership := map[string]bool{}, map[string]string{}
	for i := range d.Modules {
		m := &d.Modules[i]
		if strings.TrimSpace(m.ID) == "" || moduleIDs[m.ID] {
			return nil, fmt.Errorf("empty/duplicate module id %q", m.ID)
		}
		moduleIDs[m.ID] = true
		for field, list := range map[string][]string{"coreComponents": m.CoreComponents, "peripheralComponents": m.PeripheralComponents, "internalNets": m.InternalNets} {
			known := components
			if field == "internalNets" {
				known = nets
			}
			if err := validateDesignRefs(list, known, "module "+m.ID+"."+field); err != nil {
				return nil, err
			}
			sort.Strings(list)
		}
		for _, id := range append(append([]string{}, m.CoreComponents...), m.PeripheralComponents...) {
			if other, ok := membership[id]; ok {
				return nil, fmt.Errorf("component %s belongs to multiple module roles (%s, %s)", id, other, m.ID)
			}
			membership[id] = m.ID
		}
		portIDs := map[string]bool{}
		for j := range m.Ports {
			p := &m.Ports[j]
			if strings.TrimSpace(p.ID) == "" || portIDs[p.ID] || !nets[p.NetID] {
				return nil, fmt.Errorf("module %s has invalid/duplicate port %q or unknown net", m.ID, p.ID)
			}
			portIDs[p.ID] = true
			if err := validateDesignRefs(p.PinRefs, pins, "module "+m.ID+".port "+p.ID); err != nil {
				return nil, err
			}
			for _, ref := range p.PinRefs {
				if ambiguousPinRefs[ref] {
					return nil, fmt.Errorf("module %s port %s has ambiguous compound pin reference %q", m.ID, p.ID, ref)
				}
				matched := false
				for pair, net := range connected {
					if pair[0]+"."+pair[1] == ref && net == p.NetID && membership[pair[0]] == m.ID {
						matched = true
					}
				}
				if !matched {
					return nil, fmt.Errorf("module %s port %s pin %s does not belong to its module/net", m.ID, p.ID, ref)
				}
			}
			sort.Strings(p.PinRefs)
		}
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	var state map[string]any
	if err = json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	state["components"] = designKeyedRecords(state["components"], "id")
	for _, record := range state["components"].(map[string]any) {
		c := record.(map[string]any)
		c["pins"] = designKeyedRecords(c["pins"], "number")
	}
	state["nets"] = designKeyedRecords(state["nets"], "id")
	edges := map[string]any{}
	for _, record := range state["connections"].([]any) {
		c := record.(map[string]any)
		edges[designPairID(c["componentId"].(string), c["pinNumber"].(string))] = c
	}
	state["connections"] = edges
	order := []string{}
	for _, m := range d.Modules {
		order = append(order, m.ID)
	}
	state["moduleOrder"] = order
	state["modules"] = designKeyedRecords(state["modules"], "id")
	for _, record := range state["modules"].(map[string]any) {
		m := record.(map[string]any)
		m["ports"] = designKeyedRecords(m["ports"], "id")
	}
	return normalizeDesignNumbers(state).(map[string]any), nil
}
func designFinite(values ...float64) bool {
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}
func validateDesignRefs(refs []string, known map[string]bool, location string) error {
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref] || !known[ref] {
			return fmt.Errorf("%s: unknown/duplicate reference %q", location, ref)
		}
		seen[ref] = true
	}
	return nil
}
func designKeyedRecords(value any, key string) map[string]any {
	out := map[string]any{}
	records, _ := value.([]any)
	for _, value := range records {
		record := value.(map[string]any)
		out[record[key].(string)] = record
	}
	return out
}

// NormalizeDesignNumber removes API arithmetic tails at a fixed 1e-9 raw
// resolution, far below the drawing grid and Apply's 1e-6 tolerance. Fixed
// decimal formatting avoids multiplication overflow and is idempotent even
// for large finite coordinates. Comparison and revision hashes use the same
// normalization; this is quantization, not a pairwise epsilon comparison.
func NormalizeDesignNumber(value float64) float64 {
	text := strconv.FormatFloat(value, 'f', 9, 64)
	normalized, _ := strconv.ParseFloat(text, 64)
	if normalized == 0 {
		return 0 // IEEE signed zero has the same meaning and hash.
	}
	return normalized
}

func normalizeDesignNumbers(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			v[key] = normalizeDesignNumbers(item)
		}
	case []any:
		for i, item := range v {
			v[i] = normalizeDesignNumbers(item)
		}
	case float64:
		return NormalizeDesignNumber(v)
	}
	return value
}
func designStateRevision(state map[string]any) (string, error) {
	if state == nil {
		return "", fmt.Errorf("missing design state")
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// AppendDesignChanges permits a typed app adapter to add modeled drawing
// fields without placing EDA primitives into the electrical Document model.
func AppendDesignChanges(path []string, before, after any, changes *[]DesignChange) {
	if reflect.DeepEqual(before, after) {
		return
	}
	a, aok := before.(map[string]any)
	b, bok := after.(map[string]any)
	if aok && bok {
		keys := map[string]bool{}
		for k := range a {
			keys[k] = true
		}
		for k := range b {
			keys[k] = true
		}
		for key := range keys {
			AppendDesignChanges(append(path, key), a[key], b[key], changes)
		}
		return
	}
	domain, id := designChangeIdentity(path)
	*changes = append(*changes, DesignChange{Domain: domain, ID: id, Path: designPointer(path), Before: before, After: after})
}
func SortDesignChanges(changes []DesignChange) {
	sort.Slice(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if a.Domain != b.Domain {
			return a.Domain < b.Domain
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Path < b.Path
	})
}
func designChangeIdentity(path []string) (string, string) {
	if len(path) == 0 {
		return "document", ""
	}
	if path[0] == "drawing" {
		if len(path) > 2 {
			return "drawing", path[2]
		}
		return "drawing", ""
	}
	if path[0] == "projectId" || path[0] == "documentId" {
		return "target", ""
	}
	if path[0] == "moduleOrder" {
		return "module", ""
	}
	if len(path) < 2 {
		return "document", ""
	}
	if path[0] == "components" && len(path) >= 4 && path[2] == "pins" {
		return "pin", designPairID(path[1], path[3])
	}
	if path[0] == "modules" && len(path) >= 4 && path[2] == "ports" {
		return "port", designPairID(path[1], path[3])
	}
	return map[string]string{"components": "component", "nets": "net", "connections": "connection", "modules": "module"}[path[0]], path[1]
}
func designPairID(a, b string) string { raw, _ := json.Marshal([2]string{a, b}); return string(raw) }
func designPointer(path []string) string {
	var escaped []string
	for _, key := range path {
		escaped = append(escaped, strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1"))
	}
	return "/" + strings.Join(escaped, "/")
}
