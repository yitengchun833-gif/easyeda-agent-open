package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func newPcbModuleOwnedCmd(stdout, stderr io.Writer) *cobra.Command {
	var candidatePath, journalPath, boardPath, outPath string
	c := &cobra.Command{
		Use:   "module-owned",
		Short: "Recover exact old module object ownership offline (no editor writes)",
		Long: `Bind a previously executed module candidate and its complete apply journal
to a fresh pcb dump --include-copper. Ground tracks are recovered by exact
geometry, including host-created T-junction splits; vias, regions and pours
must retain their captured identity and exact geometry. The output can be used
as modules[].existingObjects. Shared net names never grant ownership.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			for name, value := range map[string]string{"candidate": candidatePath, "journal": journalPath, "board": boardPath, "out": outPath} {
				if value == "" {
					return fmt.Errorf("--%s is required", name)
				}
			}
			if outPath == candidatePath || outPath == journalPath || outPath == boardPath {
				return fmt.Errorf("output must not overwrite an input file")
			}
			candidateRaw, err := os.ReadFile(candidatePath)
			if err != nil {
				return err
			}
			journalRaw, err := os.ReadFile(journalPath)
			if err != nil {
				return err
			}
			boardRaw, err := os.ReadFile(boardPath)
			if err != nil {
				return err
			}
			candidate, err := loadPCBLayoutCandidate(candidatePath)
			if err != nil {
				return err
			}
			board, err := loadBoardSnapshotPath(boardPath)
			if err != nil {
				return err
			}
			owned, err := recoverPCBModuleOwned(candidate, journalRaw, board, sha256String(candidateRaw), sha256String(journalRaw), sha256String(boardRaw))
			if err != nil {
				return err
			}
			raw, err := json.MarshalIndent(owned, "", "  ")
			if err != nil {
				return err
			}
			if err := writeAtomic(outPath, append(raw, '\n')); err != nil {
				return err
			}
			fmt.Fprintf(stderr, "module-owned: %d exact object(s) → %s\n", len(owned), outPath)
			_, err = fmt.Fprintln(stdout, outPath)
			return err
		},
	}
	c.Flags().StringVar(&candidatePath, "candidate", "", "previously executed module candidate JSON")
	c.Flags().StringVar(&journalPath, "journal", "", "complete successful apply journal JSONL for that candidate")
	c.Flags().StringVar(&boardPath, "board", "", "fresh pcb dump --include-copper JSON")
	c.Flags().StringVar(&outPath, "out", "", "output existingObjects JSON array")
	return c
}

func readModuleOwnedJournal(candidate pcbLayoutCandidate, raw []byte) (map[string]string, error) {
	s := bufio.NewScanner(strings.NewReader(string(raw)))
	header, entries := 0, []journalEntry{}
	for line := 1; s.Scan(); line++ {
		var m map[string]any
		if err := json.Unmarshal(s.Bytes(), &m); err != nil {
			return nil, fmt.Errorf("journal line %d: %w", line, err)
		}
		if _, ok := m["playbookSha256"]; ok {
			header++
			if line != 1 || header != 1 || asString(m["playbookSha256"]) != candidate.ApplySHA256 {
				return nil, fmt.Errorf("journal header does not match candidate applySha256")
			}
			continue
		}
		var e journalEntry
		if err := json.Unmarshal(s.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("journal line %d: %w", line, err)
		}
		entries = append(entries, e)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if header != 1 || len(entries) != len(candidate.Apply.Steps) {
		return nil, fmt.Errorf("journal is incomplete: %d entries for %d steps", len(entries), len(candidate.Apply.Steps))
	}
	out, seen := map[string]string{}, map[string]bool{}
	for i, step := range candidate.Apply.Steps {
		id := step.ID
		if id == "" {
			id = fmt.Sprintf("s%d", i+1)
		}
		e := entries[i]
		if e.Idx != i+1 || e.ID != id || !strings.HasPrefix(e.Status, "ok") {
			return nil, fmt.Errorf("journal step %d does not prove successful %s", i+1, id)
		}
		if len(e.Captured) != len(step.Capture) {
			return nil, fmt.Errorf("journal step %s capture count differs", id)
		}
		for name, selector := range step.Capture {
			pid := e.Captured[name]
			if selector != "$.primitiveId" || pid == "" || seen[pid] {
				return nil, fmt.Errorf("journal step %s has invalid/duplicate primitive capture", id)
			}
			seen[pid] = true
			out[id] = pid
		}
	}
	return out, nil
}

func recoverPCBModuleOwned(candidate pcbLayoutCandidate, journalRaw []byte, board *boardSnapshot, candidateSHA, journalSHA, boardSHA string) ([]pcbModuleOwnedObject, error) {
	if candidate.Bundle == nil || candidate.Bundle.Kind != "crystal-guard" {
		return nil, fmt.Errorf("module-owned currently requires a crystal-guard candidate")
	}
	if board == nil || board.Copper == nil {
		return nil, fmt.Errorf("fresh board has no copper inventory")
	}
	for _, k := range []string{"routing", "vias", "pours", "poured", "regions", "fills"} {
		if board.Copper.Availability[k] != "available" {
			return nil, fmt.Errorf("fresh copper category %s is unknown", k)
		}
	}
	captured, err := readModuleOwnedJournal(candidate, journalRaw)
	if err != nil {
		return nil, err
	}
	tracks, _, _, err := crystalSnapshotRouting(board)
	if err != nil {
		return nil, err
	}
	source := fmt.Sprintf("previousCandidateSha256=%s;journalSha256=%s;freshBoardSha256=%s", candidateSHA, journalSHA, boardSHA)
	used := map[string]string{}
	var owned []pcbModuleOwnedObject
	lineMaps := primitiveMapsByID(board.Copper.Lines)
	for _, route := range candidate.Bundle.GroundRoutes {
		for i := 0; i+1 < len(route.Points); i++ {
			stepID := fmt.Sprintf("route-%s-%02d", route.ID, i+1)
			oldPID, ok := captured[stepID]
			if !ok {
				return nil, fmt.Errorf("journal has no capture for ground route step %s", stepID)
			}
			matches, covered := matchingTrackCoverage(tracks, route.Net, route.Layer, route.WidthMil, route.Points[i], route.Points[i+1])
			if !covered || len(matches) == 0 {
				return nil, fmt.Errorf("fresh tracks do not exactly cover old ground route %s segment %d", route.ID, i+1)
			}
			length := moduleRouteLength(route.Points[i : i+2])
			sum := 0.0
			oldPresent := false
			for _, t := range matches {
				sum += math.Hypot(t.X2-t.X1, t.Y2-t.Y1)
				oldPresent = oldPresent || t.ID == oldPID
			}
			if sum > length+netPathGeomEps {
				return nil, fmt.Errorf("old ground route %s segment %d has overlapping/ambiguous fresh coverage", route.ID, i+1)
			}
			if !oldPresent && (copperPrimitiveIDCount(board, oldPID) != 0 || len(matches) < 2) {
				return nil, fmt.Errorf("captured PID %s changed without an unambiguous split replacement", oldPID)
			}
			for _, t := range matches {
				if prior := used[t.ID]; prior != "" && prior != stepID {
					return nil, fmt.Errorf("fresh track %s belongs to both %s and %s", t.ID, prior, stepID)
				}
				used[t.ID] = stepID
				m, ok := lineMaps[t.ID]
				if !ok {
					return nil, fmt.Errorf("fresh track %s raw object unavailable", t.ID)
				}
				owned = append(owned, pcbModuleOwnedObject{Kind: "track", PrimitiveID: t.ID, Expected: m, Source: source + ";oldStep=" + stepID})
			}
		}
	}
	appendExact := func(kind, stepID string, expected any, list []any) error {
		pid, ok := captured[stepID]
		if !ok {
			return fmt.Errorf("journal has no capture for %s", stepID)
		}
		m, ok := primitiveMapsByID(list)[pid]
		if !ok {
			return fmt.Errorf("fresh %s %s is absent", kind, pid)
		}
		switch e := expected.(type) {
		case pcbModuleVia:
			_, _, vs, parseErr := crystalSnapshotRouting(board)
			if parseErr != nil {
				return parseErr
			}
			matches := 0
			for _, v := range vs {
				if v.ID == pid && moduleViaMatchesExpected(v, e) {
					matches++
				}
			}
			if matches != 1 {
				return fmt.Errorf("fresh via %s differs from candidate", pid)
			}
		case pcbModuleRegion:
			match, matchErr := regionMapMatchesAny(m, []pcbModuleRegion{e})
			if matchErr != nil || !match {
				return fmt.Errorf("fresh region %s differs: %v", pid, matchErr)
			}
		case pcbModulePour:
			match, matchErr := pourMapMatchesAny(m, []pcbModulePour{e})
			if matchErr != nil || !match {
				return fmt.Errorf("fresh pour %s differs: %v", pid, matchErr)
			}
			if _, findErr := findExpectedMaterializedPour(e, board.Copper.Pours, board.Copper.Poured); findErr != nil {
				return findErr
			}
		}
		owned = append(owned, pcbModuleOwnedObject{Kind: kind, PrimitiveID: pid, Expected: m, Source: source + ";oldStep=" + stepID})
		return nil
	}
	for _, v := range candidate.Bundle.Vias {
		if v.ExistingPrimitiveID == "" {
			if err := appendExact("via", "via-"+v.ID, v, board.Copper.Vias); err != nil {
				return nil, err
			}
		}
	}
	for _, r := range candidate.Bundle.Regions {
		if err := appendExact("region", "region-"+r.ID, r, board.Copper.Regions); err != nil {
			return nil, err
		}
	}
	for _, p := range candidate.Bundle.Pours {
		if err := appendExact("pour", "pour-"+p.ID, p, board.Copper.Pours); err != nil {
			return nil, err
		}
	}
	sort.Slice(owned, func(i, j int) bool {
		if owned[i].Kind != owned[j].Kind {
			return owned[i].Kind < owned[j].Kind
		}
		return owned[i].PrimitiveID < owned[j].PrimitiveID
	})
	return owned, nil
}
