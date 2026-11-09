package editor

import (
	"strings"
)

// Note is a comment attached to a span of text. It anchors to the text itself
// (Anchor — the first line of the original selection) rather than fixed offsets,
// so it survives edits above/around it and a reopen. Row/Col are the last
// resolved position; Orphaned is set when the anchor text can no longer be found.
type Note struct {
	Anchor   string `json:"anchor"`
	Body     string `json:"body"`
	Row      int    `json:"row"`
	Col      int    `json:"col"`
	Orphaned bool   `json:"orphaned,omitempty"`
	Resolved bool   `json:"resolved,omitempty"`
}

// AddNote attaches a note to the current visual selection, anchoring it to the
// selection's first line so it can be re-located after edits. It returns false
// when there is no active selection to anchor to.
func (m *Model) AddNote(body string) bool {
	start, _, ok := m.SelectionRange()
	if !ok {
		return false
	}
	text, _, _ := m.GetSelection()
	anchor := text
	if i := strings.IndexByte(anchor, '\n'); i >= 0 {
		anchor = anchor[:i] // single-line anchor
	}
	anchor = strings.TrimSpace(anchor)
	if anchor == "" {
		// Anchor to the whole line when the selection has no usable text.
		anchor = strings.TrimSpace(m.lines[start.Row])
	}
	m.notes = append(m.notes, Note{Anchor: anchor, Body: body, Row: start.Row, Col: start.Col})
	m.notesRevision++
	return true
}

// Notes returns the current notes (positions are kept fresh by relocateNotes).
func (m *Model) Notes() []Note {
	m.relocateNotes()
	return append([]Note(nil), m.notes...)
}

// SetNotes replaces the note set (e.g. after loading a file's sidecar) and
// re-anchors them against the current buffer.
func (m *Model) SetNotes(notes []Note) {
	m.notes = append([]Note(nil), notes...)
	m.relocateNotes()
	m.notesRevision++
}

// EditNote, ResolveNote, and DeleteNote provide stable index-based note
// lifecycle operations. They do not alter anchors.
func (m *Model) EditNote(index int, body string) bool {
	if index < 0 || index >= len(m.notes) || strings.TrimSpace(body) == "" {
		return false
	}
	m.notes[index].Body = strings.TrimSpace(body)
	m.notesRevision++
	return true
}

func (m *Model) ResolveNote(index int, resolved bool) bool {
	if index < 0 || index >= len(m.notes) {
		return false
	}
	m.notes[index].Resolved = resolved
	m.notesRevision++
	return true
}

func (m *Model) DeleteNote(index int) bool {
	if index < 0 || index >= len(m.notes) {
		return false
	}
	m.notes = append(m.notes[:index], m.notes[index+1:]...)
	m.notesRevision++
	return true
}

// relocateNotes re-finds each note's anchor text, preferring the occurrence
// nearest its last known row, so a note follows its text through edits. A note
// whose anchor no longer appears is marked Orphaned and left at its last row.
func (m *Model) relocateNotes() {
	for i := range m.notes {
		n := &m.notes[i]
		if n.Anchor == "" {
			continue
		}
		bestRow, bestCol, bestDist := -1, 0, 1<<62
		for row, line := range m.lines {
			col := strings.Index(line, n.Anchor)
			if col < 0 {
				continue
			}
			dist := row - n.Row
			if dist < 0 {
				dist = -dist
			}
			if dist < bestDist {
				bestRow, bestCol, bestDist = row, runeLen(line[:col]), dist
			}
		}
		if bestRow >= 0 {
			n.Row, n.Col, n.Orphaned = bestRow, bestCol, false
		} else {
			n.Orphaned = true
		}
	}
}

// noteRows returns the set of logical rows that have a (non-orphaned) note, for
// the gutter marker in View.
func (m Model) noteRows() map[int]bool {
	rows := make(map[int]bool, len(m.notes))
	for _, n := range m.notes {
		if !n.Orphaned {
			rows[n.Row] = true
		}
	}
	return rows
}
