package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Caddy struct {
	adminURL string
	client   *http.Client
	mu       sync.Mutex
}

func NewCaddy(adminURL string) *Caddy {
	return &Caddy{
		adminURL: strings.TrimRight(adminURL, "/"),
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (c *Caddy) Enabled() bool {
	return c != nil && c.adminURL != ""
}

func (c *Caddy) EnsureRoute(ctx context.Context, appName string, host string, dial string) error {
	if !c.Enabled() {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cfg, err := c.fetchConfig(ctx)
	if err != nil {
		return err
	}
	routes := ensureRoutes(cfg)
	routeID := routeID(appName)
	filtered := make([]interface{}, 0, len(routes)+1)
	for _, route := range routes {
		if m, ok := route.(map[string]interface{}); ok && m["@id"] == routeID {
			continue
		}
		filtered = append(filtered, route)
	}
	appRoute := map[string]interface{}{
		"@id": routeID,
		"match": []interface{}{
			map[string]interface{}{"host": []interface{}{host}},
		},
		"handle": []interface{}{
			map[string]interface{}{
				"handler": "reverse_proxy",
				"upstreams": []interface{}{
					map[string]interface{}{"dial": dial},
				},
			},
		},
		"terminal": true,
	}
	filtered = append([]interface{}{appRoute}, filtered...)
	setRoutes(cfg, filtered)
	return c.putConfig(ctx, cfg)
}

func (c *Caddy) DeleteRoute(ctx context.Context, appName string) error {
	if !c.Enabled() {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cfg, err := c.fetchConfig(ctx)
	if err != nil {
		return err
	}
	routes := ensureRoutes(cfg)
	routeID := routeID(appName)
	filtered := make([]interface{}, 0, len(routes))
	for _, route := range routes {
		if m, ok := route.(map[string]interface{}); ok && m["@id"] == routeID {
			continue
		}
		filtered = append(filtered, route)
	}
	setRoutes(cfg, filtered)
	return c.putConfig(ctx, cfg)
}

func (c *Caddy) fetchConfig(ctx context.Context) (map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.adminURL+"/config/", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return baseConfig(), nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("caddy GET /config returned %s", resp.Status)
	}
	var cfg map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = baseConfig()
	}
	return cfg, nil
}

func (c *Caddy) putConfig(ctx context.Context, cfg map[string]interface{}) error {
	body, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.adminURL+"/load", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("caddy POST /load returned %s", resp.Status)
	}
	return nil
}

func baseConfig() map[string]interface{} {
	return map[string]interface{}{
		"apps": map[string]interface{}{
			"http": map[string]interface{}{
				"servers": map[string]interface{}{
					"srv0": map[string]interface{}{
						"listen": []interface{}{":80"},
						"routes": []interface{}{},
					},
				},
			},
		},
	}
}

func ensureRoutes(cfg map[string]interface{}) []interface{} {
	apps := ensureMap(cfg, "apps")
	httpApp := ensureMap(apps, "http")
	servers := ensureMap(httpApp, "servers")
	srv0 := ensureMap(servers, "srv0")
	if _, ok := srv0["listen"]; !ok {
		srv0["listen"] = []interface{}{":80"}
	}
	routes, ok := srv0["routes"].([]interface{})
	if !ok {
		routes = []interface{}{}
		srv0["routes"] = routes
	}
	return routes
}

func setRoutes(cfg map[string]interface{}, routes []interface{}) {
	apps := ensureMap(cfg, "apps")
	httpApp := ensureMap(apps, "http")
	servers := ensureMap(httpApp, "servers")
	srv0 := ensureMap(servers, "srv0")
	srv0["routes"] = routes
}

func ensureMap(parent map[string]interface{}, key string) map[string]interface{} {
	if child, ok := parent[key].(map[string]interface{}); ok {
		return child
	}
	child := map[string]interface{}{}
	parent[key] = child
	return child
}

func routeID(appName string) string {
	replacer := strings.NewReplacer(" ", "-", "/", "-", "_", "-")
	return "forge-" + replacer.Replace(strings.ToLower(appName))
}
