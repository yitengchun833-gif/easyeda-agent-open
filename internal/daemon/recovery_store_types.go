package daemon

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

var (
	ErrRecoveryConflict  = errors.New("recovery revision changed")
	ErrRecoveryDuplicate = errors.New("recovery request already exists")
	ErrRecoveryCorrupt   = errors.New("recovery evidence is corrupt or unsupported")
)

type recoveryStorage interface {
	SaveScope(*recoveryScope) error
	LoadScope(string) (*recoveryScope, error)
	ListIndex() ([]recoveryIndexEntry, error)
	HasRequest(string) (bool, error)
	SaveAttempt(*recoveryAttempt) error
	LoadAttempt(string) (*recoveryAttempt, error)
	LoadAttemptEvidence(string) (*recoveryAttempt, string, error)
	LoadLegacyAudit(string, string) (json.RawMessage, error)
	SaveDisposition(*recoveryScope, *recoveryDisposition) error
	LoadDisposition(string, string, uint64) (*recoveryDisposition, error)
	ListUnresolved() ([]recoveryAttempt, error)
	ImportLegacy(string) (recoveryMigrationReport, error)
	Identity() (string, error)
	Close() error
}

type recoveryScope struct {
	Key             string                      `json:"key"`
	Revision        uint64                      `json:"revision"`
	Document        *runtimeDocument            `json:"document"`
	Receipts        []*runtimeReceipt           `json:"receipts"`
	Requests        map[string]protocol.Request `json:"requests"`
	Baselines       map[string]any              `json:"baselines"`
	EvidenceUnknown bool                        `json:"evidenceUnknown,omitempty"`
}

type recoveryAttemptState string

const (
	recoveryAttemptPending       recoveryAttemptState = "pending"
	recoveryAttemptDispatched    recoveryAttemptState = "dispatched"
	recoveryAttemptSettled       recoveryAttemptState = "settled"
	recoveryAttemptUnknown       recoveryAttemptState = "unknown"
	recoveryAttemptNotDispatched recoveryAttemptState = "not_dispatched"
)

type recoveryAttempt struct {
	RequestID            string               `json:"requestId"`
	ScopeKey             string               `json:"scopeKey,omitempty"`
	Revision             uint64               `json:"revision"`
	Request              protocol.Request     `json:"request"`
	Baseline             any                  `json:"baseline,omitempty"`
	ContinuationBaseline any                  `json:"continuationBaseline,omitempty"`
	Receipt              *runtimeReceipt      `json:"receipt,omitempty"`
	State                recoveryAttemptState `json:"state"`
	WindowID             string               `json:"windowId,omitempty"`
	Context              protocol.Context     `json:"context"`
	ConnectionEpoch      string               `json:"connectionEpoch,omitempty"`
	TaskID               string               `json:"taskId,omitempty"`
	TaskRevision         uint64               `json:"taskRevision,omitempty"`
	ContentWrite         bool                 `json:"contentWrite,omitempty"`
	Dangerous            bool                 `json:"dangerous,omitempty"`
	ProjectWide          bool                 `json:"projectWide,omitempty"`
	Evidence             any                  `json:"evidence,omitempty"`
	ExecutionKnown       bool                 `json:"executionKnown"`
	Executed             bool                 `json:"executed"`
	EvidenceUnknown      bool                 `json:"evidenceUnknown,omitempty"`
	UpdatedAt            time.Time            `json:"updatedAt"`
}

type recoveryIndexEntry struct {
	Key              string           `json:"key"`
	Revision         uint64           `json:"revision"`
	WindowID         string           `json:"windowId"`
	Context          protocol.Context `json:"context"`
	Task             *runtimeTask     `json:"task,omitempty"`
	Generation       uint64           `json:"generation"`
	ChangesSinceSave bool             `json:"mutationAttemptedSinceConfirmedSave"`
	SceneInstance    string           `json:"sceneInstance,omitempty"`
	ConnectionEpoch  string           `json:"connectionEpoch,omitempty"`
	ObjectCount      int              `json:"objectCount"`
	EvidenceUnknown  bool             `json:"evidenceUnknown,omitempty"`
}

type recoveryMigrationReport struct {
	SourceSHA256    string `json:"sourceSha256"`
	AlreadyImported bool   `json:"alreadyImported"`
	Scopes          int    `json:"scopes"`
	Receipts        int    `json:"receipts"`
	AuditRecords    int    `json:"auditRecords"`
}
