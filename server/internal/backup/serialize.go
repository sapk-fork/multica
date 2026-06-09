package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrUnsupportedVersion is returned by Unmarshal when a backup file declares a
// format version this build cannot read. Callers can test for it with
// errors.Is.
var ErrUnsupportedVersion = errors.New("backup: unsupported format version")

// New returns a BackupFile with its metadata initialised for the given source
// workspace. The format version is set to FormatVersion and ExportedAt is set
// to the current UTC time.
func New(workspaceID, workspaceName, workspaceSlug string) *BackupFile {
	return &BackupFile{
		Metadata: BackupMetadata{
			Version:             FormatVersion,
			ExportedAt:          time.Now().UTC(),
			SourceWorkspaceID:   workspaceID,
			SourceWorkspaceName: workspaceName,
			SourceWorkspaceSlug: workspaceSlug,
		},
	}
}

// Marshal serialises a backup file to indented JSON.
func Marshal(backup *BackupFile) ([]byte, error) {
	if backup == nil {
		return nil, fmt.Errorf("backup: cannot marshal nil BackupFile")
	}
	data, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("backup: marshal: %w", err)
	}
	return data, nil
}

// Unmarshal deserialises a backup file from JSON and validates its format
// version. A missing version, or one that does not match FormatVersion, is
// rejected; the version mismatch case wraps ErrUnsupportedVersion.
func Unmarshal(data []byte) (*BackupFile, error) {
	var backup BackupFile
	if err := json.Unmarshal(data, &backup); err != nil {
		return nil, fmt.Errorf("backup: unmarshal: %w", err)
	}
	if backup.Metadata.Version == "" {
		return nil, fmt.Errorf("backup: missing format version")
	}
	if backup.Metadata.Version != FormatVersion {
		return nil, fmt.Errorf("backup: %q (expected %q): %w",
			backup.Metadata.Version, FormatVersion, ErrUnsupportedVersion)
	}
	return &backup, nil
}
