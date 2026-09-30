package app

import (
	"reflect"
	"testing"
)

func TestSchModuleRowsZOrderAndContentHeight(t *testing.T) {
	var frames []schFrameSpec
	for i, v := range [][2]float64{{300, 100}, {330, 150}, {250, 120}, {350, 110}, {400, 180}} {
		frames = append(frames, schFrameSpec{ID: string(rune('A' + i)), Rect: layoutBBox{MinX: 100, MinY: 100, MaxX: 100 + v[0], MaxY: 100 + v[1]}, TitleX: 110, TitleY: 90 + v[1]})
	}
	sheet := layoutBBox{MinX: 0, MinY: 0, MaxX: 1000, MaxY: 800}
	p, err := planSchModuleRows(frames, sheet, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range p {
		if v.Frame.ID != frames[i].ID || v.Frame.Rect.MaxY-v.Frame.Rect.MinY != frames[i].Rect.MaxY-frames[i].Rect.MinY {
			t.Fatal("logical order and each content-derived frame height must be preserved")
		}
		if !boxInside(v.Frame.Rect, sheet) {
			t.Fatal("frame outside sheet")
		}
		if i == 0 || p[i-1].Row != v.Row {
			if v.Frame.Rect.MinX != 20 {
				t.Fatal("each row must start from the left margin")
			}
			if i > 0 && v.Frame.Rect.MaxY >= p[i-1].Frame.Rect.MinY {
				t.Fatal("next row must lie below previous row")
			}
		} else if v.Frame.Rect.MaxY != p[i-1].Frame.Rect.MaxY || v.Frame.Rect.MinX <= p[i-1].Frame.Rect.MaxX {
			t.Fatal("same-row modules must align and move right")
		}
	}
	if p[0].Row != 0 || p[2].Row != 0 || p[3].Row != 1 || p[4].Row != 1 {
		t.Fatal("expected three modules then two in Z order")
	}
	if p[3].Frame.Rect.MaxY != 610 || p[3].Frame.Rect.MaxY != p[1].Frame.Rect.MinY-20 {
		t.Fatal("second row must advance by the first row's 150 height, not the later row's 180 height")
	}
	if _, err := planSchModuleRows(frames, layoutBBox{MinX: 0, MinY: 0, MaxX: 500, MaxY: 400}, 20, 20); err == nil {
		t.Fatal("overflow must not create additional pages")
	}
}

func TestPowerLayoutDefaultStartsTopLeftWithoutChangingTopology(t *testing.T) {
	raw := powerLayoutBytes(t, powerLayoutFixture(t, 0, 0, 20, 0))
	o := powerLayoutTestOptions()
	o.At = nil
	o.PreservePosition = true
	original, err := planPowerLayout(raw, o)
	if err != nil {
		t.Fatal(err)
	}
	o.PreservePosition = false
	packed, err := planPowerLayout(raw, o)
	if err != nil {
		t.Fatal(err)
	}
	if packed.Frames[0].Rect.MinX != 10 || packed.Frames[0].Rect.MaxY != 815 {
		t.Fatal("default module must start at A4 upper-left inset")
	}
	if !reflect.DeepEqual(original.ExpectedPinNets, packed.ExpectedPinNets) {
		t.Fatal("packing changed topology")
	}
	dx, dy := packed.Placements[0].X-original.Placements[0].X, packed.Placements[0].Y-original.Placements[0].Y
	for i, c := range packed.Placements {
		for j, p := range c.Pins {
			q := original.Placements[i].Pins[j]
			if p.X-q.X != dx || p.Y-q.Y != dy {
				t.Fatal("pins must move with the whole module")
			}
		}
	}
	for i, w := range packed.Wires {
		for j, p := range w.Points {
			q := original.Wires[i].Points[j]
			if p[0]-q[0] != dx || p[1]-q[1] != dy {
				t.Fatal("wire must move with the whole module")
			}
		}
	}
	for i, f := range packed.Flags {
		q := original.Flags[i]
		if f.PinX-q.PinX != dx || f.PinY-q.PinY != dy || f.Direction != q.Direction {
			t.Fatal("marker must move with the whole module")
		}
	}
	// Initial random placement (even far outside the page) is measurement input,
	// not a target. Packing the same geometry must produce identical output XY.
	shifted := powerLayoutBytes(t, powerLayoutFixture(t, 2000, -3000, 20, 0))
	again, err := planPowerLayout(shifted, o)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(packed, again) {
		t.Fatal("old random positions changed the packed result")
	}
}
