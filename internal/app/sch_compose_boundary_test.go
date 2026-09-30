package app

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestComposeBorderJSONRequiresEveryCoordinate(t *testing.T) {
	for _, field := range []string{"minX", "minY", "maxX", "maxY"} {
		for _, kind := range []string{"missing", "null"} {
			t.Run(field+"/"+kind, func(t *testing.T) {
				src := composeFixture(1)
				src.Sheet = layoutBBox{0, 0, 1170, 825}
				raw, _ := json.Marshal(src)
				var data map[string]any
				if err := json.Unmarshal(raw, &data); err != nil {
					t.Fatal(err)
				}
				border := map[string]any{"minX": 0, "minY": 0, "maxX": 1170, "maxY": 825}
				if kind == "missing" {
					delete(border, field)
				} else {
					border[field] = nil
				}
				data["sheetBorder"] = border
				raw, _ = json.Marshal(data)
				dir := t.TempDir()
				from, out := filepath.Join(dir, "input.json"), filepath.Join(dir, "plan.json")
				if err := os.WriteFile(from, raw, 0600); err != nil {
					t.Fatal(err)
				}
				cmd := newSchComposeCmd(&bytes.Buffer{}, &bytes.Buffer{})
				cmd.SetArgs([]string{"--from", from, "--out", out})
				if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "sheetBorder."+field+" must be explicitly provided") {
					t.Fatalf("partial border silently became measured zero: %v", err)
				}
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Fatal("invalid border wrote a plan")
				}
			})
		}
	}
	for _, raw := range []string{`{}`, `{"sheetBorder":null}`, `{"sheetBorder":{"minX":0,"minY":0,"maxX":1170,"maxY":825}}`} {
		if err := validateSchCompositionBorderJSON([]byte(raw)); err != nil {
			t.Fatalf("valid explicit zero or legacy absence rejected: %v", err)
		}
	}
	// encoding/json otherwise accepts case-insensitive field aliases and could
	// fill the typed SheetBorder while this raw presence check sees no key.
	for _, raw := range []string{`{"SheetBorder":{"maxX":1170,"maxY":825}}`, `{"sheetborder":{"maxX":1170,"maxY":825}}`} {
		if err := validateSchCompositionBorderJSON([]byte(raw)); err == nil || !strings.Contains(err.Error(), "exact field name") {
			t.Fatalf("case alias bypassed explicit-coordinate validation: %v", err)
		}
	}
}

func TestComposeMeasuredBorderClearanceAndInwardGrid(t *testing.T) {
	src := composeFixture(4)
	src.SheetBorder = &layoutBBox{65.25, -4.75, 515.5, 566.25}
	before, _ := json.Marshal(src)
	p, err := planSchComposition(src)
	if err != nil {
		t.Fatal(err)
	}
	if p.Sheet != src.Sheet || p.SheetBorder == nil || *p.SheetBorder != *src.SheetBorder || p.PlacementBoundarySource != "explicit-sheet-border" {
		t.Fatal("paper identity and explicit drawing border must remain distinct")
	}
	if p.UsableBounds != (layoutBBox{80, 10, 505, 555}) {
		t.Fatalf("wrong inward rounding after 10-raw clearance and half stroke: %+v", p.UsableBounds)
	}
	for _, f := range p.Layout.Frames {
		b, border := f.Rect, *src.SheetBorder
		for _, clearance := range []float64{b.MinX - .5 - border.MinX, b.MinY - .5 - border.MinY, border.MaxX - b.MaxX - .5, border.MaxY - b.MaxY - .5} {
			if clearance < 10-1e-7 {
				t.Fatalf("frame %s has only %g raw visible clearance from the border", f.ID, clearance)
			}
		}
		if !boxInside(b, p.UsableBounds) || !plGrid(b.MinX) || !plGrid(b.MaxX) || !plGrid(b.MinY) || !plGrid(b.MaxY) {
			t.Fatal("every whole frame must remain within the rounded bounds")
		}
	}
	if p.Layout.Frames[0].Rect.MinX != 80 || p.Layout.Frames[0].Rect.MaxY != 555 {
		t.Fatal("layout must start at the upper-left inner-border clearance")
	}
	if p.Rows != 2 || len(p.RowHeights) != 2 || p.RowHeight != math.Max(p.RowHeights[0], p.RowHeights[1]) {
		t.Fatal("row height must be diagnostic, independently recorded for each content-sized row")
	}
	after, _ := json.Marshal(src)
	if string(before) != string(after) || !reflect.DeepEqual(src.Connectivity.Connections, p.Connectivity.Connections) {
		t.Fatal("border/layout planning mutated source input or connectivity")
	}
}

func TestComposeMissingBorderReportsFallback(t *testing.T) {
	src := composeFixture(1)
	p, err := planSchComposition(src)
	if err != nil {
		t.Fatal(err)
	}
	if p.SheetBorder != nil || p.PlacementBoundarySource != "sheet-bbox-fallback" {
		t.Fatal("a missing drawing border must not be promoted to official measured geometry")
	}
	if p.Layout.Frames[0].Rect.MinX != src.Sheet.MinX+10 || p.Layout.Frames[0].Rect.MaxY != src.Sheet.MaxY-10 {
		t.Fatal("legacy paper-bbox inset changed")
	}
}

func TestComposeBorderRejectsInvalidOrUnavailableSpace(t *testing.T) {
	for _, tc := range []struct {
		name   string
		border layoutBBox
		want   string
	}{
		{"outside paper", layoutBBox{40, -10, 520, 570}, "inside the measured sheet"},
		{"negative size", layoutBBox{80, 10, 70, 570}, "positive finite"},
		{"no usable area", layoutBBox{60, 0, 75, 15}, "no usable area"},
		{"module wider than border", layoutBBox{60, 0, 100, 570}, "wider than"},
		{"second row outside border", layoutBBox{60, 0, 390, 240}, "exceed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := composeFixture(4)
			src.SheetBorder = &tc.border
			if _, err := planSchComposition(src); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	// Keepouts retain sheet coordinates and may reach beyond the inner border.
	src := composeFixture(1)
	src.SheetBorder = &layoutBBox{60, -10, 520, 570}
	src.Keepouts = []layoutBBox{{50, 500, 250, 580}}
	if _, err := planSchComposition(src); err == nil || !strings.Contains(err.Error(), "keepout") {
		t.Fatalf("inner-border planning ignored an overlapping title-block keepout: %v", err)
	}
}

func TestComposeApplyRetainsWholePaperGuardWithInnerBorder(t *testing.T) {
	p, env := composeApplyFixture(t, true)
	p.SheetBorder = &layoutBBox{65, -5, 515, 565}
	if _, err := schCompositionPlaybook(p, composeApplyBytes(t, env), false); err != nil {
		t.Fatalf("matching whole-paper measurement rejected: %v", err)
	}
	sheet := env["result"].(map[string]any)["components"].([]any)[0].(map[string]any)
	sheet["bbox"] = p.SheetBorder
	if _, err := schCompositionPlaybook(p, composeApplyBytes(t, env), false); err == nil || !strings.Contains(err.Error(), "target sheet geometry differs") {
		t.Fatalf("inner border incorrectly substituted for the actual paper identity: %v", err)
	}
}
