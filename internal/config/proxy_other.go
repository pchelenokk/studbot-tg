//go:build !windows

package config

func readInternetSettings() map[string]string { return map[string]string{} }
