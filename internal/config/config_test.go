package config

import (
	"strings"
	"testing"
)

func environment(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) { value, ok := values[key]; return value, ok }
}

func TestDefaults(t *testing.T) {
	c, err := LoadFrom(environment(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != "data" || c.ServerHost != "[::]" || c.ServerPort != 9090 || c.ListenAddress() != "[::]:9090" {
		t.Fatalf("server defaults: %+v", c)
	}
	if c.ContextPath != "" || c.URLPrefix() != "/" || c.TimeZone != "UTC" || c.DBEngine != "sqlite" || c.DBDatabase != "linkding" {
		t.Fatalf("application defaults: %+v", c)
	}
	if c.SessionCookieAge != 1209600 || c.PreviewMaxSize != 5242880 || c.SnapshotPDFMaxSize != 15728640 || c.SingleFileTimeoutSec != 120 {
		t.Fatalf("numeric defaults: %+v", c)
	}
	if !c.EnableRefreshFavicons || c.EnableSnapshots || c.EnableOIDC || c.DisableBackgroundTasks || c.DBOptions == nil {
		t.Fatalf("feature defaults: %+v", c)
	}
	if c.SingleFileUblockOptions != defaultSingleFileUblockOptions || c.FaviconProvider != defaultFaviconProvider {
		t.Fatalf("external defaults: %+v", c)
	}
}

func TestBooleanGrammarAndRuntimeQuirks(t *testing.T) {
	for _, value := range []string{"True", "true", "1"} {
		c, err := LoadFrom(environment(map[string]string{"LD_ENABLE_OIDC": value, "LD_DISABLE_BACKGROUND_TASKS": value}))
		if err != nil {
			t.Fatal(err)
		}
		if !c.EnableOIDC || !c.DisableBackgroundTasks || !c.OIDC.UsePKCE || !c.OIDC.VerifySSL {
			t.Fatalf("%q did not enable flags: %+v", value, c)
		}
	}
	for _, value := range []string{"TRUE", "yes", "false", "0", ""} {
		c, err := LoadFrom(environment(map[string]string{"LD_ENABLE_OIDC": value, "LD_ENABLE_REFRESH_FAVICONS": value, "LD_SUPERVISOR_MANAGED": value, "LD_DISABLE_REQUEST_LOGS": value}))
		if err != nil {
			t.Fatal(err)
		}
		if c.EnableOIDC || c.EnableRefreshFavicons || c.SupervisorManaged || c.DisableRequestLogs {
			t.Fatalf("%q unexpectedly enabled flag: %+v", value, c)
		}
	}
	c, err := LoadFrom(environment(map[string]string{
		"LD_SUPERVISOR_MANAGED":   "True",
		"LD_DISABLE_REQUEST_LOGS": "true",
		"LD_LOG_X_FORWARDED_FOR":  "false",
		"LD_CSRF_TRUSTED_ORIGINS": "https://a.example,https://b.example",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !c.SupervisorManaged || !c.DisableRequestLogs || !c.LogXForwardedFor || len(c.CSRFTrustedOrigins) != 2 {
		t.Fatalf("runtime flags: %+v", c)
	}
}

func TestOIDCAndDatabaseOptions(t *testing.T) {
	c, err := LoadFrom(environment(map[string]string{
		"LD_ENABLE_OIDC":    "1",
		"OIDC_RP_CLIENT_ID": "client",
		"OIDC_USE_PKCE":     "False",
		"OIDC_VERIFY_SSL":   "0",
		"LD_DB_ENGINE":      "postgres",
		"LD_DB_PORT":        "5432",
		"LD_DB_OPTIONS":     "{\"sslmode\":\"require\",\"connect_timeout\":5}",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.OIDC.ClientID != "client" || c.OIDC.SignAlgorithm != "RS256" || c.OIDC.UsePKCE || c.OIDC.VerifySSL {
		t.Fatalf("OIDC: %+v", c.OIDC)
	}
	if c.DBEngine != "postgres" || c.DBPort != "5432" || string(c.DBOptions["sslmode"]) != "\"require\"" {
		t.Fatalf("database: %+v", c)
	}
}

func TestUnknownDatabaseEngineFollowsUpstreamSQLiteFallback(t *testing.T) {
	c, err := LoadFrom(environment(map[string]string{"LD_DB_ENGINE": "unknown"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.DBEngine != "sqlite" {
		t.Fatalf("database engine = %q, want sqlite", c.DBEngine)
	}
}

func TestInvalidNumericAndJSON(t *testing.T) {
	cases := map[string]string{
		"LD_SERVER_PORT":                "65536",
		"LD_SESSION_COOKIE_AGE":         "oops",
		"LD_PREVIEW_MAX_SIZE":           "-1",
		"LD_SNAPSHOT_PDF_MAX_SIZE":      "0",
		"LD_SINGLEFILE_TIMEOUT_SEC":     "NaN",
		"LD_REQUEST_TIMEOUT":            "0",
		"LD_REQUEST_MAX_CONTENT_LENGTH": "-1",
		"LD_DB_OPTIONS":                 "[1,2]",
	}
	for key, value := range cases {
		t.Run(key, func(t *testing.T) {
			_, err := LoadFrom(environment(map[string]string{key: value}))
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("want error mentioning %s, got %v", key, err)
			}
		})
	}
	_, err := LoadFrom(environment(map[string]string{"LD_DB_ENGINE": "postgres", "LD_DB_PORT": "oops"}))
	if err == nil || !strings.Contains(err.Error(), "LD_DB_PORT") {
		t.Fatalf("invalid PostgreSQL port: %v", err)
	}
}

func TestContextPath(t *testing.T) {
	for _, path := range []string{"", "linkding/", "parent/linkding/"} {
		c, err := LoadFrom(environment(map[string]string{"LD_CONTEXT_PATH": path}))
		if err != nil {
			t.Fatalf("%q: %v", path, err)
		}
		if c.URLPrefix() != "/"+path {
			t.Fatalf("%q: prefix = %q", path, c.URLPrefix())
		}
	}
	for _, path := range []string{"/linkding/", "linkding", "a//b/", "../a/", "a/?q=1"} {
		_, err := LoadFrom(environment(map[string]string{"LD_CONTEXT_PATH": path}))
		if err == nil {
			t.Fatalf("accepted invalid context path %q", path)
		}
	}
}
