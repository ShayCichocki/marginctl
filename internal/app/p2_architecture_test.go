package app

import (
	"os"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/projectnav"
)

func TestCommandSideEffectsAreTypedIntents(t *testing.T) {
	cmd := command.NewRegistry()
	cmd.RegisterDefaults()
	result := cmd.Get("file.save").Execute(&command.Context{CurrentFile: "/tmp/chapter.md", EditorContent: "draft"})
	if _, ok := result.Intent.(command.SaveIntent); !ok {
		t.Fatalf("save command bypassed intent boundary: %#v", result)
	}
}

func TestStaleFilesystemResultCannotReplaceBuffer(t *testing.T) {
	m, _, ch2 := openTestModel(t)
	before := m.editor.Content()
	next, cmd := m.Update(projectnav.ChapterSelectedMsg{Path: ch2})
	m = asModel(next)
	stale := cmd()
	m.fsGen++
	m = update(m, stale)
	if m.editor.Content() != before {
		t.Fatal("stale filesystem result replaced current buffer")
	}
	if _, err := os.Stat(ch2); err != nil {
		t.Fatal(err)
	}
}

func TestRootUpdateDynamicTypeIsStablePointer(t *testing.T) {
	m := New()
	next, _ := m.Update(nil)
	if _, ok := next.(*Model); !ok {
		t.Fatalf("Update returned %T, want *app.Model", next)
	}
}
