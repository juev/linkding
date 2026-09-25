package httpserver

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"
)

const latestReleaseURL = "https://api.github.com/repos/sissbruecker/linkding/releases/latest"

var settingsVersionCache struct {
	sync.Mutex
	hour  int64
	value string
	valid bool
}

func settingsVersionInfo(ctx context.Context) string {
	hour := int64(math.RoundToEven(float64(time.Now().Unix()) / 3600))
	settingsVersionCache.Lock()
	defer settingsVersionCache.Unlock()
	if settingsVersionCache.valid && settingsVersionCache.hour == hour {
		return settingsVersionCache.value
	}
	client := &http.Client{Timeout: 5 * time.Second}
	value := fetchSettingsVersionInfo(ctx, client, latestReleaseURL)
	settingsVersionCache.hour = hour
	settingsVersionCache.value = value
	settingsVersionCache.valid = true
	return value
}

func fetchSettingsVersionInfo(ctx context.Context, client *http.Client, releaseURL string) string {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return UpstreamVersion
	}
	response, err := client.Do(request)
	if err != nil {
		return UpstreamVersion
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return UpstreamVersion
	}
	var release struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil || release.Name == "" {
		return UpstreamVersion
	}
	_, prefixSize := utf8.DecodeRuneInString(release.Name)
	latest := release.Name[prefixSize:]
	if latest == UpstreamVersion {
		return UpstreamVersion + " (latest)"
	}
	return UpstreamVersion + " (latest: " + latest + ")"
}
