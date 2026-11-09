package auth

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/config"
)

func TestActivationWritesChatGPTProviderMetadata_whenEnvProviderUnset(t *testing.T) {
	// Given
	var wroteProvider string
	writeProvider := func(provider string) (config.SaveResult, error) {
		wroteProvider = provider
		return config.SaveResult{Path: filepath.Join(t.TempDir(), "config.toml"), Backup: "/x/config.toml.bak-1", Notice: "rewritten"}, nil
	}

	// When
	result, err := ActivateChatGPT(writeProvider, "")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if !result.Activated {
		t.Fatalf("Activated = false, want true; notice=%q", result.Notice)
	}
	if wroteProvider != config.ProviderChatGPT {
		t.Fatalf("wrote provider = %q, want chatgpt", wroteProvider)
	}
	if !strings.Contains(result.Notice, "chatgpt") {
		t.Fatalf("notice = %q, want chatgpt activation detail", result.Notice)
	}
	if result.Backup != "/x/config.toml.bak-1" || result.SaveNotice != "rewritten" {
		t.Fatalf("backup/save notice not carried: %+v", result)
	}
}

func TestActivationBlocksChatGPTProviderMetadata_whenEnvProviderOverrideSet(t *testing.T) {
	// Given
	writes := 0
	writeProvider := func(provider string) (config.SaveResult, error) {
		writes++
		return config.SaveResult{}, errors.New("writer must not be called")
	}

	// When
	result, err := ActivateChatGPT(writeProvider, "openai")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if result.Activated {
		t.Fatal("Activated = true, want false because env provider override blocks activation")
	}
	if writes != 0 {
		t.Fatalf("writer calls = %d, want 0", writes)
	}
	if !strings.Contains(result.Notice, "MARGINCTL_LLM_PROVIDER") || !strings.Contains(result.Notice, "openai") {
		t.Fatalf("notice = %q, want clear env override explanation", result.Notice)
	}
}

func TestAuthStorePathResolvesPlatformDataLocations(t *testing.T) {
	tempData := t.TempDir()
	tests := []struct {
		name string
		env  PathEnv
		want string
	}{
		{
			name: "linux xdg data home",
			env:  PathEnv{GOOS: "linux", XDGDataHome: "/tmp/xdg-data", HomeDir: "/tmp/home"},
			want: filepath.Join("/tmp/xdg-data", "marginctl", "auth.json"),
		},
		{
			name: "linux home fallback",
			env:  PathEnv{GOOS: "linux", HomeDir: "/tmp/home"},
			want: filepath.Join("/tmp/home", ".local", "share", "marginctl", "auth.json"),
		},
		{
			name: "unix home fallback",
			env:  PathEnv{GOOS: "freebsd", HomeDir: "/tmp/home"},
			want: filepath.Join("/tmp/home", ".local", "share", "marginctl", "auth.json"),
		},
		{
			name: "macos application support",
			env:  PathEnv{GOOS: "darwin", HomeDir: "/tmp/home"},
			want: filepath.Join("/tmp/home", "Library", "Application Support", "marginctl", "auth.json"),
		},
		{
			name: "windows appdata",
			env:  PathEnv{GOOS: "windows", AppData: `C:\Users\me\AppData\Roaming`},
			want: filepath.Join(`C:\Users\me\AppData\Roaming`, "marginctl", "auth.json"),
		},
		{
			name: "injected temp path",
			env:  PathEnv{GOOS: "linux", XDGDataHome: tempData, HomeDir: filepath.Join(t.TempDir(), "home")},
			want: filepath.Join(tempData, "marginctl", "auth.json"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			got, err := AuthStorePathFor(tt.env)

			// Then
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("path = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuthStorePathReturnsActionableError_whenRequiredBaseUnset(t *testing.T) {
	tests := []struct {
		name string
		env  PathEnv
	}{
		{name: "linux missing home", env: PathEnv{GOOS: "linux"}},
		{name: "macos missing home", env: PathEnv{GOOS: "darwin"}},
		{name: "windows missing appdata", env: PathEnv{GOOS: "windows"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			path, err := AuthStorePathFor(tt.env)

			// Then
			if err == nil {
				t.Fatalf("err = nil, want actionable error for path %q", path)
			}
			if path != "" {
				t.Fatalf("path = %q, want empty on failure", path)
			}
			if !strings.Contains(err.Error(), "cannot determine marginctl auth store path") {
				t.Fatalf("err = %q, want actionable auth store path message", err.Error())
			}
		})
	}
}
