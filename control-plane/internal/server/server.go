package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"forge/control-plane/internal/config"
	"forge/control-plane/internal/forgeyaml"
	"forge/control-plane/internal/proxy"
	"forge/control-plane/internal/store"
	"forge/control-plane/internal/vault"
)

type Server struct {
	cfg   config.Config
	store *store.Store
	vault *vault.Vault
	proxy *proxy.Caddy
	hub   *eventHub
}

func New(cfg config.Config, st *store.Store, vt *vault.Vault) *Server {
	return &Server{
		cfg:   cfg,
		store: st,
		vault: vt,
		proxy: proxy.NewCaddy(cfg.CaddyAdminURL),
		hub:   newEventHub(),
	}
}

func (s *Server) Run(ctx context.Context) error {
	if err := os.MkdirAll(s.cfg.WorkDir, 0755); err != nil {
		return err
	}
	go s.schedulerLoop(ctx)

	httpServer := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.routes(ctx),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Printf("forge control plane listening on %s", s.cfg.Addr)
	err := httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) routes(ctx context.Context) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		s.hub.serve(ctx, w, r)
	})
	mux.HandleFunc("/api/v1/webhook/github", s.handleGitHubWebhook)
	mux.HandleFunc("/api/v1/agents/register", s.handleAgentRegister)
	mux.HandleFunc("/api/v1/agents/", s.handleAgentSubroutes)
	mux.HandleFunc("/api/v1/tasks/", s.handleTaskSubroutes)
	mux.HandleFunc("/api/v1/apps/", s.handleAppSubroutes)
	mux.HandleFunc("/api/v1/deployments", s.handleDeployments)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeployments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if !s.authorizeAdmin(w, r) {
		return
	}
	deployments, err := s.store.ListDeployments(r.Context(), 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, deployments)
}

func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if s.cfg.WebhookSecret != "" && !verifyGitHubSignature(body, s.cfg.WebhookSecret, r.Header.Get("X-Hub-Signature-256")) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid github signature"})
		return
	}

	var payload githubPushPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	repoURL := payload.Repository.CloneURL
	if repoURL == "" {
		repoURL = payload.Repository.URL
	}
	if repoURL == "" {
		repoURL = payload.Repository.HTMLURL
	}
	if repoURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repository clone_url is required"})
		return
	}

	appConfig, err := s.cloneAndParseForgeYAML(r.Context(), repoURL, payload.After)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	configJSON, err := json.Marshal(appConfig)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	deployment, err := s.store.CreateDeployment(r.Context(), store.Deployment{
		AppName:    appConfig.Name,
		RepoURL:    repoURL,
		CommitSHA:  payload.After,
		Branch:     branchFromRef(payload.Ref),
		Status:     "pending",
		ConfigJSON: string(configJSON),
		Host:       sanitizeHost(appConfig.Name) + "." + strings.TrimPrefix(s.cfg.BaseDomain, "."),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.hub.publish("deployment", deployment)
	writeJSON(w, http.StatusAccepted, deployment)
}

func (s *Server) handleAgentRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !s.authorizeAgent(w, r) {
		return
	}
	var req agentRegisterRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent id is required"})
		return
	}
	address := req.Address
	if address == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil {
			address = host
		}
	}
	err := s.store.UpsertAgent(r.Context(), store.Agent{
		ID:             req.ID,
		Hostname:       req.Hostname,
		Address:        address,
		CPUCapacity:    req.CPUCapacity,
		MemoryCapacity: req.MemoryCapacity,
		CPUUsed:        req.CPUUsed,
		MemoryUsed:     req.MemoryUsed,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "registered"})
}

func (s *Server) handleAgentSubroutes(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAgent(w, r) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/agents/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	agentID, action := parts[0], parts[1]
	switch action {
	case "heartbeat":
		s.handleAgentHeartbeat(w, r, agentID)
	case "tasks":
		s.handleAgentTasks(w, r, agentID)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request, agentID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req agentHeartbeatRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	address := req.Address
	if address == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil {
			address = host
		}
	}
	if err := s.store.UpdateAgentHeartbeat(r.Context(), agentID, address, req.CPUUsed, req.MemoryUsed); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAgentTasks(w http.ResponseWriter, r *http.Request, agentID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	deadline := time.Now().Add(s.cfg.TaskPollTimeout)
	for {
		task, err := s.store.ClaimNextTask(r.Context(), agentID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if task != nil {
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(task.PayloadJSON), &payload); err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			payload["id"] = task.ID
			payload["type"] = task.Type
			payload["deployment_id"] = task.DeploymentID
			_, _ = s.store.AddTaskEvent(r.Context(), store.TaskEvent{
				TaskID:       task.ID,
				DeploymentID: task.DeploymentID,
				Level:        "info",
				Message:      "task claimed by agent " + agentID,
			})
			writeJSON(w, http.StatusOK, payload)
			return
		}
		if time.Now().After(deadline) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (s *Server) handleTaskSubroutes(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAgent(w, r) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	taskID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid task id"})
		return
	}
	switch parts[1] {
	case "events":
		s.handleTaskEvent(w, r, taskID)
	case "complete":
		s.handleTaskComplete(w, r, taskID)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleTaskEvent(w http.ResponseWriter, r *http.Request, taskID int64) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	task, ok, err := s.store.GetTask(r.Context(), taskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	var req taskEventRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Level == "" {
		req.Level = "info"
	}
	event, err := s.store.AddTaskEvent(r.Context(), store.TaskEvent{
		TaskID:       task.ID,
		DeploymentID: task.DeploymentID,
		Level:        req.Level,
		Message:      req.Message,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.hub.publish("log", event)
	writeJSON(w, http.StatusAccepted, event)
}

func (s *Server) handleTaskComplete(w http.ResponseWriter, r *http.Request, taskID int64) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	task, ok, err := s.store.GetTask(r.Context(), taskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	var req taskCompleteRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Status != "succeeded" && req.Status != "failed" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "status must be succeeded or failed"})
		return
	}
	if err := s.store.CompleteTask(r.Context(), taskID, req.Status); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if req.Message != "" {
		_, _ = s.store.AddTaskEvent(r.Context(), store.TaskEvent{
			TaskID:       task.ID,
			DeploymentID: task.DeploymentID,
			Level:        req.Status,
			Message:      req.Message,
		})
	}
	deployment, ok, err := s.store.GetDeployment(r.Context(), task.DeploymentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "deployment not found"})
		return
	}

	if req.Status == "failed" {
		_ = s.store.UpdateDeploymentStatus(r.Context(), deployment.ID, "failed")
		s.hub.publish("deployment", map[string]interface{}{"id": deployment.ID, "status": "failed"})
		writeJSON(w, http.StatusOK, map[string]string{"status": "recorded"})
		return
	}

	switch task.Type {
	case "build":
		if err := s.enqueueRunTask(r.Context(), deployment, task.AgentID); err != nil {
			_ = s.store.UpdateDeploymentStatus(r.Context(), deployment.ID, "failed")
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	case "run":
		if err := s.finishRunTask(r.Context(), deployment, task.AgentID, req.Port); err != nil {
			_ = s.store.UpdateDeploymentStatus(r.Context(), deployment.ID, "failed")
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "recorded"})
}

func (s *Server) handleAppSubroutes(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAdmin(w, r) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/apps/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 || parts[1] != "secrets" {
		http.NotFound(w, r)
		return
	}
	appName := parts[0]
	if len(parts) == 2 && r.Method == http.MethodGet {
		keys, err := s.store.ListSecretKeys(r.Context(), appName)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"app": appName, "keys": keys})
		return
	}
	if len(parts) != 3 || r.Method != http.MethodPut {
		methodNotAllowed(w)
		return
	}
	key := parts[2]
	var req secretPutRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	nonce, ciphertext, err := s.vault.Encrypt(req.Value, appName+":"+key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.store.SetSecret(r.Context(), store.Secret{
		AppName:    appName,
		Key:        key,
		Nonce:      nonce,
		Ciphertext: ciphertext,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stored"})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	ctx := r.Context()
	deploymentCounts, err := s.store.DeploymentCounts(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	taskCounts, err := s.store.TaskCounts(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	agents, err := s.store.OnlineAgents(ctx, time.Now().Add(-s.cfg.OnlineWindow))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintln(w, "# HELP forge_deployments_total Deployments by state.")
	fmt.Fprintln(w, "# TYPE forge_deployments_total gauge")
	for status, count := range deploymentCounts {
		fmt.Fprintf(w, "forge_deployments_total{status=%q} %d\n", status, count)
	}
	fmt.Fprintln(w, "# HELP forge_tasks_total Tasks by state.")
	fmt.Fprintln(w, "# TYPE forge_tasks_total gauge")
	for status, count := range taskCounts {
		fmt.Fprintf(w, "forge_tasks_total{status=%q} %d\n", status, count)
	}
	fmt.Fprintln(w, "# HELP forge_agents_online Online worker agents.")
	fmt.Fprintln(w, "# TYPE forge_agents_online gauge")
	fmt.Fprintf(w, "forge_agents_online %d\n", len(agents))
}

func (s *Server) schedulerLoop(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.SchedulerTick)
	defer ticker.Stop()
	for {
		if err := s.schedulePending(ctx); err != nil {
			log.Printf("scheduler: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) schedulePending(ctx context.Context) error {
	deployments, err := s.store.ListDeploymentsByStatus(ctx, "pending")
	if err != nil {
		return err
	}
	if len(deployments) == 0 {
		return nil
	}
	agents, err := s.store.OnlineAgents(ctx, time.Now().Add(-s.cfg.OnlineWindow))
	if err != nil {
		return err
	}
	for _, deployment := range deployments {
		var appConfig forgeyaml.Config
		if err := json.Unmarshal([]byte(deployment.ConfigJSON), &appConfig); err != nil {
			_ = s.store.UpdateDeploymentStatus(ctx, deployment.ID, "failed")
			continue
		}
		agent, ok := chooseAgent(agents, appConfig.Resources.CPU, appConfig.Resources.MemoryBytes)
		if !ok {
			continue
		}
		payload, err := s.taskPayload(ctx, deployment, appConfig)
		if err != nil {
			_ = s.store.UpdateDeploymentStatus(ctx, deployment.ID, "failed")
			continue
		}
		payloadJSON, _ := json.Marshal(payload)
		if _, err := s.store.CreateTask(ctx, store.Task{
			DeploymentID: deployment.ID,
			AgentID:      agent.ID,
			Type:         "build",
			Status:       "pending",
			PayloadJSON:  string(payloadJSON),
		}); err != nil {
			return err
		}
		if err := s.store.UpdateDeploymentAssignment(ctx, deployment.ID, agent.ID, "building"); err != nil {
			return err
		}
		s.hub.publish("deployment", map[string]interface{}{"id": deployment.ID, "status": "building", "agent_id": agent.ID})
	}
	return nil
}

func (s *Server) enqueueRunTask(ctx context.Context, deployment store.Deployment, agentID string) error {
	var appConfig forgeyaml.Config
	if err := json.Unmarshal([]byte(deployment.ConfigJSON), &appConfig); err != nil {
		return err
	}
	usedPorts, err := s.store.UsedTargetPorts(ctx, agentID)
	if err != nil {
		return err
	}
	if deployment.TargetPort <= 0 || usedPorts[deployment.TargetPort] {
		port, err := chooseDeploymentPort(deployment.ID, usedPorts, s.cfg.AppPortStart, s.cfg.AppPortEnd)
		if err != nil {
			return err
		}
		if err := s.store.SetDeploymentTargetPort(ctx, deployment.ID, port); err != nil {
			return err
		}
		deployment.TargetPort = port
	}
	payload, err := s.taskPayload(ctx, deployment, appConfig)
	if err != nil {
		return err
	}
	payloadJSON, _ := json.Marshal(payload)
	if _, err := s.store.CreateTask(ctx, store.Task{
		DeploymentID: deployment.ID,
		AgentID:      agentID,
		Type:         "run",
		Status:       "pending",
		PayloadJSON:  string(payloadJSON),
	}); err != nil {
		return err
	}
	if err := s.store.UpdateDeploymentStatus(ctx, deployment.ID, "deploying"); err != nil {
		return err
	}
	s.hub.publish("deployment", map[string]interface{}{"id": deployment.ID, "status": "deploying", "agent_id": agentID})
	return nil
}

func (s *Server) finishRunTask(ctx context.Context, deployment store.Deployment, agentID string, port int) error {
	if port <= 0 {
		port = deployment.TargetPort
		if port <= 0 {
			var appConfig forgeyaml.Config
			if err := json.Unmarshal([]byte(deployment.ConfigJSON), &appConfig); err == nil {
				port = appConfig.Run.Port
			}
		}
	}
	agent, ok, err := s.store.GetAgent(ctx, agentID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("agent %s not found", agentID)
	}
	dial := fmt.Sprintf("%s:%d", agent.Address, port)
	if err := s.proxy.EnsureRoute(ctx, deployment.AppName, deployment.Host, dial); err != nil {
		return err
	}
	if err := s.store.MarkDeploymentRunning(ctx, deployment.ID, port); err != nil {
		return err
	}
	s.hub.publish("deployment", map[string]interface{}{
		"id":          deployment.ID,
		"status":      "running",
		"host":        deployment.Host,
		"target_port": port,
	})
	return nil
}

func (s *Server) taskPayload(ctx context.Context, deployment store.Deployment, appConfig forgeyaml.Config) (map[string]interface{}, error) {
	env := make(map[string]string)
	for _, key := range appConfig.Env {
		secret, ok, err := s.store.GetSecret(ctx, appConfig.Name, key)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		value, err := s.vault.Decrypt(secret.Nonce, secret.Ciphertext, appConfig.Name+":"+key)
		if err != nil {
			return nil, err
		}
		env[key] = value
	}
	return map[string]interface{}{
		"deployment_id":  deployment.ID,
		"app_name":       appConfig.Name,
		"repo_url":       deployment.RepoURL,
		"commit_sha":     deployment.CommitSHA,
		"branch":         deployment.Branch,
		"runtime":        appConfig.Runtime,
		"host":           deployment.Host,
		"workdir":        filepath.Join(s.cfg.DefaultAgentRoot, sanitizeHost(appConfig.Name), strconv.FormatInt(deployment.ID, 10)),
		"build_commands": appConfig.Build.Commands,
		"run_command":    appConfig.Run.Command,
		"port":           deploymentPort(deployment, appConfig),
		"resources": map[string]interface{}{
			"memory":       appConfig.Resources.Memory,
			"memory_bytes": appConfig.Resources.MemoryBytes,
			"cpu":          appConfig.Resources.CPU,
		},
		"health": map[string]interface{}{
			"path":     appConfig.Health.Path,
			"interval": appConfig.Health.Interval,
			"timeout":  appConfig.Health.Timeout,
			"retries":  appConfig.Health.Retries,
		},
		"env": env,
	}, nil
}

func deploymentPort(deployment store.Deployment, appConfig forgeyaml.Config) int {
	if deployment.TargetPort > 0 {
		return deployment.TargetPort
	}
	return appConfig.Run.Port
}

func chooseDeploymentPort(deploymentID int64, used map[int]bool, start int, end int) (int, error) {
	if start <= 0 || end <= 0 || start > end {
		start = 20000
		end = 39999
	}
	span := end - start + 1
	offset := 0
	if deploymentID > 0 {
		offset = int((deploymentID - 1) % int64(span))
	}
	for i := 0; i < span; i++ {
		port := start + ((offset + i) % span)
		if !used[port] {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free app ports in range %d-%d", start, end)
}

func (s *Server) cloneAndParseForgeYAML(ctx context.Context, repoURL string, commit string) (forgeyaml.Config, error) {
	target := filepath.Join(s.cfg.WorkDir, "repos", strconv.FormatInt(time.Now().UnixNano(), 10))
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return forgeyaml.Config{}, err
	}
	cloneCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cloneCtx, "git", "clone", "--depth=1", repoURL, target)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return forgeyaml.Config{}, fmt.Errorf("git clone failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if commit != "" {
		checkout := exec.CommandContext(cloneCtx, "git", "-C", target, "checkout", commit)
		if output, err := checkout.CombinedOutput(); err != nil {
			return forgeyaml.Config{}, fmt.Errorf("git checkout failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	data, err := os.ReadFile(filepath.Join(target, "forge.yaml"))
	if err != nil {
		return forgeyaml.Config{}, fmt.Errorf("read forge.yaml: %w", err)
	}
	return forgeyaml.Parse(data)
}

func chooseAgent(agents []store.Agent, requiredCPU float64, requiredMemory int64) (store.Agent, bool) {
	var chosen store.Agent
	bestScore := -1.0
	for _, agent := range agents {
		freeCPU := agent.CPUCapacity - agent.CPUUsed
		freeMemory := agent.MemoryCapacity - agent.MemoryUsed
		if agent.CPUCapacity > 0 && freeCPU < requiredCPU {
			continue
		}
		if agent.MemoryCapacity > 0 && freeMemory < requiredMemory {
			continue
		}
		score := freeCPU + float64(freeMemory)/(1024*1024*1024)
		if score > bestScore {
			bestScore = score
			chosen = agent
		}
	}
	return chosen, bestScore >= 0
}

func verifyGitHubSignature(body []byte, secret string, signature string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(signature, prefix) {
		return false
	}
	expectedMAC := hmac.New(sha256.New, []byte(secret))
	expectedMAC.Write(body)
	expected := make([]byte, len(prefix)+hex.EncodedLen(expectedMAC.Size()))
	copy(expected, prefix)
	hex.Encode(expected[len(prefix):], expectedMAC.Sum(nil))
	return hmac.Equal([]byte(signature), expected)
}

func branchFromRef(ref string) string {
	return strings.TrimPrefix(ref, "refs/heads/")
}

func sanitizeHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		isAllowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAllowed {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "app"
	}
	return out
}

func (s *Server) authorizeAgent(w http.ResponseWriter, r *http.Request) bool {
	if s.cfg.AgentToken == "" {
		return true
	}
	if bearerToken(r) == s.cfg.AgentToken || r.Header.Get("X-Forge-Agent-Token") == s.cfg.AgentToken {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid agent token"})
	return false
}

func (s *Server) authorizeAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.cfg.AdminToken == "" {
		return true
	}
	if bearerToken(r) == s.cfg.AdminToken {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid admin token"})
	return false
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
}

func readJSON(r *http.Request, target interface{}) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
}

type githubPushPayload struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Repository struct {
		CloneURL string `json:"clone_url"`
		HTMLURL  string `json:"html_url"`
		URL      string `json:"url"`
		FullName string `json:"full_name"`
	} `json:"repository"`
}

type agentRegisterRequest struct {
	ID             string  `json:"id"`
	Hostname       string  `json:"hostname"`
	Address        string  `json:"address"`
	CPUCapacity    float64 `json:"cpu_capacity"`
	MemoryCapacity int64   `json:"memory_capacity"`
	CPUUsed        float64 `json:"cpu_used"`
	MemoryUsed     int64   `json:"memory_used"`
}

type agentHeartbeatRequest struct {
	Address    string  `json:"address"`
	CPUUsed    float64 `json:"cpu_used"`
	MemoryUsed int64   `json:"memory_used"`
}

type taskEventRequest struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

type taskCompleteRequest struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	PID     int    `json:"pid"`
	Port    int    `json:"port"`
}

type secretPutRequest struct {
	Value string `json:"value"`
}
