package config

import (
	"strconv"
	"strings"
)

// detectSystemProxy читает прокси из настроек Windows Internet.
// Возвращает URL прокси или пустую строку, если прокси выключен.
func detectSystemProxy() string {
	settings := readInternetSettings()
	if len(settings) == 0 {
		return ""
	}

	enabled, _ := strconv.Atoi(settings["ProxyEnable"])
	if enabled != 1 {
		return ""
	}

	server := settings["ProxyServer"]
	if server == "" {
		return ""
	}

	// ProxyServer может быть "host:port" или "http=host:port;https=host:port"
	if strings.Contains(server, "=") {
		for _, part := range strings.Split(server, ";") {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) == 2 && (kv[0] == "https" || kv[0] == "http") {
				return "http://" + kv[1]
			}
		}
		return ""
	}

	return "http://" + server
}
