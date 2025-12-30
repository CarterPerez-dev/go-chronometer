/*
ⒸAngelaMos | 2025
main.go
*/

package main

import (
	"embed"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

//go:embed static/index.html
var staticFiles embed.FS

type TimerState struct {
	StartTime     int64 `json:"start_time"`
	StoppedAt     int64 `json:"stopped_at"`
	OffsetSeconds int64 `json:"offset_seconds"`
	IsRunning     bool  `json:"is_running"`
}

type TimerResponse struct {
	IsRunning        bool   `json:"is_running"`
	ElapsedSeconds   int64  `json:"elapsed_seconds"`
	ElapsedFormatted string `json:"elapsed_formatted"`
}

type StartRequest struct {
	OffsetHours float64 `json:"offset_hours"`
}

var (
	state     TimerState
	stateMu   sync.RWMutex
	stateFile = "timer.json"
)

func loadState() error {
	stateMu.Lock()
	defer stateMu.Unlock()

	data, err := os.ReadFile(stateFile)
	if os.IsNotExist(err) {
		state = TimerState{}
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &state)
}

func saveState() error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(stateFile, data, 0644)
}

func getElapsed() int64 {
	if !state.IsRunning && state.StartTime == 0 {
		return state.OffsetSeconds
	}

	var elapsed int64
	if state.IsRunning {
		elapsed = time.Now().Unix() - state.StartTime
	} else {
		elapsed = state.StoppedAt - state.StartTime
	}
	return elapsed + state.OffsetSeconds
}

func formatElapsed(seconds int64) string {
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60

	result := ""
	if hours > 0 {
		result = string(rune('0'+hours/100)) + string(rune('0'+hours/10%10)) + string(rune('0'+hours%10))
		for len(result) > 1 && result[0] == '0' {
			result = result[1:]
		}
		result += ":"
	}

	if hours > 0 {
		result += string(rune('0'+minutes/10)) + string(rune('0'+minutes%10))
	} else {
		result += string(rune('0'+minutes/10)) + string(rune('0'+minutes%10))
	}
	result += ":"
	result += string(rune('0'+secs/10)) + string(rune('0'+secs%10))

	return result
}

func handleGetTimer(w http.ResponseWriter, r *http.Request) {
	stateMu.RLock()
	elapsed := getElapsed()
	running := state.IsRunning
	stateMu.RUnlock()

	resp := TimerResponse{
		IsRunning:        running,
		ElapsedSeconds:   elapsed,
		ElapsedFormatted: formatElapsed(elapsed),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func handleStart(w http.ResponseWriter, r *http.Request) {
	var req StartRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
	}

	stateMu.Lock()
	defer stateMu.Unlock()

	if state.IsRunning {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "already running"})
		return
	}

	now := time.Now().Unix()

	if state.StoppedAt > 0 {
		pausedDuration := state.StoppedAt - state.StartTime
		state.OffsetSeconds += pausedDuration
		state.StoppedAt = 0
	}

	if req.OffsetHours > 0 {
		state.OffsetSeconds = int64(req.OffsetHours * 3600)
	}

	state.StartTime = now
	state.IsRunning = true

	if err := saveState(); err != nil {
		slog.Error("failed to save state", "error", err)
		http.Error(w, "failed to save state", http.StatusInternalServerError)
		return
	}

	slog.Info("timer started", "offset_hours", req.OffsetHours)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

func handleStop(w http.ResponseWriter, r *http.Request) {
	stateMu.Lock()
	defer stateMu.Unlock()

	if !state.IsRunning {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "already stopped"})
		return
	}

	state.StoppedAt = time.Now().Unix()
	state.IsRunning = false

	if err := saveState(); err != nil {
		slog.Error("failed to save state", "error", err)
		http.Error(w, "failed to save state", http.StatusInternalServerError)
		return
	}

	slog.Info("timer stopped")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
}

func handleReset(w http.ResponseWriter, r *http.Request) {
	stateMu.Lock()
	defer stateMu.Unlock()

	state = TimerState{}

	if err := saveState(); err != nil {
		slog.Error("failed to save state", "error", err)
		http.Error(w, "failed to save state", http.StatusInternalServerError)
		return
	}

	slog.Info("timer reset")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "reset"})
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	data, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "index not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func main() {
	if err := loadState(); err != nil {
		slog.Error("failed to load state", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", handleIndex)
	mux.HandleFunc("GET /api/timer", handleGetTimer)
	mux.HandleFunc("POST /api/start", handleStart)
	mux.HandleFunc("POST /api/stop", handleStop)
	mux.HandleFunc("POST /api/reset", handleReset)

	addr := ":8329"
	slog.Info("server starting", "addr", "http://localhost"+addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}
