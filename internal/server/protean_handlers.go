package server

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Leihb/octo-agent/internal/app"
	"github.com/Leihb/octo-agent/internal/config"
	"github.com/Leihb/octo-agent/internal/protean"
	"github.com/Leihb/octo-agent/internal/tools"
)

// proteanSkillInfo is the web UI's view of a Protean skill.
type proteanSkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// proteanRecordState holds the in-flight recorder started from the web UI.
// It is process-local; a server restart aborts any active recording.
var proteanRecordState struct {
	recorder *protean.Recorder
	startAt  time.Time
}

// handleProteanInfo reports whether Protean is available and where it lives.
func (s *Server) handleProteanInfo(w http.ResponseWriter, r *http.Request) {
	_ = r
	cfg, _ := config.Load()
	bridge := protean.NewBridge(cfg.Protean)
	writeJSON(w, http.StatusOK, bridge.Info())
}

// handleProteanListSkills lists generated Protean skills on disk.
func (s *Server) handleProteanListSkills(w http.ResponseWriter, r *http.Request) {
	_ = r
	cfg, _ := config.Load()
	bridge := protean.NewBridge(cfg.Protean)
	names, err := bridge.ListSkills()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]proteanSkillInfo, 0, len(names))
	for _, name := range names {
		out = append(out, proteanSkillInfo{Name: name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": out})
}

// handleProteanRecordStart begins a Protean screen recording.
func (s *Server) handleProteanRecordStart(w http.ResponseWriter, r *http.Request) {
	cfg, _ := config.Load()
	bridge := protean.NewBridge(cfg.Protean)
	if !bridge.Available() {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("Protean not available at %s", bridge.Venv()))
		return
	}
	if proteanRecordState.recorder != nil {
		writeError(w, http.StatusConflict, "recording already in progress")
		return
	}
	outDir := bridge.RecordingsDir()
	rec := protean.NewRecorder(bridge, outDir)
	if err := rec.Start(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	proteanRecordState.recorder = rec
	proteanRecordState.startAt = time.Now()
	writeJSON(w, http.StatusOK, map[string]any{
		"recording_id": filepath.Base(outDir),
		"out_dir":      outDir,
	})
}

// handleProteanRecordStop stops the active recording.
func (s *Server) handleProteanRecordStop(w http.ResponseWriter, r *http.Request) {
	_ = r
	if proteanRecordState.recorder == nil {
		writeError(w, http.StatusConflict, "no recording in progress")
		return
	}
	startAt := proteanRecordState.startAt
	if err := proteanRecordState.recorder.Stop(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	outDir := proteanRecordState.recorder.OutDir
	proteanRecordState.recorder = nil
	writeJSON(w, http.StatusOK, map[string]any{
		"out_dir":  outDir,
		"duration": time.Since(startAt).Seconds(),
	})
}

// handleProteanGenerate turns a stopped recording into a Protean skill using
// the server's current model config (the one octo is already using).
func (s *Server) handleProteanGenerate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RecordingDir string `json:"recording_dir"`
		TaskDesc     string `json:"task_desc"`
	}
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.RecordingDir == "" {
		writeError(w, http.StatusBadRequest, "recording_dir is required")
		return
	}

	cfg, _ := config.Load()
	entry := cfg.DefaultEntry()
	if s.sender != nil {
		// Prefer the model the server was started with when it differs from the
		// config default; this keeps generated skills aligned with what the user
		// actually sees in the UI.
		if s.model != "" {
			entry.Model = s.model
		}
	}
	if entry.Provider == "" || entry.Model == "" {
		writeError(w, http.StatusServiceUnavailable, "no model configured; set up a model in Settings first")
		return
	}

	sender := s.sender
	if sender == nil {
		showReasoning := false
		if entry.ShowReasoning != nil {
			showReasoning = *entry.ShowReasoning
		}
		built, err := app.NewSender(app.SenderOptions{
			Provider:        entry.Provider,
			APIKey:          entry.APIKey,
			BaseURL:         entry.BaseURL,
			ReasoningEffort: entry.ReasoningEffort,
			ShowReasoning:   showReasoning,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("build sender: %v", err))
			return
		}
		sender = built
	}

	bridge := protean.NewBridge(cfg.Protean)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	res, err := bridge.Generate(ctx, req.RecordingDir, strings.TrimSpace(req.TaskDesc), entry.Model, sender)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleProteanRun executes a named Protean skill with the deterministic
// step_by_step executor.
func (s *Server) handleProteanRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	cfg, _ := config.Load()
	bridge := protean.NewBridge(cfg.Protean)
	if !bridge.Available() {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("Protean not available at %s", bridge.Venv()))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	res, err := bridge.RunSkill(ctx, req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !res.Success {
		writeError(w, http.StatusInternalServerError, res.Error)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// injectProteanBridge lets the server pass a custom bridge to the tools
// package (used in tests).
func injectProteanBridge(b *protean.Bridge) {
	tools.SetProteanBridge(b)
}
