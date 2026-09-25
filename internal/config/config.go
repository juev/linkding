// Package config reads the environment options supported by linkding v1.47.0.
package config

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
)

const defaultFaviconProvider = "https://t1.gstatic.com/faviconV2?client=SOCIAL&type=FAVICON&fallback_opts=TYPE,SIZE,URL&url={url}&size=32"
const defaultSingleFileUblockOptions = `'--browser-arg="--headless=new"' '--browser-arg="--user-data-dir=./chromium-profile"' '--browser-arg="--no-sandbox"' '--browser-arg="--load-extension=uBOLite.chromium.mv3"'`

// Config contains the public environment settings of the pinned Python server.
// Optional string settings are empty when unset; DBOptions is always non-nil.
type Config struct {
	DataDir                 string // Relative to the working directory, as in upstream bootstrap.
	ServerHost              string
	ServerPort              int
	ContextPath             string // Empty or a relative path ending in '/', e.g. "linkding/".
	TimeZone                string
	HostName                string
	UseXForwardedHost       bool
	DisableURLValidation    bool
	AllowedInternalHosts    string
	DisableBackgroundTasks  bool
	SessionCookieAge        int
	DisableLoginForm        bool
	EnableOIDC              bool
	OIDC                    OIDCConfig
	EnableAuthProxy         bool
	AuthProxyUsernameHeader string
	AuthProxyLogoutURL      string
	CSRFTrustedOrigins      []string
	CORSAllowedOrigins      string
	DBEngine                string
	DBHost                  string
	DBDatabase              string
	DBUser                  string
	DBPassword              string
	DBPort                  string
	DBOptions               map[string]json.RawMessage
	FaviconProvider         string
	EnableRefreshFavicons   bool
	PreviewMaxSize          int64
	EnableSnapshots         bool
	DisableAssetUpload      bool
	SingleFilePath          string
	SingleFileUblockOptions string
	SingleFileOptions       string
	SingleFileTimeoutSec    float64
	SnapshotPDFMaxSize      int64
	MonolithPath            string
	MonolithOptions         string
	SuperuserName           string
	SuperuserPassword       string
	SupervisorManaged       bool
	RequestTimeout          int
	RequestMaxContentLength int64
	LogXForwardedFor        bool
	DisableRequestLogs      bool
}

type OIDCConfig struct {
	AuthorizationEndpoint string
	TokenEndpoint         string
	UserEndpoint          string
	JWKSEndpoint          string
	ClientID              string
	ClientSecret          string
	SignAlgorithm         string
	Scopes                string
	UsePKCE               bool
	VerifySSL             bool
	UsernameClaim         string
}

// Load reads the process environment.
func Load() (Config, error) { return LoadFrom(os.LookupEnv) }

// LoadFrom reads configuration from a testable environment lookup. It rejects
// malformed numeric/JSON values instead of allowing Python's startup panic.
func LoadFrom(getenv func(string) (string, bool)) (Config, error) {
	get := func(key, fallback string) string {
		if value, ok := getenv(key); ok {
			return value
		}
		return fallback
	}
	getBool := func(key string, fallback bool) bool {
		if value, ok := getenv(key); ok {
			return value == "True" || value == "true" || value == "1"
		}
		return fallback
	}
	getInt := func(key string, fallback, min, max int) (int, error) {
		value, ok := getenv(key)
		if !ok {
			return fallback, nil
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < min || parsed > max {
			return 0, fmt.Errorf("%s: expected integer in [%d, %d]", key, min, max)
		}
		return parsed, nil
	}
	getInt64 := func(key string, fallback, min int64) (int64, error) {
		value, ok := getenv(key)
		if !ok {
			return fallback, nil
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < min {
			return 0, fmt.Errorf("%s: expected integer >= %d", key, min)
		}
		return parsed, nil
	}

	c := Config{
		DataDir:                 "data",
		ServerHost:              get("LD_SERVER_HOST", "[::]"),
		ContextPath:             get("LD_CONTEXT_PATH", ""),
		TimeZone:                get("TZ", "UTC"),
		HostName:                get("HOST_NAME", ""),
		UseXForwardedHost:       getBool("LD_USE_X_FORWARDED_HOST", false),
		DisableURLValidation:    getBool("LD_DISABLE_URL_VALIDATION", false),
		AllowedInternalHosts:    get("LD_ALLOWED_INTERNAL_HOSTS", ""),
		DisableBackgroundTasks:  getBool("LD_DISABLE_BACKGROUND_TASKS", false),
		DisableLoginForm:        getBool("LD_DISABLE_LOGIN_FORM", false),
		EnableOIDC:              getBool("LD_ENABLE_OIDC", false),
		EnableAuthProxy:         getBool("LD_ENABLE_AUTH_PROXY", false),
		AuthProxyUsernameHeader: get("LD_AUTH_PROXY_USERNAME_HEADER", "REMOTE_USER"),
		AuthProxyLogoutURL:      get("LD_AUTH_PROXY_LOGOUT_URL", ""),
		CORSAllowedOrigins:      get("LD_CORS_ALLOWED_ORIGINS", ""),
		DBEngine:                get("LD_DB_ENGINE", "sqlite"),
		DBHost:                  get("LD_DB_HOST", "localhost"),
		DBDatabase:              get("LD_DB_DATABASE", "linkding"),
		DBUser:                  get("LD_DB_USER", "linkding"),
		DBPassword:              get("LD_DB_PASSWORD", ""),
		DBPort:                  get("LD_DB_PORT", ""),
		FaviconProvider:         get("LD_FAVICON_PROVIDER", defaultFaviconProvider),
		EnableRefreshFavicons:   getBool("LD_ENABLE_REFRESH_FAVICONS", true),
		EnableSnapshots:         getBool("LD_ENABLE_SNAPSHOTS", false),
		DisableAssetUpload:      getBool("LD_DISABLE_ASSET_UPLOAD", false),
		SingleFilePath:          get("LD_SINGLEFILE_PATH", "single-file"),
		SingleFileUblockOptions: get("LD_SINGLEFILE_UBLOCK_OPTIONS", defaultSingleFileUblockOptions),
		SingleFileOptions:       get("LD_SINGLEFILE_OPTIONS", ""),
		MonolithPath:            get("LD_MONOLITH_PATH", "monolith"),
		MonolithOptions:         get("LD_MONOLITH_OPTIONS", "-a -v -s"),
		SuperuserName:           get("LD_SUPERUSER_NAME", ""),
		SuperuserPassword:       get("LD_SUPERUSER_PASSWORD", ""),
		SupervisorManaged:       get("LD_SUPERVISOR_MANAGED", "") == "True",
		DisableRequestLogs:      get("LD_DISABLE_REQUEST_LOGS", "") == "true",
	}
	if _, present := getenv("LD_LOG_X_FORWARDED_FOR"); present {
		c.LogXForwardedFor = true
	}
	if c.ContextPath != "" {
		if strings.HasPrefix(c.ContextPath, "/") || !strings.HasSuffix(c.ContextPath, "/") || strings.Contains(c.ContextPath, "//") || strings.ContainsAny(c.ContextPath, "?#\\") {
			return Config{}, fmt.Errorf("LD_CONTEXT_PATH: expected relative path ending in / without query or fragment")
		}
		for _, segment := range strings.Split(strings.TrimSuffix(c.ContextPath, "/"), "/") {
			if segment == "." || segment == ".." {
				return Config{}, fmt.Errorf("LD_CONTEXT_PATH: dot segments are not allowed")
			}
		}
	}
	var err error
	if c.ServerPort, err = getInt("LD_SERVER_PORT", 9090, 1, 65535); err != nil {
		return Config{}, err
	}
	if c.SessionCookieAge, err = getInt("LD_SESSION_COOKIE_AGE", 1209600, 1, int(^uint(0)>>1)); err != nil {
		return Config{}, err
	}
	if c.PreviewMaxSize, err = getInt64("LD_PREVIEW_MAX_SIZE", 5242880, 1); err != nil {
		return Config{}, err
	}
	if c.SnapshotPDFMaxSize, err = getInt64("LD_SNAPSHOT_PDF_MAX_SIZE", 15728640, 1); err != nil {
		return Config{}, err
	}
	if value, ok := getenv("LD_SINGLEFILE_TIMEOUT_SEC"); ok {
		c.SingleFileTimeoutSec, err = strconv.ParseFloat(value, 64)
		if err != nil || c.SingleFileTimeoutSec <= 0 || math.IsNaN(c.SingleFileTimeoutSec) || math.IsInf(c.SingleFileTimeoutSec, 0) {
			return Config{}, fmt.Errorf("LD_SINGLEFILE_TIMEOUT_SEC: expected positive finite number")
		}
	} else {
		c.SingleFileTimeoutSec = 120
	}
	if value, ok := getenv("LD_REQUEST_TIMEOUT"); ok {
		c.RequestTimeout, err = strconv.Atoi(value)
		if err != nil || c.RequestTimeout <= 0 {
			return Config{}, fmt.Errorf("LD_REQUEST_TIMEOUT: expected positive integer")
		}
	}
	if value, ok := getenv("LD_REQUEST_MAX_CONTENT_LENGTH"); ok {
		c.RequestMaxContentLength, err = strconv.ParseInt(value, 10, 64)
		if err != nil || c.RequestMaxContentLength <= 0 {
			return Config{}, fmt.Errorf("LD_REQUEST_MAX_CONTENT_LENGTH: expected positive integer")
		}
	}
	if c.DBEngine == "postgres" && c.DBPort != "" {
		port, err := strconv.ParseUint(c.DBPort, 10, 16)
		if err != nil || port == 0 {
			return Config{}, fmt.Errorf("LD_DB_PORT: expected port number")
		}
	}
	if c.DBEngine != "postgres" {
		c.DBEngine = "sqlite"
	}
	c.DBOptions = make(map[string]json.RawMessage)
	if value := get("LD_DB_OPTIONS", ""); value != "" {
		if err := json.Unmarshal([]byte(value), &c.DBOptions); err != nil || c.DBOptions == nil {
			return Config{}, fmt.Errorf("LD_DB_OPTIONS: expected JSON object")
		}
	}
	if origins := get("LD_CSRF_TRUSTED_ORIGINS", ""); origins != "" {
		c.CSRFTrustedOrigins = strings.Split(origins, ",")
	}
	if c.EnableOIDC {
		c.OIDC = OIDCConfig{
			AuthorizationEndpoint: get("OIDC_OP_AUTHORIZATION_ENDPOINT", ""),
			TokenEndpoint:         get("OIDC_OP_TOKEN_ENDPOINT", ""),
			UserEndpoint:          get("OIDC_OP_USER_ENDPOINT", ""),
			JWKSEndpoint:          get("OIDC_OP_JWKS_ENDPOINT", ""),
			ClientID:              get("OIDC_RP_CLIENT_ID", ""),
			ClientSecret:          get("OIDC_RP_CLIENT_SECRET", ""),
			SignAlgorithm:         get("OIDC_RP_SIGN_ALGO", "RS256"),
			Scopes:                get("OIDC_RP_SCOPES", "openid email profile"),
			UsePKCE:               getBool("OIDC_USE_PKCE", true),
			VerifySSL:             getBool("OIDC_VERIFY_SSL", true),
			UsernameClaim:         get("OIDC_USERNAME_CLAIM", "email"),
		}
	}
	return c, nil
}

// ListenAddress follows the Docker bootstrap host and port convention.
func (c Config) ListenAddress() string {
	return net.JoinHostPort(strings.Trim(c.ServerHost, "[]"), strconv.Itoa(c.ServerPort))
}

// URLPrefix is the leading-slash form used by routes and cookie paths.
func (c Config) URLPrefix() string { return "/" + c.ContextPath }
