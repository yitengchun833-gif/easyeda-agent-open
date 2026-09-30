package app

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// A pin can already be on USB_DM while its wire exits inward. Net membership
// must not turn a geometry rejection (or an uncertain timeout) into success.
func TestConnectFailureCannotBeAdoptedFromExistingNet(t *testing.T) {
	for _, code := range []string{"SCHEMATIC_GEOMETRY_INVALID", "DISPATCH_FAILED", "EDA_CALL_FAILED"} {
		t.Run(code, func(t *testing.T) {
			failure := fmt.Sprintf(`{"ok":false,"error":{"code":%q,"message":"rejected"}}`, code)
			cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
				switch call.Action {
				case "schematic.components.list":
					return `{"ok":true,"result":{"components":[{"primitiveId":"d1","componentType":"part","designator":"D1","x":210,"y":1060,"pins":[{"pinNumber":"3","pinName":"I/O2","net":"USB_DM","x":165,"y":1050,"rotation":180}]}]}}`
				case "schematic.power.connect_pin":
					return failure
				case "schematic.read":
					return `{"ok":true,"result":{"nets":[{"net":"USB_DM","pins":["D1.3"]}]}}`
				default:
					return `{"ok":true,"result":{}}`
				}
			})
			defer cleanup()
			var out, stderr bytes.Buffer
			cmd := newSchCmd(cfg, &out, &stderr)
			cmd.SetArgs([]string{"connect", "--pin", "D1:3", "--kind", "net_port_bi", "--net", "USB_DM", "--direction", "left", "--offset", "20"})
			err := cmd.Execute()
			var ae *actionError
			if !errors.As(err, &ae) || ae.Code != code {
				t.Fatalf("must preserve %s, got %v; output %s", code, err, out.String())
			}
			if strings.TrimSpace(out.String()) != failure {
				t.Fatalf("must emit only the original failure: %s", out.String())
			}
			actual := calls.snapshot()
			if len(actual) != 2 || actual[1].Action != "schematic.power.connect_pin" {
				t.Fatalf("unexpected retry/adoption or audit success: %+v", actual)
			}
		})
	}
}
