package app

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func compactTitleMetrics(title string, width float64) *schTitleMetrics {
	return &schTitleMetrics{Title: title, FontSize: 20, Width: width, Height: 20}
}

func compactTitleFrame(t *testing.T, id string, obstacles []layoutBBox, width float64, sheet *layoutBBox) schFrameSpec {
	t.Helper()
	f, err := measureSchModuleFrameObstacles(id, "Module", obstacles, compactTitleMetrics("Module", width), sheet)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkSchFrameTitleOccupancy(f, f.titleBounds()); err != nil {
		t.Fatalf("planner produced an occupied title: %v", err)
	}
	return f
}

func TestCompactTitleUsesUpperLowerSilhouette(t *testing.T) {
	cases := []struct {
		name      string
		obstacles []layoutBBox
		wantTop   bool
	}{
		{"upper gap", []layoutBBox{{100, 100, 300, 180}, {300, 100, 400, 240}}, true},
		{"lower gap", []layoutBBox{{100, 160, 300, 240}, {300, 100, 400, 240}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := append([]layoutBBox(nil), tc.obstacles...)
			f := compactTitleFrame(t, "M", tc.obstacles, 60, nil)
			// Occupied height 140 plus two 10-unit margins. A title that fits
			// beside the tall right-hand object must add no title-only band.
			if f.Rect.MaxY-f.Rect.MinY != 160 {
				t.Fatalf("unused local gap increased height: %+v", f)
			}
			b := f.titleBounds()
			if tc.wantTop && b.MinY <= 180 || !tc.wantTop && b.MaxY >= 160 {
				t.Fatalf("title did not use the available silhouette gap: %+v", b)
			}
			if !reflect.DeepEqual(before, tc.obstacles) {
				t.Fatal("planning changed source obstacle data")
			}
		})
	}
}

func TestCompactTitleFullSilhouetteNeedsOnlyMinimumExtension(t *testing.T) {
	f := compactTitleFrame(t, "M", []layoutBBox{{100, 100, 400, 240}}, 60, nil)
	// 140 content + 10 opposite margin + 5 clearance + 20 text + 10
	// title inset = 185, instead of reserving an additional fixed band.
	if got := f.Rect.MaxY - f.Rect.MinY; got != 185 {
		t.Fatalf("expected the minimum 185-unit height, got %g", got)
	}
	if f.TitleY != 265 || f.titleBounds().MinY != 245 {
		t.Fatalf("equal upper/lower candidates should select the stable upper candidate: %+v", f)
	}
	if f.Rect.MaxX-f.Rect.MinX != 320 {
		t.Fatal("title unnecessarily widened the module")
	}
}

func TestCompactTitleFindsInteriorHorizontalGap(t *testing.T) {
	obstacles := []layoutBBox{{100, 100, 150, 240}, {150, 100, 350, 160}, {350, 100, 400, 240}}
	f := compactTitleFrame(t, "M", obstacles, 150, nil)
	b := f.titleBounds()
	if f.Rect.MaxY-f.Rect.MinY != 160 || b.MinX < 155 || b.MaxX > 345 || b.MinY >= 240 {
		t.Fatalf("missed the upper concavity between two towers: %+v", f)
	}
	// Input order carries no placement meaning: candidate tie breaks must be
	// independent of the order an API returned its primitive inventory.
	obstacles[0], obstacles[2] = obstacles[2], obstacles[0]
	again := compactTitleFrame(t, "M", obstacles, 150, nil)
	if f.Rect != again.Rect || f.TitleX != again.TitleX || f.TitleY != again.TitleY {
		t.Fatal("obstacle inventory order changed the layout")
	}
}

func TestCompactTitleSelectsLegalSideAtSheetEdge(t *testing.T) {
	obstacles := []layoutBBox{{100, 100, 400, 240}}
	for _, tc := range []struct {
		name    string
		sheet   layoutBBox
		wantTop bool
	}{
		{"top edge forces bottom", layoutBBox{0, 0, 500, 260}, false},
		{"bottom edge forces top", layoutBBox{0, 85, 500, 400}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := compactTitleFrame(t, "M", obstacles, 60, &tc.sheet)
			if !boxInside(f.Rect, tc.sheet) {
				t.Fatal("candidate crossed the sheet boundary")
			}
			if tc.wantTop && f.TitleY <= 240 || !tc.wantTop && f.TitleY >= 100 {
				t.Fatalf("did not select the other legal side: %+v", f)
			}
		})
	}
	tooSmall := layoutBBox{0, 85, 500, 255}
	if _, err := measureSchModuleFrameObstacles("M", "Module", obstacles, compactTitleMetrics("Module", 60), &tooSmall); err == nil {
		t.Fatal("both sides outside the page must fail without creating another page")
	}
}

func TestCompactTitleUsesMeasuredLongTextAndRejectsInvalidMetrics(t *testing.T) {
	title := "Long title / 电源模块 AMS1117"
	obstacles := []layoutBBox{{100, 100, 400, 240}}
	m := compactTitleMetrics(title, 543.2)
	m.Height = 22.4
	f, err := measureSchModuleFrameObstacles("M", title, obstacles, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.TitleLayout.Width != 545 || f.TitleLayout.Height != 25 || f.FontSize != 20 {
		t.Fatalf("measured envelope should round outwards without changing font size: %+v", f)
	}
	if f.Rect.MaxX-f.Rect.MinX != 565 {
		t.Fatalf("long title must widen only by its measured width and insets: %+v", f.Rect)
	}
	if err := checkSchFrameTitleOccupancy(f, f.titleBounds()); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*schTitleMetrics){
		func(m *schTitleMetrics) { m.Title = "another title" },
		func(m *schTitleMetrics) { m.FontSize = 10 },
		func(m *schTitleMetrics) { m.Width = 0 },
		func(m *schTitleMetrics) { m.Width = math.NaN() },
		func(m *schTitleMetrics) { m.Height = -1 },
		func(m *schTitleMetrics) { m.Height = math.Inf(1) },
	} {
		bad := *m
		change(&bad)
		if _, err := measureSchModuleFrameObstacles("M", title, obstacles, &bad, nil); err == nil {
			t.Fatalf("invalid or mismatched title metrics were accepted: %+v", bad)
		}
	}
	for _, title := range []string{"POWER / AMS1117-3.3", "电源模块 / POWER", "MWii 123"} {
		fallback, err := measureSchModuleFrameObstacles("M", title, obstacles, nil, nil)
		if err != nil || fallback.FontSize != 20 || fallback.TitleLayout.Width <= 0 {
			t.Fatalf("fallback title planning failed for %q: %v", title, err)
		}
		if err := checkSchFrameTitleOccupancy(fallback, fallback.titleBounds()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCompactTitleNativeBoundsRejectOverflowAndCircuitCollision(t *testing.T) {
	f := compactTitleFrame(t, "M", []layoutBBox{{100, 100, 400, 240}}, 60, nil)
	actual := f.titleBounds()
	actual.MaxX -= 3.2
	if err := checkSchFrameTitleOccupancy(f, actual); err != nil {
		t.Fatalf("smaller native width should fit the reserved envelope: %v", err)
	}
	for _, change := range []func(*layoutBBox){
		func(b *layoutBBox) { b.MaxX += .1 },
		func(b *layoutBBox) { b.MinY -= .1 },
	} {
		b := f.titleBounds()
		change(&b)
		if err := checkSchFrameTitleOccupancy(f, b); err == nil || !strings.Contains(err.Error(), "envelope") {
			t.Fatalf("rendered text larger than its estimate was not rejected: %v", err)
		}
	}
	// A wire can be entirely inside the module while crossing the title.
	// A frame-only containment check would incorrectly report success.
	crossing := translateSchFrame(f, 0, 0)
	b := crossing.titleBounds()
	crossing.TitleLayout.Obstacles = append(crossing.TitleLayout.Obstacles,
		layoutBBox{b.MinX + 10, b.MinY + 9.5, b.MaxX - 10, b.MinY + 10.5})
	if err := checkSchFrameTitleOccupancy(crossing, b); err == nil || !strings.Contains(err.Error(), "clearance") {
		t.Fatalf("native title colliding with a wire was accepted: %v", err)
	}
}

func TestCompactTitleOccupancyIncludesWireMiddleAndFlagStub(t *testing.T) {
	contains := func(boxes []layoutBBox, x, y float64) bool {
		for _, b := range boxes {
			if b.MinX <= x && b.MaxX >= x && b.MinY <= y && b.MaxY >= y {
				return true
			}
		}
		return false
	}
	plan := &powerLayoutPlan{Wires: []powerLayoutWire{{Points: [][2]float64{{100, 100}, {300, 100}, {300, 200}}}}}
	boxes := powerLayoutContentObstacles(plan)
	for _, p := range [][2]float64{{200, 100}, {300, 150}} {
		if !contains(boxes, p[0], p[1]) {
			t.Fatalf("wire middle missing from title occupancy at %v", p)
		}
	}
	for _, tc := range []struct {
		direction string
		x, y      float64
	}{{"left", 450, 500}, {"right", 550, 500}, {"up", 500, 550}, {"down", 500, 450}} {
		plan := &powerLayoutPlan{Flags: []powerLayoutFlag{{Net: "GND", PinX: 500, PinY: 500, Direction: tc.direction, Offset: 100}}}
		if !contains(powerLayoutContentObstacles(plan), tc.x, tc.y) {
			t.Fatalf("%s flag stub is absent even though marker glyph cannot cover its midpoint", tc.direction)
		}
	}
	plan = &powerLayoutPlan{Placements: []powerLayoutPlacement{{Designator: "U1", BBox: layoutBBox{100, 100, 120, 120}, Pins: []powerLayoutPin{{X: 80, Y: 110}}}}}
	if !contains(powerLayoutContentObstacles(plan), 90, 110) {
		t.Fatal("pin stem between body and outer connection point is missing")
	}
}

func TestCompactTitleRowsTranslateOccupancyWithoutChangingSource(t *testing.T) {
	frames := []schFrameSpec{
		compactTitleFrame(t, "A", []layoutBBox{{100, 100, 300, 180}, {300, 100, 400, 240}}, 60, nil),
		compactTitleFrame(t, "B", []layoutBBox{{100, 160, 300, 240}, {300, 100, 400, 240}}, 60, nil),
		compactTitleFrame(t, "C", []layoutBBox{{100, 100, 400, 240}}, 60, nil),
	}
	before, _ := json.Marshal(frames)
	rows, err := planSchModuleRows(frames, layoutBBox{0, 0, 750, 800}, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Row != 0 || rows[1].Row != 0 || rows[2].Row != 1 {
		t.Fatal("expected two modules followed by a left-aligned second row")
	}
	for i, row := range rows {
		f := row.Frame
		if f.Rect.MaxY-f.Rect.MinY != frames[i].Rect.MaxY-frames[i].Rect.MinY {
			t.Fatal("tight titles must retain their own compact frame height")
		}
		if err := checkSchFrameTitleOccupancy(f, f.titleBounds()); err != nil {
			t.Fatalf("translated title no longer clears translated circuit: %v", err)
		}
		for j, o := range f.TitleLayout.Obstacles {
			original := frames[i].TitleLayout.Obstacles[j]
			if o.MinX-original.MinX != row.DX || o.MaxX-original.MaxX != row.DX || o.MinY-original.MinY != row.DY || o.MaxY-original.MaxY != row.DY {
				t.Fatal("occupancy moved separately from its circuit and title")
			}
		}
	}
	// Mutating either the returned envelope or an obstacle must not mutate the
	// caller's canonical input, including when translation was zero.
	rows[0].Frame.TitleLayout.Width++
	rows[0].Frame.TitleLayout.Obstacles[0].MinX++
	zero := translateSchFrame(frames[1], 0, 0)
	zero.TitleLayout.Obstacles[0].MinY++
	after, _ := json.Marshal(frames)
	if string(before) != string(after) {
		t.Fatal("Z row conversion aliased the canonical frame data")
	}
}
