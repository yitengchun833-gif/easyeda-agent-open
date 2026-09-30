package blocks

import (
	"encoding/json"
	"strings"
)

// PlacementHint is a block's placement.<REF> declaration, projected for the PCB
// placer to consume. The circuit-block library (data/*.json) is the single
// source-of-truth for placement roles; the placer reverse-maps a PLACED
// component (which carries no block link) back to its declared role via the
// part device id or the designator prefix.
type PlacementHint struct {
	BoardEdge   bool   `json:"board_edge"`
	Anchor      bool   `json:"anchor"` // deliberate non-edge position → pin in place (don't spiral)
	Edge        string `json:"edge"`
	Side        string `json:"side"`
	Orientation string `json:"orientation"`
	Severity    string `json:"severity"`
	Device      string `json:"-"` // library part id (parts[ref].part) this hint applies to
	Ref         string `json:"-"` // block-internal designator the hint was declared under
}

// PlacementIndex is the reverse-map the placer consults before falling back to
// the hardcoded regex: designator-prefix → block placement hint.
//
// ByRefPrefix is deliberately restricted to DISTINCTIVE prefixes (2+ letters
// such as JP / SW / LED / ANT). Generic single-letter prefixes (U / C / R / X /
// J) are excluded: on a real board they denote a whole component class, so
// blanket prefix-mapping them would misfile (e.g. snapping every U* IC to a
// board edge). Those fall through to the regex fallback.
//
// NOTE — device-level precision is a deliberate FUTURE layer, not indexed here.
// A block's parts[ref].part is an internal role-id ("conn.sip2_254"), which a
// PLACED component never exposes — it reports the real device name / deviceUuid
// (e.g. "SIP2-2.54mm单排针"). Matching those requires the role-id → device bridge
// in standard-parts.json, which lives in the skill tree and can't be go:embed'd
// here (go:embed can't reach `..`). Until that bridge is embedded, the
// designator prefix (itself block-declared, per the improvements-sink-to-blocks
// rule) is the real matcher; keying on the raw role-id would match nothing.
type PlacementIndex struct {
	ByRefPrefix map[string]PlacementHint // upper-cased alpha designator prefix → hint
}

// blockPlacementRaw mirrors just the fields of a block JSON the index needs. Both
// maps are kept as RawMessage so a schema doc key (`_doc`, a STRING value) or any
// other non-conforming entry can be skipped PER-KEY — unmarshalling straight into
// map[string]PlacementHint would fail the whole block and silently drop every hint.
type blockPlacementRaw struct {
	Parts     map[string]json.RawMessage `json:"parts"`
	Placement map[string]json.RawMessage `json:"placement"`
}

// refPrefix returns the leading alphabetic run of a designator, upper-cased
// ("JP701" → "JP", "SW_BOOT" → "SW", "J4" → "J"). Empty if it doesn't start
// with a letter.
func refPrefix(ref string) string {
	ref = strings.ToUpper(strings.TrimSpace(ref))
	i := 0
	for i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z' {
		i++
	}
	return ref[:i]
}

// LoadPlacementIndex builds the reverse-map from every embedded block's
// placement.* declarations. It reads the raw JSON directly (not the Block
// projection, which drops `placement`) so the block library stays the sole
// source-of-truth. A malformed single block is skipped, not fatal.
func LoadPlacementIndex() (PlacementIndex, error) {
	idx := PlacementIndex{
		ByRefPrefix: map[string]PlacementHint{},
	}
	all, err := Load()
	if err != nil {
		return idx, err
	}
	// A prefix that resolves to conflicting board_edge across blocks is
	// ambiguous → drop it, let the regex fallback decide instead.
	prefixConflict := map[string]bool{}
	for _, b := range all {
		var raw blockPlacementRaw
		if json.Unmarshal(b.Raw, &raw) != nil {
			continue
		}
		for ref, rawHint := range raw.Placement {
			if strings.HasPrefix(ref, "_") { // schema doc key (e.g. "_doc"), not a real ref
				continue
			}
			var hint PlacementHint
			if json.Unmarshal(rawHint, &hint) != nil {
				continue // a single non-conforming entry — skip it, not the whole block
			}
			hint.Ref = ref
			if praw, ok := raw.Parts[ref]; ok {
				var p struct {
					Part string `json:"part"`
				}
				if json.Unmarshal(praw, &p) == nil {
					hint.Device = p.Part // kept for diagnostics / the future device-bridge layer
				}
			}
			prefix := refPrefix(ref)
			// Skip generic single-letter prefixes (see PlacementIndex doc).
			if len(prefix) < 2 || prefixConflict[prefix] {
				continue
			}
			if prev, seen := idx.ByRefPrefix[prefix]; seen {
				if prev.BoardEdge != hint.BoardEdge {
					delete(idx.ByRefPrefix, prefix)
					prefixConflict[prefix] = true
				}
				continue
			}
			idx.ByRefPrefix[prefix] = hint
		}
	}
	return idx, nil
}
