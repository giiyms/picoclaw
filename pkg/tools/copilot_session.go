package tools

import (
	"context"
	"fmt"
)

// CopilotSessionHost is implemented by GitHubCopilotProvider.
// It exposes multi-turn session management separately from the single-shot Chat() path.
type CopilotSessionHost interface {
	OpenSession(ctx context.Context) (string, error)
	SendToSession(ctx context.Context, sessionID, prompt string) (string, error)
	CloseSession(ctx context.Context, sessionID string) error
}

// ── copilot_start ────────────────────────────────────────────────────────────

// CopilotStartTool opens a new GitHub Copilot multi-turn session (costs 1 credit).
type CopilotStartTool struct{ host CopilotSessionHost }

func NewCopilotStartTool(host CopilotSessionHost) *CopilotStartTool {
	return &CopilotStartTool{host: host}
}

func (t *CopilotStartTool) Name() string { return "copilot_start" }

func (t *CopilotStartTool) Description() string {
	return "Open a persistent GitHub Copilot session for a complex task that needs steering. " +
		"Returns a session_id you must pass to copilot_send/copilot_stop. " +
		"Costs 1 Copilot premium credit regardless of how many copilot_send calls follow."
}

func (t *CopilotStartTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
		"required":   []string{},
	}
}

func (t *CopilotStartTool) Execute(ctx context.Context, _ map[string]any) *ToolResult {
	id, err := t.host.OpenSession(ctx)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to open copilot session: %v", err)).WithError(err)
	}
	msg := fmt.Sprintf("Copilot session opened. session_id: %s\nUse copilot_send to interact, copilot_stop when done.", id)
	return &ToolResult{ForLLM: msg, ForUser: msg}
}

// ── copilot_send ─────────────────────────────────────────────────────────────

// CopilotSendTool sends a message (task or steering instruction) into an open session.
type CopilotSendTool struct{ host CopilotSessionHost }

func NewCopilotSendTool(host CopilotSessionHost) *CopilotSendTool {
	return &CopilotSendTool{host: host}
}

func (t *CopilotSendTool) Name() string { return "copilot_send" }

func (t *CopilotSendTool) Description() string {
	return "Send a message or steering instruction to an open Copilot session and wait for its response. " +
		"Does NOT cost additional credits — uses the existing session opened by copilot_start. " +
		"Use this to give initial tasks, follow-up instructions, clarifications, or course corrections."
}

func (t *CopilotSendTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{
				"type":        "string",
				"description": "The session_id returned by copilot_start",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "The message, task, or steering instruction to send",
			},
		},
		"required": []string{"session_id", "message"},
	}
}

func (t *CopilotSendTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	sessionID, ok := args["session_id"].(string)
	if !ok || sessionID == "" {
		return ErrorResult("session_id is required").WithError(fmt.Errorf("missing session_id"))
	}
	message, ok := args["message"].(string)
	if !ok || message == "" {
		return ErrorResult("message is required").WithError(fmt.Errorf("missing message"))
	}

	resp, err := t.host.SendToSession(ctx, sessionID, message)
	if err != nil {
		return ErrorResult(fmt.Sprintf("copilot_send failed: %v", err)).WithError(err)
	}

	// Truncate for user display; LLM gets full response
	userResp := resp
	if len(userResp) > 800 {
		userResp = userResp[:800] + "\n…(truncated, full response available to orchestrator)"
	}
	return &ToolResult{
		ForLLM:  fmt.Sprintf("[Copilot session %s response]\n%s", sessionID, resp),
		ForUser: userResp,
	}
}

// ── copilot_stop ─────────────────────────────────────────────────────────────

// CopilotStopTool closes and destroys an open Copilot session.
type CopilotStopTool struct{ host CopilotSessionHost }

func NewCopilotStopTool(host CopilotSessionHost) *CopilotStopTool {
	return &CopilotStopTool{host: host}
}

func (t *CopilotStopTool) Name() string { return "copilot_stop" }

func (t *CopilotStopTool) Description() string {
	return "Close and destroy an open Copilot session when the task is complete. Always call this when done."
}

func (t *CopilotStopTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{
				"type":        "string",
				"description": "The session_id to close",
			},
		},
		"required": []string{"session_id"},
	}
}

func (t *CopilotStopTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	sessionID, ok := args["session_id"].(string)
	if !ok || sessionID == "" {
		return ErrorResult("session_id is required").WithError(fmt.Errorf("missing session_id"))
	}

	if err := t.host.CloseSession(ctx, sessionID); err != nil {
		return ErrorResult(fmt.Sprintf("failed to close session %s: %v", sessionID, err)).WithError(err)
	}

	msg := fmt.Sprintf("Copilot session %s closed.", sessionID)
	return &ToolResult{ForLLM: msg, ForUser: msg}
}
