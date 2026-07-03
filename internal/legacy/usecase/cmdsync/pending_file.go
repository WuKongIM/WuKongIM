package cmdsync

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const pendingConversationUpdatesFile = "cmd_conversation_updates.json"

func (u *ConversationUpdater) pendingFilePath() string {
	if u == nil || u.dataDir == "" {
		return ""
	}
	return filepath.Join(u.dataDir, "conversationv2", pendingConversationUpdatesFile)
}

func (u *ConversationUpdater) savePendingFile() error {
	path := u.pendingFilePath()
	if path == "" {
		return nil
	}
	updates := u.snapshotUpdates()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if len(updates) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}

	tmpPath := path + ".tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	encodeErr := json.NewEncoder(file).Encode(updates)
	closeErr := file.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmpPath, path)
}

func (u *ConversationUpdater) loadPendingFile() error {
	path := u.pendingFilePath()
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()

	var updates []PendingConversationUpdate
	if err := json.NewDecoder(file).Decode(&updates); err != nil {
		if errors.Is(err, io.EOF) {
			_ = os.Remove(path)
			return nil
		}
		badPath := path + ".bad"
		_ = os.Remove(badPath)
		if renameErr := os.Rename(path, badPath); renameErr != nil && u.logger != nil {
			u.logger.Warn("rename bad pending CMD conversation updates file failed")
		}
		if u.logger != nil {
			u.logger.Warn("ignore bad pending CMD conversation updates file")
		}
		return nil
	}
	if len(updates) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	for _, update := range updates {
		u.putLoadedUpdate(update)
	}
	u.markRestoredPendingFileDirty()
	return nil
}
