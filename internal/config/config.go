package config

import (
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Token          string
	WebAppURL      string
	ListenAddr     string
	DBPath         string
	UploadDir      string
	ExportDir      string
	ProxyURL       string
	AdminAddr      string
	AdminPassword  string
	AdminOnlyLocal bool
	AttendancePath string
	RootUserIDs    []int64
	AdminUserIDs   []int64
	ProfOrgUserIDs []int64
}

func Load() *Config {
	_ = godotenv.Load()

	proxy := getEnv("PROXY_URL", "")
	explicit := proxy != ""
	if !explicit {
		proxy = detectSystemProxy()
	}
	if proxy != "" && !proxyReachable(proxy) {
		if explicit {
			log.Printf("WARNING: PROXY_URL %s is not reachable, trying a direct connection", proxy)
		} else {
			log.Printf("system proxy %s is not reachable, connecting directly", proxy)
		}
		proxy = ""
	} else if proxy != "" {
		log.Printf("using proxy %s", proxy)
	}

	return &Config{
		Token:          getEnv("BOT_TOKEN", ""),
		WebAppURL:      getEnv("WEBAPP_URL", "https://example.com"),
		ListenAddr:     getEnv("LISTEN_ADDR", ":8080"),
		DBPath:         getEnv("DB_PATH", "data/studbot.db"),
		UploadDir:      getEnv("UPLOAD_DIR", "data/uploads"),
		ExportDir:      getEnv("EXPORT_DIR", "data/exports"),
		ProxyURL:       proxy,
		AdminAddr:      getEnv("ADMIN_ADDR", "127.0.0.1:8081"),
		AdminPassword:  getEnv("ADMIN_PASSWORD", ""),
		AdminOnlyLocal: getEnv("ADMIN_ONLY_LOCAL", "true") == "true",
		AttendancePath: getEnv("ATTENDANCE_PATH", "data/attendance.xlsx"),
		RootUserIDs:    parseInt64List(getEnv("ROOT_USER_IDS", "")),
		AdminUserIDs:   parseInt64List(getEnv("ADMIN_USER_IDS", "")),
		ProfOrgUserIDs: parseInt64List(getEnv("PROFORG_USER_IDS", "")),
	}
}

// proxyReachable checks that something actually listens on the proxy address.
// Windows keeps ProxyEnable=1 after the client is closed, and a dead proxy
// would make every Telegram request fail.
func proxyReachable(proxyURL string) bool {
	u, err := url.Parse(proxyURL)
	if err != nil || u.Host == "" {
		return false
	}
	host := u.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "80")
	}
	conn, err := net.DialTimeout("tcp", host, 700*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseInt64List(s string) []int64 {
	if s == "" {
		return nil
	}
	parts := splitComma(s)
	var out []int64
	for _, p := range parts {
		if v, err := strconv.ParseInt(p, 10, 64); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
