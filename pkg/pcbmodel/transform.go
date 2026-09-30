package pcbmodel

import "math"

type Transform struct {
	Pivot         Point   `json:"pivot"`
	DX            float64 `json:"dx"`
	DY            float64 `json:"dy"`
	RotationDelta float64 `json:"rotationDelta"`
}

func (t Transform) Point(p Point) Point {
	a := t.RotationDelta * math.Pi / 180
	c, s := math.Cos(a), math.Sin(a)
	x, y := p[0]-t.Pivot[0], p[1]-t.Pivot[1]
	return Point{t.Pivot[0] + t.DX + x*c - y*s, t.Pivot[1] + t.DY + x*s + y*c}
}

func (t Transform) BBox(b BBox) BBox {
	points := []Point{{b.MinX, b.MinY}, {b.MinX, b.MaxY}, {b.MaxX, b.MinY}, {b.MaxX, b.MaxY}}
	out := BBox{MinX: math.Inf(1), MinY: math.Inf(1), MaxX: math.Inf(-1), MaxY: math.Inf(-1)}
	for _, p := range points {
		p = t.Point(p)
		out.MinX, out.MaxX = math.Min(out.MinX, p[0]), math.Max(out.MaxX, p[0])
		out.MinY, out.MaxY = math.Min(out.MinY, p[1]), math.Max(out.MaxY, p[1])
	}
	return out
}

func (t Transform) Component(c Component) Component {
	out := c
	out.Anchor = t.Point(c.Anchor)
	out.Rotation = normalize(c.Rotation + t.RotationDelta)
	out.BBox = t.BBox(c.BBox)
	out.Pads = append([]Pad(nil), c.Pads...)
	for i := range out.Pads {
		out.Pads[i].Center = t.Point(c.Pads[i].Center)
		out.Pads[i].Rotation = normalize(c.Pads[i].Rotation + t.RotationDelta)
	}
	return out
}

func (t Transform) Track(track Track) Track {
	out := track
	out.Points = make([]Point, len(track.Points))
	for i, p := range track.Points {
		out.Points[i] = t.Point(p)
	}
	return out
}

func (t Transform) Via(v Via) Via {
	out := v
	out.At = t.Point(v.At)
	return out
}

func (t Transform) Region(region Region) Region {
	out := region
	out.Points = make([]Point, len(region.Points))
	for i, p := range region.Points {
		out.Points[i] = t.Point(p)
	}
	out.Contours = make([][]Point, len(region.Contours))
	for i, contour := range region.Contours {
		out.Contours[i] = make([]Point, len(contour))
		for j, p := range contour {
			out.Contours[i][j] = t.Point(p)
		}
	}
	return out
}

func normalize(v float64) float64 {
	v = math.Mod(v, 360)
	if v < 0 {
		v += 360
	}
	if math.Abs(v-360) < 1e-7 {
		return 0
	}
	return v
}
