package app

import "testing"

func TestSchModuleDefaultMarginsAndGapsAreTenthInch(t *testing.T) {
	// A local upper gap fits the title without growing the circuit envelope.
	// This isolates the requested 0.1-inch frame padding on all four sides.
	obstacles := []layoutBBox{{100, 100, 300, 180}, {300, 100, 400, 240}}
	f := compactTitleFrame(t, "A", obstacles, 60, nil)
	content := schBoundsUnion(obstacles)
	for side, distance := range map[string]float64{
		"left":   content.MinX - f.Rect.MinX,
		"right":  f.Rect.MaxX - content.MaxX,
		"bottom": content.MinY - f.Rect.MinY,
		"top":    f.Rect.MaxY - content.MaxY,
	} {
		if distance != 10 {
			t.Fatalf("%s circuit-to-frame spacing = %g; want 10 raw units (0.1 inch)", side, distance)
		}
	}
	if f.TitleX-f.Rect.MinX != 10 || f.Rect.MaxY-f.TitleY != 10 {
		t.Fatal("title must use the same 10-unit frame inset")
	}
	if f.FontSize != 20 || f.TitleLayout.Clearance != 5 {
		t.Fatal("compact margins changed the 0.2-inch title or its 5-unit circuit clearance")
	}

	frames := []schFrameSpec{f, translateSchFrame(f, 1000, -500), translateSchFrame(f, -400, 1000)}
	frames[1].ID, frames[2].ID = "B", "C"
	// Two 320-wide modules with one 10-unit gap exactly fill the first row.
	// Two 160-high rows with a 10-unit gap exactly fill the sheet height.
	sheet := layoutBBox{0, 0, 670, 350}
	rows, err := planSchModuleRows(frames, sheet, schModulePageMargin, schModuleGap)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Row != 0 || rows[1].Row != 0 || rows[2].Row != 1 {
		t.Fatal("default spacing changed the expected two-then-one Z order")
	}
	for edge, distance := range map[string]float64{
		"page left":       rows[0].Frame.Rect.MinX - sheet.MinX,
		"page top":        sheet.MaxY - rows[0].Frame.Rect.MaxY,
		"page right":      sheet.MaxX - rows[1].Frame.Rect.MaxX,
		"page bottom":     rows[2].Frame.Rect.MinY - sheet.MinY,
		"horizontal gap":  rows[1].Frame.Rect.MinX - rows[0].Frame.Rect.MaxX,
		"vertical gap":    rows[0].Frame.Rect.MinY - rows[2].Frame.Rect.MaxY,
		"second row left": rows[2].Frame.Rect.MinX - sheet.MinX,
	} {
		if distance != 10 {
			t.Fatalf("%s = %g; want a fixed 10-unit margin/gap", edge, distance)
		}
	}
	for _, row := range rows {
		if err := checkSchFrameTitleOccupancy(row.Frame, row.Frame.titleBounds()); err != nil {
			t.Fatalf("tighter shared defaults caused a title collision: %v", err)
		}
	}
}
