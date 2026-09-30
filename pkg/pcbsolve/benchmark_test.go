package pcbsolve_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbsolve"
)

func BenchmarkSolveRouteFeedback(b *testing.B) {
	board, request := solveBoard(), solveRequest()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if report, err := pcbsolve.Solve(context.Background(), board, request); err != nil || report.Status != pcbsolve.Pass {
			b.Fatal(err)
		}
	}
}

func BenchmarkSolveDozensOfComponents(b *testing.B) {
	board, request := solveBoard(), solveRequest()
	board.Outline = pcbmodel.Outline{Source: "polygon", BBox: pcbmodel.BBox{MinX: 0, MinY: 0, MaxX: 1200, MaxY: 900}, Points: []pcbmodel.Point{{0, 0}, {1200, 0}, {1200, 900}, {0, 900}}}
	for i := 0; i < 48; i++ {
		x, y := 400+float64(i%12)*55, 300+float64(i/12)*80
		board.Components = append(board.Components, pcbmodel.Component{Ref: fmt.Sprintf("F%d", i+1), Side: 1, Anchor: pcbmodel.Point{x, y}, BBox: pcbmodel.BBox{MinX: x - 10, MinY: y - 10, MaxX: x + 10, MaxY: y + 10}, Pads: []pcbmodel.Pad{{Number: "1", Net: fmt.Sprintf("N%d", i+1), Layer: 1, Center: pcbmodel.Point{x, y}, Width: 8, Height: 8, Shape: "circle"}}})
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if report, err := pcbsolve.Solve(context.Background(), board, request); err != nil || report.Status != pcbsolve.Pass {
			b.Fatal(err)
		}
	}
}
