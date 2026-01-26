package homeassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// LightState represents the state of a light entity.
type LightState struct {
	On         bool
	Brightness uint8 // 0-255
}

// Client is a Home Assistant API client.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient creates a new Home Assistant API client.
func NewClient(baseURL, token string) *Client {
	// Ensure baseURL doesn't have trailing slash
	baseURL = strings.TrimSuffix(baseURL, "/")

	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// CallService calls a Home Assistant service.
func (c *Client) CallService(ctx context.Context, domain, service string, data map[string]any) error {
	url := fmt.Sprintf("%s/api/services/%s/%s", c.baseURL, domain, service)

	var body []byte
	var err error
	if data != nil {
		body, err = json.Marshal(data)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("API error: %s", resp.Status)
	}

	return nil
}

// GetLightState fetches the current state of a light entity.
func (c *Client) GetLightState(ctx context.Context, entityID string) (LightState, error) {
	url := fmt.Sprintf("%s/api/states/%s", c.baseURL, entityID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return LightState{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return LightState{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return LightState{}, fmt.Errorf("API error: %s", resp.Status)
	}

	var data struct {
		State      string `json:"state"`
		Attributes struct {
			Brightness *int `json:"brightness"`
		} `json:"attributes"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return LightState{}, fmt.Errorf("failed to decode response: %w", err)
	}

	state := LightState{
		On: data.State == "on",
	}

	if data.Attributes.Brightness != nil {
		state.Brightness = uint8(*data.Attributes.Brightness)
	}

	return state, nil
}
