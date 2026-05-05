package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"forge/control-plane/internal/config"
	"forge/control-plane/internal/forgeyaml"
	"forge/control-plane/internal/store"
)

type caddyAdminMock struct {
	failLoad bool

	loadCalls   int32
	configCalls int32

	mu           sync.Mutex
	lastLoadBody []byte

	server *httptest.Server
}

func newCaddyAdminMock(t *testing.T, failLoad bool) *caddyAdminMock {
	t.Helper()

	m := &caddyAdminMock{failLoad: failLoad}
	mux := http.NewServeMux()

	// Caddy client does GET {adminURL}/config/
	mux.HandleFunc("/config/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&m.configCalls, 1)
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// Minimal config with empty routes.
		_, _ = w.Write([]byte(`{
			"apps":{
				"http":{
					"servers":{
						"srv0":{
							"listen":[":80"],
							"routes":[]
						}
					}
				}
			}
		}`))
	})

	// Caddy client does POST {adminURL}/load
	mux.HandleFunc("/load", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&m.loadCalls, 1)
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		m.mu.Lock()
		m.lastLoadBody = append(m.lastLoadBody[:0], body...)
		m.mu.Unlock()
		if m.failLoad {
			http.Error(w, "caddy load error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

func (m *caddyAdminMock) URL() string {
	return m.server.URL
}

func (m *caddyAdminMock) LastLoadBody() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.lastLoadBody...)
}

func TestFinishRunTaskStopsAllPreviousRunningDeploymentsForHost(t *testing.T) {
	ctx := context.Background()
	srv, st := newDeploymentStateTestServer(t, "")
	if err := st.UpsertAgent(ctx, store.Agent{ID: "worker-1", Hostname: "worker-1", Address: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	configJSON := testForgeConfigJSON(t, "app")
	first := createDeploymentForStateTest(t, st, store.Deployment{
		AppName:         "app",
		RepoURL:         "https://github.com/example/app.git",
		CommitSHA:       "1111111111111111111111111111111111111111",
		Branch:          "main",
		Status:          "running",
		ConfigJSON:      configJSON,
		AssignedAgentID: "worker-1",
		Host:            "app.nforge.space",
		TargetPort:      21001,
	})
	second := createDeploymentForStateTest(t, st, store.Deployment{
		AppName:         "app",
		RepoURL:         "https://github.com/example/app.git",
		CommitSHA:       "2222222222222222222222222222222222222222",
		Branch:          "main",
		Status:          "running",
		ConfigJSON:      configJSON,
		AssignedAgentID: "worker-1",
		Host:            "app.nforge.space",
		TargetPort:      21002,
	})
	current := createDeploymentForStateTest(t, st, store.Deployment{
		AppName:         "app",
		RepoURL:         "https://github.com/example/app.git",
		CommitSHA:       "3333333333333333333333333333333333333333",
		Branch:          "main",
		Status:          "deploying",
		ConfigJSON:      configJSON,
		AssignedAgentID: "worker-1",
		Host:            "app.nforge.space",
		TargetPort:      21003,
	})
	if err := srv.finishRunTask(ctx, current, "worker-1", 21003); err != nil {
		t.Fatal(err)
	}
	assertDeploymentStatus(t, st, current.ID, "running")
	assertDeploymentStatus(t, st, first.ID, "stopping")
	assertDeploymentStatus(t, st, second.ID, "stopping")
}

func TestFinishRunTaskEnsureRouteFailureMarksDeploymentFailed(t *testing.T) {
	ctx := context.Background()

	caddy := newCaddyAdminMock(t, true)
	srv, st := newDeploymentStateTestServer(t, caddy.URL())

	if err := st.UpsertAgent(ctx, store.Agent{ID: "worker-1", Hostname: "worker-1", Address: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}

	configJSON := testForgeConfigJSON(t, "app")
	current := createDeploymentForStateTest(t, st, store.Deployment{
		AppName:         "app",
		RepoURL:         "https://github.com/example/app.git",
		CommitSHA:       "3333333333333333333333333333333333333333",
		Branch:          "main",
		Status:          "deploying",
		ConfigJSON:      configJSON,
		AssignedAgentID: "worker-1",
		Host:            "app.nforge.space",
		TargetPort:      21003,
	})

	err := srv.finishRunTask(ctx, current, "worker-1", 21003)
	if err == nil {
		t.Fatal("expected finishRunTask to fail")
	}
	assertDeploymentStatus(t, st, current.ID, "failed")
	if atomic.LoadInt32(&caddy.loadCalls) == 0 {
		t.Fatalf("expected caddy /load to be called")
	}
}

func TestRetryDueFailedDeploymentsQueuesLatestFailedDeploymentOnly(t *testing.T) {
	ctx := context.Background()
	srv, st := newDeploymentStateTestServer(t, "")

	olderFailed := createDeploymentForStateTest(t, st, store.Deployment{
		AppName:    "app-old",
		RepoURL:    "https://github.com/example/app-old.git",
		CommitSHA:  "1111111111111111111111111111111111111111",
		Branch:     "main",
		Status:     "failed",
		ConfigJSON: testForgeConfigJSON(t, "app-old"),
		Host:       "app-old.nforge.space",
	})
	if err := st.UpsertDeploymentRetryState(ctx, olderFailed.ID, 1, time.Now().Add(-time.Minute), "temporary failure"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateDeployment(ctx, store.Deployment{
		AppName:    "app-old",
		RepoURL:    "https://github.com/example/app-old.git",
		CommitSHA:  "2222222222222222222222222222222222222222",
		Branch:     "main",
		Status:     "running",
		ConfigJSON: testForgeConfigJSON(t, "app-old"),
		Host:       "app-old.nforge.space",
	}); err != nil {
		t.Fatal(err)
	}

	latestFailed := createDeploymentForStateTest(t, st, store.Deployment{
		AppName:    "app-new",
		RepoURL:    "https://github.com/example/app-new.git",
		CommitSHA:  "3333333333333333333333333333333333333333",
		Branch:     "main",
		Status:     "failed",
		ConfigJSON: testForgeConfigJSON(t, "app-new"),
		Host:       "app-new.nforge.space",
	})
	if err := st.UpsertDeploymentRetryState(ctx, latestFailed.ID, 1, time.Now().Add(-time.Minute), "temporary failure"); err != nil {
		t.Fatal(err)
	}

	if err := srv.retryDueFailedDeployments(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}

	assertDeploymentStatus(t, st, olderFailed.ID, "failed")
	assertDeploymentStatus(t, st, latestFailed.ID, "pending")
	if _, ok, err := st.DeploymentRetryStateByDeploymentID(ctx, latestFailed.ID); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatal("expected retry state to remain after auto retry")
	}
}

func TestReconcileRunningDeploymentsMarksUnhealthyDeploymentFailed(t *testing.T) {
	ctx := context.Background()
	srv, st := newDeploymentStateTestServer(t, "")
	healthySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer healthySrv.Close()

	healthyHost, healthyPort, err := splitTestServerAddress(healthySrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	deadPort := unusedLocalPort(t)

	if err := st.UpsertAgent(ctx, store.Agent{ID: "worker-1", Hostname: "worker-1", Address: healthyHost}); err != nil {
		t.Fatal(err)
	}

	healthy := createDeploymentForStateTest(t, st, store.Deployment{
		AppName:         "healthy",
		RepoURL:         "https://github.com/example/healthy.git",
		CommitSHA:       "1111111111111111111111111111111111111111",
		Branch:          "main",
		Status:          "running",
		ConfigJSON:      testForgeConfigJSON(t, "healthy"),
		AssignedAgentID: "worker-1",
		Host:            "healthy.nforge.space",
		TargetPort:      healthyPort,
	})
	_ = healthy

	unhealthy := createDeploymentForStateTest(t, st, store.Deployment{
		AppName:         "unhealthy",
		RepoURL:         "https://github.com/example/unhealthy.git",
		CommitSHA:       "2222222222222222222222222222222222222222",
		Branch:          "main",
		Status:          "running",
		ConfigJSON:      testForgeConfigJSON(t, "unhealthy"),
		AssignedAgentID: "worker-1",
		Host:            "unhealthy.nforge.space",
		TargetPort:      deadPort,
	})

	if err := srv.reconcileRunningDeployments(ctx); err != nil {
		t.Fatal(err)
	}

	assertDeploymentStatus(t, st, unhealthy.ID, "failed")
}

func TestReconcileRunningDeploymentsKeepsRunningDeploymentWhenAgentOffline(t *testing.T) {
	ctx := context.Background()
	srv, st := newDeploymentStateTestServer(t, "")
	srv.cfg.OnlineWindow = time.Nanosecond
	if err := st.UpsertAgent(ctx, store.Agent{
		ID:       "worker-1",
		Hostname: "worker-1",
		Address:  "127.0.0.1",
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	deployment := createDeploymentForStateTest(t, st, store.Deployment{
		AppName:         "app",
		RepoURL:         "https://github.com/example/app.git",
		CommitSHA:       "1111111111111111111111111111111111111111",
		Branch:          "main",
		Status:          "running",
		ConfigJSON:      testForgeConfigJSON(t, "app"),
		AssignedAgentID: "worker-1",
		Host:            "app.nforge.space",
		TargetPort:      21001,
	})

	if err := srv.reconcileRunningDeployments(ctx); err != nil {
		t.Fatal(err)
	}

	assertDeploymentStatus(t, st, deployment.ID, "running")
	healthByID, err := st.DeploymentHealthObservationsByDeploymentIDs(ctx, []int64{deployment.ID})
	if err != nil {
		t.Fatal(err)
	}
	health, ok := healthByID[deployment.ID]
	if !ok || health.Status != "unhealthy" || health.Reason != "assigned agent is offline" {
		t.Fatalf("expected offline health observation, got ok=%v health=%+v", ok, health)
	}
}

func TestReconcileRunningDeploymentsDeletesRouteForAppWhenNoOtherRunningDeployments(t *testing.T) {
	ctx := context.Background()

	caddy := newCaddyAdminMock(t, false)
	srv, st := newDeploymentStateTestServer(t, caddy.URL())

	if err := st.UpsertAgent(ctx, store.Agent{ID: "worker-1", Hostname: "worker-1", Address: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}

	deadPort := unusedLocalPort(t)
	configJSON := testForgeConfigJSON(t, "app")

	current := createDeploymentForStateTest(t, st, store.Deployment{
		AppName:         "app",
		RepoURL:         "https://github.com/example/app.git",
		CommitSHA:       "3333333333333333333333333333333333333333",
		Branch:          "main",
		Status:          "running",
		ConfigJSON:      configJSON,
		AssignedAgentID: "worker-1",
		Host:            "app.nforge.space",
		TargetPort:      deadPort,
	})

	if err := srv.reconcileRunningDeployments(ctx); err != nil {
		t.Fatal(err)
	}

	assertDeploymentStatus(t, st, current.ID, "failed")
	if atomic.LoadInt32(&caddy.loadCalls) == 0 {
		t.Fatalf("expected caddy /load to be called by DeleteRoute cleanup")
	}
}

func TestReconcileRunningDeploymentsRestoresRouteToLatestHealthyDeployment(t *testing.T) {
	ctx := context.Background()

	healthySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer healthySrv.Close()

	healthyHost, healthyPort, err := splitTestServerAddress(healthySrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	deadPort := unusedLocalPort(t)

	caddy := newCaddyAdminMock(t, false)
	srv, st := newDeploymentStateTestServer(t, caddy.URL())

	if err := st.UpsertAgent(ctx, store.Agent{ID: "worker-1", Hostname: "worker-1", Address: healthyHost}); err != nil {
		t.Fatal(err)
	}

	configJSON := testForgeConfigJSON(t, "app")
	healthy := createDeploymentForStateTest(t, st, store.Deployment{
		AppName:         "app",
		RepoURL:         "https://github.com/example/app.git",
		CommitSHA:       "1111111111111111111111111111111111111111",
		Branch:          "main",
		Status:          "running",
		ConfigJSON:      configJSON,
		AssignedAgentID: "worker-1",
		Host:            "app.nforge.space",
		TargetPort:      healthyPort,
	})
	current := createDeploymentForStateTest(t, st, store.Deployment{
		AppName:         "app",
		RepoURL:         "https://github.com/example/app.git",
		CommitSHA:       "2222222222222222222222222222222222222222",
		Branch:          "main",
		Status:          "running",
		ConfigJSON:      configJSON,
		AssignedAgentID: "worker-1",
		Host:            "app.nforge.space",
		TargetPort:      deadPort,
	})

	if err := srv.reconcileRunningDeployments(ctx); err != nil {
		t.Fatal(err)
	}

	assertDeploymentStatus(t, st, current.ID, "failed")
	assertDeploymentStatus(t, st, healthy.ID, "running")

	body := caddy.LastLoadBody()
	if len(body) == 0 {
		t.Fatal("expected caddy config to be reloaded")
	}
	wantDial := net.JoinHostPort(healthyHost, strconv.Itoa(healthyPort))
	if gotDial := caddyRouteDial(t, body, "app"); gotDial != wantDial {
		t.Fatalf("expected route dial %q, got %q", wantDial, gotDial)
	}
}

func newDeploymentStateTestServer(t *testing.T, caddyAdminURL string) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		BaseDomain:       "nforge.space",
		DefaultAgentRoot: "/tmp/forge-agent/apps",
		OnlineWindow:     time.Hour,
		CaddyAdminURL:    caddyAdminURL,
	}
	srv := New(cfg, st, nil)
	return srv, st
}

func createDeploymentForStateTest(t *testing.T, st *store.Store, deployment store.Deployment) store.Deployment {
	t.Helper()
	created, err := st.CreateDeployment(context.Background(), deployment)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func assertDeploymentStatus(t *testing.T, st *store.Store, id int64, want string) {
	t.Helper()
	deployment, ok, err := st.GetDeployment(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("deployment %d not found", id)
	}
	if deployment.Status != want {
		t.Fatalf("deployment %d: expected status %q, got %q", id, want, deployment.Status)
	}
}

func testForgeConfigJSON(t *testing.T, name string) string {
	t.Helper()
	cfg := forgeyaml.Config{
		Name:    name,
		Runtime: "python3",
		Build:   forgeyaml.BuildConfig{Commands: []string{"python3 --version"}},
		Run:     forgeyaml.RunConfig{Command: "python3 app.py", Port: 8000},
		Resources: forgeyaml.ResourceConfig{
			Memory:      "128M",
			MemoryBytes: 128 * 1024 * 1024,
			CPU:         0.1,
		},
		Health: forgeyaml.HealthConfig{
			Path:     "/health",
			Interval: "1ms",
			Timeout:  "100ms",
			Retries:  1,
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func splitTestServerAddress(rawURL string) (string, int, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", 0, err
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return "", 0, err
	}
	return host, port, nil
}

func unusedLocalPort(t *testing.T) int {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

func caddyRouteDial(t *testing.T, body []byte, appName string) string {
	t.Helper()
	var cfg map[string]interface{}
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatal(err)
	}
	apps, _ := cfg["apps"].(map[string]interface{})
	httpApp, _ := apps["http"].(map[string]interface{})
	servers, _ := httpApp["servers"].(map[string]interface{})
	srv0, _ := servers["srv0"].(map[string]interface{})
	routes, _ := srv0["routes"].([]interface{})
	routeID := "forge-" + strings.NewReplacer(" ", "-", "/", "-", "_", "-").Replace(strings.ToLower(appName))
	for _, rawRoute := range routes {
		route, _ := rawRoute.(map[string]interface{})
		if route["@id"] != routeID {
			continue
		}
		handles, _ := route["handle"].([]interface{})
		if len(handles) == 0 {
			t.Fatalf("route %q has no handlers", routeID)
		}
		handle, _ := handles[0].(map[string]interface{})
		upstreams, _ := handle["upstreams"].([]interface{})
		if len(upstreams) == 0 {
			t.Fatalf("route %q has no upstreams", routeID)
		}
		upstream, _ := upstreams[0].(map[string]interface{})
		dial, _ := upstream["dial"].(string)
		return dial
	}
	t.Fatalf("route %q not found in loaded Caddy config", routeID)
	return ""
}

func TestHandleListAppsIncludesActiveDeploymentState(t *testing.T) {
	ctx := context.Background()
	srv, st := newDeploymentStateTestServer(t, "")
	srv.cfg.AdminToken = "admin-token"

	if err := st.UpsertAgent(ctx, store.Agent{ID: "worker-1", Hostname: "worker-1", Address: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}

	configJSON := testForgeConfigJSON(t, "app")
	healthy := createDeploymentForStateTest(t, st, store.Deployment{
		AppName:         "app",
		RepoURL:         "https://github.com/example/app.git",
		CommitSHA:       "1111111111111111111111111111111111111111",
		Branch:          "main",
		Status:          "running",
		ConfigJSON:      configJSON,
		AssignedAgentID: "worker-1",
		Host:            "app.nforge.space",
		TargetPort:      21001,
	})
	current := createDeploymentForStateTest(t, st, store.Deployment{
		AppName:         "app",
		RepoURL:         "https://github.com/example/app.git",
		CommitSHA:       "2222222222222222222222222222222222222222",
		Branch:          "main",
		Status:          "failed",
		ConfigJSON:      configJSON,
		AssignedAgentID: "worker-1",
		Host:            "app.nforge.space",
		TargetPort:      21002,
	})
	_ = current

	handler := srv.routes(ctx)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/apps", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d body=%s", http.StatusOK, res.Code, res.Body.String())
	}

	var views []struct {
		ID                 int64  `json:"id"`
		AppName            string `json:"app_name"`
		Status             string `json:"status"`
		Host               string `json:"host"`
		ActiveDeploymentID int64  `json:"active_deployment_id"`
		ActiveStatus       string `json:"active_status"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 {
		t.Fatalf("expected one app view, got %+v", views)
	}
	if views[0].ID != current.ID {
		t.Fatalf("expected latest deployment id %d, got %d", current.ID, views[0].ID)
	}
	if views[0].Status != "failed" {
		t.Fatalf("expected latest status failed, got %q", views[0].Status)
	}
	if views[0].ActiveDeploymentID != healthy.ID {
		t.Fatalf("expected active deployment id %d, got %d", healthy.ID, views[0].ActiveDeploymentID)
	}
	if views[0].ActiveStatus != "running" {
		t.Fatalf("expected active status running, got %q", views[0].ActiveStatus)
	}
}

// Ensure the mock server doesn't leak goroutines on failure paths.
