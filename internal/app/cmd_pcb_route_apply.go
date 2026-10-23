package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newPcbRouteApplyCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var boardPath, fromPath, planPath, outPath string
	var dryRun bool
	cmd := &cobra.Command{Use: "apply", Short: "Apply a checked single-layer route with fresh copper readback", Args: cobra.NoArgs,
		Long: `Add only the copper in a schemaVersion 1 route solve/check report (mil, y-UP).
Requires explicit --window, --project (UUID), --doc (PCB UUID), --board, --from,
--plan and a NEW --out receipt. Existing tracks may not be split or replaced.
Every write is followed by fresh copper verification. A partial/unknown write
stops the remaining segments without retry or rollback. Native DRC is reported
separately from applied geometry. No explicit save is sent; persistence remains
unverified. --dry-run checks the live target and baseline without writes.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if boardPath == "" || fromPath == "" || planPath == "" || outPath == "" || *window == "" || cfg.project == "" || cfg.doc == "" {
				return fmt.Errorf("route apply requires --board, --from, --plan, --out, --window, --project UUID and --doc UUID")
			}
			if err := pcbRouteOutputSafe(outPath, []string{boardPath, fromPath, planPath}); err != nil {
				return err
			}
			boardRaw, err := os.ReadFile(boardPath)
			if err != nil {
				return err
			}
			requestRaw, err := os.ReadFile(fromPath)
			if err != nil {
				return err
			}
			planRaw, err := os.ReadFile(planPath)
			if err != nil {
				return err
			}
			var before boardSnapshot
			var in pcbRouteRequest
			var plan pcbRouteReport
			if err := decodePCBRouteJSON(boardRaw, &before, false); err != nil {
				return fmt.Errorf("board: %w", err)
			}
			if err := decodePCBRouteJSON(requestRaw, &in, true); err != nil {
				return fmt.Errorf("request: %w", err)
			}
			if err := decodePCBRouteJSON(planRaw, &plan, true); err != nil {
				return fmt.Errorf("plan: %w", err)
			}
			before.sanitizeOutline()
			checked := newPCBRouteReport(boardRaw, requestRaw)
			if err := checkPCBRoute(cmd.Context(), in, &before, plan, &checked); err != nil {
				return fmt.Errorf("route apply check: %w", err)
			}
			receipt := pcbRouteApplyReceipt{SchemaVersion: 1, Status: "not_executed", Target: actionContext{ProjectUUID: cfg.project, DocumentUUID: cfg.doc, DocumentType: "pcb"}, Window: *window,
				BoardSHA256: sha256String(boardRaw), RequestSHA256: sha256String(requestRaw), PlanSHA256: sha256String(planRaw), Candidate: *checked.Candidate, DryRun: dryRun,
				Steps: []pcbRouteApplyStep{}, Remaining: []int{}, Connectivity: map[string]any{"status": "not_checked"}, DRC: map[string]any{"status": "not_checked"}, Persistence: "unverified"}
			for i := 0; i+1 < len(receipt.Candidate.Points); i++ {
				receipt.Remaining = append(receipt.Remaining, i)
			}
			if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return fmt.Errorf("new route receipt: %w", err)
			}
			if err := f.Close(); err != nil {
				return err
			}
			persist := func() error {
				b, err := marshalPCBRouteApplyReceipt(&receipt)
				if err != nil {
					return err
				}
				return writePCBRouteApplyReceipt(outPath, b)
			}
			if err := persist(); err != nil {
				return err
			}
			// Pin the current page; do not let the generic --doc guard navigate.
			localCfg := *cfg
			localCfg.doc = ""
			call := func(action string, payload map[string]any) (*actionResult, error) {
				if payload == nil {
					payload = map[string]any{}
				}
				payload["_target"] = map[string]any{"projectUuid": receipt.Target.ProjectUUID, "documentUuid": receipt.Target.DocumentUUID}
				res, err := requestActionOnce(&localCfg, action, *window, payload, defaultActionTimeout)
				if err != nil {
					return res, err
				}
				if res.Context == nil || res.Context.ProjectUUID != receipt.Target.ProjectUUID || res.Context.DocumentUUID != receipt.Target.DocumentUUID || res.Context.DocumentType != "pcb" {
					return res, fmt.Errorf("route apply target context changed or is unavailable")
				}
				return res, nil
			}
			snapshot := func() (*boardSnapshot, error) {
				return stablePCBRouteApplySnapshot(call, before.Silk != nil)
			}
			release, lockErr := acquirePCBRouteApplyLock(receipt.Target.ProjectUUID, receipt.Target.DocumentUUID)
			if lockErr != nil {
				receipt.Error = lockErr.Error()
				_ = persist()
				err = lockErr
			} else {
				defer release()
				err = executePCBRouteApply(cmd.Context(), in, &before, &receipt, call, snapshot, persist)
			}
			if blob, encodeErr := marshalPCBRouteApplyReceipt(&receipt); encodeErr == nil {
				_, _ = stdout.Write(blob)
			}
			fmt.Fprintf(stderr, "route apply: %s; receipt: %s\n", receipt.Status, outPath)
			return err
		}}
	cmd.Flags().StringVar(&boardPath, "board", "", "source pcb dump --include-copper JSON")
	cmd.Flags().StringVar(&fromPath, "from", "", "independent route request JSON")
	cmd.Flags().StringVar(&planPath, "plan", "", "route solve/check report JSON")
	cmd.Flags().StringVar(&outPath, "out", "", "new route apply receipt JSON; never overwrites an existing file")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "check the fresh target and baseline without writing copper")
	return cmd
}

func writePCBRouteApplyReceipt(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".route-apply-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
