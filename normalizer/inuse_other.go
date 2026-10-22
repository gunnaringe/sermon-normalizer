//go:build !windows

package normalizer

func inUse(error) bool { return false }
