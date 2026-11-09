package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/fsutil"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
	"github.com/ShayCichocki/marginctl/internal/state"
)

// proposalCommand makes each extracted item independently reviewable. Review
// reads the live target and displays the exact bytes that an accept will replace.
func (m *Model) proposalCommand(verb, arg string) tea.Cmd {
	if m.pendingAction == nil || m.pendingAction.Kind != pendingStateProposals {
		m.setErrorNotice("No state proposals pending")
		return nil
	}
	var n int
	if _, err := fmt.Sscanf(arg, "%d", &n); err != nil || n < 1 || n > len(m.pendingAction.Proposals) {
		m.setErrorNotice("usage: :" + verb + " <proposal-number>")
		return nil
	}
	i, p := n-1, m.pendingAction.Proposals[n-1]
	path, pathErr := proposalPath(m.project.Root, m.state, p)
	if pathErr != nil && (p.Action == "update" || p.Action == "append") {
		m.err = pathErr
		return nil
	}
	var loaded loadResult
	loadErr := pathErr
	if pathErr == nil {
		loaded, loadErr = m.persistence.load(path)
	}
	original := ""
	if loadErr == nil {
		original = loaded.content
	}
	if verb == "proposal-review" {
		// Display only (ReviewOnly, action ID 0): the app maps y/n to accept/skip
		// of this proposal and Esc/q back to the list; the batch stays pending.
		m.showResultAction(command.Result{Type: command.ResultDiff, ReviewOnly: true, DiffLabel: "state file",
			OriginalText: original, ProposedText: finalProposalContent(p, original, false)}, 0, m.pendingAction.Operation)
		m.proposalReviewN = n
		return nil
	}
	if verb == "proposal-skip" {
		m.pendingAction.Proposals = append(m.pendingAction.Proposals[:i], m.pendingAction.Proposals[i+1:]...)
		m.refreshProposalPane()
		m.setNotice("Proposal skipped")
		return nil
	}
	if verb == "proposal-edit" {
		proposed := strings.TrimSpace(strings.TrimPrefix(arg, fmt.Sprintf("%d", n)))
		if proposed == "" {
			m.setErrorNotice("usage: :proposal-edit <number> <content>")
			return nil
		}
		p.Proposed = proposed
		if p.Action != "create" && p.Action != "" {
			if pathErr != nil {
				m.err = pathErr
				return nil
			}
			p.Target = filepath.ToSlash(strings.TrimPrefix(path, filepath.Join(m.project.Root, "state")+string(os.PathSeparator)))
		} else if pathErr == nil {
			// The entry appeared since extraction: edit it in place.
			p.Action = "update"
			p.Target = filepath.ToSlash(strings.TrimPrefix(path, filepath.Join(m.project.Root, "state")+string(os.PathSeparator)))
		}
		m.pendingAction.Proposals[i] = p
		m.refreshProposalPane()
		m.setNotice("Proposal edited")
		return nil
	}
	// Every remaining verb writes state files: the extraction must still match
	// the live buffer/project/request identity.
	if !m.operationCurrent(m.pendingAction.Operation, true) {
		// Close the pane too: a stale list must not keep advertising "y apply all".
		m.setResultsVisible(false)
		m.setErrorNotice("Stale result: the proposals no longer match the current buffer; re-run the extraction")
		return nil
	}
	// writeExisting rewrites the target entry in place from its current content.
	writeExisting := func(merge bool) error {
		if loadErr != nil {
			return loadErr
		}
		return m.writeExternalDocument(path, finalProposalContent(p, loaded.content, merge), loaded.fingerprint)
	}
	var err error
	switch {
	case verb == "proposal-merge":
		// Merge appends to the entry that already exists, whatever the action.
		err = writeExisting(true)
	case p.Action == "" || p.Action == "create":
		_, err = m.state.CreateStateFileWithBody(p.Name, proposalStateType(p.Category), stateCategoryFor(p), finalProposalContent(p, "", false))
	default: // update, append
		err = writeExisting(false)
	}
	if err != nil {
		m.err = err
		return nil
	}
	m.pendingAction.Proposals = append(m.pendingAction.Proposals[:i], m.pendingAction.Proposals[i+1:]...)
	m.refreshProposalPane()
	m.refreshStateNav()
	m.setNotice("Proposal applied")
	return nil
}

// finalProposalContent is the single place that decides what a proposal writes,
// shared by accept-all and the per-proposal commands. existing is the target's
// current content (ignored for create/update). Append (and merge) keeps the
// existing text and adds the proposed text after a blank line; create and update
// write the proposed text (falling back to the summary) as the whole file.
func finalProposalContent(p command.StateProposal, existing string, merge bool) string {
	content := p.Proposed
	if content == "" {
		content = p.Summary
	}
	if merge || p.Action == "append" {
		return strings.TrimRight(existing, "\n") + "\n\n" + strings.TrimSpace(content) + "\n"
	}
	return content
}

// proposalsPending reports whether a state-proposal batch awaits a decision.
func (m *Model) proposalsPending() bool {
	return m.pendingAction != nil && m.pendingAction.Kind == pendingStateProposals
}

// reviewingProposal reports whether the pane still shows a single proposal's
// review. It is derived from the pane and the pending batch, so replacing the
// pane (or ending the batch) ends the review without any bookkeeping.
func (m *Model) reviewingProposal() bool {
	return m.proposalReviewN > 0 && m.proposalsPending() && m.results.ReviewOnly()
}

// proposalTarget returns the state-relative file a proposal will touch ("" when
// unknown) and, for update/append proposals naming no existing entry or for
// creates of an existing entry, the skip reason.
func (m *Model) proposalTarget(p command.StateProposal, reserved map[string]bool) (target, skip string) {
	stateDir := filepath.Join(m.project.Root, "state")
	switch p.Action {
	case "", "create":
		typ, cat := proposalStateType(p.Category), stateCategoryFor(p)
		if m.state.EntryExists(typ, cat, p.Name) {
			return "", skipExists
		}
		if path, err := m.state.PlannedEntryPath(p.Name, typ, cat, reserved); err == nil && path != "" {
			target = relToState(stateDir, path)
		}
	default:
		path, err := proposalPath(m.project.Root, m.state, p)
		if err != nil && errors.Is(err, errNoMatchingEntry) {
			return "", skipNoMatch
		}
		if err == nil {
			target = relToState(stateDir, path)
		}
	}
	return target, ""
}

// proposalListText renders the numbered confirmation list. The numbers are the
// arguments of the :proposal-* commands.
func proposalListText(props []command.StateProposal, targets []string, skipped skipTally) string {
	var b strings.Builder
	var nc, nu, na int
	for _, p := range props {
		switch p.Action {
		case "update":
			nu++
		case "append":
			na++
		default:
			nc++
		}
	}
	fmt.Fprintf(&b, "Apply %d state proposal(s): %s?\n", len(props), actionCounts(nc, nu, na))
	for i, p := range props {
		verb := p.Action
		if verb == "" {
			verb = "create"
		}
		if i < len(targets) && targets[i] != "" {
			fmt.Fprintf(&b, "  %d. %s %s/%s -> state/%s — %s\n", i+1, verb, p.Category, p.Name, targets[i], p.Summary)
		} else {
			fmt.Fprintf(&b, "  %d. %s %s/%s — %s\n", i+1, verb, p.Category, p.Name, p.Summary)
		}
	}
	b.WriteString("y apply all, n discard all; :proposal-accept|review|edit|merge|skip N\n")
	if skipped.total() > 0 {
		fmt.Fprintf(&b, "(%d skipped: %s)\n", skipped.total(), skipped)
	}
	return strings.TrimRight(b.String(), "\n")
}

// showProposalList (re)renders the pending proposals in the results pane, bound
// to the existing action ID and operation so y/n keep their identity checks.
func (m *Model) showProposalList() {
	if m.pendingAction == nil || m.pendingAction.Kind != pendingStateProposals {
		m.setErrorNotice("No state proposals pending")
		return
	}
	m.proposalReviewN = 0
	targets := make([]string, len(m.pendingAction.Proposals))
	if m.state != nil {
		reserved := map[string]bool{}
		for i, p := range m.pendingAction.Proposals {
			targets[i], _ = m.proposalTarget(p, reserved)
		}
	}
	m.results.ShowAction(command.Result{Type: command.ResultConfirm, Content: proposalListText(m.pendingAction.Proposals, targets, m.pendingAction.Skipped)}, m.pendingAction.ID, m.pendingAction.Operation)
	if m.focus != FocusLeftPane {
		m.focus = FocusResults
		m.editor.Blur()
	}
	m.updateLayout()
	m.updateStatusBar()
}

// refreshProposalPane re-renders the list after a per-proposal change, or, when
// none remain, clears the pending action and closes the pane.
func (m *Model) refreshProposalPane() {
	if len(m.pendingAction.Proposals) == 0 {
		m.setResultsVisible(false) // also clears the pending action
		return
	}
	m.showProposalList()
}

// skipTally counts skipped state proposals by a short human reason.
type skipTally map[string]int

const (
	skipMalformed = "malformed"
	skipExists    = "already exists"
	skipNoMatch   = "no matching entry"
)

func (t skipTally) add(reason string) { t[reason]++ }

func (t skipTally) total() int {
	n := 0
	for _, c := range t {
		n += c
	}
	return n
}

// String renders "1 already exists, 2 no matching entry" in a stable order.
func (t skipTally) String() string {
	reasons := make([]string, 0, len(t))
	for r := range t {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	parts := make([]string, 0, len(reasons))
	for _, r := range reasons {
		parts = append(parts, fmt.Sprintf("%d %s", t[r], r))
	}
	return strings.Join(parts, ", ")
}

// showStateProposals presents extracted bible entries as a confirmation. Entries
// that already exist, and update/append proposals naming no existing entry, are
// dropped up front (and counted) so "Apply" never claims to do something that
// cannot happen. parseSkipped is the number of malformed proposals the parser
// already dropped. Nothing is skipped silently.
func (m *Model) showStateProposals(props []command.StateProposal, parseSkipped int, op llmcmd.Operation) (tea.Model, tea.Cmd) {
	if !m.operationCurrent(op, true) || m.state == nil {
		return m, nil
	}
	skipped := skipTally{}
	if parseSkipped > 0 {
		skipped[skipMalformed] = parseSkipped
	}
	// One rescan per batch; every lookup below is in-memory.
	_ = m.state.Refresh()
	var fresh []command.StateProposal
	var targets []string // state-relative file each fresh proposal will touch
	reserved := map[string]bool{}
	for _, p := range props {
		target, skip := m.proposalTarget(p, reserved)
		if skip != "" {
			skipped.add(skip)
			continue
		}
		fresh = append(fresh, p)
		targets = append(targets, target)
	}

	m.focus = FocusEditor
	if len(fresh) == 0 {
		m.editor.Focus()
		if len(props) == 0 && skipped.total() == 0 {
			m.setNotice("No new state entries found to add")
		} else {
			m.setNotice(fmt.Sprintf("No state entries to apply; %d skipped (%s)", skipped.total(), skipped))
		}
		m.updateStatusBar()
		return m, nil
	}

	m.nextActionID++
	m.pendingAction = &PendingAction{ID: m.nextActionID, Kind: pendingStateProposals, Operation: op, Proposals: fresh, Skipped: skipped}
	m.proposalReviewN = 0
	m.results.ShowAction(command.Result{Type: command.ResultConfirm, Content: proposalListText(fresh, targets, skipped)}, m.nextActionID, op)
	m.focus = FocusResults
	m.editor.Blur()
	m.updateLayout()
	m.updateStatusBar()
	return m, nil
}

// applyStateProposals writes the confirmed bible entries and refreshes the nav.
func (m *Model) applyStateProposals(id uint64) (tea.Model, tea.Cmd) {
	if m.pendingAction == nil || m.pendingAction.ID != id || m.pendingAction.Kind != pendingStateProposals || !m.operationCurrent(m.pendingAction.Operation, true) {
		return m, nil
	}
	props := m.pendingAction.Proposals
	skipped := skipTally{}
	for r, n := range m.pendingAction.Skipped {
		skipped[r] = n
	}
	earlier := skipped.total()
	m.clearPendingAction()
	m.focus = FocusEditor
	m.editor.Focus()
	m.updateLayout()

	if m.state == nil {
		return m, nil
	}
	// One rescan for the whole batch; lookups below are in-memory.
	_ = m.state.Refresh()
	var created, updated, appended int
	var errs []error
	for _, p := range props {
		if p.Action == "update" || p.Action == "append" {
			path, err := proposalPath(m.project.Root, m.state, p)
			if err != nil {
				if errors.Is(err, errNoMatchingEntry) {
					skipped.add(skipNoMatch)
				} else {
					errs = append(errs, err)
				}
				continue
			}
			loaded, err := m.persistence.load(path)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if err = m.writeExternalDocument(path, finalProposalContent(p, loaded.content, false), loaded.fingerprint); err != nil {
				errs = append(errs, err)
				continue
			}
			if p.Action == "append" {
				appended++
			} else {
				updated++
			}
			continue
		}
		if _, err := m.state.CreateStateFileFromScan(p.Name, proposalStateType(p.Category), stateCategoryFor(p), finalProposalContent(p, "", false)); err != nil {
			if errors.Is(err, os.ErrExist) {
				skipped.add(skipExists)
			} else {
				errs = append(errs, err)
			}
			continue
		}
		created++
	}
	_ = m.state.Refresh()
	m.statenav.SetStateFilesWithCategories(m.state.All(), m.state.Categories())
	total := len(props) + earlier
	applied := created + updated + appended
	skippedNote := ""
	if skipped.total() > 0 {
		skippedNote = fmt.Sprintf("; %d skipped (%s)", skipped.total(), skipped)
	}
	switch {
	case len(errs) > 0:
		m.err = errors.Join(errs...)
		m.setErrorNotice(fmt.Sprintf("Applied %d of %d state proposals (%s); %d failed: %v%s", applied, total, actionCounts(created, updated, appended), len(errs), errs[0], skippedNote))
	case skipped.total() > 0:
		m.setNotice(fmt.Sprintf("Applied %d of %d state proposals (%s)%s", applied, total, actionCounts(created, updated, appended), skippedNote))
	case updated == 0 && appended == 0:
		m.setNotice(fmt.Sprintf("Created %d state entr(y/ies)", created))
	default:
		m.setNotice(fmt.Sprintf("Applied %d state proposals (%s)", applied, actionCounts(created, updated, appended)))
	}
	m.updateStatusBar()
	return m, nil
}

// actionCounts renders "1 created, 2 updated" listing only non-zero counts
// ("none" when all are zero).
func actionCounts(created, updated, appended int) string {
	var parts []string
	for _, c := range []struct {
		n    int
		verb string
	}{{created, "created"}, {updated, "updated"}, {appended, "appended"}} {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c.n, c.verb))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// relToState returns path relative to stateDir with forward slashes.
func relToState(stateDir, path string) string {
	if rel, err := filepath.Rel(stateDir, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return path
}

// errNoMatchingEntry reports that an update/append proposal named no existing
// entry. A name is never turned into a path, so there is nothing to write.
var errNoMatchingEntry = errors.New("no matching state entry")

// proposalPath resolves the file an update/append proposal addresses and
// guarantees it lies strictly inside <root>/state with no symlinked component.
// Proposal fields come from model output, so nothing here is trusted. An
// explicit Target must be a clean relative path. Without one, timeline/threads
// map to their fixed files and any other proposal is resolved by looking up an
// EXISTING entry by display name or filename stem (errNoMatchingEntry when none
// matches); a path is never constructed from Name.
func proposalPath(root string, mgr *state.Manager, p command.StateProposal) (string, error) {
	stateDir := filepath.Join(root, "state")
	if err := fsutil.RejectSymlinkDir(stateDir); err != nil {
		return "", err
	}
	var path string
	if t := strings.TrimSpace(p.Target); t != "" {
		if err := fsutil.SafeRelPath(t); err != nil {
			return "", fmt.Errorf("unsafe proposal target %q", t)
		}
		path = filepath.Join(stateDir, filepath.FromSlash(t))
	} else {
		switch p.Category {
		case "timeline":
			path = filepath.Join(stateDir, "timeline.md")
		case "threads", "open_threads":
			path = filepath.Join(stateDir, "open_threads.md")
		default:
			if mgr == nil {
				return "", errNoMatchingEntry
			}
			sf := mgr.FindEntry(proposalStateType(p.Category), stateCategoryFor(p), p.Name)
			if sf == nil {
				return "", fmt.Errorf("%w: %q in %s", errNoMatchingEntry, p.Name, p.Category)
			}
			path = sf.Path
		}
	}
	contained, err := fsutil.ContainedPath(stateDir, path, true)
	if err != nil {
		return "", err
	}
	if contained == filepath.Clean(stateDir) {
		return "", fmt.Errorf("unsafe proposal target %q", p.Target)
	}
	return contained, nil
}

// proposalStateType maps a proposal category to a state.StateType.
func proposalStateType(category string) state.StateType {
	switch strings.ToLower(category) {
	case "characters":
		return state.StateCharacter
	case "locations":
		return state.StateLocation
	default:
		return state.StateCustom
	}
}

// stateCategoryFor returns the custom-category name for a proposal (empty for the
// built-in character/location types).
func stateCategoryFor(p command.StateProposal) string {
	switch proposalStateType(p.Category) {
	case state.StateCustom:
		return p.Category
	default:
		return ""
	}
}
