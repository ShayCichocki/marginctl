package app

import (
	"path/filepath"
	"strings"

	"github.com/ShayCichocki/marginctl/internal/auth"
	"github.com/ShayCichocki/marginctl/internal/notification"
)

// setNotice shows a transient, non-error bottom-line message.
func (m *Model) setNotice(text string) {
	m.notice, m.noticeIsErr = text, false
}

// setErrorNotice shows a transient, error-styled bottom-line message. It does
// not set m.err and is never promoted to a persistent notification.
func (m *Model) setErrorNotice(text string) {
	m.notice, m.noticeIsErr = text, true
}

// clearNotice removes the transient bottom-line message and its error styling.
func (m *Model) clearNotice() {
	m.notice, m.noticeIsErr = "", false
}

// reportConfigRewrite tells the user a config save fell back to a full rewrite.
// The one-row bottom bar leads with a concise line naming the backup file; the
// full explanation (absolute backup path, what was lost) is pushed as a
// transient Warning notification, inspectable with :notifications but never
// drawn in the bar (see View), so it cannot stick or mask later messages.
// It reports whether it handled the notice (false when nothing was rewritten).
func (m *Model) reportConfigRewrite(backup, saveNotice, context string) bool {
	if backup == "" && saveNotice == "" {
		return false
	}
	name := "config.toml.bak-*"
	if backup != "" {
		name = filepath.Base(backup)
	}
	summary := "Config rewritten; backup: " + name + " (:notifications)"
	details := context
	if saveNotice != "" {
		details += "\n\n" + saveNotice
	}
	if backup != "" && !strings.Contains(details, backup) {
		details += "\n\nOriginal saved to " + backup
	}
	m.notifications.Push(notification.Notification{
		Severity:    notification.Warning,
		Summary:     summary,
		Details:     auth.RedactSecrets(details),
		Operation:   "config rewrite",
		Persistence: notification.Transient,
	})
	m.setNotice(summary)
	return true
}
