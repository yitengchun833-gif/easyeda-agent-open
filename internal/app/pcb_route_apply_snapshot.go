package app

import "fmt"

// Every observation uses the caller's target/context checks, including silk and rules.
func readPCBRouteApplySnapshot(call pcbRouteApplyCall, withSilk bool) (*boardSnapshot, error) {
	s := &boardSnapshot{Copper: &boardCopperSnapshot{Availability: map[string]string{}}}
	read := func(action, key string, payload map[string]any) (map[string]any, error) {
		res, err := call(action, payload)
		if err != nil {
			return nil, err
		}
		if res == nil || res.Result == nil {
			return nil, fmt.Errorf("%s returned no observation", action)
		}
		if key != "" {
			if _, ok := res.Result[key].([]any); !ok {
				return nil, fmt.Errorf("%s returned no %s inventory", action, key)
			}
		}
		return res.Result, nil
	}
	components, err := read("pcb.components.list", "components", map[string]any{"includeBBox": true, "includePads": true})
	if err != nil {
		return nil, err
	}
	if _, err := parseNetPathPads(components); err != nil {
		return nil, err
	}
	s.Components = parseBoardComponents(components)
	outline, err := read("pcb.outline.get", "", nil)
	if err != nil {
		return nil, err
	}
	s.Outline = parseBoardOutline(outline)
	s.sanitizeOutline()
	rules, err := read("pcb.drc.rules", "", nil)
	if err != nil {
		return nil, err
	}
	s.Rules = rulesToBoard(parsePcbRules(rules))
	layers, err := read("pcb.layers.list", "", nil)
	if err != nil {
		return nil, err
	}
	n, ok := asFloatOK(layers["copperLayerCount"])
	if !ok || n < 1 || n != float64(int(n)) {
		return nil, fmt.Errorf("copper layer count unavailable")
	}
	s.CopperLayers = int(n)
	for _, category := range []struct {
		name, action, key string
		destination       *[]any
		payload           map[string]any
	}{
		{"routing", "pcb.line.list", "lines", &s.Copper.Lines, nil},
		{"vias", "pcb.via.list", "vias", &s.Copper.Vias, nil},
		{"pours", "pcb.pour.list", "pours", &s.Copper.Pours, nil},
		{"poured", "pcb.poured.list", "poured", &s.Copper.Poured, nil},
		{"regions", "pcb.region.list", "regions", &s.Copper.Regions, nil},
		{"fills", "pcb.fill.list", "fills", &s.Copper.Fills, map[string]any{"includeBBox": true}},
	} {
		result, err := read(category.action, category.key, category.payload)
		if err != nil {
			return nil, err
		}
		*category.destination = result[category.key].([]any)
		s.Copper.Availability[category.name] = "available"
		if category.name == "routing" {
			if _, _, err := parseNetPathLines(result); err != nil {
				return nil, err
			}
			s.Copper.Arcs = result["arcs"].([]any)
			available := true
			s.Copper.ArcsAvailable = &available
		}
	}
	if withSilk {
		result, err := read("pcb.silk.list", "texts", nil)
		if err != nil {
			return nil, err
		}
		s.Silk = parsePcbSilk(result)
	}
	return s, nil
}

func stablePCBRouteApplySnapshot(call pcbRouteApplyCall, withSilk bool) (*boardSnapshot, error) {
	first, err := readPCBRouteApplySnapshot(call, withSilk)
	if err != nil {
		return nil, err
	}
	second, err := readPCBRouteApplySnapshot(call, withSilk)
	if err != nil {
		return nil, err
	}
	a, err := routeApplyFacts(first, false)
	if err != nil {
		return nil, err
	}
	b, err := routeApplyFacts(second, false)
	if err != nil {
		return nil, err
	}
	if a != b {
		return nil, fmt.Errorf("board facts changed while reading the bound route snapshot")
	}
	return second, nil
}
