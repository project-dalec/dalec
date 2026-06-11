package targets

const testingAltTargetSuffix = "-testing-alt"

// TestingAltTargetKey returns the test-only alternate target key for key.
func TestingAltTargetKey(key string) string {
	return key + testingAltTargetSuffix
}
