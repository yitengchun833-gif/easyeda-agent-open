package pcbsolve_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbrouting"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbsolve"
)

func TestFixedPlacementRouteOrderBacktracking(t *testing.T) {
	var board pcbmodel.Board
	var req pcbsolve.Request
	var witness []pcbrouting.JointCandidate
	readCaseJSON(t, "testdata/fixed-route-order/board.json", &board)
	readCaseJSON(t, "testdata/fixed-route-order/request.json", &req)
	readCaseJSON(t, "testdata/fixed-route-order/witness.json", &witness)

	// This constructive witness is independent of Solve: A can go around B's
	// lower endpoint while B stays straight. Its coordinates are not a required
	// solver answer; they prove the fixed geometry has a legal joint solution.
	candidate := fixedRouteWitness(board, req, witness)
	if err := pcbsolve.Check(context.Background(), board, req, candidate); err != nil {
		t.Fatalf("independent route witness is invalid: %v", err)
	}
	witness[0].Routes[0].Points = []pcbrouting.Point{{30, 100}, {270, 100}}
	if err := pcbsolve.Check(context.Background(), board, req, fixedRouteWitness(board, req, witness)); err == nil {
		t.Fatal("two independently reachable but crossing straight routes passed")
	}
	for _, demand := range req.Routing.Demands {
		one := req
		one.Routing.Demands = []pcbsolve.Demand{demand}
		rep, err := pcbsolve.Solve(context.Background(), board, one)
		if err != nil || rep.Status != pcbsolve.Pass {
			t.Fatalf("single demand %s failed: %v %+v", demand.ID, err, rep)
		}
	}
	for _, mode := range []string{"original", "reordered", "renamed"} {
		t.Run(mode, func(t *testing.T) {
			r := req
			r.Routing.Demands = append([]pcbsolve.Demand(nil), req.Routing.Demands...)
			switch mode {
			case "reordered":
				r.Routing.Demands[0], r.Routing.Demands[1] = r.Routing.Demands[1], r.Routing.Demands[0]
			case "renamed":
				r.Routing.Demands[0].ID, r.Routing.Demands[1].ID = "b", "a"
			}
			rep, err := pcbsolve.Solve(context.Background(), board, r)
			if err != nil || rep.Status != pcbsolve.Pass || len(rep.Candidates) == 0 {
				t.Fatalf("fixed placement is jointly routable: err=%v status=%s reason=%s states=%d exhausted=%v", err, rep.Status, rep.Reason, rep.States, rep.Exhausted)
			}
			c := rep.Candidates[0]
			if !reflect.DeepEqual(c.Board, board) || len(c.Moves) != 0 || c.Metrics.MovedGroups != 0 || c.Metrics.NewVias != 0 {
				t.Fatalf("fixed geometry was altered to repair route order: %+v", c.Metrics)
			}
			if err := pcbsolve.Check(context.Background(), board, r, c); err != nil {
				t.Fatalf("independent joint check: %v", err)
			}
		})
	}
}

func fixedRouteWitness(board pcbmodel.Board, req pcbsolve.Request, routes []pcbrouting.JointCandidate) pcbsolve.Candidate {
	raw, _ := json.Marshal(req)
	sum := sha256.Sum256(raw)
	c := pcbsolve.Candidate{BaseSemanticHash: board.SemanticHash, RequestHash: hex.EncodeToString(sum[:]), Board: board, Routes: routes}
	for _, route := range routes {
		c.Metrics.NewVias += len(route.Vias)
		for _, segment := range route.Routes {
			length, bends := pcbrouting.Metrics(segment.Points)
			c.Metrics.RouteLength += length
			c.Metrics.Bends += bends
		}
	}
	return c
}
