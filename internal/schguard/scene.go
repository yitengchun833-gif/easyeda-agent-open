package schguard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type normalizedPin struct {
	Number   string   `json:"number"`
	X        float64  `json:"x"`
	Y        float64  `json:"y"`
	Rotation *float64 `json:"rotation,omitempty"`
}

type normalizedComponent struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Designator string          `json:"designator,omitempty"`
	Net        string          `json:"net,omitempty"`
	X          *float64        `json:"x,omitempty"`
	Y          *float64        `json:"y,omitempty"`
	Rotation   *float64        `json:"rotation,omitempty"`
	BBox       *BBox           `json:"bbox,omitempty"`
	Pins       []normalizedPin `json:"pins,omitempty"`
}

type normalizedWire struct {
	ID string  `json:"id"`
	X0 float64 `json:"x0"`
	Y0 float64 `json:"y0"`
	X1 float64 `json:"x1"`
	Y1 float64 `json:"y1"`
}

// SceneFingerprint hashes only the geometry/identity evidence shared by a
// normal components.list snapshot and the daemon's protected pre/post reads.
// Additional hydrated library/property fields do not create false staleness.
func SceneFingerprint(result map[string]any, excludeIDs ...string) (string, error) {
	excluded := map[string]bool{}
	for _, id := range excludeIDs {
		excluded[id] = true
	}
	components, ok := array(result["components"])
	if !ok {
		return "", fmt.Errorf("components unavailable")
	}
	wires, ok := array(result["wires"])
	if !ok {
		return "", fmt.Errorf("wires unavailable")
	}
	normalizedComponents := make([]normalizedComponent, 0, len(components))
	for _, value := range components {
		component, ok := value.(map[string]any)
		if !ok {
			return "", fmt.Errorf("invalid component")
		}
		id, _ := component["primitiveId"].(string)
		if id == "" || excluded[id] {
			continue
		}
		item := normalizedComponent{ID: id}
		item.Type, _ = component["componentType"].(string)
		item.Designator, _ = component["designator"].(string)
		item.Net, _ = component["net"].(string)
		if n, ok := numeric(component["x"]); ok {
			item.X = &n
		}
		if n, ok := numeric(component["y"]); ok {
			item.Y = &n
		}
		if n, ok := numeric(component["rotation"]); ok {
			item.Rotation = &n
		}
		if b, ok := bbox(component["bbox"]); ok {
			item.BBox = &b
		}
		if rawPins, ok := array(component["pins"]); ok {
			for _, raw := range rawPins {
				pin, ok := raw.(map[string]any)
				if !ok {
					return "", fmt.Errorf("invalid pin")
				}
				number, _ := pin["pinNumber"].(string)
				if number == "" {
					number, _ = pin["number"].(string)
				}
				x, xok := numeric(pin["x"])
				y, yok := numeric(pin["y"])
				if number == "" || !xok || !yok {
					return "", fmt.Errorf("incomplete pin")
				}
				p := normalizedPin{Number: number, X: x, Y: y}
				if n, ok := numeric(pin["rotation"]); ok {
					p.Rotation = &n
				}
				item.Pins = append(item.Pins, p)
			}
			sort.Slice(item.Pins, func(i, j int) bool { return item.Pins[i].Number < item.Pins[j].Number })
		}
		normalizedComponents = append(normalizedComponents, item)
	}
	sort.Slice(normalizedComponents, func(i, j int) bool { return normalizedComponents[i].ID < normalizedComponents[j].ID })
	normalizedWires := []normalizedWire{}
	for _, value := range wires {
		wire, ok := value.(map[string]any)
		if !ok {
			return "", fmt.Errorf("invalid wire")
		}
		id, _ := wire["primitiveId"].(string)
		if id == "" || excluded[id] {
			continue
		}
		points, ok := wirePoints(wire)
		if !ok {
			return "", fmt.Errorf("wire %s geometry unavailable", id)
		}
		for i := 1; i < len(points); i++ {
			normalizedWires = append(normalizedWires, normalizedWire{ID: id, X0: points[i-1].X, Y0: points[i-1].Y, X1: points[i].X, Y1: points[i].Y})
		}
	}
	sort.Slice(normalizedWires, func(i, j int) bool {
		a, b := normalizedWires[i], normalizedWires[j]
		return fmt.Sprintf("%s:%g:%g:%g:%g", a.ID, a.X0, a.Y0, a.X1, a.Y1) < fmt.Sprintf("%s:%g:%g:%g:%g", b.ID, b.X0, b.Y0, b.X1, b.Y1)
	})
	raw, err := json.Marshal(struct {
		Components []normalizedComponent `json:"components"`
		Wires      []normalizedWire      `json:"wires"`
	}{normalizedComponents, normalizedWires})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func FindingSignature(f Finding) string {
	pins := append([]string(nil), f.Pins...)
	sort.Strings(pins)
	return strings.Join([]string{f.Type, f.Level, f.Designator, f.PrimitiveId, f.WirePrimitiveId, strings.Join(pins, ",")}, "|")
}

func FindingSignatures(findings []Finding, excludeWireIDs ...string) []string {
	excluded := map[string]bool{}
	for _, id := range excludeWireIDs {
		excluded[id] = true
	}
	out := []string{}
	for _, finding := range findings {
		if !excluded[finding.WirePrimitiveId] {
			out = append(out, FindingSignature(finding))
		}
	}
	sort.Strings(out)
	return out
}
