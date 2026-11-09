package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type PathEnv struct {
	GOOS        string
	XDGDataHome string
	HomeDir     string
	AppData     string
}

func DefaultPathEnv() PathEnv {
	home, _ := os.UserHomeDir()
	return PathEnv{
		GOOS:        runtime.GOOS,
		XDGDataHome: os.Getenv("XDG_DATA_HOME"),
		HomeDir:     home,
		AppData:     os.Getenv("APPDATA"),
	}
}

func AuthStorePath() (string, error) {
	return AuthStorePathFor(DefaultPathEnv())
}

func AuthStorePathFor(env PathEnv) (string, error) {
	switch env.GOOS {
	case "windows":
		if env.AppData == "" {
			return "", authStorePathError("APPDATA is unset")
		}
		return filepath.Join(env.AppData, "marginctl", "auth.json"), nil
	case "darwin":
		if env.HomeDir == "" {
			return "", authStorePathError("home directory is unavailable")
		}
		return filepath.Join(env.HomeDir, "Library", "Application Support", "marginctl", "auth.json"), nil
	default:
		if env.XDGDataHome != "" {
			return filepath.Join(env.XDGDataHome, "marginctl", "auth.json"), nil
		}
		if env.HomeDir == "" {
			return "", authStorePathError("XDG_DATA_HOME is unset and home directory is unavailable")
		}
		return filepath.Join(env.HomeDir, ".local", "share", "marginctl", "auth.json"), nil
	}
}

func authStorePathError(reason string) error {
	return fmt.Errorf("cannot determine marginctl auth store path: %w", errors.New(reason))
}
