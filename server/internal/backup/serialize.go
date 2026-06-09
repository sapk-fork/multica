package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrUnsupportedVersion is returned by Unmarshal when a backup file declares a
// format version this build cannot read. Callers can test for it with
// errors.Is.
var ErrUnsupportedVersion = errors.New("backup: unsupported format version")

// New returns a BackupFile with its metadata initialised. The format version
// is set to FormatVersion and ExportedAt is set to the current UTC time.
func New() *BackupFile {
	return &BackupFile{
		Metadata: BackupMetadata{
			Version:    FormatVersion,
			ExportedAt: time.Now().UTC(),
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
// version. A missing version is rejected. The major version must match
// FormatVersion; minor version differences are accepted (forward compatible).
// The version mismatch case wraps ErrUnsupportedVersion.
func Unmarshal(data []byte) (*BackupFile, error) {
	var backup BackupFile
	if err := json.Unmarshal(data, &backup); err != nil {
		return nil, fmt.Errorf("backup: unmarshal: %w", err)
	}
	if backup.Metadata.Version == "" {
		return nil, fmt.Errorf("backup: missing format version")
	}
	if !compatibleVersion(backup.Metadata.Version) {
		return nil, fmt.Errorf("backup: %q (expected major version matching %q): %w",
			backup.Metadata.Version, FormatVersion, ErrUnsupportedVersion)
	}
	return &backup, nil
}

// compatibleVersion checks if a version string is compatible with FormatVersion.
// Compatible means the major version matches; minor version can be equal or lower.
// compatibleVersion checks if a version string is compatible with FormatVersion.
// Compatible means the major version matches; minor version differences are
// accepted (forward compatible).
func compatibleVersion(version string) bool {
	fileParts := strings.Split(version, ".")
	currentParts := strings.Split(FormatVersion, ".")

	if len(fileParts) != 2 || len(currentParts) != 2 {
		return false
	}

	return fileParts[0] == currentParts[0]
}
