package daemon

import (
	"encoding/json"
	"net/http"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// Project at the HTTP boundary, after runtime ingestion and audit. Raw CLI
// clients retain the original response. Never mutate shared recovery evidence.
func writeActionResponse(w http.ResponseWriter, r *http.Request, status int, value any) {
	if r.Header.Get("X-Easyeda-Response") != "public-v1" {
		writeJSON(w, status, value)
		return
	}
	var response protocol.Response
	switch v := value.(type) {
	case protocol.Response:
		response = v
	case *protocol.Response:
		response = *v
	default:
		writeJSON(w, status, value)
		return
	}
	if response.Result != nil {
		response.Result = publicActionValue(response.Result).(map[string]any)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Easyeda-Response", "public-v1")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func publicActionValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			switch key {
			case "_baseline", "_readback", "_resumeBaseline", "_netIndex", "_scene":
				continue
			case "otherProperty":
				out[key] = item // Opaque user metadata, even if a key matches ours.
			default:
				out[key] = publicActionValue(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = publicActionValue(item)
		}
		return out
	default:
		return value
	}
}
