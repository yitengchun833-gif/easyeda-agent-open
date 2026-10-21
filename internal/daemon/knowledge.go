package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type knowledgeConnection struct {
	BaseURL string `json:"baseUrl,omitempty"`
	Token   string `json:"token,omitempty"`
}

func defaultKnowledgeRoot(packageRoot string) string {
	projectRoot := filepath.Dir(packageRoot)
	if filepath.Base(packageRoot) == "source" {
		projectRoot = filepath.Dir(projectRoot)
	}
	return filepath.Join(projectRoot, "knowledge")
}

func connectLocalKnowledge(ctx context.Context) (knowledgeConnection, error) {
	var result knowledgeConnection
	executable, err := os.Executable()
	if err != nil {
		return result, err
	}
	packageRoot := filepath.Dir(filepath.Dir(executable))
	root := os.Getenv("EASYEDA_KNOWLEDGE_ROOT")
	if root == "" {
		root = defaultKnowledgeRoot(packageRoot)
	}
	node := os.Getenv("EASYEDA_NODE_BIN")
	if node == "" {
		stored, _ := os.ReadFile(filepath.Join(root, ".runtime", "node-path"))
		node = strings.TrimSpace(string(stored))
	}
	if node == "" {
		node, err = exec.LookPath("node")
	}
	if err != nil || node == "" {
		return result, fmt.Errorf("Node runtime unavailable")
	}
	cmd := exec.CommandContext(ctx, node, filepath.Join(packageRoot, "knowledge", "src", "connect.mjs"))
	cmd.Env = append(os.Environ(), "EASYEDA_KNOWLEDGE_ROOT="+root)
	hideKnowledgeWindow(cmd)
	data, err := cmd.Output()
	if err != nil {
		return result, fmt.Errorf("knowledge startup failed; inspect knowledge-stderr.log")
	}
	if json.Unmarshal(data, &result) != nil {
		return result, fmt.Errorf("invalid knowledge connection result")
	}
	u, err := url.Parse(result.BaseURL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || len(result.Token) < 32 {
		return knowledgeConnection{}, fmt.Errorf("invalid local knowledge identity")
	}
	return result, nil
}

func (s *Server) sendKnowledgeConnection(ctx context.Context, c *conn, windowID string) {
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	go func() {
		select {
		case <-c.done:
			cancel()
		case <-bounded.Done():
		}
	}()
	frame := map[string]any{"type": "knowledge_status", "windowId": windowID, "ok": false}
	if !c.knowledgeAllowed {
		frame["error"] = "知识库连接仅支持本机嘉立创编辑器。"
	} else {
		// ponytail: serialize the short bootstrap only; reuse the client's cross-process startup lock.
		s.knowledgeMu.Lock()
		if bounded.Err() != nil {
			s.knowledgeMu.Unlock()
			return
		}
		connect := s.knowledgeConnect
		if connect == nil {
			connect = connectLocalKnowledge
		}
		result, err := connect(bounded)
		s.knowledgeMu.Unlock()
		if err != nil {
			frame["error"] = "画图已连接，知识库启动失败。请检查知识库日志后重连。"
			s.logf("knowledge connection failed") // Never log credentials or child output.
		} else {
			frame["ok"], frame["baseUrl"], frame["token"] = true, result.BaseURL, result.Token
		}
	}
	if c.id() != windowID {
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = c.write(writeCtx, frame)
}
