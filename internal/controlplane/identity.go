package controlplane

import (
	"encoding/hex"
	"strings"
)

// ValidApplicationID reports whether applicationID can name an application in
// control-plane keys: non-empty and free of the key separator ".".
func ValidApplicationID(applicationID string) bool {
	return applicationID != "" && !strings.Contains(applicationID, ".")
}

// SHA256DigestSuffix returns the lowercase hex part of a "sha256:<64 hex>"
// digest, or ErrDeploymentArtifactInvalid.
func SHA256DigestSuffix(digest string) (string, error) {
	const prefix = "sha256:"
	if !strings.HasPrefix(digest, prefix) || len(digest) != len(prefix)+64 {
		return "", ErrDeploymentArtifactInvalid
	}
	suffix := strings.TrimPrefix(digest, prefix)
	if suffix != strings.ToLower(suffix) {
		return "", ErrDeploymentArtifactInvalid
	}
	if _, err := hex.DecodeString(suffix); err != nil {
		return "", ErrDeploymentArtifactInvalid
	}
	return suffix, nil
}

// ValidSHA256Digest reports whether digest is a well-formed sha256 digest.
func ValidSHA256Digest(digest string) bool {
	_, err := SHA256DigestSuffix(digest)
	return err == nil
}
