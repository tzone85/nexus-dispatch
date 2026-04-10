package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// SubprocessClient implements Client by shelling out to an external command.
// The command receives a JSON-encoded CompletionRequest on stdin and must
// write a JSON-encoded CompletionResponse to stdout.
type SubprocessClient struct {
	command string
	timeout time.Duration
}

// NewSubprocessClient returns a SubprocessClient that invokes the given
// command with the specified timeout for each completion call.
func NewSubprocessClient(command string, timeout time.Duration) *SubprocessClient {
	return &SubprocessClient{command: command, timeout: timeout}
}

// Complete sends req as JSON to the subprocess stdin and parses the
// JSON response from stdout.
func (c *SubprocessClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, c.command)
	cmd.Stdin = bytes.NewReader(reqJSON)

	out, err := cmd.Output()
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("subprocess provider %s: %w", c.command, err)
	}

	var resp CompletionResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return CompletionResponse{}, fmt.Errorf("parse subprocess response: %w", err)
	}

	return resp, nil
}
