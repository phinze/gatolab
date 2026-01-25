package nowplaying

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"os/exec"
	"sync"
	"time"
)

// NowPlaying represents the media-control JSON output (with --micros flag)
type NowPlaying struct {
	Title                string `json:"title"`
	Artist               string `json:"artist"`
	Album                string `json:"album"`
	DurationMicros       int64  `json:"durationMicros"`
	ElapsedTimeMicros    int64  `json:"elapsedTimeMicros"`
	TimestampEpochMicros int64  `json:"timestampEpochMicros"`
	Playing              bool   `json:"playing"`
	ArtworkData          string `json:"artworkData"`
	ArtworkMime          string `json:"artworkMimeType"`
}

// liveState wraps NowPlaying with thread-safe access.
type liveState struct {
	sync.RWMutex
	NowPlaying
}

// newLiveState creates a new liveState.
func newLiveState() *liveState {
	return &liveState{}
}

// get returns a copy of the current state.
func (s *liveState) get() NowPlaying {
	s.RLock()
	defer s.RUnlock()
	return s.NowPlaying
}

// StreamPayload wraps the stream JSON structure with raw payload for proper merging.
type StreamPayload struct {
	Diff    bool            `json:"diff"`
	Payload json.RawMessage `json:"payload"`
}

// startMediaStream runs the media-control stream and updates state.
func (m *Module) startMediaStream(ctx context.Context) {
	cmd := exec.CommandContext(ctx, "media-control", "stream", "--micros")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("Failed to get stdout pipe: %v", err)
		return
	}

	if err := cmd.Start(); err != nil {
		log.Printf("Failed to start media-control stream: %v", err)
		return
	}

	log.Println("Started media-control stream")

	scanner := bufio.NewScanner(stdout)
	// Increase buffer size for large artwork payloads
	buf := make([]byte, 0, 1024*1024) // 1MB buffer
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		var envelope StreamPayload
		if err := json.Unmarshal(line, &envelope); err != nil {
			continue
		}

		// Parse payload as a map to see which fields are present
		var payloadMap map[string]interface{}
		if err := json.Unmarshal(envelope.Payload, &payloadMap); err != nil {
			continue
		}

		m.liveState.Lock()
		if !envelope.Diff && len(payloadMap) == 0 {
			// Reset to defaults
			m.liveState.NowPlaying = NowPlaying{
				Title:                "?",
				Artist:               "?",
				TimestampEpochMicros: time.Now().UnixMicro(),
			}
		} else {
			// Merge only fields that are present in the payload
			mergePayloadMap(&m.liveState.NowPlaying, payloadMap)
		}
		m.liveState.Unlock()
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Scanner error: %v", err)
	}

	cmd.Wait()
}

// mergePayloadMap merges a map of fields into a NowPlaying struct.
func mergePayloadMap(dst *NowPlaying, src map[string]interface{}) {
	if v, ok := src["title"].(string); ok {
		dst.Title = v
	}
	if v, ok := src["artist"].(string); ok {
		dst.Artist = v
	}
	if v, ok := src["album"].(string); ok {
		dst.Album = v
	}
	if v, ok := src["durationMicros"].(float64); ok {
		dst.DurationMicros = int64(v)
	}
	if v, ok := src["elapsedTimeMicros"].(float64); ok {
		dst.ElapsedTimeMicros = int64(v)
	}
	if v, ok := src["timestampEpochMicros"].(float64); ok {
		dst.TimestampEpochMicros = int64(v)
	}
	// Only update playing if it's actually present in the payload
	if v, ok := src["playing"].(bool); ok {
		dst.Playing = v
	}
	if v, ok := src["artworkData"].(string); ok {
		dst.ArtworkData = v
	}
	if v, ok := src["artworkMimeType"].(string); ok {
		dst.ArtworkMime = v
	}
}

// getLiveElapsedMicros calculates the live elapsed time based on timestamp and playing state.
func getLiveElapsedMicros(np *NowPlaying) int64 {
	if !np.Playing {
		return np.ElapsedTimeMicros
	}
	// Calculate: elapsed + (now - timestamp)
	nowMicros := time.Now().UnixMicro()
	timeDiff := nowMicros - np.TimestampEpochMicros
	return np.ElapsedTimeMicros + timeDiff
}
