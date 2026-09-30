package app

// A module frame is presentation data, independent of the electrical model.
// This adapter deliberately accepts already-computed coordinates: it neither
// queries the current placement to plan a layout nor moves electrical objects.

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

type schFrameDocument struct {
	SchemaVersion int            `json:"schemaVersion"`
	DocumentID    string         `json:"documentId"`
	Frames        []schFrameSpec `json:"frames"`
}

type schFrameSpec struct {
	ID          string               `json:"id"`
	Title       string               `json:"title"`
	Rect        layoutBBox           `json:"rect"`
	TitleX      float64              `json:"titleX"`
	TitleY      float64              `json:"titleY"`
	FontSize    float64              `json:"fontSize"`
	Color       string               `json:"color"`
	LineType    int                  `json:"lineType"`
	TitleLayout *schFrameTitleLayout `json:"titleLayout,omitempty"`
}

// The planner persists its reserved text envelope and occupied circuit bounds.
// Native readback checks both: fitting inside the frame alone is insufficient.
type schFrameTitleLayout struct {
	Width     float64      `json:"width"`
	Height    float64      `json:"height"`
	Clearance float64      `json:"clearance"`
	Obstacles []layoutBBox `json:"obstacles"`
}

func (f schFrameSpec) titleBounds() layoutBBox {
	return layoutBBox{MinX: f.TitleX, MinY: f.TitleY - f.TitleLayout.Height, MaxX: f.TitleX + f.TitleLayout.Width, MaxY: f.TitleY}
}

func checkSchFrameTitleOccupancy(f schFrameSpec, b layoutBBox) error {
	l := f.TitleLayout
	if l == nil {
		return nil
	} // Older hand-authored frames have no occupancy model.
	if !plFinite(l.Width) || !plFinite(l.Height) || !plFinite(l.Clearance) || l.Width <= 0 || l.Height < f.FontSize || l.Clearance < 0 || len(l.Obstacles) == 0 {
		return fmt.Errorf("frame %s has invalid titleLayout dimensions/clearance/obstacles", f.ID)
	}
	reserved := f.titleBounds()
	if !boxInside(reserved, f.Rect) || !boxInside(b, reserved) {
		return fmt.Errorf("frame %s title exceeds its planned envelope; refresh titleMetrics and replan", f.ID)
	}
	for i, o := range l.Obstacles {
		if !plBoxValid(o) || !boxInside(o, f.Rect) {
			return fmt.Errorf("frame %s obstacle %d invalid/outside frame", f.ID, i)
		}
		if b.MinX < o.MaxX+l.Clearance-1e-6 && b.MaxX > o.MinX-l.Clearance+1e-6 && b.MinY < o.MaxY+l.Clearance-1e-6 && b.MaxY > o.MinY-l.Clearance+1e-6 {
			return fmt.Errorf("frame %s title overlaps circuit clearance at obstacle %d", f.ID, i)
		}
	}
	return nil
}

func (f schFrameSpec) hash() string {
	b, _ := json.Marshal(f)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

var schFrameColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (p schFrameDocument) validate() error {
	if p.SchemaVersion != 1 || strings.TrimSpace(p.DocumentID) == "" || len(p.Frames) == 0 {
		return fmt.Errorf("frame data requires schemaVersion:1, documentId and nonempty frames")
	}
	seen := map[string]bool{}
	for _, f := range p.Frames {
		if strings.TrimSpace(f.ID) == "" || seen[f.ID] {
			return fmt.Errorf("frame id %q is empty or repeated", f.ID)
		}
		seen[f.ID] = true
		if strings.TrimSpace(f.Title) == "" || strings.ContainsAny(f.Title, "\r\n") {
			return fmt.Errorf("frame %s requires a single-line title", f.ID)
		}
		for _, n := range []float64{f.Rect.MinX, f.Rect.MinY, f.Rect.MaxX, f.Rect.MaxY, f.TitleX, f.TitleY, f.FontSize} {
			if math.IsNaN(n) || math.IsInf(n, 0) {
				return fmt.Errorf("frame %s has nonfinite coordinates", f.ID)
			}
		}
		if f.Rect.MinX >= f.Rect.MaxX || f.Rect.MinY >= f.Rect.MaxY || f.FontSize <= 0 {
			return fmt.Errorf("frame %s needs a positive rectangle and fontSize", f.ID)
		}
		if !schFrameColorRE.MatchString(f.Color) || f.LineType != 1 {
			return fmt.Errorf("frame %s needs a #RRGGBB color and dashed lineType:1", f.ID)
		}
		if f.TitleLayout != nil {
			if err := checkSchFrameTitleOccupancy(f, f.titleBounds()); err != nil {
				return err
			}
		}
		// LEFT_TOP anchor, y-UP. Width is also checked after creation against
		// the native text bbox; this validation rejects impossible vertical data.
		if f.TitleX < f.Rect.MinX || f.TitleX >= f.Rect.MaxX || f.TitleY > f.Rect.MaxY || f.TitleY-f.FontSize < f.Rect.MinY {
			return fmt.Errorf("frame %s title anchor/height is outside its rectangle", f.ID)
		}
	}
	for i, a := range p.Frames {
		for _, b := range p.Frames[i+1:] {
			if a.Rect.MinX < b.Rect.MaxX && a.Rect.MaxX > b.Rect.MinX && a.Rect.MinY < b.Rect.MaxY && a.Rect.MaxY > b.Rect.MinY {
				return fmt.Errorf("partition-overlap: frames %s and %s overlap; replan complete zones before applying", a.ID, b.ID)
			}
		}
	}
	return nil
}

func parseSchFrameDocument(raw []byte) (schFrameDocument, error) {
	var p schFrameDocument
	// Accept the same power-layout file without duplicating its presentation
	// data. Only the four explicitly known electrical fields may be ignored;
	// all frame fields remain strict to catch typos before an API call.
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return p, err
	}
	for _, key := range []string{"placements", "wires", "flags", "expectedPinNets"} {
		delete(envelope, key)
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return p, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return p, fmt.Errorf("frame data: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return p, fmt.Errorf("frame data must contain exactly one JSON document")
	}
	return p, p.validate()
}

func newSchFrameCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	c := &cobra.Command{Use: "frame", Short: "Apply or verify computed module frames and titles from JSON"}
	for _, mode := range []string{"apply", "check"} {
		mode := mode
		var from, data string
		sub := &cobra.Command{
			Use:   mode,
			Short: map[string]string{"apply": "Apply owned dashed frames and titles; matching data performs zero graphics writes", "check": "Read back owned frame geometry and styling; never modify the schematic"}[mode],
			Long: `Read a schemaVersion:1 document containing documentId and frames. Each frame
has id, title, rect:{minX,minY,maxX,maxY}, titleX, titleY, fontSize, color and
lineType:1 (dashed). Coordinates are y-UP raw units (0.01 inch). titleX/titleY
are the LEFT_TOP text anchor. A 0.2 inch title uses fontSize:20; the project
color is #AA00AA. Optional titleLayout:{width,height,clearance,obstacles:[bbox]}
verifies the native title against its planned envelope and occupied circuit bounds.
Only primitives recorded for this page and frame id are
replaced. Existing user graphics and zone-draw frames remain independently owned.
Unknown write outcomes stop and retain a recovery receipt rather than retrying.`,
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if (from == "") == (data == "") {
					return fmt.Errorf("use exactly one of --from or --data")
				}
				raw := []byte(data)
				if from != "" {
					var err error
					raw, err = os.ReadFile(from)
					if err != nil {
						return err
					}
				}
				p, err := parseSchFrameDocument(raw)
				if err != nil {
					return err
				}
				result, runErr := runSchFrameDocument(cfg, *window, p, mode == "apply")
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(result); err != nil {
					return err
				}
				return runErr
			},
		}
		sub.Flags().StringVar(&from, "from", "", "frame JSON file")
		sub.Flags().StringVar(&data, "data", "", "inline frame JSON (for sch apply Run steps)")
		c.AddCommand(sub)
	}
	return c
}
