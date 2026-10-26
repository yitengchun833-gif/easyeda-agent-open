package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newPcbRouteReplaceCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var boardPath, fromPath, planPath, idsText, outPath string
	var dryRun bool
	cmd := &cobra.Command{Use: "replace", Short: "Replace an explicitly selected independent single-layer track chain",
		Long: `Preview first with --dry-run --ids: produces a JSON remove/add diff and SVG
using only the supplied board/request. Then execute that --plan with a NEW --out
and explicit --window. Both require --project UUID and --doc PCB_UUID. No save,
automatic restore or replay. Only independent pad-to-pad same-net TOP/BOTTOM
track chains are supported; branches, touching vias/third pads, locked/embedded
tracks, same-net areas and layer transitions are refused before deletion.
Native DRC is reported by the additive executor, never a drawing prerequisite.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if boardPath == "" || fromPath == "" || outPath == "" || cfg.project == "" || cfg.doc == "" {
				return fmt.Errorf("replace requires --board, --from, --out, --project UUID and --doc PCB_UUID")
			}
			if dryRun && (idsText == "" || planPath != "") || !dryRun && (planPath == "" || idsText != "" || *window == "") {
				return fmt.Errorf("preview requires --dry-run --ids; execution requires --plan --window and no --ids")
			}
			inputs := []string{boardPath, fromPath}
			if planPath != "" {
				inputs = append(inputs, planPath)
			}
			if err := pcbRouteOutputSafe(outPath, inputs); err != nil {
				return err
			}
			braw, err := os.ReadFile(boardPath)
			if err != nil {
				return err
			}
			rraw, err := os.ReadFile(fromPath)
			if err != nil {
				return err
			}
			var before boardSnapshot
			var in pcbRouteRequest
			if err := decodePCBRouteJSON(braw, &before, false); err != nil {
				return err
			}
			before.sanitizeOutline()
			if err := decodePCBRouteJSON(rraw, &in, true); err != nil {
				return err
			}
			var preview pcbRouteReplaceReceipt
			var planRaw []byte
			var ids []string
			if dryRun {
				ids, err = parseIDList(idsText)
				if err != nil {
					return err
				}
			} else {
				planRaw, err = os.ReadFile(planPath)
				if err != nil {
					return err
				}
				if err := decodePCBRouteJSON(planRaw, &preview, true); err != nil {
					return err
				}
				if preview.SchemaVersion != 1 || preview.Status != "preview" || !preview.DryRun || preview.DeleteAttempted || preview.Partial || preview.Addition != nil || len(preview.DeletedIDs) > 0 || preview.Target.ProjectUUID != cfg.project || preview.Target.DocumentUUID != cfg.doc || preview.Target.DocumentType != "pcb" || preview.BoardSHA256 != sha256String(braw) || preview.RequestSHA256 != sha256String(rraw) {
					return fmt.Errorf("replacement preview is not a fresh unexecuted plan for these inputs/target")
				}
				for _, m := range preview.Remove {
					ids = append(ids, asString(m["primitiveId"]))
				}
			}
			projected, removed, err := preparePCBRouteReplacement(&before, in, ids)
			if err != nil {
				return fmt.Errorf("replacement boundary: %w", err)
			}
			projectedRaw, err := json.Marshal(projected)
			if err != nil {
				return err
			}
			route := newPCBRouteReport(projectedRaw, rraw)
			var solveErr error
			if dryRun {
				solveErr = solvePCBRoute(cmd.Context(), in, projected, &route)
				if solveErr == nil {
					solveErr = routeApplyNoExistingSplit(projected, *route.Candidate)
				}
			} else {
				if canonicalJSON(removed) != canonicalJSON(preview.Remove) {
					return fmt.Errorf("preview removal facts differ from the board")
				}
				if err := checkPCBRoute(cmd.Context(), in, projected, preview.Route, &route); err != nil {
					return fmt.Errorf("replacement candidate: %w", err)
				}
				if err := routeApplyNoExistingSplit(projected, *route.Candidate); err != nil {
					return err
				}
			}
			r := pcbRouteReplaceReceipt{SchemaVersion: 1, Status: "not_executed", DryRun: dryRun, Target: actionContext{ProjectUUID: cfg.project, DocumentUUID: cfg.doc, DocumentType: "pcb"}, BoardSHA256: sha256String(braw), RequestSHA256: sha256String(rraw), Remove: removed, Route: route, DeletedIDs: []string{}, RemainingIDs: append([]string(nil), ids...), Persistence: "unverified"}
			if len(planRaw) > 0 {
				r.PlanSHA256 = sha256String(planRaw)
			}
			var svg bytes.Buffer
			if dryRun {
				r.Status = "preview"
				if solveErr != nil {
					r.Status = "incomplete"
					r.Error = solveErr.Error()
				}
				r.Route.Preview = pcbRoutePreviewPath(outPath)
				if r.Route.Preview == outPath {
					return fmt.Errorf("--out must name a JSON preview, not an SVG")
				}
				if err := pcbRouteOutputSafe(r.Route.Preview, append(inputs, outPath)); err != nil {
					return err
				}
				if err := renderPCBRouteSVG(&svg, projected, in, r.Route); err != nil {
					return err
				}
			}
			if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
				return err
			}
			if dryRun {
				if _, statErr := os.Lstat(outPath); statErr == nil {
					return fmt.Errorf("preview already exists: %s", outPath)
				} else if !os.IsNotExist(statErr) {
					return statErr
				}
				// The JSON must never claim a reviewable preview while its SVG is
				// missing or belongs to an earlier candidate.
				f, err := os.OpenFile(r.Route.Preview, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if err != nil {
					return err
				}
				_, err = f.Write(svg.Bytes())
				closeErr := f.Close()
				if err != nil {
					return err
				}
				if closeErr != nil {
					return closeErr
				}
			}
			file, err := os.OpenFile(outPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
			persist := func() error {
				data, err := marshalPCBRouteReplacement(&r)
				if err != nil {
					return err
				}
				return writePCBRouteApplyReceipt(outPath, data)
			}
			if err := persist(); err != nil {
				return err
			}
			if !dryRun {
				local := *cfg
				local.doc = "" // Never navigate an implicitly selected page.
				call := func(action string, payload map[string]any) (*actionResult, error) {
					if payload == nil {
						payload = map[string]any{}
					}
					payload["_target"] = map[string]any{"projectUuid": r.Target.ProjectUUID, "documentUuid": r.Target.DocumentUUID}
					res, err := requestActionOnce(&local, action, *window, payload, defaultActionTimeout)
					if err != nil {
						return res, err
					}
					if res == nil || res.Context == nil || res.Context.ProjectUUID != r.Target.ProjectUUID || res.Context.DocumentUUID != r.Target.DocumentUUID || res.Context.DocumentType != "pcb" {
						return res, fmt.Errorf("replacement target context changed or is unavailable")
					}
					return res, nil
				}
				release, lockErr := acquirePCBRouteApplyLock(cfg.project, cfg.doc)
				if lockErr != nil {
					r.Error = lockErr.Error()
					_ = persist()
					err = lockErr
				} else {
					defer release()
					err = executePCBRouteReplacement(cmd.Context(), in, &before, projected, &r, call, func() (*boardSnapshot, error) { return stablePCBRouteApplySnapshot(call, before.Silk != nil) }, persist)
				}
			}
			if data, e := marshalPCBRouteReplacement(&r); e == nil {
				_, _ = stdout.Write(data)
			}
			fmt.Fprintf(stderr, "route replace: %s; receipt: %s\n", r.Status, outPath)
			if solveErr != nil {
				return solveErr
			}
			return err
		}}
	cmd.Flags().StringVar(&boardPath, "board", "", "source pcb dump --include-copper JSON")
	cmd.Flags().StringVar(&fromPath, "from", "", "independent route request JSON (pad endpoints/net/layer/width, mil)")
	cmd.Flags().StringVar(&idsText, "ids", "", "explicit selected track IDs, CSV (preview only)")
	cmd.Flags().StringVar(&planPath, "plan", "", "reviewed replacement preview JSON (execution only)")
	cmd.Flags().StringVar(&outPath, "out", "", "new preview/receipt JSON; never overwrites")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "offline remove/add diff and SVG; no EDA calls")
	return cmd
}
