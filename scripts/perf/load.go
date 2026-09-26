// Run with: go run ./scripts/perf/load.go -base-url http://127.0.0.1:9090 -token-file PATH -case list
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type sample struct {
	latency time.Duration
	bytes   int64
	status  int
	err     string
}

type result struct {
	Case          string         `json:"case"`
	Concurrency   int            `json:"concurrency"`
	Requests      int            `json:"requests"`
	ElapsedSec    float64        `json:"elapsed_sec"`
	RPS           float64        `json:"rps"`
	P50Ms         float64        `json:"p50_ms"`
	P95Ms         float64        `json:"p95_ms"`
	P99Ms         float64        `json:"p99_ms"`
	MeanBytes     float64        `json:"mean_bytes"`
	StatusCounts  map[int]int    `json:"status_counts"`
	ErrorCounts   map[string]int `json:"error_counts"`
	ExpectedCount int            `json:"expected_count,omitempty"`
}

func main() {
	base := flag.String("base-url", "", "server origin, including scheme and port")
	tokenPath := flag.String("token-file", "", "path to a disposable fixture API token")
	caseName := flag.String("case", "list", "list, search, compound, ui, or create")
	cookiePath := flag.String("cookie-file", "", "file containing an authenticated Cookie header for the ui case")
	concurrency := flag.Int("concurrency", 1, "number of concurrent clients")
	requests := flag.Int("requests", 1000, "number of completed requests")
	runID := flag.String("run-id", "", "unique identifier for create URLs")
	expectedCount := flag.Int("expected-count", -1, "required API result count for read cases")
	flag.Parse()
	if *base == "" || *tokenPath == "" || *concurrency < 1 || *requests < 1 {
		fail("base-url, token-file, positive concurrency, and positive requests are required")
	}
	if *caseName == "create" && *runID == "" {
		fail("create requires a unique run-id")
	}
	baseURL, err := url.Parse(*base)
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" || baseURL.Path != "" {
		fail("base-url must be an HTTP origin without a path")
	}
	tokenBytes, err := os.ReadFile(*tokenPath)
	if err != nil {
		fail("read token file: %v", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		fail("token file is empty")
	}
	cookie := ""
	if *caseName == "ui" {
		if *cookiePath == "" {
			fail("ui requires cookie-file")
		}
		cookieBytes, err := os.ReadFile(*cookiePath)
		if err != nil {
			fail("read cookie file: %v", err)
		}
		cookie = strings.TrimSpace(string(cookieBytes))
		if cookie == "" {
			fail("cookie file is empty")
		}
	}
	endpoint := strings.TrimRight(*base, "/") + "/api/bookmarks/"
	switch *caseName {
	case "list":
		endpoint += "?limit=100"
	case "search":
		endpoint += "?q=systems&limit=100"
	case "compound":
		endpoint += "?q=systems%20and%20%23tag-008&limit=100"
	case "create":
		endpoint += "?disable_scraping=1&disable_html_snapshot=1"
	case "ui":
		endpoint = strings.TrimRight(*base, "/") + "/bookmarks"
	default:
		fail("unknown case %q", *caseName)
	}
	transport := &http.Transport{
		MaxIdleConns:        *concurrency,
		MaxIdleConnsPerHost: *concurrency,
		MaxConnsPerHost:     *concurrency,
		DisableCompression:  true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	if *caseName == "ui" {
		checkUI(client, endpoint, cookie)
	} else if *caseName != "create" {
		checkRead(client, endpoint, token, *expectedCount)
	}
	results := make(chan sample, *requests)
	var next atomic.Int64
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < *concurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for {
				index := int(next.Add(1))
				if index > *requests {
					return
				}
				results <- perform(client, endpoint, token, cookie, *caseName, *runID, index)
			}
		}()
	}
	started := time.Now()
	close(start)
	workers.Wait()
	elapsed := time.Since(started)
	close(results)
	report := result{Case: *caseName, Concurrency: *concurrency, Requests: *requests,
		ElapsedSec: elapsed.Seconds(), RPS: float64(*requests) / elapsed.Seconds(),
		StatusCounts: make(map[int]int), ErrorCounts: make(map[string]int)}
	var latencies []time.Duration
	var totalBytes int64
	for item := range results {
		report.StatusCounts[item.status]++
		if item.err != "" {
			report.ErrorCounts[item.err]++
		}
		latencies = append(latencies, item.latency)
		totalBytes += item.bytes
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	report.P50Ms = percentile(latencies, 0.50)
	report.P95Ms = percentile(latencies, 0.95)
	report.P99Ms = percentile(latencies, 0.99)
	report.MeanBytes = float64(totalBytes) / float64(*requests)
	if *expectedCount >= 0 {
		report.ExpectedCount = *expectedCount
	}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		fail("write result: %v", err)
	}
	if len(report.ErrorCounts) > 0 {
		os.Exit(1)
	}
}

func perform(client *http.Client, endpoint, token, cookie, caseName, runID string, index int) sample {
	method := http.MethodGet
	var body io.Reader
	if caseName == "create" {
		method = http.MethodPost
		body = strings.NewReader(fmt.Sprintf(`{"url":"https://example.org/bench/%s/%d","title":"Benchmark %d"}`, runID, index, index))
	}
	request, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return sample{err: err.Error()}
	}
	if caseName == "ui" {
		request.Header.Set("Cookie", cookie)
	} else {
		request.Header.Set("Authorization", "Token "+token)
		request.Header.Set("Accept", "application/json")
	}
	request.Header.Set("Accept-Encoding", "identity")
	if method == http.MethodPost {
		// uWSGI's HTTP frontend may close an idle connection without advertising it.
		// A non-idempotent POST cannot be replayed safely after an EOF on reuse.
		request.Close = true
		request.Header.Set("Content-Type", "application/json")
	}
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return sample{latency: time.Since(started), err: err.Error()}
	}
	defer response.Body.Close()
	expectedStatus := http.StatusOK
	if method == http.MethodPost {
		expectedStatus = http.StatusCreated
	}
	var failureBody []byte
	if response.StatusCode != expectedStatus {
		failureBody, _ = io.ReadAll(io.LimitReader(response.Body, 512))
	}
	remainder, err := io.Copy(io.Discard, response.Body)
	count := int64(len(failureBody)) + remainder
	item := sample{latency: time.Since(started), bytes: count, status: response.StatusCode}
	if err != nil {
		item.err = err.Error()
	} else if response.StatusCode != expectedStatus {
		item.err = fmt.Sprintf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(failureBody)))
	}
	return item
}

func checkUI(client *http.Client, endpoint, cookie string) {
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		fail("prepare UI preflight: %v", err)
	}
	request.Header.Set("Cookie", cookie)
	response, err := client.Do(request)
	if err != nil {
		fail("UI preflight: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), "bookmark-list") {
		fail("UI preflight: status=%d body contains bookmark list=%t error=%v", response.StatusCode, strings.Contains(string(body), "bookmark-list"), err)
	}
}

func checkRead(client *http.Client, endpoint, token string, expectedCount int) {
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		fail("prepare preflight: %v", err)
	}
	request.Header.Set("Authorization", "Token "+token)
	request.Header.Set("Accept-Encoding", "identity")
	response, err := client.Do(request)
	if err != nil {
		fail("preflight: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		fail("preflight: HTTP %d", response.StatusCode)
	}
	var data struct {
		Count int               `json:"count"`
		Items []json.RawMessage `json:"results"`
	}
	if err := json.NewDecoder(response.Body).Decode(&data); err != nil {
		fail("preflight response: %v", err)
	}
	if expectedCount >= 0 && data.Count != expectedCount {
		fail("preflight count: got %d, want %d", data.Count, expectedCount)
	}
	if data.Count == 0 || len(data.Items) == 0 {
		fail("preflight returned no bookmarks")
	}
}

func percentile(values []time.Duration, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	index := int(float64(len(values)-1)*p + 0.5)
	return float64(values[index]) / float64(time.Millisecond)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
