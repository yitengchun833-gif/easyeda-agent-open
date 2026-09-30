package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func layoutEditPair(raw string) (float64, float64, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("coordinate must be X,Y")
	}
	x, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid X: %w", err)
	}
	y, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid Y: %w", err)
	}
	if !plGrid(x) || !plGrid(y) {
		return 0, 0, fmt.Errorf("coordinates must be finite on the 5-raw grid")
	}
	return x, y, nil
}

func layoutEditPinRef(raw string) (string, string, error) {
	id, pin, ok := strings.Cut(raw, ":")
	if !ok || strings.TrimSpace(id) == "" || strings.TrimSpace(pin) == "" {
		return "", "", fmt.Errorf("repair pin must be STABLE_COMPONENT_ID:PIN")
	}
	return strings.TrimSpace(id), strings.TrimSpace(pin), nil
}

func fileSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func layoutEditDistinct(paths ...string) error {
	seen := map[string]string{}
	for _, path := range paths {
		if path == "" {
			continue
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if previous := seen[absolute]; previous != "" {
			return fmt.Errorf("%s and %s must be different files", previous, path)
		}
		seen[absolute] = path
	}
	return nil
}

func writeLayoutEditJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0644)
}

func newSchLayoutEditCmd(stdout io.Writer) *cobra.Command {
	var sourcePath, pagePath, snapshotPath, moveCore, target, repairPin string
	var outPath, reportPath, playbookPath string
	var offset float64
	cmd := &cobra.Command{
		Use:   "layout-edit",
		Short: "Plan a core-relative move or protected pin-marker repair from retained data",
		Long: `Read an owned zones source, one selected layout page and a fresh components.list
snapshot. Exactly one edit is required:
  --move-core <stable-id> --to X,Y
  --repair-pin <stable-id>:<pin> [--offset 20]

The command is offline and never changes EasyEDA. Core moves preserve the whole
zone first; on collision they fix the requested core coordinate and select a
newly solved legal zone shape. Output is a fixed-flow selected page accepted by
compose --layout-page. Pin repair searches only along the official outward pin
axis and emits a protected schematic.pin.repair_marker playbook when --playbook
is supplied. --report records hashes, selected strategy and relative changes.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sourcePath == "" || pagePath == "" || snapshotPath == "" || outPath == "" {
				return fmt.Errorf("--source, --page, --snapshot and --out are required")
			}
			if (moveCore == "") == (repairPin == "") {
				return fmt.Errorf("choose exactly one of --move-core or --repair-pin")
			}
			if moveCore != "" && target == "" {
				return fmt.Errorf("--move-core requires --to X,Y")
			}
			if repairPin != "" && target != "" {
				return fmt.Errorf("--to only applies to --move-core")
			}
			if err := layoutEditDistinct(sourcePath, pagePath, snapshotPath, outPath, reportPath, playbookPath); err != nil {
				return err
			}

			sourceRaw, err := os.ReadFile(sourcePath)
			if err != nil {
				return err
			}
			source, err := decodeSchematicZonesInput(sourceRaw)
			if err != nil {
				return fmt.Errorf("source: %w", err)
			}
			pageRaw, err := os.ReadFile(pagePath)
			if err != nil {
				return err
			}
			page, err := decodeSchCompositionLayoutPage(pageRaw)
			if err != nil {
				return fmt.Errorf("page: %w", err)
			}
			snapshotRaw, err := os.ReadFile(snapshotPath)
			if err != nil {
				return err
			}
			snapshot, err := decodeSchematicLayoutEditSnapshot(snapshotRaw)
			if err != nil {
				return fmt.Errorf("snapshot: %w", err)
			}

			report := &SchematicLayoutEditReport{SchemaVersion: 1, Status: "planned", SourceSHA256: fileSHA256(sourceRaw), PageSHA256: fileSHA256(pageRaw), SnapshotSHA256: fileSHA256(snapshotRaw)}
			if moveCore != "" {
				x, y, err := layoutEditPair(target)
				if err != nil {
					return err
				}
				report.Operation = "move_core"
				result, err := planSchematicCoreMove(source, *page, snapshot, moveCore, x, y, report)
				if err != nil {
					report.Status = "blocked"
					report.Error = err.Error()
					_ = writeOptionalLayoutEditReport(reportPath, report)
					return err
				}
				if err := writeLayoutEditJSON(outPath, result); err != nil {
					return err
				}
			} else {
				componentID, pin, err := layoutEditPinRef(repairPin)
				if err != nil {
					return err
				}
				report.Operation = "repair_pin_marker"
				result, playbook, err := planSchematicPinMarkerRepair(source, *page, snapshot, componentID, pin, offset, report)
				if err != nil {
					report.Status = "blocked"
					report.Error = err.Error()
					_ = writeOptionalLayoutEditReport(reportPath, report)
					return err
				}
				if err := writeLayoutEditJSON(outPath, result); err != nil {
					return err
				}
				if playbookPath != "" {
					if err := writeLayoutEditJSON(playbookPath, playbook); err != nil {
						return err
					}
				}
			}
			if err := writeOptionalLayoutEditReport(reportPath, report); err != nil {
				return err
			}
			if reportPath == "" {
				return json.NewEncoder(stdout).Encode(report)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&sourcePath, "source", "", "owned zones source JSON used to generate the page")
	cmd.Flags().StringVar(&pagePath, "page", "", "selected complete layout page JSON")
	cmd.Flags().StringVar(&snapshotPath, "snapshot", "", "fresh sch list --include-pins --include-bbox --include-wires JSON")
	cmd.Flags().StringVar(&moveCore, "move-core", "", "stable core component ID to move")
	cmd.Flags().StringVar(&target, "to", "", "new core page coordinate X,Y")
	cmd.Flags().StringVar(&repairPin, "repair-pin", "", "pin marker branch as STABLE_COMPONENT_ID:PIN")
	cmd.Flags().Float64Var(&offset, "offset", 20, "preferred pin-marker stub length; outward alternatives remain 10..80 raw")
	cmd.Flags().StringVar(&outPath, "out", "", "target page (move) or repair target JSON")
	cmd.Flags().StringVar(&reportPath, "report", "", "machine-readable edit report")
	cmd.Flags().StringVar(&playbookPath, "playbook", "", "protected repair playbook output (repair-pin only)")
	return cmd
}

func writeOptionalLayoutEditReport(path string, report *SchematicLayoutEditReport) error {
	if path == "" {
		return nil
	}
	return writeLayoutEditJSON(path, report)
}
