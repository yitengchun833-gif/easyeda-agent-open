package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func TestKnowledgeRegistration(t *testing.T) {
	for _, tc := range []struct {
		name, origin  string
		request, fail bool
	}{
		{"ready", "https://pro.lceda.cn", true, false},
		{"untrusted", "https://example.com", true, false},
		{"originless", "", true, false},
		{"failed", "https://pro.lceda.cn", true, true},
		{"legacy", "https://pro.lceda.cn", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port := freePort(t)
			srv := New(Options{Host: "127.0.0.1", PortStart: port, PortEnd: port, AuditDir: t.TempDir()})
			var calls atomic.Int32
			secret := strings.Repeat("private", 10)
			srv.knowledgeConnect = func(ctx context.Context) (knowledgeConnection, error) {
				calls.Add(1)
				if tc.fail {
					return knowledgeConnection{}, fmt.Errorf("failure contains %s", secret)
				}
				return knowledgeConnection{BaseURL: "http://127.0.0.1:60933", Token: secret}, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			var log bytes.Buffer
			done := make(chan error, 1)
			go func() { done <- srv.Run(ctx, &log) }()
			base := fmt.Sprintf("127.0.0.1:%d", port)
			waitForHealth(t, "http://"+base+"/health")
			c, _, err := websocket.Dial(ctx, "ws://"+base+"/eda", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{tc.origin}}})
			if tc.name == "untrusted" {
				cancel()
				<-done
				if err == nil {
					c.CloseNow()
					t.Fatal("foreign origin accepted")
				}
				if calls.Load() != 0 {
					t.Fatal("untrusted origin started service")
				}
				return
			}
			if err != nil {
				cancel()
				<-done
				t.Fatal(err)
			}
			defer func() {
				c.CloseNow()
				cancel()
				<-done
				if strings.Contains(log.String(), secret) {
					t.Error("credential leaked to log")
				}
			}()
			var frame map[string]any
			if err = wsjson.Read(ctx, c, &frame); err != nil {
				t.Fatal(err)
			}
			if err = wsjson.Write(ctx, c, protocol.Register{Type: "register", WindowID: "kb-window", Knowledge: tc.request}); err != nil {
				t.Fatal(err)
			}
			if tc.request {
				if err = wsjson.Read(ctx, c, &frame); err != nil {
					t.Fatal(err)
				}
				if frame["type"] != "knowledge_status" || frame["windowId"] != "kb-window" {
					t.Fatalf("invalid frame: %v", frame["type"])
				}
				wantReady := tc.origin == "https://pro.lceda.cn" && !tc.fail
				if frame["ok"] != wantReady {
					t.Fatalf("ready=%v", frame["ok"])
				}
				if !wantReady && frame["token"] != nil {
					t.Fatal("failure disclosed token")
				}
				if wantReady && frame["token"] != secret {
					t.Fatal("missing private configuration")
				}
			}
			if err = wsjson.Write(ctx, c, protocol.Ping{Type: "ping", ID: "probe"}); err != nil {
				t.Fatal(err)
			}
			if err = wsjson.Read(ctx, c, &frame); err != nil {
				t.Fatal(err)
			}
			if frame["type"] != "pong" {
				t.Fatal("legacy or failed registration disrupted drawing transport")
			}
			h := getHealth(t, base)
			public, _ := json.Marshal(h)
			if strings.Contains(string(public), secret) {
				t.Fatal("credential leaked to health")
			}
			wantCalls := int32(0)
			if tc.request && tc.origin == "https://pro.lceda.cn" {
				wantCalls = 1
			}
			if calls.Load() != wantCalls {
				t.Fatalf("starter calls=%d", calls.Load())
			}
		})
	}
}

func TestKnowledgeRootMatchesClient(t *testing.T) {
	project := filepath.Join(t.TempDir(), "project")
	pack := filepath.Join(project, "easyeda-agent-open")
	for _, root := range []string{pack, filepath.Join(pack, "source")} {
		if got := defaultKnowledgeRoot(root); got != filepath.Join(project, "knowledge") {
			t.Fatalf("wrong root %s", got)
		}
	}
}
