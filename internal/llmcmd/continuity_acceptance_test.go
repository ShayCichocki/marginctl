package llmcmd

import (
	"context"
	"strings"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/config"
	"github.com/ShayCichocki/marginctl/internal/llm"
)

func TestContinuityFailsClosedOnStateReadError(t *testing.T) {
	c := llm.NewClient(testConfig())
	ctx := &command.Context{StateFilePaths: map[string]string{"characters/ada": t.TempDir() + "/missing.md"}}
	msg := ExecuteContinuityCheck(c, ctx, context.Background())().(ResponseMsg)
	if msg.Result.Type != command.ResultError || !strings.Contains(msg.Result.Content, "characters/ada") {
		t.Fatalf("result = %#v", msg.Result)
	}
}

func testConfig() config.LLMConfig {
	return config.LLMConfig{Provider: config.ProviderAnthropic, APIKey: "test-key"}
}
