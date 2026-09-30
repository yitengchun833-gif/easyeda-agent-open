package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// No appConfig/window is accepted: this constructor cannot dispatch an editor
// action. Failed searches still write a diagnostic JSON report and preview.
func newPcbEscapePlanCmd(stdout, stderr io.Writer) *cobra.Command {
	var fromPath, boardPath, outPath string
	cmd := &cobra.Command{
		Use:   "escape-plan",
		Short: "Jointly reserve measured pad escape paths offline (no editor writes)",
		Long: `Read schemaVersion 3 layout routing intent and a pcb dump --include-copper.
Account for every pad of the declared owners, including explicit NC reasons;
jointly search straight/45-degree paths using exact pad and copper geometry.
The JSON and adjacent SVG show planning reservations, not newly created copper.
Missing geometry, unavailable transitions or finite-search exhaustion report
incomplete. Pass proves only the declared pad paths/corridors, not whole-board
routing or saved editor state. Re-run after any geometry/rule/intent change.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if fromPath == "" || boardPath == "" || outPath == "" {
				return fmt.Errorf("--from, --board and --out are required")
			}
			preview := strings.TrimSuffix(outPath, filepath.Ext(outPath)) + ".svg"
			if filepath.Clean(preview) == filepath.Clean(outPath) {
				return fmt.Errorf("--out must name a JSON report, not its .svg preview")
			}
			for _, destination := range []string{outPath, preview} {
				for _, inputPath := range []string{fromPath, boardPath} {
					destAbs, _ := filepath.Abs(destination)
					inputAbs, _ := filepath.Abs(inputPath)
					destInfo, destErr := os.Stat(destination)
					inputInfo, inputErr := os.Stat(inputPath)
					if destAbs == inputAbs || destErr == nil && inputErr == nil && os.SameFile(destInfo, inputInfo) {
						return fmt.Errorf("output must not overwrite an input file: %s", inputPath)
					}
				}
			}
			layoutRaw, err := os.ReadFile(fromPath)
			if err != nil {
				return err
			}
			boardRaw, err := os.ReadFile(boardPath)
			if err != nil {
				return err
			}
			// Decode the public routing projection independently so unrelated
			// module strategies do not run merely to inspect escape feasibility.
			var layout struct {
				SchemaVersion      int             `json:"schemaVersion"`
				Units              string          `json:"units"`
				CoordinateSemantic string          `json:"coordinateSemantic"`
				Routing            json.RawMessage `json:"routing"`
			}
			if err := json.Unmarshal(layoutRaw, &layout); err != nil {
				return fmt.Errorf("read layout routing: %w", err)
			}
			if layout.SchemaVersion != 3 || layout.Units != "mil" || layout.CoordinateSemantic != "footprint-anchor" || layout.Routing == nil {
				return fmt.Errorf("escape-plan requires schemaVersion 3, units mil, coordinateSemantic footprint-anchor and routing")
			}
			var routing pcbRoutingIntent
			decoder := json.NewDecoder(bytes.NewReader(layout.Routing))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&routing); err != nil {
				return fmt.Errorf("routing contract: %w", err)
			}
			snap, err := loadBoardSnapshotFile(bytes.NewReader(boardRaw))
			if err != nil {
				return err
			}
			report, planErr := planPCBEscape(routing, snap)
			report.BoardSHA256, report.LayoutSHA256 = sha256String(boardRaw), sha256String(layoutRaw)
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return err
			}
			blob, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				return err
			}
			if err := writeAtomic(outPath, append(blob, '\n')); err != nil {
				return err
			}
			var svg bytes.Buffer
			if snap.Outline != nil {
				candidate := pcbLayoutCandidate{ID: "escape-reservations", Module: "routing", Strategy: "joint-escape", Bundle: &pcbLayoutModuleBundle{Kind: "escape-reservations", SignalRoutes: report.Routes, Vias: report.Vias}}
				if err := renderPCBLayoutCandidateSVG(&svg, snap, candidate, nil); err != nil {
					return err
				}
			} else {
				fmt.Fprintln(&svg, `<svg xmlns="http://www.w3.org/2000/svg" width="640" height="80"><text x="10" y="30">Incomplete: measured board outline unavailable</text></svg>`)
			}
			if err := writeAtomic(preview, svg.Bytes()); err != nil {
				return err
			}
			fmt.Fprintf(stderr, "escape-plan: %s; %d reservations; %d/%d states → %s\n", report.Status, len(report.Routes), report.Budget.Used, report.Budget.MaxStates, outPath)
			fmt.Fprintln(stdout, outPath)
			return planErr
		},
	}
	cmd.Flags().StringVar(&fromPath, "from", "", "schemaVersion 3 layout intent JSON with routing")
	cmd.Flags().StringVar(&boardPath, "board", "", "measured JSON from pcb dump --include-copper")
	cmd.Flags().StringVar(&outPath, "out", "", "report JSON path; adjacent .svg is the planning preview")
	return cmd
}
