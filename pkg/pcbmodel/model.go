// Package pcbmodel defines the editor-independent PCB geometry shared by
// placement, routing and combined solving packages.
//
// Coordinates are y-up and use Board.Units (normally mil). Runtime primitive
// identifiers are retained as provenance only; algorithms address components
// and pads by their stable reference names.
package pcbmodel

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type Point = [2]float64

const (
	LayerTop    = 1
	LayerBottom = 2
	LayerMulti  = 12
)

type BBox struct {
	MinX float64 `json:"minX"`
	MinY float64 `json:"minY"`
	MaxX float64 `json:"maxX"`
	MaxY float64 `json:"maxY"`
}

func (b BBox) Valid() bool {
	return finite(b.MinX) && finite(b.MinY) && finite(b.MaxX) && finite(b.MaxY) && b.MaxX >= b.MinX && b.MaxY >= b.MinY
}

func (b BBox) Width() float64  { return b.MaxX - b.MinX }
func (b BBox) Height() float64 { return b.MaxY - b.MinY }
func (b BBox) Center() Point   { return Point{(b.MinX + b.MaxX) / 2, (b.MinY + b.MaxY) / 2} }

func (b BBox) Translate(dx, dy float64) BBox {
	return BBox{MinX: b.MinX + dx, MinY: b.MinY + dy, MaxX: b.MaxX + dx, MaxY: b.MaxY + dy}
}

func (b BBox) Gap(other BBox) float64 {
	dx := math.Max(math.Max(b.MinX-other.MaxX, other.MinX-b.MaxX), 0)
	dy := math.Max(math.Max(b.MinY-other.MaxY, other.MinY-b.MaxY), 0)
	return math.Hypot(dx, dy)
}

func (b BBox) Overlaps(other BBox) bool {
	return b.MinX < other.MaxX && other.MinX < b.MaxX && b.MinY < other.MaxY && other.MinY < b.MaxY
}

type Outline struct {
	BBox   BBox    `json:"bbox"`
	Points []Point `json:"points,omitempty"`
	Source string  `json:"source"` // polygon | bbox
}

func (o Outline) Exact() bool { return o.Source == "polygon" && len(o.Points) >= 3 }

func (o Outline) Contains(p Point) bool {
	if !o.Exact() {
		return p[0] >= o.BBox.MinX && p[0] <= o.BBox.MaxX && p[1] >= o.BBox.MinY && p[1] <= o.BBox.MaxY
	}
	inside := false
	for i, a := range o.Points {
		b := o.Points[(i+1)%len(o.Points)]
		if pointOnSegment(p, a, b) {
			return true
		}
		if (a[1] > p[1]) != (b[1] > p[1]) && p[0] < (b[0]-a[0])*(p[1]-a[1])/(b[1]-a[1])+a[0] {
			inside = !inside
		}
	}
	return inside
}

func (o Outline) ContainsBBox(b BBox) bool {
	if !b.Valid() {
		return false
	}
	for _, p := range []Point{{b.MinX, b.MinY}, {b.MinX, b.MaxY}, {b.MaxX, b.MinY}, {b.MaxX, b.MaxY}, b.Center()} {
		if !o.Contains(p) {
			return false
		}
	}
	// Corner-only tests miss a concave boundary crossing the rectangle.
	if o.Exact() {
		for _, p := range o.Points {
			if p[0] > b.MinX && p[0] < b.MaxX && p[1] > b.MinY && p[1] < b.MaxY {
				return false
			}
		}
	}
	return true
}

type Pad struct {
	ID           string  `json:"primitiveId,omitempty"`
	Number       string  `json:"padNumber"`
	Net          string  `json:"net"`
	Layer        int     `json:"layer"`
	Center       Point   `json:"center"`
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
	Rotation     float64 `json:"rotation,omitempty"`
	Shape        string  `json:"shape"` // rect | roundrect | oval | circle
	CornerRadius float64 `json:"cornerRadius,omitempty"`
}

func (p Pad) Ref(component string) string { return component + "." + p.Number }
func (p Pad) ThroughHole() bool           { return p.Layer == LayerMulti }

type Component struct {
	ID       string  `json:"primitiveId,omitempty"`
	Ref      string  `json:"ref"`
	Side     int     `json:"side"` // LayerTop | LayerBottom
	Anchor   Point   `json:"anchor"`
	Rotation float64 `json:"rotation"`
	Locked   bool    `json:"locked,omitempty"`
	BBox     BBox    `json:"bbox"`
	Pads     []Pad   `json:"pads,omitempty"`
}

type Track struct {
	ID     string  `json:"primitiveId,omitempty"`
	Net    string  `json:"net"`
	Layer  int     `json:"layer"`
	Width  float64 `json:"width"`
	Points []Point `json:"points"`
	Owner  string  `json:"owner,omitempty"`
}

type Via struct {
	ID       string  `json:"primitiveId,omitempty"`
	Net      string  `json:"net"`
	At       Point   `json:"at"`
	Diameter float64 `json:"diameter"`
	Drill    float64 `json:"drill"`
	From     int     `json:"fromLayer"`
	To       int     `json:"toLayer"`
	Owner    string  `json:"owner,omitempty"`
}

type Region struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"` // rule | copper
	Net      string    `json:"net,omitempty"`
	Layer    int       `json:"layer"`
	Points   []Point   `json:"points,omitempty"` // one-contour compatibility form
	Contours [][]Point `json:"contours,omitempty"`
	Rules    []string  `json:"rules,omitempty"`
	Owner    string    `json:"owner,omitempty"`
}

type Rules struct {
	Clearance         float64 `json:"clearance"`
	TrackTrack        float64 `json:"trackTrack,omitempty"`
	CopperToEdge      float64 `json:"copperToEdge"`
	TrackWidthMinimum float64 `json:"trackWidthMinimum"`
	ViaDiameter       float64 `json:"viaDiameter"`
	ViaDrill          float64 `json:"viaDrill"`
	Source            string  `json:"source"` // live | fixture
}

type Layer struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Role     string `json:"role"` // signal | plane
	PlaneNet string `json:"planeNet,omitempty"`
}

type Stackup struct {
	Copper []Layer `json:"copper"`
}

func (s Stackup) Layer(id int) (Layer, bool) {
	for _, l := range s.Copper {
		if l.ID == id {
			return l, true
		}
	}
	return Layer{}, false
}

func (s Stackup) SignalLayers() []int {
	var out []int
	for _, l := range s.Copper {
		if l.Role == "signal" {
			out = append(out, l.ID)
		}
	}
	return out
}

type Board struct {
	SchemaVersion int         `json:"schemaVersion"`
	Units         string      `json:"units"`
	Outline       Outline     `json:"outline"`
	Stackup       Stackup     `json:"stackup"`
	Rules         Rules       `json:"rules"`
	Components    []Component `json:"components"`
	Tracks        []Track     `json:"tracks,omitempty"`
	Vias          []Via       `json:"vias,omitempty"`
	Regions       []Region    `json:"regions,omitempty"`
	SemanticHash  string      `json:"semanticHash,omitempty"`
}

func (b Board) Component(ref string) (Component, bool) {
	for _, c := range b.Components {
		if c.Ref == ref {
			return c, true
		}
	}
	return Component{}, false
}

func (b Board) Pad(ref string) (Pad, Component, bool) {
	parts := strings.SplitN(ref, ".", 2)
	if len(parts) != 2 {
		return Pad{}, Component{}, false
	}
	c, ok := b.Component(parts[0])
	if !ok {
		return Pad{}, Component{}, false
	}
	var found *Pad
	for i := range c.Pads {
		if c.Pads[i].Number == parts[1] {
			if found != nil {
				return Pad{}, Component{}, false
			}
			found = &c.Pads[i]
		}
	}
	if found == nil {
		return Pad{}, Component{}, false
	}
	return *found, c, true
}

func (b Board) Validate(twoLayerOnly bool) error {
	if b.Units != "mil" {
		return fmt.Errorf("board units must be mil")
	}
	if !b.Outline.BBox.Valid() || !b.Outline.Exact() {
		return fmt.Errorf("board requires an exact polygon outline")
	}
	if b.Rules.Source == "" || b.Rules.Clearance <= 0 || b.Rules.TrackTrack < 0 || b.Rules.TrackWidthMinimum <= 0 || b.Rules.CopperToEdge < 0 || b.Rules.ViaDrill <= 0 || b.Rules.ViaDiameter <= b.Rules.ViaDrill {
		return fmt.Errorf("board requires measured positive routing rules")
	}
	if twoLayerOnly && len(b.Stackup.Copper) != 2 {
		return fmt.Errorf("two-layer solve requires exactly two declared copper layers")
	}
	seenLayer := map[int]bool{}
	for _, l := range b.Stackup.Copper {
		if seenLayer[l.ID] || l.ID == LayerMulti || l.Role != "signal" && l.Role != "plane" || l.Role == "plane" && strings.TrimSpace(l.PlaneNet) == "" {
			return fmt.Errorf("invalid or duplicate copper layer %d", l.ID)
		}
		seenLayer[l.ID] = true
	}
	seenRef := map[string]bool{}
	for _, c := range b.Components {
		// Existing boards can intentionally carry edge connectors whose rendered
		// body crosses the outline. That is a placement finding, not missing or
		// malformed measurement. pcblayout.Check records it in the baseline and
		// rejects only newly introduced off-board states.
		if c.Ref == "" || seenRef[c.Ref] || c.Side != LayerTop && c.Side != LayerBottom || !c.BBox.Valid() {
			return fmt.Errorf("invalid or duplicate component %q", c.Ref)
		}
		seenRef[c.Ref] = true
		for _, p := range c.Pads {
			// A footprint may legitimately repeat one logical pad number across
			// several physical copper islands (USB shield tabs are common). Keep
			// all of them as obstacles. Board.Pad deliberately remains ambiguous
			// for routing endpoints until the caller supplies a unique pad number.
			if p.Number == "" || !finitePoint(p.Center) || p.Width <= 0 || p.Height <= 0 {
				return fmt.Errorf("component %s has invalid measured pad %q", c.Ref, p.Number)
			}
			if p.Layer != LayerMulti && !seenLayer[p.Layer] {
				return fmt.Errorf("component %s pad %s uses undeclared layer %d", c.Ref, p.Number, p.Layer)
			}
			if !supportedPadShape(p.Shape) {
				return fmt.Errorf("component %s pad %s has unsupported shape %q", c.Ref, p.Number, p.Shape)
			}
			if p.CornerRadius < 0 || p.CornerRadius > math.Min(p.Width, p.Height)/2 {
				return fmt.Errorf("component %s pad %s has invalid corner radius", c.Ref, p.Number)
			}
		}
	}
	for _, t := range b.Tracks {
		if !seenLayer[t.Layer] || t.Width < b.Rules.TrackWidthMinimum || len(t.Points) < 2 {
			return fmt.Errorf("invalid measured track %q", t.ID)
		}
		for _, p := range t.Points {
			if !finitePoint(p) {
				return fmt.Errorf("track %q has non-finite geometry", t.ID)
			}
		}
	}
	for _, v := range b.Vias {
		if v.ID == "" || !finitePoint(v.At) || v.Diameter <= 0 || v.Drill <= 0 || v.Diameter <= v.Drill || !seenLayer[v.From] || !seenLayer[v.To] || v.From == v.To {
			return fmt.Errorf("invalid measured via %q", v.ID)
		}
	}
	for _, r := range b.Regions {
		contours := r.Contours
		if len(contours) == 0 && len(r.Points) > 0 {
			contours = [][]Point{r.Points}
		}
		if r.ID == "" || r.Kind != "rule" && r.Kind != "copper" || r.Layer != LayerMulti && !seenLayer[r.Layer] || len(contours) == 0 {
			return fmt.Errorf("invalid measured region %q", r.ID)
		}
		for _, contour := range contours {
			if len(contour) < 3 {
				return fmt.Errorf("region %q has an incomplete contour", r.ID)
			}
			for _, p := range contour {
				if !finitePoint(p) {
					return fmt.Errorf("region %q has non-finite geometry", r.ID)
				}
			}
		}
	}
	return nil
}

func SortedComponentRefs(b Board) []string {
	refs := make([]string, 0, len(b.Components))
	for _, c := range b.Components {
		refs = append(refs, c.Ref)
	}
	sort.Strings(refs)
	return refs
}

func supportedPadShape(s string) bool {
	switch strings.ToLower(s) {
	case "rect", "roundrect", "oval", "circle":
		return true
	default:
		return false
	}
}

func finite(v float64) bool    { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func finitePoint(p Point) bool { return finite(p[0]) && finite(p[1]) }

func pointOnSegment(p, a, b Point) bool {
	cross := (p[0]-a[0])*(b[1]-a[1]) - (p[1]-a[1])*(b[0]-a[0])
	if math.Abs(cross) > 1e-7 {
		return false
	}
	return p[0] >= math.Min(a[0], b[0])-1e-7 && p[0] <= math.Max(a[0], b[0])+1e-7 && p[1] >= math.Min(a[1], b[1])-1e-7 && p[1] <= math.Max(a[1], b[1])+1e-7
}
