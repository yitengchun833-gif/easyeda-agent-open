package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

func newPcbPlaceBatchCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var from string
	cmd := &cobra.Command{
		Use:   "place-batch",
		Short: "Apply explicit absolute component positions and optional rotations in one request",
		Long: `Read {"placements":[{"primitiveId":"...","x":100,"y":200,"rotation":90}]}.
Coordinates are mil and rotation is degrees. Optional expected source fields
(x/y/rotation/layer/uniqueId/designator) refuse stale targets. Only named components
are modified; locked objects are skipped. Each pose is freshly verified. Partial
or unknown writes stop the remaining work without replay. Existing copper is not
rerouted, no save is sent, and DRC is not a drawing prerequisite.`,
		Args: cobra.NoArgs,
		Example: `  easyeda pcb place-batch --from placements.json --window WINDOW --doc PCB_UUID
  easyeda call pcb.components.place --payload '{"placements":[{"primitiveId":"id1","x":300,"y":500}]}'`,
		RunE: func(_ *cobra.Command, _ []string) error {
			if from == "" {
				return fmt.Errorf("--from is required")
			}
			raw, err := os.ReadFile(from)
			if err != nil {
				return err
			}
			var payload map[string]any
			if err := json.Unmarshal(raw, &payload); err != nil {
				return fmt.Errorf("invalid placement JSON: %w", err)
			}
			placements, ok := payload["placements"].([]any)
			if !ok || len(placements) == 0 {
				return fmt.Errorf("placements must be a nonempty array")
			}
			return dispatch(cfg, "pcb.components.place", *window, payload, stdout, stderr)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "JSON object with explicit placements (required)")
	return cmd
}
