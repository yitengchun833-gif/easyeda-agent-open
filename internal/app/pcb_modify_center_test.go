package app

import (
	"encoding/json"
	"math"
	"testing"
)

// rotateVecCCW rotates a footprint-local vector by the EasyEDA y-up CCW
// rotation (test helper — mirrors how a rendered bbox moves with rotation).
func rotateVecCCW(x, y float64, deg int) (float64, float64) {
	switch ((deg % 360) + 360) % 360 {
	case 90:
		return -y, x
	case 180:
		return -x, -y
	case 270:
		return y, -x
	default:
		return x, y
	}
}

// TestAnchorForCenterAcrossRotations builds a deliberately ASYMMETRIC
// footprint (anchor far off the body center: extents x∈[-10,+90],
// y∈[-20,+40] relative to the anchor at rotation 0), renders its bbox at
// each rotation the way EasyEDA does (rotation baked into the bbox), and
// checks that writing the converted anchor puts the bbox center exactly on
// the requested target.
func TestAnchorForCenterAcrossRotations(t *testing.T) {
	const (
		anchorX, anchorY   = 1000.0, 2000.0
		targetCX, targetCY = 5000.0, 6250.0
		exMin, exMax       = -10.0, 90.0 // x extents rel. anchor @ rot 0
		eyMin, eyMax       = -20.0, 40.0 // y extents rel. anchor @ rot 0
	)
	for _, rot := range []int{0, 90, 180, 270} {
		// Rendered bbox at this rotation: rotate the two extreme corners
		// about the anchor and take the axis-aligned envelope.
		x1, y1 := rotateVecCCW(exMin, eyMin, rot)
		x2, y2 := rotateVecCCW(exMax, eyMax, rot)
		minX := anchorX + math.Min(x1, x2)
		maxX := anchorX + math.Max(x1, x2)
		minY := anchorY + math.Min(y1, y2)
		maxY := anchorY + math.Max(y1, y2)

		ax, ay := anchorForCenter(anchorX, anchorY, minX, minY, maxX, maxY, targetCX, targetCY)

		// Moving the anchor translates the bbox rigidly; verify the moved
		// bbox center lands exactly on the target.
		dx, dy := ax-anchorX, ay-anchorY
		gotCX, gotCY := bboxCenterXY(minX+dx, minY+dy, maxX+dx, maxY+dy)
		if math.Abs(gotCX-targetCX) > 1e-9 || math.Abs(gotCY-targetCY) > 1e-9 {
			t.Errorf("rot %d: bbox center after move = (%g,%g), want (%g,%g)", rot, gotCX, gotCY, targetCX, targetCY)
		}

		// The anchor-to-center offset must be preserved by the move (pure
		// translation, rotation untouched).
		curCX, curCY := bboxCenterXY(minX, minY, maxX, maxY)
		if math.Abs((ax-targetCX)-(anchorX-curCX)) > 1e-9 || math.Abs((ay-targetCY)-(anchorY-curCY)) > 1e-9 {
			t.Errorf("rot %d: anchor-to-center offset not preserved", rot)
		}
	}
}

// TestAnchorForCenterKnownValues pins one concrete case so a sign slip in
// the translation cannot cancel out: rotation 0, anchor (1000,2000), bbox
// x[990,1090] y[1980,2040] → current center (1040,2010), anchor−center =
// (−40,−10). Target center (500,700) → anchor (460,690).
func TestAnchorForCenterKnownValues(t *testing.T) {
	ax, ay := anchorForCenter(1000, 2000, 990, 1980, 1090, 2040, 500, 700)
	if ax != 460 || ay != 690 {
		t.Fatalf("anchorForCenter = (%g,%g), want (460,690)", ax, ay)
	}
}

func TestBBoxCenter(t *testing.T) {
	cx, cy := bboxCenterXY(-10, 20, 30, 100)
	if cx != 10 || cy != 60 {
		t.Fatalf("bboxCenter = (%g,%g), want (10,60)", cx, cy)
	}
}

func TestInjectBBoxCenters(t *testing.T) {
	in := []byte(`{"ok":true,"result":{"count":2,"components":[` +
		`{"primitiveId":"a","designator":"U1","x":100,"y":200,"bbox":{"minX":90,"minY":180,"maxX":190,"maxY":240}},` +
		`{"primitiveId":"b","designator":"R1","x":300,"y":400}]}}`)
	out := injectBBoxCenters(in)

	var env struct {
		OK     bool `json:"ok"`
		Result struct {
			Count      int `json:"count"`
			Components []struct {
				PrimitiveID string `json:"primitiveId"`
				Center      *struct {
					X float64 `json:"x"`
					Y float64 `json:"y"`
				} `json:"center"`
			} `json:"components"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if !env.OK || env.Result.Count != 2 || len(env.Result.Components) != 2 {
		t.Fatalf("envelope fields not preserved: %s", out)
	}
	c0 := env.Result.Components[0]
	if c0.Center == nil || c0.Center.X != 140 || c0.Center.Y != 210 {
		t.Errorf("component with bbox: center = %+v, want (140,210)", c0.Center)
	}
	if env.Result.Components[1].Center != nil {
		t.Errorf("component without bbox must not get a center")
	}
}

func TestInjectBBoxCentersPassthrough(t *testing.T) {
	for _, in := range []string{
		`not json at all`,
		`{"ok":false,"error":{"message":"boom"}}`,
		`{"ok":true,"result":{"components":[{"primitiveId":"a"}]}}`, // no bbox anywhere
	} {
		if got := string(injectBBoxCenters([]byte(in))); got != in {
			t.Errorf("expected passthrough for %q, got %q", in, got)
		}
	}
}
