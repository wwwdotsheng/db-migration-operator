package adapter

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
)

// checkVersionRange compares the numeric version already extracted by a Driver.
func checkVersionRange(raw, version, versionRange string) error {
	parsed, err := semver.NewVersion(version)
	if err != nil {
		return fmt.Errorf("%w: %q", ErrUnrecognizedServerVersion, raw)
	}
	constraint, err := semver.NewConstraint(versionRange)
	if err != nil {
		return fmt.Errorf("invalid server version constraint %q: %w", versionRange, err)
	}
	if !constraint.Check(parsed) {
		return fmt.Errorf("%w: %q does not satisfy %s", ErrIncompatibleServerVersion, raw, versionRange)
	}
	return nil
}
