//go:build windows

package config

import (
	"strconv"

	"golang.org/x/sys/windows/registry"
)

func readInternetSettings() map[string]string {
	out := map[string]string{}

	key, err := registry.OpenKey(
		registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		registry.QUERY_VALUE,
	)
	if err != nil {
		return out
	}
	defer key.Close()

	for _, name := range []string{"ProxyEnable", "ProxyServer", "AutoConfigURL"} {
		if val, _, err := key.GetIntegerValue(name); err == nil {
			out[name] = strconv.FormatInt(int64(val), 10)
			continue
		}
		if val, _, err := key.GetStringValue(name); err == nil {
			out[name] = val
		}
	}
	return out
}
