package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
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
	if err := os.MkdirAll(s.cfg.WorkDir, 0750); err != nil {
		return err
	}
	go s.syncRunningRoutesWithRetry(ctx)
	go s.schedulerLoop(ctx)

	httpServer := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.routes(ctx),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { // #nosec G118 -- shutdown needs a fresh deadline after the parent context is canceled.
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

func (s *Server) syncRunningRoutesWithRetry(ctx context.Context) {
	for attempt := 1; attempt <= 5; attempt++ {
		if err := s.syncRunningRoutes(ctx); err != nil {
			log.Printf("caddy route sync failed on attempt %d: %v", attempt, err)
		} else {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (s *Server) syncRunningRoutes(ctx context.Context) error {
	if !s.proxy.Enabled() {
		return nil
	}
	deployments, err := s.store.ListDeploymentsByStatus(ctx, "running")
	if err != nil {
		return err
	}
	for _, deployment := range deployments {
		if deployment.AssignedAgentID == "" || deployment.TargetPort <= 0 || deployment.Host == "" {
			continue
		}
		agent, ok, err := s.store.GetAgent(ctx, deployment.AssignedAgentID)
		if err != nil {
			return err
		}
		if !ok || agent.Address == "" {
			continue
		}
		dial := fmt.Sprintf("%s:%d", agent.Address, deployment.TargetPort)
		if err := s.proxy.EnsureRoute(ctx, deployment.AppName, deployment.Host, dial); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) routes(ctx context.Context) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/api/v1/tls/ask", s.handleTLSAsk)
	mux.HandleFunc("/api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeAdmin(w, r) {
			return
		}
		s.hub.serve(ctx, w, r)
	})
	mux.HandleFunc("/api/v1/webhook/github", s.handleGitHubWebhook)
	mux.HandleFunc("/api/v1/agents/register", s.handleAgentRegister)
	mux.HandleFunc("/api/v1/agents/", s.handleAgentSubroutes)
	mux.HandleFunc("/api/v1/tasks/", s.handleTaskSubroutes)
	mux.HandleFunc("/api/v1/apps/", s.handleAppSubroutes)
	mux.HandleFunc("/api/v1/deployments", s.handleDeployments)
	mux.HandleFunc("/api/v1/deployments/", s.handleDeploymentSubroutes)
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
	views := make([]deploymentView, 0, len(deployments))
	for _, deployment := range deployments {
		views = append(views, deploymentView{
			ID:              deployment.ID,
			AppName:         deployment.AppName,
			RepoURL:         deployment.RepoURL,
			CommitSHA:       deployment.CommitSHA,
			Branch:          deployment.Branch,
			Status:          deployment.Status,
			AssignedAgentID: deployment.AssignedAgentID,
			Host:            deployment.Host,
			TargetPort:      deployment.TargetPort,
			CreatedAt:       deployment.CreatedAt,
			UpdatedAt:       deployment.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleDeploymentSubroutes(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAdmin(w, r) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/deployments/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[1] != "logs" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	deploymentID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid deployment id"})
		return
	}
	logs, err := s.store.ListTaskEventsByDeployment(r.Context(), deploymentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"deployment_id": deploymentID, "events": logs})
}

func (s *Server) handleTLSAsk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	domain := normalizeDomain(r.URL.Query().Get("domain"))
	allowed, err := s.tlsDomainAllowed(r.Context(), domain)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !allowed {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) tlsDomainAllowed(ctx context.Context, domain string) (bool, error) {
	if !validDNSName(domain) {
		return false, nil
	}
	base := normalizeDomain(s.cfg.BaseDomain)
	if domain == base {
		return true, nil
	}
	if !strings.HasSuffix(domain, "."+base) {
		return false, nil
	}
	return s.store.HasRunningDeploymentHost(ctx, domain)
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
	if !verifyGitHubSignature(body, s.cfg.WebhookSecret, r.Header.Get("X-Hub-Signature-256")) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid github signature"})
		return
	}
	event := r.Header.Get("X-GitHub-Event")
	if event != "push" {
		if event == "ping" {
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "pong"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported github event"})
		return
	}

	var payload githubPushPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !validCommitSHA(payload.After) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid commit sha"})
		return
	}
	branch, ok := branchFromRef(payload.Ref)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "only branch push events are supported"})
		return
	}
	if !s.branchAllowed(branch) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "branch is not allowed"})
		return
	}
	if !validRepoName(payload.Repository.FullName) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repository full_name"})
		return
	}
	if !s.repoAllowed(payload.Repository.FullName) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "repository is not allowed"})
		return
	}
	repoURL, err := s.repoCloneURL(payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	appConfig, err := s.cloneAndParseForgeYAML(r.Context(), repoURL, branch, payload.After)
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
		Branch:     branch,
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
		if taskAction(task.PayloadJSON) == "stop" {
			_ = s.store.UpdateDeploymentStatus(r.Context(), deployment.ID, "running")
		} else {
			_ = s.store.UpdateDeploymentStatus(r.Context(), deployment.ID, "failed")
		}
		if previous, ok, err := s.store.LatestRunningDeploymentByHostExcluding(r.Context(), deployment.Host, deployment.ID); err == nil && ok {
			if rollbackErr := s.rollbackDeploymentRoute(r.Context(), previous); rollbackErr != nil {
				log.Printf("rollback failed for deployment %d: %v", deployment.ID, rollbackErr)
			}
		}
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
		if taskAction(task.PayloadJSON) == "stop" {
			if err := s.store.UpdateDeploymentStatus(r.Context(), deployment.ID, "stopped"); err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			s.hub.publish("deployment", map[string]interface{}{"id": deployment.ID, "status": "stopped"})
			writeJSON(w, http.StatusOK, map[string]string{"status": "recorded"})
			return
		}
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
	if len(parts) != 3 || (r.Method != http.MethodPut && r.Method != http.MethodDelete) {
		methodNotAllowed(w)
		return
	}
	key := parts[2]
	if r.Method == http.MethodDelete {
		if err := s.store.DeleteSecret(r.Context(), appName, key); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
		return
	}
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
	if !s.authorizeLocalhostOrAdmin(w, r) {
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
	_, _ = fmt.Fprintln(w, "# HELP forge_deployments_total Deployments by state.")
	_, _ = fmt.Fprintln(w, "# TYPE forge_deployments_total gauge")
	for status, count := range deploymentCounts {
		_, _ = fmt.Fprintf(w, "forge_deployments_total{status=%q} %d\n", status, count)
	}
	_, _ = fmt.Fprintln(w, "# HELP forge_tasks_total Tasks by state.")
	_, _ = fmt.Fprintln(w, "# TYPE forge_tasks_total gauge")
	for status, count := range taskCounts {
		_, _ = fmt.Fprintf(w, "forge_tasks_total{status=%q} %d\n", status, count)
	}
	_, _ = fmt.Fprintln(w, "# HELP forge_agents_online Online worker agents.")
	_, _ = fmt.Fprintln(w, "# TYPE forge_agents_online gauge")
	_, _ = fmt.Fprintf(w, "forge_agents_online %d\n", len(agents))
}

func (s *Server) schedulerLoop(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.SchedulerTick)
	defer ticker.Stop()
	lastTaskEventPrune := time.Time{}
	for {
		if err := s.schedulePending(ctx); err != nil {
			log.Printf("scheduler: %v", err)
		}
		now := time.Now()
		if lastTaskEventPrune.IsZero() || now.Sub(lastTaskEventPrune) >= 24*time.Hour {
			if err := s.store.PruneTaskEventsBefore(ctx, now.Add(-30*24*time.Hour)); err != nil {
				log.Printf("task event retention: %v", err)
			} else {
				lastTaskEventPrune = now
			}
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
	activeTasks, err := s.store.ActiveTaskCountsByAgent(ctx)
	if err != nil {
		return err
	}
	scheduled := 0
	for _, deployment := range deployments {
		if scheduled >= s.cfg.MaxScheduleBatch {
			return nil
		}
		var appConfig forgeyaml.Config
		if err := json.Unmarshal([]byte(deployment.ConfigJSON), &appConfig); err != nil {
			_ = s.store.UpdateDeploymentStatus(ctx, deployment.ID, "failed")
			continue
		}
		agent, ok := chooseAgent(agentsWithTaskCapacity(agents, activeTasks, s.cfg.MaxTasksPerAgent), appConfig.Resources.CPU, appConfig.Resources.MemoryBytes)
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
		activeTasks[agent.ID]++
		scheduled++
		s.hub.publish("deployment", map[string]interface{}{"id": deployment.ID, "status": "building", "agent_id": agent.ID})
	}
	return nil
}

func agentsWithTaskCapacity(agents []store.Agent, activeTasks map[string]int, maxTasks int) []store.Agent {
	if maxTasks <= 0 {
		maxTasks = 1
	}
	available := make([]store.Agent, 0, len(agents))
	for _, agent := range agents {
		if activeTasks[agent.ID] < maxTasks {
			available = append(available, agent)
		}
	}
	return available
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
	previous, hasPrevious, err := s.store.LatestRunningDeploymentByHostExcluding(ctx, deployment.Host, deployment.ID)
	if err != nil {
		return err
	}
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
		if hasPrevious {
			if rollbackErr := s.rollbackDeploymentRoute(ctx, previous); rollbackErr != nil {
				return fmt.Errorf("ensure route failed: %w (rollback failed: %v)", err, rollbackErr)
			}
		}
		return err
	}
	if err := s.store.MarkDeploymentRunning(ctx, deployment.ID, port); err != nil {
		if hasPrevious {
			if rollbackErr := s.rollbackDeploymentRoute(ctx, previous); rollbackErr != nil {
				return fmt.Errorf("mark running failed: %w (rollback failed: %v)", err, rollbackErr)
			}
		}
		return err
	}
	if hasPrevious {
		if stopErr := s.enqueueStopTask(ctx, previous); stopErr != nil {
			log.Printf("stop enqueue failed for deployment %d: %v", previous.ID, stopErr)
		}
	}
	s.hub.publish("deployment", map[string]interface{}{
		"id":          deployment.ID,
		"status":      "running",
		"host":        deployment.Host,
		"target_port": port,
	})
	return nil
}

func (s *Server) enqueueStopTask(ctx context.Context, deployment store.Deployment) error {
	if deployment.AssignedAgentID == "" {
		return fmt.Errorf("deployment %d has no assigned agent", deployment.ID)
	}
	var appConfig forgeyaml.Config
	if err := json.Unmarshal([]byte(deployment.ConfigJSON), &appConfig); err != nil {
		return err
	}
	payload, err := s.taskPayload(ctx, deployment, appConfig)
	if err != nil {
		return err
	}
	payload["action"] = "stop"
	payload["run_command"] = ""
	payload["build_commands"] = []string{}
	payloadJSON, _ := json.Marshal(payload)
	if _, err := s.store.CreateTask(ctx, store.Task{
		DeploymentID: deployment.ID,
		AgentID:      deployment.AssignedAgentID,
		Type:         "run",
		Status:       "pending",
		PayloadJSON:  string(payloadJSON),
	}); err != nil {
		return err
	}
	return s.store.UpdateDeploymentStatus(ctx, deployment.ID, "stopping")
}

func (s *Server) rollbackDeploymentRoute(ctx context.Context, deployment store.Deployment) error {
	agent, ok, err := s.store.GetAgent(ctx, deployment.AssignedAgentID)
	if err != nil {
		return err
	}
	if !ok || agent.Address == "" || deployment.TargetPort <= 0 {
		return fmt.Errorf("previous deployment %d is not routable", deployment.ID)
	}
	dial := fmt.Sprintf("%s:%d", agent.Address, deployment.TargetPort)
	if err := s.proxy.EnsureRoute(ctx, deployment.AppName, deployment.Host, dial); err != nil {
		return err
	}
	s.hub.publish("deployment", map[string]interface{}{
		"id":          deployment.ID,
		"status":      "running",
		"host":        deployment.Host,
		"target_port": deployment.TargetPort,
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

func taskAction(payloadJSON string) string {
	if payloadJSON == "" {
		return ""
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return ""
	}
	action, _ := payload["action"].(string)
	return action
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

func (s *Server) cloneAndParseForgeYAML(ctx context.Context, repoURL string, branch string, commit string) (forgeyaml.Config, error) {
	target := filepath.Join(s.cfg.WorkDir, "repos", strconv.FormatInt(time.Now().UnixNano(), 10))
	defer func() { _ = os.RemoveAll(target) }()
	if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
		return forgeyaml.Config{}, err
	}
	cloneCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	// #nosec G204 -- branch and repoURL are validated before git sees them, and -- prevents repoURL from being parsed as a flag.
	cmd := exec.CommandContext(cloneCtx, "git", "clone", "--depth=1", "--branch", branch, "--", repoURL, target)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return forgeyaml.Config{}, fmt.Errorf("git clone failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if commit != "" {
		if !validCommitSHA(commit) {
			return forgeyaml.Config{}, fmt.Errorf("invalid commit sha")
		}
		checkout := exec.CommandContext(cloneCtx, "git", "-C", target, "checkout", "--detach", commit) // #nosec G204 -- commit is restricted to a hex object id.
		if output, err := checkout.CombinedOutput(); err != nil {
			return forgeyaml.Config{}, fmt.Errorf("git checkout failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	data, err := os.ReadFile(filepath.Join(target, "forge.yaml")) // #nosec G304 -- target is a freshly created clone directory under the configured work dir.
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
	if secret == "" {
		return false
	}
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

func branchFromRef(ref string) (string, bool) {
	const prefix = "refs/heads/"
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	branch := strings.TrimPrefix(ref, prefix)
	return branch, isSafeBranchName(branch)
}

func validCommitSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		return false
	}
	return true
}

func validRepoName(value string) bool {
	owner, repo, ok := strings.Cut(value, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return false
	}
	return safeRepoPart(owner) && safeRepoPart(repo)
}

func safeRepoPart(value string) bool {
	if value == "." || value == ".." || strings.HasPrefix(value, ".") || strings.HasPrefix(value, "-") {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func isSafeBranchName(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return false
	}
	if strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.Contains(value, "\\") {
		return false
	}
	if strings.HasSuffix(value, ".lock") {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '/', '.', '_', '-':
			continue
		default:
			return false
		}
	}
	return true
}

func (s *Server) repoAllowed(fullName string) bool {
	for _, allowed := range s.cfg.AllowedRepos {
		if strings.EqualFold(allowed, fullName) {
			return true
		}
	}
	return false
}

func (s *Server) branchAllowed(branch string) bool {
	for _, allowed := range s.cfg.AllowedBranches {
		if allowed == branch {
			return true
		}
	}
	return false
}

func (s *Server) repoCloneURL(payload githubPushPayload) (string, error) {
	if s.cfg.AllowLocalRepos && strings.HasPrefix(strings.ToLower(payload.Repository.FullName), "local/") {
		repoURL := strings.TrimSpace(payload.Repository.CloneURL)
		if repoURL == "" {
			repoURL = strings.TrimSpace(payload.Repository.URL)
		}
		if repoURL == "" {
			return "", fmt.Errorf("local repository reference rejected")
		}
		repoURL = strings.TrimPrefix(repoURL, "file://")
		if !filepath.IsAbs(repoURL) {
			return "", fmt.Errorf("local repository reference rejected")
		}
		repoURL = filepath.Clean(repoURL)
		allowedBase := filepath.Clean(os.TempDir())
		if repoURL != allowedBase && !strings.HasPrefix(repoURL, allowedBase+string(os.PathSeparator)) {
			return "", fmt.Errorf("local repository reference rejected")
		}
		return repoURL, nil
	}
	if !validRepoName(payload.Repository.FullName) {
		return "", fmt.Errorf("invalid repository full_name")
	}
	return "https://github.com/" + payload.Repository.FullName + ".git", nil
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

func normalizeDomain(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
}

func validDNSName(value string) bool {
	if value == "" || len(value) > 253 || strings.ContainsAny(value, "*:/\\") {
		return false
	}
	labels := strings.Split(value, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func (s *Server) authorizeAgent(w http.ResponseWriter, r *http.Request) bool {
	if constantTimeTokenEqual(bearerToken(r), s.cfg.AgentToken) || constantTimeTokenEqual(r.Header.Get("X-Forge-Agent-Token"), s.cfg.AgentToken) {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid agent token"})
	return false
}

func (s *Server) authorizeAdmin(w http.ResponseWriter, r *http.Request) bool {
	if constantTimeTokenEqual(bearerToken(r), s.cfg.AdminToken) {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid admin token"})
	return false
}

func (s *Server) authorizeLocalhostOrAdmin(w http.ResponseWriter, r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		ip := net.ParseIP(host)
		if ip != nil && ip.IsLoopback() {
			return true
		}
	}
	return s.authorizeAdmin(w, r)
}

func constantTimeTokenEqual(got string, want string) bool {
	if got == "" || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
}

func readJSON(r *http.Request, target interface{}) error {
	defer func() { _ = r.Body.Close() }()
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
	if status >= http.StatusInternalServerError {
		log.Printf("http %d: %v", status, err)
		writeJSON(w, status, map[string]string{"error": http.StatusText(status)})
		return
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

type deploymentView struct {
	ID              int64     `json:"id"`
	AppName         string    `json:"app_name"`
	RepoURL         string    `json:"repo_url"`
	CommitSHA       string    `json:"commit_sha"`
	Branch          string    `json:"branch"`
	Status          string    `json:"status"`
	AssignedAgentID string    `json:"assigned_agent_id"`
	Host            string    `json:"host"`
	TargetPort      int       `json:"target_port"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
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
