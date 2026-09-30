package app

import (
	"fmt"
	"sort"
)

// Review is advisory source-topology evidence, never a new ownership assignment
// or proof of physical wire connectivity. Display refs are not classification input.
type SchematicZoneReview struct {
	SchemaVersion int                          `json:"schemaVersion"`
	RuleVersion   string                       `json:"ruleVersion"`
	SourceSHA256  string                       `json:"sourceSha256"`
	Status        string                       `json:"status"`
	Scope         string                       `json:"scope"`
	Limitations   []string                     `json:"limitations"`
	Findings      []SchematicZoneReviewFinding `json:"findings"`
	Error         string                       `json:"error,omitempty"`
}

type SchematicZoneReviewFinding struct {
	Rule            string                         `json:"rule"`
	Severity        string                         `json:"severity"`
	ZoneID          string                         `json:"zoneId"`
	CoreComponentID string                         `json:"coreComponentId"`
	Components      []SchematicZoneReviewComponent `json:"components"`
	Nets            []SchematicZoneReviewNet       `json:"nets"`
	Message         string                         `json:"message"`
	Suggestion      string                         `json:"suggestion"`
}

type SchematicZoneReviewComponent struct {
	ID       string `json:"id"`
	Ref      string `json:"ref"`
	PinCount int    `json:"pinCount"`
}

type SchematicZoneReviewNet struct {
	Name   string                  `json:"name"`
	Policy string                  `json:"policy"`
	Pins   []SchematicLayoutAttach `json:"pins"`
}

func reviewSchematicZonesJSON(raw []byte) (*SchematicZoneReview, error) {
	r := &SchematicZoneReview{SchemaVersion: 1, RuleVersion: "1", SourceSHA256: sha256Hex(raw),
		Status: "no-findings", Scope: "source-zone-topology-advisory", Findings: []SchematicZoneReviewFinding{},
		Limitations: []string{
			"未命中规则不证明功能归属正确；AI 需结合器件身份、手册和设计意图决定保留或拆分。",
			"仅排除显式 local_power/local_ground；direct/module_port 也可能承载电源，不从网名猜类型。",
			"共享源网络不证明真实导线连通；本报告不验证布局、库身份或现场图面。",
		}}
	in, err := decodeSchematicZonesInput(raw)
	if err == nil {
		err = populateSchematicZoneReview(in, r)
	}
	if err != nil {
		r.Status, r.Error = "invalid", err.Error()
		return r, err
	}
	if len(r.Findings) > 0 {
		r.Status = "review-required"
	}
	return r, nil
}

func populateSchematicZoneReview(in SchematicZonesInput, report *SchematicZoneReview) error {
	index, err := validateSchematicZoneOwnership(in)
	if err != nil {
		return err
	}
	// Check the pin/attachment evidence before generating any semantic hints.
	pins := map[string]map[string]string{}
	for _, c := range in.Components {
		pins[c.ID] = map[string]string{}
		if len(c.Measurement.Pins) == 0 {
			return fmt.Errorf("component %s has no pin evidence", c.ID)
		}
		for _, p := range c.Measurement.Pins {
			if _, exists := pins[c.ID][p.Number]; exists || p.Number == "" {
				return fmt.Errorf("component %s has duplicate/empty pin %s", c.ID, p.Number)
			}
			pins[c.ID][p.Number] = p.Net
			state := c.PinStates[p.Number]
			if p.Net == "" && state != "nc" && state != "unconnected" || p.Net != "" && state != "" {
				return fmt.Errorf("component %s pin %s lacks consistent net/NC/unconnected evidence", c.ID, p.Number)
			}
		}
		for number := range c.PinStates {
			if _, exists := pins[c.ID][number]; !exists {
				return fmt.Errorf("component %s has unknown pin state %s", c.ID, number)
			}
		}
	}
	parents := map[string]string{}
	explicitAttachments := map[string]SchematicLayoutPeripheral{}
	attachmentsSeen := map[string]bool{}
	for _, a := range in.Attachments {
		zone := index.owners[a.ComponentID]
		var core string
		for _, z := range in.Zones {
			if z.ID == zone {
				core = z.CoreComponentID
			}
		}
		if attachmentsSeen[a.ComponentID] || a.ComponentID == core {
			return fmt.Errorf("invalid/duplicate attachment %s", a.ComponentID)
		}
		attachmentsSeen[a.ComponentID] = true
		if a.PinNumber != "" {
			if _, exists := pins[a.ComponentID][a.PinNumber]; !exists {
				return fmt.Errorf("attachment %s has unknown pin %s", a.ComponentID, a.PinNumber)
			}
		}
		// No attachTo means the solver may choose a connected peripheral host.
		// Do not invent an attachment to the zone core for the advisory review.
		if a.AttachTo == nil {
			continue
		}
		host := a.AttachTo.ComponentID
		if _, exists := pins[host][a.AttachTo.PinNumber]; !exists {
			return fmt.Errorf("attachment %s has unknown host pin %s.%s", a.ComponentID, host, a.AttachTo.PinNumber)
		}
		matched := false
		for childPin, net := range pins[a.ComponentID] {
			if net == "" || a.PinNumber != "" && childPin != a.PinNumber {
				continue
			}
			for hostPin, other := range pins[host] {
				if a.AttachTo != nil && hostPin != a.AttachTo.PinNumber {
					continue
				}
				matched = matched || net == other
			}
		}
		if !matched {
			return fmt.Errorf("attachment %s to %s has no shared net on specified pins", a.ComponentID, host)
		}
		parents[a.ComponentID] = host
		explicitAttachments[a.ComponentID] = a
	}
	for id := range parents {
		seen := map[string]bool{}
		for node := id; node != ""; node = parents[node] {
			if seen[node] {
				return fmt.Errorf("attachment cycle at %s", node)
			}
			seen[node] = true
		}
	}
	for _, zone := range in.Zones {
		members := append([]string(nil), zone.ComponentIDs...)
		sort.Strings(members)
		nets := map[string][]SchematicLayoutAttach{}
		for _, id := range members {
			for _, p := range index.components[id].Measurement.Pins {
				if p.Net != "" {
					nets[p.Net] = append(nets[p.Net], SchematicLayoutAttach{ComponentID: id, PinNumber: p.Number})
				}
			}
		}
		add := func(rule string, ids []string, netNames []string, message, suggestion string) {
			f := SchematicZoneReviewFinding{Rule: rule, Severity: "warning", ZoneID: zone.ID,
				CoreComponentID: zone.CoreComponentID, Components: []SchematicZoneReviewComponent{},
				Nets: []SchematicZoneReviewNet{}, Message: message, Suggestion: suggestion}
			for _, id := range ids {
				c := index.components[id]
				f.Components = append(f.Components, SchematicZoneReviewComponent{ID: id, Ref: c.Measurement.Designator, PinCount: len(c.Measurement.Pins)})
			}
			sort.Strings(netNames)
			for _, n := range netNames {
				witnesses := append([]SchematicLayoutAttach(nil), nets[n]...)
				sort.Slice(witnesses, func(i, j int) bool {
					if witnesses[i].ComponentID != witnesses[j].ComponentID {
						return witnesses[i].ComponentID < witnesses[j].ComponentID
					}
					return witnesses[i].PinNumber < witnesses[j].PinNumber
				})
				f.Nets = append(f.Nets, SchematicZoneReviewNet{Name: n, Policy: in.NetPolicies[n], Pins: witnesses})
			}
			report.Findings = append(report.Findings, f)
		}
		var multipin []string
		for _, id := range members {
			if len(pins[id]) >= 4 {
				multipin = append(multipin, id)
			}
		}
		if len(multipin) >= 2 {
			add("multiple-multipin-members", multipin, nil,
				"同一区含多个 >=4 引脚器件，可能存在多个功能核心；引脚数只是弱线索。",
				"AI 核对器件功能：独立核心可连同专属外围拆区；接口与保护阵列可合理同区，保留时说明依据。")
		}
		// Bipartite traversal visits each net once; shared rails never create edges.
		visited, visitedNet := map[string]bool{}, map[string]bool{}
		for _, seed := range members {
			if visited[seed] {
				continue
			}
			queue, group, groupNets := []string{seed}, []string{}, map[string]bool{}
			visited[seed] = true
			containsCore := false
			for len(queue) > 0 {
				id := queue[0]
				queue = queue[1:]
				group = append(group, id)
				containsCore = containsCore || id == zone.CoreComponentID
				for _, p := range index.components[id].Measurement.Pins {
					if p.Net == "" || zoneReviewRail(in.NetPolicies[p.Net]) || visitedNet[p.Net] {
						continue
					}
					visitedNet[p.Net], groupNets[p.Net] = true, true
					for _, peer := range nets[p.Net] {
						if !visited[peer.ComponentID] {
							visited[peer.ComponentID] = true
							queue = append(queue, peer.ComponentID)
						}
					}
				}
			}
			if !containsCore && len(group) >= 2 {
				sort.Strings(group)
				names := []string{}
				for n := range groupNets {
					names = append(names, n)
				}
				add("non-rail-subgraph-detached", group, names,
					"排除声明的电源/地网络后，该子图与当前核心没有源网络路径。",
					"AI 复核是否为独立功能子电路；需要拆分时一起迁移核心及专属外围，并复核 attachment 和边界策略。")
			}
		}
		for _, child := range members {
			host, exists := parents[child]
			if !exists {
				continue
			}
			shared := map[string]bool{}
			allRails := true
			relation := explicitAttachments[child]
			for childPin, a := range pins[child] {
				if a == "" || relation.PinNumber != "" && childPin != relation.PinNumber {
					continue
				}
				for hostPin, b := range pins[host] {
					if hostPin != relation.AttachTo.PinNumber {
						continue
					}
					if a == b {
						shared[a] = true
						allRails = allRails && zoneReviewRail(in.NetPolicies[a])
					}
				}
			}
			if allRails && len(shared) > 0 {
				names := []string{}
				for n := range shared {
					names = append(names, n)
				}
				add("rail-only-attachment", []string{child, host}, names,
					"该 attachment 两端器件只共享声明的电源/地网络，无法仅据此证明专属外围关系。",
					"AI 核对去耦/供电支路等功能依据；合法外围可以保留，独立电源功能可考虑拆区。")
			}
		}
	}
	sort.Slice(report.Findings, func(i, j int) bool {
		a, b := report.Findings[i], report.Findings[j]
		if a.ZoneID != b.ZoneID {
			return a.ZoneID < b.ZoneID
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return a.Components[0].ID < b.Components[0].ID
	})
	return nil
}

func zoneReviewRail(policy string) bool { return policy == "local_power" || policy == "local_ground" }
