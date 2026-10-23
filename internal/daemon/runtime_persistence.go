package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The marker identifies a storage generation, never an execution permission.
// A missing/replaced database must not silently become an empty recovery log.
type recoveryStorageMarker struct {
	Version  int    `json:"version"`
	Database string `json:"database"`
	Identity string `json:"identity"`
}

func (s *runtimeStore) initializeRecovery(databasePath, legacyPath string) error {
	s.dirty = make(chan struct{}, 1)
	s.checkpointPath = databasePath
	markerPath := databasePath + ".identity.json"
	var marker recoveryStorageMarker
	if databasePath != ":memory:" {
		data, err := os.ReadFile(markerPath)
		if err == nil {
			if json.Unmarshal(data, &marker) != nil || marker.Version != 1 || marker.Database != filepath.Base(databasePath) || marker.Identity == "" {
				return s.failRecovery(fmt.Errorf("recovery storage identity marker is invalid"))
			}
			if _, err := os.Stat(databasePath); err != nil {
				return s.failRecovery(fmt.Errorf("required recovery database unavailable: %w", err))
			}
		} else if !os.IsNotExist(err) {
			return s.failRecovery(fmt.Errorf("read recovery storage identity: %w", err))
		}
	}
	store, err := openRecoveryStore(databasePath)
	if err != nil {
		return s.failRecovery(err)
	}
	s.recovery = store
	identity, err := store.Identity()
	if err != nil {
		return s.failRecovery(err)
	}
	if marker.Identity != "" && identity != marker.Identity {
		return s.failRecovery(fmt.Errorf("recovery database identity changed; existing evidence was not replaced"))
	}
	if legacyPath != "" {
		if _, err := os.Stat(legacyPath); err == nil {
			if _, err := store.ImportLegacy(legacyPath); err != nil {
				return s.failRecovery(fmt.Errorf("legacy recovery import failed: %w", err))
			}
		} else if os.IsNotExist(err) {
			if _, err := store.ImportAuditDir(filepath.Dir(legacyPath)); err != nil {
				return s.failRecovery(fmt.Errorf("legacy audit import failed: %w", err))
			}
		} else {
			return s.failRecovery(fmt.Errorf("read legacy recovery input: %w", err))
		}
	}
	if databasePath != ":memory:" && marker.Identity == "" {
		marker = recoveryStorageMarker{1, filepath.Base(databasePath), identity}
		if err := writeRecoveryStorageMarker(markerPath, marker); err != nil {
			return s.failRecovery(err)
		}
	}
	index, err := store.ListIndex()
	if err != nil {
		return s.failRecovery(err)
	}
	if s.scopeVersions == nil {
		s.scopeVersions = make(map[string]uint64)
	}
	for _, item := range index {
		s.scopeVersions[item.Key] = item.Revision
	}
	// Persistent history stays in the store. Only a relevant native observation
	// may lazily load its task; old objects and queues are not active resources.
	return nil
}

func (s *runtimeStore) failRecovery(err error) error {
	s.recoveryError = err
	s.checkpointError = err.Error()
	return err
}

func writeRecoveryStorageMarker(path string, marker recoveryStorageMarker) error {
	data, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".recovery-identity-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("persist recovery identity: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close recovery identity: %w", closeErr)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace recovery identity: %w", err)
	}
	return nil
}

func (s *runtimeStore) flushRecoveryScopes() error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recoveryError != nil {
		return s.recoveryError
	}
	for _, document := range s.documents {
		if err := s.saveScopeLocked(document); err != nil {
			return s.failRecovery(err)
		}
	}
	now := time.Now().UTC()
	s.checkpointAt, s.checkpointError = &now, ""
	return nil
}

func (s *runtimeStore) closeRecovery() error {
	if s == nil || s.recovery == nil {
		return nil
	}
	if err := s.flushRecoveryScopes(); err != nil {
		_ = s.recovery.Close()
		return err
	}
	return s.recovery.Close()
}
