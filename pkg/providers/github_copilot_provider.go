package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	copilot "github.com/github/copilot-sdk/go"
)

type GitHubCopilotProvider struct {
	uri         string
	connectMode string // "stdio" or "grpc"

	client  *copilot.Client
	session *copilot.Session

	mu sync.Mutex

	// multi-turn sessions: picoclaw session ID -> copilot Session
	sessions    map[string]*copilot.Session
	sessionNext int
}

func NewGitHubCopilotProvider(uri string, connectMode string, model string) (*GitHubCopilotProvider, error) {
	if connectMode == "" {
		connectMode = "grpc"
	}

	switch connectMode {
	case "stdio":
		// TODO:
		return nil, fmt.Errorf("stdio mode not implemented")
	case "grpc":
		var opts *copilot.ClientOptions
		if uri != "" {
			opts = &copilot.ClientOptions{CLIUrl: uri}
		}
		client := copilot.NewClient(opts)
		if err := client.Start(context.Background()); err != nil {
			return nil, fmt.Errorf(
				"can't connect to Github Copilot: %w; `https://github.com/github/copilot-sdk/blob/main/docs/getting-started.md#connecting-to-an-external-cli-server` for details",
				err,
			)
		}

		session, err := client.CreateSession(context.Background(), &copilot.SessionConfig{
			Model: model,
			Hooks: &copilot.SessionHooks{},
		})
		if err != nil {
			client.Stop()
			return nil, fmt.Errorf("create session failed: %w", err)
		}

		return &GitHubCopilotProvider{
			uri:         uri,
			connectMode: connectMode,
			client:      client,
			session:     session,
			sessions:    make(map[string]*copilot.Session),
		}, nil
	default:
		return nil, fmt.Errorf("unknown connect mode: %s", connectMode)
	}
}

func (p *GitHubCopilotProvider) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil {
		p.client.Stop()
		p.client = nil
		p.session = nil
	}
}

func (p *GitHubCopilotProvider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	type tempMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	out := make([]tempMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, tempMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	fullcontent, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("marshal messages: %w", err)
	}
	p.mu.Lock()
	session := p.session
	p.mu.Unlock()

	if session == nil {
		return nil, fmt.Errorf("provider closed")
	}

	resp, _ := session.SendAndWait(ctx, copilot.MessageOptions{
		Prompt: string(fullcontent),
	})

	if resp == nil {
		return nil, fmt.Errorf("empty response from copilot")
	}
	if resp.Data.Content == nil {
		return nil, fmt.Errorf("no content in copilot response")
	}
	content := *resp.Data.Content

	return &LLMResponse{
		FinishReason: "stop",
		Content:      content,
	}, nil
}

func (p *GitHubCopilotProvider) GetDefaultModel() string {
	return "gpt-4.1"
}

// OpenSession creates a new multi-turn copilot session and returns its picoclaw ID.
// Each session costs 1 GitHub Copilot premium credit; subsequent SendToSession calls are free.
func (p *GitHubCopilotProvider) OpenSession(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client == nil {
		return "", fmt.Errorf("copilot provider closed")
	}

	sess, err := p.client.CreateSession(ctx, &copilot.SessionConfig{
		Model: p.GetDefaultModel(),
		Hooks: &copilot.SessionHooks{},
	})
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}

	p.sessionNext++
	id := fmt.Sprintf("cs-%d", p.sessionNext)
	p.sessions[id] = sess
	return id, nil
}

// SendToSession sends a message to an open multi-turn session and returns the response.
// This does NOT cost an additional credit — it reuses the existing session.
func (p *GitHubCopilotProvider) SendToSession(ctx context.Context, sessionID, prompt string) (string, error) {
	p.mu.Lock()
	sess, ok := p.sessions[sessionID]
	p.mu.Unlock()

	if !ok {
		return "", fmt.Errorf("session %q not found (use copilot_start first)", sessionID)
	}

	resp, err := sess.SendAndWait(ctx, copilot.MessageOptions{Prompt: prompt})
	if err != nil {
		return "", fmt.Errorf("send to session %s: %w", sessionID, err)
	}
	if resp == nil || resp.Data.Content == nil {
		return "", fmt.Errorf("empty response from session %s", sessionID)
	}
	return *resp.Data.Content, nil
}

// CloseSession destroys an open multi-turn session and frees its resources.
func (p *GitHubCopilotProvider) CloseSession(ctx context.Context, sessionID string) error {
	p.mu.Lock()
	sess, ok := p.sessions[sessionID]
	if ok {
		delete(p.sessions, sessionID)
	}
	p.mu.Unlock()

	if !ok {
		return fmt.Errorf("session %q not found", sessionID)
	}
	return sess.Destroy()
}
