package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/hooks"
	"github.com/open-octo/octo-agent/internal/mcp"
	"github.com/open-octo/octo-agent/internal/tools"
	"github.com/open-octo/octo-agent/internal/tui"
	"golang.org/x/term"
)

// runTUI is the interactive bubbletea REPL: the TTY counterpart to runREPL's
// plain-text loop. The event loop owns the main goroutine; each turn runs in a
// background goroutine driven by runTurn through a tuiSink, which marshals the
// agent's streaming events back onto the loop as tea.Msgs and blocks the
// agent goroutine on a channel for Ask prompts (modals). See
// dev-docs/tui-input-modes-design.md §3, §6.
//
// Input modes while a turn runs (design §7):
//   - Enter              → steer  (folded into the turn at the next tool-batch boundary)
//   - Shift+Enter        → newline (on terminals that emit CSI-u sequences)
//   - Alt+Enter          → newline (traditional escape sequence)
//   - Ctrl+J             → newline (LF — works on all terminals)
//   - Ctrl+Q             → queue  (run as a fresh turn after this one finishes)
//   - Esc                → take the turn back if no output yet (text returns to the input), else interrupt; queue survives
//   - Ctrl+C             → interrupt if a turn runs, else save & quit
//   - Ctrl+D             → save & quit
func runTUI(cfg replConfig) int {
	defer tools.KillAllBackground()
	defer tools.CleanSpillFiles()
	defer hooks.DrainSpill(5 * time.Second) // flush queued async hooks on exit
	// MCP connects in the background (mcpBoot → connectMCPCmd → mcpReadyMsg),
	// which installs the registry globally. Tear it down here on exit since the
	// TUI path owns its lifecycle (chat.go only defers cleanup for the
	// synchronous headless connect). nil-safe when no servers connected.
	defer func() {
		if reg := tools.ActiveMCPRegistry(); reg != nil {
			tools.SetMCPRegistry(nil)
			reg.Close()
		}
	}()

	m := newTUIModel(cfg)
	p := tea.NewProgram(m, tea.WithFilter(shiftEnterFilter))
	sink := &tuiSink{prog: p}
	m.sink = sink

	// Eagerly probe terminal background colour before bubbletea owns stdin.
	// lipgloss.AdaptiveColor sends an OSC 11 query on first use; if that
	// happens after p.Run() the terminal's response (e.g. "11;rgb:1e1e/1e1e/2e2e\\")
	// leaks into bubbletea's input reader and shows up as apparent user input.
	//
	// tui.IsDark() guards the probe with sync.Once, so calling it here warms
	// the cache. The result is also passed to markdownRenderer so glamour can
	// use an explicit dark/light style instead of WithAutoStyle(), which would
	// fire its own (uncached) termenv query later.
	_ = tui.IsDark()

	// Gate + asker raise their prompts through the same sink, so they render
	// as modals on this event loop instead of reading stdin (which bubbletea
	// owns). Overrides whatever the plain path wired.
	if cfg.permEngine != nil {
		cfg.a.Gate = newCLIGate(cfg.permEngine, sink)
	}
	tools.SetAsker(newREPLAsker(sink))

	// Sub-agent manager: wire the onExit hook so completion notifications ride
	// the same inbox path as background-process notices, and send a TUI msg
	// for scrollback display.
	if cfg.subAgentMgr != nil {
		tools.SetDefaultSubAgentManager(cfg.subAgentMgr)
		cfg.subAgentMgr.SetOnExit(func(ev tools.SubAgentNotification) {
			cfg.a.Inbox.Enqueue(tools.FormatSubAgentNote(ev))
			p.Send(subAgentNoteMsg{ev})
		})
		// Runtime activity (started + per-tool) for the live bottom panel.
		cfg.subAgentMgr.SetOnEvent(func(ev tools.SubAgentEvent) {
			p.Send(subAgentEventMsg{ev})
		})
		defer func() {
			cfg.subAgentMgr.SetOnExit(nil)
			cfg.subAgentMgr.SetOnEvent(nil)
			tools.SetDefaultSubAgentManager(nil)
			cfg.subAgentMgr.KillAll()
		}()
	}

	// Background-process completion notifications. Fired from a process's
	// waiter goroutine, so each path is goroutine-safe: Inbox.Enqueue is
	// mutex-guarded, and prog.Send marshals the scrollback notice onto the
	// event loop. Without this a finished background command is invisible
	// until the model polls terminal_output.
	tools.SetBackgroundOnExit(func(e tools.BgExit) {
		cfg.a.Inbox.Enqueue(tools.FormatBgNoteWithSummary(tools.DefaultBackgroundManager(), e))
		p.Send(bgExitMsg{e})
	})
	defer tools.SetBackgroundOnExit(nil)

	// Background-workflow completion notifications. A workflow runs detached, so
	// without this its result is invisible until the model polls workflow_status.
	tools.SetDefaultWorkflowOnDone(func(ev tools.WorkflowNotification) {
		cfg.a.Inbox.Enqueue(tools.FormatWorkflowNote(ev))
		p.Send(workflowNoteMsg{ev})
	})
	// Live workflow events drive the running-workflow panel (started/progress/done).
	tools.SetDefaultWorkflowOnEvent(func(ev tools.WorkflowEvent) {
		p.Send(workflowEventMsg{ev})
	})
	defer func() {
		tools.SetDefaultWorkflowOnEvent(nil)
		tools.SetDefaultWorkflowOnDone(nil)
		tools.KillDefaultWorkflows()
	}()

	_, err := p.Run()
	if err != nil {
		fmt.Fprintf(cfg.stderr, "octo: tui: %v\n", err)
		return 1
	}

	// Final save on exit (mirrors runREPL's exit save). Unbind before saving so
	// other entries see the session as released once the TUI process exits.
	if !cfg.noSave {
		cfg.session.SyncFrom(cfg.a.History)
		cfg.session.Unbind(agent.EntryTUI)
		if err := cfg.session.Save(); err != nil {
			fmt.Fprintf(cfg.stderr, "session save: %v\n", err)
			return 1
		}
	} else {
		cfg.session.Unbind(agent.EntryTUI)
	}
	if !cfg.verbosity.quiet() {
		fmt.Fprintf(cfg.stdout, "\nResume: octo -c %s\n", cfg.session.ShortID())
	}
	return 0
}

// inputPlaceholder is the idle hint shown in the empty input box. It's swapped
// for a pending follow-up suggestion (ghost text) when one is available.
const inputPlaceholder = "Ask anything…"

// ── messages marshalled from the agent goroutine onto the event loop ──

type turnStartedMsg struct{}
type agentEventMsg struct{ ev agent.AgentEvent }
type turnEndedMsg struct {
	reply agent.Reply
	stats TurnStats
	err   error
}
type turnFinishedMsg struct{ err error } // the turn goroutine returned
type noticeMsg struct{ text string }
type bgExitMsg struct{ e tools.BgExit }                      // a background process finished (async)
type subAgentNoteMsg struct{ ev tools.SubAgentNotification } // a sub-agent completed (async)
type workflowNoteMsg struct{ ev tools.WorkflowNotification } // a background workflow finished (async)
type mcpReadyMsg struct{ reg *mcp.Registry }                 // background MCP connect finished (async)
type subAgentEventMsg struct{ ev tools.SubAgentEvent }       // a sub-agent's runtime activity (async)
type workflowEventMsg struct{ ev tools.WorkflowEvent }       // a background workflow's runtime activity (async)
type suggestionMsg struct{ text string }                     // an after-turn follow-up suggestion (async)
type armWakeupMsg struct {                                   // schedule_wakeup tool asked to arm a loop (from the turn goroutine)
	delay  time.Duration
	prompt string
	reason string
	repeat bool
}
type wakeupMsg struct { // an armed loop wakeup fired (async, from the wakeup timer)
	prompt string
	repeat bool
	delay  time.Duration
}
type cancelWakeupMsg struct{}       // schedule_wakeup(cancel=true) asked to stop the loop
type titleMsg struct{ text string } // a generated session title (async)
type tickMsg struct{}               // animation tick while a turn runs
type askMsg struct {
	prompt UserPrompt
	resp   chan UserResponse
}

// tickInterval drives the spinner / elapsed-clock animation. ~8 Hz is smooth
// without being wasteful; it only runs while a turn is in flight.
const tickInterval = 120 * time.Millisecond

// answerSprintDur is how long the wait-on-model line holds the first answer
// deltas while the ↓ token counter sprints, before releasing the prose.
// Halved from an original 280ms (#1097): the flourish is still visible but
// adds noticeably less perceived latency to the first answer tokens.
const answerSprintDur = 140 * time.Millisecond

// answerSprintMinChars gates the sprint: only turns with a real reasoning /
// uplink phase get the flourish, so a quick direct reply isn't delayed.
const answerSprintMinChars = 240

// shiftEnterFilter intercepts Kitty-keyboard-protocol CSI sequences that
// bubbletea v1 does not natively understand (e.g. Shift+Enter = \x1b[13;2u)
// and converts them into plain KeyMsg events the rest of the app already
// handles. This is a no-op on terminals that do not emit CSI-u sequences.
func shiftEnterFilter(_ tea.Model, msg tea.Msg) tea.Msg {
	// unknownCSISequenceMsg is unexported in bubbletea, so we identify it by
	// reflection. Its underlying type is []byte.
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Type().Elem().Kind() != reflect.Uint8 {
		return msg
	}
	seq := string(v.Bytes())
	switch seq {
	case "\x1b[13;2u": // Shift+Enter (kitty CSI u)
		return tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	case "\x1b[13;3u": // Alt+Enter (CSI-u form)
		return tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	case "\x1b[13;5u": // Ctrl+Enter (CSI-u form)
		return tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	case "\x1b[27;2;13~": // Shift+Enter (xterm modifyOtherKeys)
		return tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	case "\x1b[27;3;13~": // Alt+Enter (modifyOtherKeys)
		return tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	case "\x1b[27;5;13~": // Ctrl+Enter (modifyOtherKeys)
		return tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	case "\x1b[27;6;13~": // Shift+Ctrl+Enter (modifyOtherKeys)
		return tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	}
	return msg
}

// tickCmd schedules the next animation tick.
func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

// startTicker begins the tickMsg->tickCmd animation chain if one isn't
// already alive, else it's a no-op. Callers that might fire while a chain
// from an earlier event is still running (subagent/workflow events arriving
// between turns) must go through this rather than calling tickCmd() directly
// — tea.Tick always starts a brand-new independent timer, so an unconditional
// tickCmd() call would spawn a second, parallel chain alongside the existing
// one, doubling spinnerFrame's effective advance rate (and tripling, etc. with
// more redundant calls). The tickMsg handler in Update is the one place that
// legitimately continues the already-active chain and calls tickCmd()
// directly for that reason.
func (m *tuiModel) startTicker() tea.Cmd {
	if m.tickerActive {
		return nil
	}
	m.tickerActive = true
	return tickCmd()
}

// tuiSink implements ViewSink by sending each call onto the bubbletea program.
// Ask blocks the calling (agent) goroutine until the modal is answered or the
// turn's context is cancelled.
type tuiSink struct {
	prog interface{ Send(tea.Msg) }
}

func (s *tuiSink) TurnStarted()             { s.prog.Send(turnStartedMsg{}) }
func (s *tuiSink) Emit(ev agent.AgentEvent) { s.prog.Send(agentEventMsg{ev}) }
func (s *tuiSink) Notice(msg string)        { s.prog.Send(noticeMsg{msg}) }
func (s *tuiSink) TurnEnded(r agent.Reply, stats TurnStats, e error) {
	s.prog.Send(turnEndedMsg{reply: r, stats: stats, err: e})
}

func (s *tuiSink) Ask(ctx context.Context, p UserPrompt) (UserResponse, error) {
	ch := make(chan UserResponse, 1)
	s.prog.Send(askMsg{prompt: p, resp: ch})
	select {
	case r := <-ch:
		return r, nil
	case <-ctx.Done():
		// Turn interrupted while the modal was open — deny/cancel gracefully.
		return UserResponse{Cancelled: true}, nil
	}
}

// ── pending queue ──

type pendingItem struct {
	text string
	// blocks carries image attachments that rode a steer which wasn't drained
	// in-loop (e.g. enqueued in the narrow window as a turn was ending). They
	// are attached to the next user message via AttachUserBlocks on dequeue so
	// the image is never silently dropped.
	blocks []agent.ContentBlock
}

// ── model ──

// pendingAttachment is an image captured from the clipboard, waiting to be
// folded into the next user message. block carries the bytes for the model;
// label is the human-readable chip text (e.g. "image (PNG, 84 KB)").
type pendingAttachment struct {
	block agent.ContentBlock
	label string
}

type tuiModel struct {
	cfg  replConfig
	a    *agent.Agent
	sink *tuiSink

	// md renders committed assistant markdown blocks (glamour). Skipped under
	// --plain (cfg.plain), where text commits as raw lines.
	md markdownRenderer

	// ta is the multi-line text input (bubbles/textarea).
	ta textarea.Model

	// inputHistory stores submitted lines for ↑/↓ recall, seeded from and
	// appended to historyFile so it survives restart.
	inputHistory    []string
	inputHistoryIdx int    // -1 = not browsing, 0..len-1 = browsing
	historyFile     string // path from defaultInputHistoryFile(); "" disables persistence
	// inputDraft holds the text that was in the input box before the user
	// started browsing history with ↑, so ↓ can restore it.
	inputDraft string

	// Slash-command completion menu. complItems is the current filtered
	// candidate set (built-in commands + skills); non-empty means the menu is
	// open. complIdx is the highlighted row. Recomputed on every keystroke when
	// the input is a bare "/command" token (see updateCompletion).
	complItems []complItem
	complIdx   int

	// turnRunning is true between starting a turn and turnFinishedMsg.
	turnRunning bool
	cancelTurn  context.CancelFunc

	// wakeupTimer is the armed in-session loop wakeup (schedule_wakeup tool),
	// nil when no loop is running. loopActive mirrors it for the activity line.
	// The loop coexists with user messages (CC-style); it stops only on an
	// explicit Ctrl+C or schedule_wakeup(cancel=true) — see cancelWakeup.
	// loopStart is the anti-leak clock: stamped on the first arm, kept across
	// ticks, so a loop past tools.MaxLoopLifetime stops itself.
	wakeupTimer *time.Timer
	loopActive  bool
	loopStart   time.Time

	// echoPending is the user-message echo held in the live View() area instead
	// of being committed straight to the scrollback. It commits on the turn's
	// first output (commitEcho) so the message lands above the assistant reply,
	// or is dropped if the user hits Esc before the model responds. Empty means
	// nothing deferred (already committed, or a turn with no user echo).
	echoPending string
	// echoRestore is the raw typed text put back into the input box when the
	// user takes the turn back with Esc before any output. Empty when the turn
	// isn't a typed user message (e.g. a skill, /init, or a dequeued item).
	echoRestore string

	// partial holds the in-progress assistant text not yet committed to the
	// scrollback.  In the TUI the raw partial is rendered live in the View()
	// area so the user sees tokens arrive in real time; only complete blocks
	// (identified by splitCommittableMarkdown) are promoted to printlnBuf and
	// flushed to the terminal scrollback via tea.Println.
	partial strings.Builder

	// turnOutChars counts this turn's streamed output characters (reasoning +
	// answer text + tool arguments). The reasoning trace itself never lands in
	// the scrollback — over a long agentic turn it would dominate the transcript
	// — the count just feeds the activity line's "↓ ~N tokens" readout so the
	// wait still reads as progress (Claude Code style).
	turnOutChars int

	// answerSprint plays a brief accelerating token-counter flourish at the
	// hand-off from the model's reasoning/uplink phase to its answer: the first
	// answer deltas are held in sprintBuf for answerSprintDur while the ↓
	// counter races up, then released so the prose streams. sprintStartTok is
	// the token estimate when the sprint began; the displayed value eases from
	// it up to the live estimate (which keeps climbing as held deltas arrive).
	answerSprint   bool
	sprintStart    time.Time
	sprintStartTok int
	sprintBuf      strings.Builder

	// toolInput caches each tool call's input from EventToolStarted so the
	// matching EventToolDone can render a card (tool_result events don't carry
	// the input back). Keyed by ToolID; entry removed on done/error.
	toolInput map[string]map[string]any

	// queue holds Ctrl+Q messages to run as future turns (design §8/§10).
	queue []pendingItem

	// goalEditPending marks that /goal edit armed the input: the next
	// submitted line is the edited objective, not a message.
	goalEditPending bool
	// goalLastStatus is the last goal status seen via EventGoalUpdated, so
	// only transitions print a notice.
	goalLastStatus agent.GoalStatus

	// pendingSteer holds steer messages typed during a running turn that
	// haven't been drained yet. Shown in the live View area (below the
	// scrollback) so the user sees immediate feedback without breaking
	// the chronological message order (Claude Code style).
	pendingSteer []string

	// pendingAttachments holds images pasted with Ctrl+V, waiting to ride the
	// next user turn. Shown as chips above the input; cleared on submit (sent)
	// or Esc (discarded).
	pendingAttachments []pendingAttachment

	// inputFolded is set when the input box contains a large amount of pasted
	// text. When true, the textarea is collapsed and shows a summary like
	// "[123 lines pasted]" instead of the full content. Tab toggles.
	inputFolded bool
	// inputFoldedLines stores the line count when folded for display.
	inputFoldedLines int

	// pastedBlocks holds large bracketed-paste captures that were collapsed
	// into inline "[#N pasted …]" placeholder tokens in the input box, keeping
	// the box readable while composing. On submit/queue each token is expanded
	// back to its full content before the message is sent. Cleared whenever the
	// box is submitted, queued, or cleared.
	pastedBlocks []pastedBlock
	// pasteSeq numbers placeholder tokens within the current draft; reset to 0
	// with pastedBlocks so a fresh draft starts numbering again at #1.
	pasteSeq int

	// modal, when non-nil, is an active Ask prompt (design §6).
	modal *modalState

	// showTasks pins the task checklist in the live area regardless of turn
	// state (Ctrl+T toggle, Claude Code style). The pinned view also shows a
	// fully-completed list (normally hidden once nothing is outstanding).
	showTasks bool

	width int
	quit  bool

	// cwd is the home-abbreviated working directory shown in the status bar
	// (computed once — it doesn't change during a session).
	cwd string
	// turnStart timestamps the current turn so the status bar can show elapsed.
	turnStart time.Time

	// spinnerFrame advances on every tickMsg while a turn runs, animating the
	// thinking placeholder, the running-tool indicator, and the elapsed clock.
	spinnerFrame int
	// tickerActive is true while a tickMsg->tickCmd chain is alive. Every event
	// that might need the ticker running (turn start, subagent/workflow events
	// arriving between turns) used to unconditionally call tickCmd(), which — since
	// tea.Tick always starts a brand-new independent timer — spawned a whole
	// separate self-perpetuating chain each time. A busy background workflow
	// firing many workflowEventMsg/subAgentEventMsg while idle between turns could
	// accumulate dozens of parallel chains, each incrementing spinnerFrame every
	// tickInterval, so the spinner visibly spun many times faster than intended.
	// This flag ensures at most one chain is alive at a time.
	tickerActive bool
	// running, when non-nil, is a card tool executing right now — shown as a
	// live spinner line until its done event commits the finished card. (Card
	// tools suppress their started line, so without this they'd show nothing
	// until completion.)
	running *runningTool

	// toolStream* track a tool call whose arguments are still streaming from the
	// model — e.g. a large write_file whose whole content arrives as one long
	// input_json_delta. Without a readout the turn shows only the generic
	// "Thinking" spinner for many seconds, reading as a freeze; instead View
	// shows a live "Writing… N KB" line whose byte counter visibly grows.
	// toolStreamID detects when a new tool's args start (reset the counter).
	toolStreamID    string
	toolStreamName  string
	toolStreamBytes int

	// compacting is set between EventCompactStarted and EventCompactDone while
	// history compaction runs. It drives a dedicated live spinner line so the
	// user sees the conversation isn't frozen while the summary streams in.
	compacting       bool
	compactStart     time.Time
	compactTokens    int    // running estimate of the summary generated so far
	compactMaxTokens int    // the summary's output-token cap (denominator)
	compactPreview   string // tail of the streamed summary, shown under the spinner

	// printlnBuf accumulates lines to emit via tea.Println at the end of each
	// Update cycle. In inline mode, committed output goes to the terminal
	// scrollback above the live View() area — no alt-screen buffer needed.
	printlnBuf []string
	// lastPrintBlank tracks whether the last queued scrollback line was blank,
	// so printlnBlock can separate block-level items with exactly one blank
	// line instead of stacking them edge-to-edge.
	lastPrintBlank bool

	// assistantFirstBlock is true from the start of a turn until the first
	// assistant text block is committed to the scrollback. Used to prepend
	// the ◆ indicator so the assistant's prose is visually anchored like the
	// "> " prefix is for user messages.
	assistantFirstBlock bool

	// historyReplayed guards the one-time replay of a resumed session's prior
	// turns into the scrollback. Done on the first WindowSizeMsg so markdown
	// wraps to the real terminal width rather than the Init-time fallback.
	historyReplayed bool

	// height is the terminal height in cells, updated by WindowSizeMsg.
	height int

	// foldedFullText holds the complete input text when inputFolded is true.
	// Cleared when expanded.
	foldedFullText string

	// suggestion is a pending after-turn follow-up suggestion shown as ghost
	// text in the empty input box (the textarea placeholder). Accepted with
	// Tab/→, cleared when a turn starts. Empty means none pending.
	suggestion string

	// titlePending guards the one-shot async title generation so a second turn
	// finishing before the first title returns doesn't fire it twice.
	titlePending bool

	// subAgents holds the live state of currently-running sub-agents, keyed by
	// manager handle (agent_N), rendered as a bottom panel. An entry appears on
	// the "started" event and is removed when its completion note arrives.
	// subAgentOrder preserves launch order for stable rendering.
	subAgents     map[string]*subAgentUI
	subAgentOrder []string

	// subAgentFocus is the index into subAgentOrder of the currently-focused
	// sub-agent in the panel (-1 when the input box has focus). Navigation with
	// ↑/↓ moves the focus; Enter toggles expand/collapse; Esc returns focus to
	// the input box.
	subAgentFocus int

	// workflows holds the live state of background workflow runs, keyed by run
	// id (wf_N). An entry appears on the "started" event and is removed when the
	// "done" event arrives, so only running workflows are shown in the panel.
	workflows map[string]*workflowUI
}

// subAgentUI is the live panel state for one running sub-agent.
type subAgentUI struct {
	description string
	agentType   string // subagent_type, e.g. "explore" (empty for an untyped fork)
	start       time.Time
	toolCount   int
	recent      []string // last few tool names, for the live chain (collapsed view)
	history     []string // complete tool history (expanded view)
	errored     bool
	expanded    bool // true when the panel shows the full history
}

// workflowUI is the live panel state for one background workflow run.
type workflowUI struct {
	description string
	status      string // running | done | error
	start       time.Time
	lastLine    string // most recent progress line
}

// runningTool is the live indicator state for an in-flight card tool.
type runningTool struct {
	verb   string
	target string
	start  time.Time
	// tail holds the last few lines of streamed output (StreamingToolExecutor
	// tools only — currently just terminal) so the live spinner can show a
	// dimmed preview of what's happening during a long-running command
	// instead of going dark (issue #1094). Capped at runningTailMaxLines.
	tail []string
}

// runningTailMaxLines bounds the live preview under the spinner to a few
// lines — enough to show the command is progressing, not a full replay.
const runningTailMaxLines = 3

// modalState renders a permission / question prompt and collects the answer.
type modalState struct {
	prompt UserPrompt
	resp   chan UserResponse
	// cursor is the highlighted option (question mode); options include the
	// trailing "Other (free text)" slot.
	cursor   int
	options  []string // rendered option labels (questions only)
	selected map[int]bool
	// otherActive is true when the user has selected "Other" and is now typing
	// free text inline inside the modal. otherInput is a real textinput.Model
	// (not a hand-rolled append/backspace buffer) so it gets cursor movement,
	// word-jump, and delete-forward for free, matching the main input box's
	// editing quality (#1097).
	otherActive bool
	otherInput  textinput.Model
	// detail caches the rendered permission body (detailWidth is the width it
	// was rendered for; detailSet distinguishes "not rendered yet" from a
	// legitimate width of 0). View() runs on every spinner tick while the
	// prompt is open, and the edit_file detail reads the target file — the
	// cache keeps that to once per width instead of ~8×/second.
	detail      string
	detailWidth int
	detailSet   bool
}

func newTUIModel(cfg replConfig) *tuiModel {
	ta := textarea.New()
	ta.Placeholder = inputPlaceholder
	ta.Focus()
	ta.ShowLineNumbers = false
	// Only the first line shows "> "; subsequent lines are padded with spaces
	// so text aligns (Claude Code style).
	ta.SetPromptFunc(2, func(lineIdx int) string {
		if lineIdx == 0 {
			return "> "
		}
		return "  "
	})
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(tui.ColBrand).Bold(true)
	ta.BlurredStyle.Prompt = lipgloss.NewStyle().Foreground(tui.ColBrand).Bold(true)
	// Disable up/down line navigation so we can use them for input-history
	// recall instead.
	ta.KeyMap.LineNext = key.Binding{}
	ta.KeyMap.LinePrevious = key.Binding{}
	style := "dark"
	if !tui.IsDark() {
		style = "light"
	}
	historyFile := defaultInputHistoryFile()
	// Bubbletea reports the real terminal size asynchronously via the first
	// WindowSizeMsg, which arrives only after Init() has already run — too
	// late for Init's banner print, which would otherwise always see width 0
	// and fall back to the narrow-terminal text-only banner. Query it
	// synchronously up front instead; the WindowSizeMsg handler still
	// overwrites these on resize.
	width, height := 80, 24
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		width, height = w, h
	}
	m := &tuiModel{cfg: cfg, a: cfg.a, cwd: abbreviateHome(workingDir()), ta: ta, inputHistory: loadInputHistory(historyFile), inputHistoryIdx: -1, historyFile: historyFile, md: markdownRenderer{style: style}, subAgents: map[string]*subAgentUI{}, subAgentFocus: -1, workflows: map[string]*workflowUI{}, width: width, height: height}
	// Seed the last-seen goal status so a resumed session's first transition
	// (e.g. the budget crossing) prints its notice instead of being treated
	// as the baseline.
	if m.goalsWired() {
		if g, ok := cfg.session.GoalSnapshot(); ok {
			m.goalLastStatus = g.Status
		}
	}
	_ = m.updateTextAreaHeight()
	return m
}

// autoSubmitMsg drives an automatic first turn (e.g. the onboarding ceremony on
// a first run) as if the user had typed cfg.autoFirstInput.
type autoSubmitMsg struct{ text string }

func (m *tuiModel) Init() tea.Cmd {
	bootCmds := []tea.Cmd{tea.Println(tui.Banner("", m.a.Model, m.cwd, m.width))}
	// A resumed session carrying a goal gets a one-line reminder under the
	// banner (the Codex resume-paused prompt, as a hint instead of a modal).
	if m.goalsWired() {
		if notice := goalStartupNotice(m.cfg.session); notice != "" {
			bootCmds = append(bootCmds, tea.Println(notice))
		}
	}
	bootCmds = append(bootCmds, textarea.Blink)
	boot := tea.Sequence(bootCmds...)
	cmds := []tea.Cmd{boot}
	// Connect MCP concurrently with the first paint (tea.Batch, not Sequence)
	// so the banner + input appear immediately and the servers' handshake cost
	// is hidden. The result lands as mcpReadyMsg.
	if m.cfg.mcpBoot != nil {
		cmds = append(cmds, m.connectMCPCmd())
	}
	// Auto-submit a first turn (onboarding) once startup is underway.
	if m.cfg.autoFirstInput != "" {
		input := m.cfg.autoFirstInput
		cmds = append(cmds, func() tea.Msg { return autoSubmitMsg{text: input} })
	}
	return tea.Batch(cmds...)
}

// connectMCPCmd returns a tea.Cmd that runs the (slow) MCP handshake off the
// event loop and reports the live registry back as mcpReadyMsg. Bubbletea runs
// each Cmd in its own goroutine, so this overlaps the rest of startup. The
// OAuth device-flow prompt and connect-failure warnings are routed to the TUI
// sink — never the raw terminal, which bubbletea owns.
func (m *tuiModel) connectMCPCmd() tea.Cmd {
	boot := m.cfg.mcpBoot
	sink := m.sink
	return func() tea.Msg {
		reg := mcp.ConnectAll(
			context.Background(),
			boot.cfg,
			boot.info,
			func(serverName string) mcp.OAuthPrompt { return newTUIOAuthPrompt(sink, serverName) },
			sinkWriter{sink: sink},
			boot.childErr,
		)
		return mcpReadyMsg{reg: reg}
	}
}

// tuiWaker implements tools.Waker by posting an armWakeupMsg onto the bubbletea
// program. The timer and all loop state live on the model (the UI goroutine);
// only the program send crosses goroutines, the same goroutine-safe path the
// background-process and sub-agent notices use.
type tuiWaker struct {
	prog interface{ Send(tea.Msg) }
}

func (w tuiWaker) ScheduleWakeup(delay time.Duration, prompt, reason string, repeat bool) error {
	w.prog.Send(armWakeupMsg{delay: delay, prompt: prompt, reason: reason, repeat: repeat})
	return nil
}

func (w tuiWaker) CancelWakeup() error {
	w.prog.Send(cancelWakeupMsg{})
	return nil
}

// armWakeup (re)starts the loop's wakeup timer, replacing any pending one — a
// session holds at most one armed loop. loopStart is stamped on the first arm
// and kept across ticks (anti-leak clock); once the loop has run past
// tools.MaxLoopLifetime it stops instead of re-arming, the same bound the
// server enforces, so a forgotten loop can't tick forever.
func (m *tuiModel) armWakeup(delay time.Duration, prompt string, repeat bool) {
	if m.loopStart.IsZero() {
		m.loopStart = time.Now()
	}
	if tools.LoopExpired(m.loopStart) {
		m.printlnBlock(noticeStyle.Render("● Loop stopped — reached max runtime"))
		m.cancelWakeup()
		return
	}
	if m.wakeupTimer != nil {
		m.wakeupTimer.Stop()
	}
	prog := m.sink.prog
	wm := wakeupMsg{prompt: prompt, repeat: repeat, delay: delay}
	m.wakeupTimer = time.AfterFunc(delay, func() { prog.Send(wm) })
	m.loopActive = true
}

// wakeupFired clears the spent timer after a tick but KEEPS the loop clock
// (loopStart), so a dynamic loop's lifetime accumulates across ticks even
// though the model re-arms it each turn.
func (m *tuiModel) wakeupFired() {
	m.wakeupTimer = nil
	m.loopActive = false
}

// cancelWakeup stops any armed loop and resets the clock. The loop coexists
// with user messages (CC-style), so this fires only on an explicit stop: Ctrl+C,
// schedule_wakeup(cancel=true), or the anti-leak lifetime bound.
func (m *tuiModel) cancelWakeup() {
	if m.wakeupTimer != nil {
		m.wakeupTimer.Stop()
		m.wakeupTimer = nil
	}
	m.loopActive = false
	m.loopStart = time.Time{}
}

// startTurn launches a turn whose transcript echo is the submitted line itself.
func (m *tuiModel) startTurn(line string) tea.Cmd {
	return m.startTurnEcho(line, line)
}

// pendingFromInbox folds drained inbox items into one follow-up turn item,
// joining their text and collecting any image blocks so attachments survive the
// requeue (the in-loop drain normally consumes steers first; this covers a
// steer that raced in as the turn was ending).
func pendingFromInbox(items []agent.InboxItem) pendingItem {
	var blocks []agent.ContentBlock
	for _, it := range items {
		blocks = append(blocks, it.Blocks...)
	}
	return pendingItem{text: strings.Join(agent.Texts(items), "\n\n"), blocks: blocks}
}

// startQueued launches a dequeued follow-up turn, attaching any image blocks the
// item carried so they ride the new user message rather than being dropped.
func (m *tuiModel) startQueued(it pendingItem) tea.Cmd {
	if len(it.blocks) > 0 {
		m.a.AttachUserBlocks(it.blocks)
	}
	return m.startTurnEcho(it.text, "") // echo already shown when queued
}

// startTurnEcho launches a turn in a background goroutine driven by runTurn. The
// sink streams events back as tea.Msgs; turnFinishedMsg fires when runTurn
// returns so Update can save, drain steer, and dequeue. echo is the transcript
// line shown to the user — pass a short label (not line) when line is an
// expanded prompt that shouldn't be dumped verbatim (e.g. /init, /<skill>);
// pass "" to suppress the echo entirely.
func (m *tuiModel) startTurnEcho(line, echo string) tea.Cmd {
	return m.startTurnEchoRestore(line, echo, "")
}

// startTurnEchoRestore is startTurnEcho plus restore: the raw text put back
// into the input box if the user hits Esc before the model produces any output.
// Pass "" for turns that aren't a verbatim typed message (skills, /init,
// dequeued items) — those can't be meaningfully restored.
func (m *tuiModel) startTurnEchoRestore(line, echo, restore string) tea.Cmd {
	// Any turn start cancels a pending /goal edit — async idle auto-turns
	// (background exits, sub-agent notes, loop wakeups) can fire while the
	// edit is armed, and a stale flag would silently consume the user's next
	// unrelated message as the objective.
	if m.goalEditPending {
		m.goalEditPending = false
		m.printlnBlock(noticeStyle.Render("Goal edit cancelled"))
	}
	m.turnRunning = true
	m.turnStart = time.Now()
	m.spinnerFrame = 0
	m.running = nil
	m.partial.Reset()
	m.turnOutChars = 0
	m.answerSprint = false
	m.sprintBuf.Reset()
	m.toolStreamName, m.toolStreamID, m.toolStreamBytes = "", "", 0
	m.clearSuggestion() // a new turn supersedes any pending follow-up
	ctx, cancel := context.WithCancel(context.Background())
	ctx = tools.WithWaker(ctx, tuiWaker{prog: m.sink.prog})
	m.cancelTurn = cancel
	a := m.a
	cfg := m.cfg
	sink := m.sink
	prog := m.sink.prog
	go func() {
		_, err := runTurn(ctx, a, cfg, sink, line)
		cancel()
		prog.Send(turnFinishedMsg{err: err})
	}()
	// Defer the echo: hold it in the live View() area rather than committing it
	// to the scrollback now, so an Esc before the model responds can drop it (and
	// restore the typed text to the input) without leaving an orphan line in the
	// scrollback. It commits on the turn's first output via commitEcho. Excludes a
	// degraded background-notice "turn", which already surfaced as a bg notice and
	// isn't user input. Either way, start the animation ticker for the spinner.
	m.echoPending = ""
	m.echoRestore = ""
	if echo != "" && !strings.HasPrefix(echo, "<system-reminder>") {
		m.echoPending = userEchoStyle.Render("> ") + echo
		m.echoRestore = restore
	}
	return m.startTicker()
}

// startCompact launches an explicit /compact in a background goroutine. It
// reuses the turn machinery: the sink streams EventCompact* events so the live
// "Compacting…" spinner and the success/reclaim notice render exactly as
// auto-compaction does, and turnFinishedMsg saves the folded history. A no-op
// (nothing foldable) or error gets its own scrollback notice, since those
// don't ride the compaction events.
func (m *tuiModel) startCompact() tea.Cmd {
	m.turnRunning = true
	m.turnStart = time.Now()
	m.spinnerFrame = 0
	m.running = nil
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelTurn = cancel
	a := m.a
	sink := m.sink
	prog := m.sink.prog
	go func() {
		stats, err := a.ForceCompact(ctx, sink.Emit)
		cancel()
		switch {
		case err != nil:
			prog.Send(noticeMsg{text: "compact failed: " + err.Error()})
		case stats.FoldedMsgs == 0 && stats.ReclaimedTokens == 0:
			prog.Send(noticeMsg{text: "✦ nothing to compact yet"})
		}
		prog.Send(turnFinishedMsg{err: err})
	}()
	return m.startTicker()
}

// commitEcho promotes the deferred user-message echo from the live View() area
// to the scrollback, so it lands just above the turn's first output. Idempotent
// — a no-op once the echo has been committed or dropped.
func (m *tuiModel) commitEcho() {
	if m.echoPending == "" {
		return
	}
	m.printlnBlock(m.echoPending)
	m.echoPending = ""
	m.echoRestore = ""
}

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ta.SetWidth(msg.Width)
		_ = m.updateTextAreaHeight()
		// Replay a resumed session's prior turns into the scrollback once, now
		// that the real wrap width is known. No-op for a fresh session.
		if !m.historyReplayed {
			m.historyReplayed = true
			for _, line := range m.replayHistoryLines() {
				m.println(line)
			}
		}
		return m, m.flushPrints()

	case tea.KeyMsg:
		return m.handleKey(msg)

	case autoSubmitMsg:
		// Route exactly like a typed line: slash commands (e.g. /onboard) go
		// through the slash dispatcher; anything else starts a normal turn.
		if strings.HasPrefix(msg.text, "/") {
			return m.dispatchSlash(msg.text)
		}
		return m, m.startTurnEcho(msg.text, msg.text)

	case turnStartedMsg:
		m.assistantFirstBlock = true
		return m, m.flushPrints()

	case tickMsg:
		// Animate while a turn runs OR while background processes/workflows are
		// still going (so the live bottom panels keep ticking even between turns);
		// let the ticker die once everything is quiet.
		if !m.turnRunning && len(tools.RunningBackground()) == 0 && len(m.subAgents) == 0 && len(m.workflows) == 0 {
			m.tickerActive = false
			return m, m.flushPrints()
		}
		m.spinnerFrame++
		// End the answer hand-off sprint once its window elapses: release the
		// held prose so streaming resumes.
		if m.answerSprint && time.Since(m.sprintStart) >= answerSprintDur {
			m.flushSprint()
			return m, tea.Batch(m.flushPrints(), tickCmd())
		}
		return m, tickCmd()

	case agentEventMsg:
		m.handleEvent(msg.ev)
		return m, m.flushPrints()

	case noticeMsg:
		m.printlnBlock(noticeStyle.Render(msg.text))
		return m, m.flushPrints()

	case mcpReadyMsg:
		// Background MCP connect finished. Install the registry and refresh the
		// tool list so the next turn carries the MCP surface. Only when built-in
		// tools were enabled (executor wired); plain chat keeps an empty surface.
		// Recomputing cfg.tools busts the provider's tools-prompt cache once —
		// acceptable, and only if the user already sent a turn this first second.
		if msg.reg != nil && msg.reg.Len() > 0 {
			tools.SetMCPRegistry(msg.reg)
			if m.cfg.executor != nil {
				m.cfg.tools = tools.DefaultToolsFor(m.cfg.a.Model)
				// The tool array just picked up the bridge tools (or full MCP
				// schemas); redo the system prompt's "# Available MCP tools"
				// layer to match — it was necessarily empty at startup compose
				// time (see cfg.recomposeMCPManifest's doc comment).
				if m.cfg.recomposeMCPManifest != nil {
					m.cfg.recomposeMCPManifest()
				}
			}
			if m.cfg.verbosity.verbose() {
				m.printlnBlock(noticeStyle.Render(fmt.Sprintf("● MCP ready — %d server(s) connected", msg.reg.Len())))
			}
		}
		return m, m.flushPrints()

	case bgExitMsg:
		// Print a concise, Claude-Code-style scrollback notice so the user
		// sees the completion even when the model-facing <system-reminder>
		// is folded into a later turn.
		m.printlnBlock(bgDoneStyle.Render(fmt.Sprintf("● Background `%s` %s", msg.e.Command, msg.e.Status)))
		// Idle auto-turn: if no turn is running and nothing is queued, drain the
		// inbox (which holds the full <system-reminder> notice) and start a
		// turn so the model sees the completion immediately instead of waiting
		// for the next user message.
		if !m.turnRunning && len(m.queue) == 0 {
			if items := m.a.Inbox.Drain(); len(items) > 0 {
				s := strings.Join(agent.Texts(items), "\n\n")
				return m, tea.Sequence(m.flushPrints(), m.startTurnEcho(s, ""))
			}
		}
		return m, m.flushPrints()

	case workflowNoteMsg:
		// Scrollback notice for the user; the full <system-reminder> rode the
		// inbox for the model. Idle auto-turn mirrors bgExitMsg so the model
		// reacts to a finished background workflow immediately.
		status := msg.ev.Status
		if status == "" {
			status = "done"
		}
		m.printlnBlock(bgDoneStyle.Render(fmt.Sprintf("● Workflow %s (%s) %s", msg.ev.RunID, msg.ev.Description, status)))
		if !m.turnRunning && len(m.queue) == 0 {
			if items := m.a.Inbox.Drain(); len(items) > 0 {
				s := strings.Join(agent.Texts(items), "\n\n")
				return m, tea.Sequence(m.flushPrints(), m.startTurnEcho(s, ""))
			}
		}
		return m, m.flushPrints()

	case subAgentEventMsg:
		m.handleSubAgentEvent(msg.ev)
		// Ensure the animation ticker is running so the panel's spinner/elapsed
		// updates even between turns. startTicker is a no-op if a chain from an
		// earlier event is already alive — a busy background subagent firing many
		// of these while idle must not spawn a parallel tickMsg chain each time
		// (that used to make the spinner spin several times faster than intended).
		if !m.turnRunning {
			return m, tea.Batch(m.startTicker(), m.flushPrints())
		}
		return m, m.flushPrints()

	case workflowEventMsg:
		m.handleWorkflowEvent(msg.ev)
		// Keep the ticker alive for the workflow panel's spinner/elapsed clock —
		// see the subAgentEventMsg case above for why this goes through
		// startTicker rather than tickCmd() directly.
		if !m.turnRunning {
			return m, tea.Batch(m.startTicker(), m.flushPrints())
		}
		return m, m.flushPrints()

	case armWakeupMsg:
		// schedule_wakeup fired from the turn goroutine: (re)arm the loop timer.
		m.armWakeup(msg.delay, msg.prompt, msg.repeat)
		cadence := "next in " + msg.delay.String()
		if msg.repeat {
			cadence = "every " + msg.delay.String()
		}
		note := "● Loop armed — " + cadence
		if msg.reason != "" {
			note += " · " + msg.reason
		}
		m.printlnBlock(noticeStyle.Render(note))
		return m, m.flushPrints()

	case wakeupMsg:
		// An armed loop wakeup fired. The loop COEXISTS with the user, so a
		// wakeup that lands while a turn is running (a user message, or the
		// loop's own prior turn) is NOT dropped — re-arm it so the tick runs
		// once the session is idle again. When idle, run the loop prompt as a
		// fresh turn: interval mode re-arms for the next tick; dynamic mode
		// clears, and the model re-arms inside the turn (or ends the loop by
		// not calling schedule_wakeup).
		if m.turnRunning || len(m.queue) > 0 {
			m.armWakeup(msg.delay, msg.prompt, msg.repeat)
			return m, m.flushPrints()
		}
		if msg.repeat {
			m.armWakeup(msg.delay, msg.prompt, true)
		} else {
			m.wakeupFired() // dynamic: keep the clock; the model re-arms in the turn
		}
		m.printlnBlock(noticeStyle.Render("● Loop tick"))
		return m, tea.Sequence(m.flushPrints(), m.startTurnEcho(msg.prompt, ""))

	case cancelWakeupMsg:
		// schedule_wakeup(cancel=true): the model stopped the loop on request.
		if m.loopActive {
			m.printlnBlock(noticeStyle.Render("● Loop stopped"))
		}
		m.cancelWakeup()
		return m, m.flushPrints()

	case suggestionMsg:
		// Show the follow-up only while idle with an empty input — a new turn or
		// in-progress typing makes a stale suggestion unwelcome.
		if !m.turnRunning && strings.TrimSpace(m.ta.Value()) == "" {
			m.setSuggestion(msg.text)
		}
		return m, m.flushPrints()

	case titleMsg:
		// Persist the generated title (silent — it surfaces in the session list).
		m.titlePending = false
		if msg.text != "" && !m.cfg.noSave {
			_ = m.cfg.session.SetTitle(msg.text)
		}
		return m, m.flushPrints()

	case subAgentNoteMsg:
		// A sub-agent finished this round — drop it from the live panel (it's
		// idle now; a later Continue re-adds it via a fresh "started").
		m.removeSubAgent(msg.ev.AgentID)
		// Async sub-agent completion: the full result rode into the conversation
		// via Inbox; no scrollback notice needed.
		// Idle auto-turn: same logic as bgExitMsg — drain inbox and trigger a
		// turn so the model sees the notification immediately.
		if !m.turnRunning && len(m.queue) == 0 {
			if items := m.a.Inbox.Drain(); len(items) > 0 {
				s := strings.Join(agent.Texts(items), "\n\n")
				return m, m.startTurnEcho(s, "")
			}
		}
		return m, m.flushPrints()

	case turnEndedMsg:
		// Release any answer text still held by an in-flight hand-off sprint so
		// the trailing-block flush below sees it.
		m.flushSprint()
		// Commit a still-pending echo (a turn that produced no events — error,
		// or Ctrl+C). The Esc take-back path clears it before cancelling, so this
		// is a no-op there.
		m.commitEcho()
		// Flush any trailing assistant block (markdown-rendered); then render
		// the cache/error footer.
		if s, ok := m.flushTextString(); ok {
			m.printlnBlock(s)
		}
		if msg.err != nil && msg.err != context.Canceled {
			m.printlnBlock(errorStyle.Render("error: " + msg.err.Error()))
		} else {
			if s := turnSummaryLine(m.cfg.verbosity, msg.stats); s != "" {
				m.printlnBlock(s)
			}
			if c := cacheLine(m.cfg.verbosity, msg.reply); c != "" {
				m.printlnBlock(c)
			}
		}
		// turnEndedMsg only marks the end of the agent loop's output; the turn
		// goroutine is still alive until turnFinishedMsg. Do NOT reset turnRunning
		// or start the next queued/inbox turn here — that is handleTurnFinished's
		// job, and it fires the moment the goroutine actually returns. Starting a
		// turn now (the old eager-restart path) launched a second runTurn goroutine
		// while the interrupted one was still draining a blocking tool dispatch;
		// the two then interleaved their History.Append calls and produced a
		// structurally invalid log (consecutive assistant messages, a tool_use
		// answered twice) that permanently 400'd the session. Keeping turnRunning
		// true until the goroutine returns makes every `!turnRunning` start-gate a
		// correct single-turn guardrail. A background notice that races in via the
		// Inbox during this window is drained by handleTurnFinished, so nothing is
		// lost — only deferred by the few ms it takes the cancelled turn to unwind.

		// On a clean completion, kick off the off-loop after-turn helpers:
		// a follow-up suggestion (when enabled via --suggest) and, once per
		// session, a generated title for the session list. Both are skipped on
		// error/interrupt.
		if msg.err == nil {
			cmds := []tea.Cmd{m.flushPrints()}
			// While a goal is active the continuation kick starts the next
			// turn immediately and would discard the suggestion unread — on
			// an unbounded loop that is one paid throwaway call per
			// iteration. An active goal makes "suggest my next message"
			// noise anyway; skip it.
			goalActive := false
			if m.goalsWired() {
				if g, ok := m.cfg.session.GoalSnapshot(); ok && g.Status == agent.GoalActive {
					goalActive = true
				}
			}
			if m.cfg.suggest && !goalActive {
				cmds = append(cmds, m.suggestCmd())
			}
			if c := m.titleCmd(); c != nil {
				cmds = append(cmds, c)
			}
			return m, tea.Batch(cmds...)
		}
		return m, m.flushPrints()

	case turnFinishedMsg:
		return m.handleTurnFinished(msg.err)

	case askMsg:
		m.openModal(msg)
		return m, m.flushPrints()

	}
	return m, m.flushPrints()
}

// handleEvent updates model state for a streaming agent event.  Text deltas
// are accumulated in m.partial so they render live in View(); only complete
// markdown blocks are promoted to the scrollback via println/flushPrints.
// Tool events flush any pending text first, then commit their line/card.
// maxSubAgentRecentTools bounds the live tool-chain shown per sub-agent.
const maxSubAgentRecentTools = 4

// handleSubAgentEvent folds one sub-agent runtime event into the live panel
// state. "started" creates/refreshes the entry; "tool"/"tool_error" append to
// its chain.
func (m *tuiModel) handleSubAgentEvent(ev tools.SubAgentEvent) {
	// "done" removes the entry; handled before the create-on-miss below so a
	// late done can't resurrect a panel slot the completion note already
	// removed.
	if ev.Kind == "done" {
		m.removeSubAgent(ev.AgentID)
		return
	}
	sa := m.subAgents[ev.AgentID]
	if sa == nil {
		sa = &subAgentUI{description: ev.Description, agentType: ev.AgentType, start: time.Now()}
		m.subAgents[ev.AgentID] = sa
		m.subAgentOrder = append(m.subAgentOrder, ev.AgentID)
	}
	switch ev.Kind {
	case "started":
		// A fresh round (sub_agent): reset the chain, keep the slot.
		sa.toolCount = 0
		sa.recent = nil
		sa.history = nil
		sa.errored = false
		if ev.Description != "" {
			sa.description = ev.Description
		}
		if ev.AgentType != "" {
			sa.agentType = ev.AgentType
		}
	case "tool", "tool_error":
		sa.toolCount++
		name := ev.ToolName
		if ev.Kind == "tool_error" {
			sa.errored = true
			name += " ✗"
		}
		sa.recent = append(sa.recent, name)
		if len(sa.recent) > maxSubAgentRecentTools {
			sa.recent = sa.recent[len(sa.recent)-maxSubAgentRecentTools:]
		}
		sa.history = append(sa.history, name)
	}
}

// handleWorkflowEvent folds one background workflow event into the live panel
// state. "started" creates the entry; "progress" updates the latest line; "done"
// removes it (the completion notice is rendered separately by workflowNoteMsg).
func (m *tuiModel) handleWorkflowEvent(ev tools.WorkflowEvent) {
	switch ev.Kind {
	case "started":
		if m.workflows[ev.RunID] == nil {
			m.workflows[ev.RunID] = &workflowUI{
				description: ev.Description,
				status:      "running",
				start:       time.Now(),
			}
		}
	case "progress":
		if wf := m.workflows[ev.RunID]; wf != nil {
			wf.lastLine = ev.Line
		}
	case "done":
		delete(m.workflows, ev.RunID)
	}
}

// workflowOrder returns running workflow ids sorted by start time (oldest first)
// so the panel order is stable across ticks. Run ids are "wf_N"; if timestamps
// tie, the numeric suffix is used so "wf_10" sorts after "wf_2".
func (m *tuiModel) workflowOrder() []string {
	ids := make([]string, 0, len(m.workflows))
	for id := range m.workflows {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		wi, wj := m.workflows[ids[i]], m.workflows[ids[j]]
		if !wi.start.Equal(wj.start) {
			return wi.start.Before(wj.start)
		}
		return workflowSeqLess(ids[i], ids[j])
	})
	return ids
}

// workflowSeqLess compares two "wf_N" run ids by their numeric suffix.
func workflowSeqLess(a, b string) bool {
	na, errA := strconv.Atoi(strings.TrimPrefix(a, "wf_"))
	nb, errB := strconv.Atoi(strings.TrimPrefix(b, "wf_"))
	if errA != nil || errB != nil {
		return a < b
	}
	return na < nb
}

// removeSubAgent drops a sub-agent from the live panel once it finishes a round.
func (m *tuiModel) removeSubAgent(id string) {
	if _, ok := m.subAgents[id]; !ok {
		return
	}
	delete(m.subAgents, id)
	removedIdx := -1
	for i, x := range m.subAgentOrder {
		if x == id {
			m.subAgentOrder = append(m.subAgentOrder[:i], m.subAgentOrder[i+1:]...)
			removedIdx = i
			break
		}
	}
	// Adjust focus if the removed agent had focus.
	if m.subAgentFocus >= 0 {
		if removedIdx < m.subAgentFocus {
			m.subAgentFocus--
		} else if removedIdx == m.subAgentFocus {
			if m.subAgentFocus >= len(m.subAgentOrder) {
				m.subAgentFocus = len(m.subAgentOrder) - 1
			}
			if m.subAgentFocus < 0 {
				m.subAgentFocus = -1
			}
		}
	}
}

// suggestCmd asks the model for one follow-up suggestion off the event loop.
// A nil/empty result yields a nil msg, which bubbletea ignores.
func (m *tuiModel) suggestCmd() tea.Cmd {
	a := m.a
	// Same toolbelt as the agentic loop, so the suggest request's
	// tools→system→history prefix matches and hits the prompt cache.
	tools := m.cfg.tools
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		s, err := a.Suggest(ctx, tools)
		if err != nil || strings.TrimSpace(s) == "" {
			return nil
		}
		return suggestionMsg{text: s}
	}
}

// titleCmd generates a session title off the event loop, once per session. It
// returns nil (no-op) when the session is already titled, a generation is
// already in flight, saving is off, or there's no turn to summarize yet — so it
// fires exactly once, after the first completed turn. The result is persisted
// by the titleMsg handler.
func (m *tuiModel) titleCmd() tea.Cmd {
	if m.cfg.noSave || m.titlePending || m.cfg.session.Title != "" {
		return nil
	}
	if m.a.History.Len() == 0 {
		return nil
	}
	m.titlePending = true
	a := m.a
	tools := m.cfg.tools // same toolbelt as the loop, so the request hits the prompt cache
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		t, err := a.GenerateTitle(ctx, tools)
		if err != nil {
			return titleMsg{text: ""}
		}
		return titleMsg{text: t}
	}
}

// setSuggestion shows s as ghost text in the empty input box (the placeholder),
// truncated to one line so it never wraps the input.
func (m *tuiModel) setSuggestion(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	m.suggestion = s
	m.ta.Placeholder = truncate1Line(s)
}

// clearSuggestion drops the pending suggestion and restores the idle hint.
func (m *tuiModel) clearSuggestion() {
	if m.suggestion == "" {
		return
	}
	m.suggestion = ""
	m.ta.Placeholder = inputPlaceholder
}

// acceptSuggestion fills the input with the pending suggestion for the user to
// edit or send, then clears it.
func (m *tuiModel) acceptSuggestion() {
	if m.suggestion == "" {
		return
	}
	m.ta.SetValue(m.suggestion)
	m.ta.CursorEnd()
	m.clearSuggestion()
}

func (m *tuiModel) handleEvent(ev agent.AgentEvent) {
	// Promote the deferred user echo above this turn's first output so the
	// transcript order (your message → reply) is preserved.
	m.commitEcho()
	// Any event other than a continued answer delta ends a hand-off sprint and
	// releases the held text first, so a following tool call / turn-end commits
	// in the right order.
	if ev.Kind != agent.EventTextDelta {
		m.flushSprint()
	}
	switch ev.Kind {
	case agent.EventThinkingDelta:
		// The terminal never renders the reasoning trace — that display lives
		// only on the Web UI (gated by show_reasoning). Here the delta only
		// feeds the activity line's output-token readout, so a long agentic
		// turn still reads as progress without spilling thinking into the TUI.
		m.turnOutChars += len(ev.Text)
		return

	case agent.EventToolInputDelta:
		// The model is still streaming this tool call's arguments (e.g. a large
		// write_file content). Surface a live byte counter so the wait reads as
		// progress, not a freeze. Reset when a different tool's args begin.
		if ev.ToolID != m.toolStreamID {
			m.toolStreamID = ev.ToolID
			m.toolStreamBytes = 0
		}
		m.toolStreamName = ev.ToolName
		m.toolStreamBytes += len(ev.InputDelta)
		m.turnOutChars += len(ev.InputDelta)
		return

	case agent.EventTextDelta:
		m.turnOutChars += len(ev.Text)
		if m.answerSprint {
			// Mid-sprint: keep the prose held; the live count climbs as these
			// held deltas land, which is what makes the ↓ counter race.
			m.sprintBuf.WriteString(ev.Text)
			return
		}
		// First answer text after a real reasoning/uplink phase: hold it for a
		// brief window and let the ↓ counter sprint as a hand-off flourish. A
		// quick direct reply (little prior output) skips the hold and streams
		// immediately.
		if m.assistantFirstBlock && m.partial.Len() == 0 &&
			m.turnOutChars-len(ev.Text) >= answerSprintMinChars {
			m.answerSprint = true
			m.sprintStart = time.Now()
			m.sprintStartTok = (m.turnOutChars - len(ev.Text)) / 4
			m.sprintBuf.Reset()
			m.sprintBuf.WriteString(ev.Text)
			return
		}
		m.appendText(ev.Text)
		return

	case agent.EventToolStarted:
		// Args finished streaming; the tool now executes. Clear the stream
		// readout so the running-card spinner (or one-line status) takes over.
		m.toolStreamName, m.toolStreamID, m.toolStreamBytes = "", "", 0
		if m.toolInput == nil {
			m.toolInput = map[string]map[string]any{}
		}
		m.toolInput[ev.ToolID] = ev.Input
		// The plain path keeps the dense one-liner transcript (started line now,
		// ✓/✗ later) — it matches the headless renderer.
		if m.cfg.plain {
			m.commitToolLine(fmt.Sprintf("↳ %s: %s", ev.ToolName, summariseInput(ev.Input)))
			return
		}
		// Rich TUI: every tool suppresses its started line and shows a live
		// spinner indicator in the View region (animated by the ticker) until
		// the done event commits the card (card tools) or status line (others).
		verb, target := cardVerbFor(ev.ToolName), ""
		if verb == "" {
			verb, target = ev.ToolName, summariseInput(ev.Input)
		} else {
			target = cardTargetFor(ev.ToolName, ev.Input)
		}
		m.running = &runningTool{verb: verb, target: target, start: time.Now()}
		return

	case agent.EventToolProgress:
		// Rich TUI: keep a dimmed tail under the live spinner (see View) instead
		// of committing to the transcript — the done card/status line still
		// carries the full output once the call finishes. The chunk is raw
		// command stdout/stderr — sanitize before it ever reaches the terminal,
		// the same control-byte injection surface #1101/#1104 closed for tool
		// results and inputs (a stray ESC could move the cursor or erase the
		// "[Ctrl+B] background" hint on the next line).
		chunk := boundProgressChunk(sanitizeForPrompt(ev.Chunk))
		if m.cfg.plain {
			m.commitToolLine("│ " + chunk)
			return
		}
		if m.running != nil && chunk != "" {
			m.running.tail = append(m.running.tail, chunk)
			if n := len(m.running.tail) - runningTailMaxLines; n > 0 {
				m.running.tail = m.running.tail[n:]
			}
		}
		return

	case agent.EventToolDone:
		input := m.toolInput[ev.ToolID]
		delete(m.toolInput, ev.ToolID)
		elapsed := time.Duration(0)
		if m.running != nil {
			elapsed = time.Since(m.running.start)
		}
		m.running = nil // the finished card replaces the live indicator
		m.commitToolLine(m.renderToolOutcome(ev.ToolName, input, ev.Output, false, elapsed))
		return

	case agent.EventToolError:
		input := m.toolInput[ev.ToolID]
		delete(m.toolInput, ev.ToolID)
		elapsed := time.Duration(0)
		if m.running != nil {
			elapsed = time.Since(m.running.start)
		}
		m.running = nil
		m.commitToolLine(m.renderToolOutcome(ev.ToolName, input, firstNonEmpty(ev.Output, ev.Err), true, elapsed))
		return

	case agent.EventCompactStarted:
		m.compacting = true
		m.compactStart = time.Now()
		m.compactTokens = 0
		m.compactPreview = ""
		if ev.Compact != nil {
			m.compactMaxTokens = ev.Compact.MaxTokens
		}
		return

	case agent.EventCompactProgress:
		if ev.Compact != nil {
			m.compactTokens = ev.Compact.SummaryTokens
		}
		// Keep a bounded tail of the streamed summary for the live preview.
		m.compactPreview = lastRunes(m.compactPreview+ev.Chunk, 240)
		return

	case agent.EventCompactDone:
		m.compacting = false
		m.compactPreview = ""
		// Only announce a real reduction; a no-op (summary failed / unchanged)
		// clears the indicator silently rather than implying work was done.
		if c := ev.Compact; c != nil && c.AfterTokens > 0 && c.AfterTokens < c.BeforeTokens {
			if c.FoldedMsgs == 0 && c.ReclaimedTokens > 0 {
				// Cheap tier: stale tool output reclaimed without a summarize.
				m.printlnBlock(noticeStyle.Render(fmt.Sprintf(
					"✦ reclaimed stale tool output · ~%s → ~%s tokens",
					humanTokens(c.BeforeTokens), humanTokens(c.AfterTokens))))
			} else {
				m.printlnBlock(noticeStyle.Render(fmt.Sprintf(
					"✦ compacted context · folded %d message(s) · ~%s → ~%s tokens",
					c.FoldedMsgs, humanTokens(c.BeforeTokens), humanTokens(c.AfterTokens))))
			}
		}
		return

	case agent.EventGoalUpdated:
		m.handleGoalUpdated(ev.Goal)
		return

	case agent.EventSteerInjected:
		// Inbox drained mid-turn: print the steer messages to the scrollback
		// immediately so they appear in chronological order (before the next
		// assistant reply), and remove them from the pending live display.
		// First commit any buffered assistant text so the prior reply's trailing
		// paragraph stays above the steer line (e.g. when a steer lands right
		// after the model finished a text answer and the turn loops to answer it).
		if s, ok := m.flushTextString(); ok {
			m.printlnBlock(s)
		}
		// Skip injected model-facing spans (<system-reminder>, <goal_context>)
		// and empty text (an image-only steer carries its payload in blocks,
		// not Messages).
		for _, s := range ev.Messages {
			if visible := strings.TrimSpace(agent.StripSystemReminders(s)); visible != "" {
				m.printlnBlock(userEchoStyle.Render("> ") + visible)
			}
		}
		// pendingSteer is FIFO and mirrors the inbox, so the drained messages
		// are always a prefix.
		n := len(ev.Messages)
		if n >= len(m.pendingSteer) {
			m.pendingSteer = nil
		} else {
			m.pendingSteer = m.pendingSteer[n:]
		}
		return
	}
}

// rendersCard reports whether the TUI should render this tool as a rich card.
// --plain (cfg.plain) forces one-line status for every tool, matching the
// plain/headless path; otherwise card tools (cardVerbFor) get a card.
func (m *tuiModel) rendersCard(toolName string) bool {
	return !m.cfg.plain && cardVerbFor(toolName) != ""
}

// renderToolOutcome produces the scrollback item for a finished tool call: a
// rich card for card tools, a styled status line otherwise, or the dense
// one-liner under --plain. resultText is the tool's output (or its error
// message on failure); elapsed is the call's wall-clock duration (0 = unknown,
// e.g. resumed-history replay, which doesn't record timings). This is the
// single rendering path shared by the live done/error events and replay, so a
// resumed transcript looks like it did live.
func (m *tuiModel) renderToolOutcome(toolName string, input map[string]any, resultText string, isErr bool, elapsed time.Duration) string {
	if m.rendersCard(toolName) {
		return renderToolCard(toolName, input, resultText, isErr, m.width, elapsed)
	}
	// show_artifact's whole point is "present this file to the user"; the web
	// UI has the Artifacts panel, the TUI's equivalent is a click-to-open
	// file:// hyperlink (plain text on terminals without OSC 8 support).
	// Only absolute paths are linkified: a relative one re-resolved at render
	// time can diverge from the execute-time cwd on resumed-history replay,
	// producing a confident link to the wrong file.
	if toolName == "show_artifact" && !isErr && !m.cfg.plain {
		if p, _ := input["path"].(string); filepath.IsAbs(strings.TrimSpace(p)) {
			return tui.RenderArtifactStatus(strings.TrimSpace(p))
		}
	}
	if m.cfg.plain {
		if isErr {
			return toolErrStyle.Render(fmt.Sprintf("↳ %s ✗ — %s", toolName, truncate1Line(resultText)))
		}
		return fmt.Sprintf("↳ %s ✓", toolName)
	}
	errText := ""
	if isErr {
		errText = truncate1Line(resultText)
	}
	status := tui.RenderToolStatus(toolName, summariseInput(input), isErr, errText)
	// Non-card tools (MCP included) show only their input on the status line —
	// the result itself is invisible to the user (issue #1093). A short result
	// rides the line inline (control bytes neutralized: this is model-supplied
	// text going straight to the terminal, the same injection surface the
	// permission prompt sanitizes); anything longer is persisted and linked so
	// it stays reachable. Errors already carry their first line via errText.
	if trimmed := strings.TrimSpace(sanitizeForPrompt(resultText)); trimmed != "" && !isErr {
		if lines := strings.Count(trimmed, "\n") + 1; lines == 1 && len([]rune(trimmed)) <= 80 {
			status += " " + hintStyle.Render("— "+trimmed)
		} else if path, err := tools.WriteCardSpill(toolName, resultText); err == nil {
			label := "output (" + pluraliseLineCount(lines) + ") ↗"
			status += " " + tui.Hyperlink(tui.FileURI(path), hintStyle.Underline(true).Render(label))
		}
	}
	return status
}

// pluraliseLineCount renders "1 line" / "N lines" for the status-line link.
func pluraliseLineCount(n int) string {
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}

// renderToolOutcomeFull is renderToolOutcome's uncapped counterpart, used by
// /transcript to re-print a past tool call in full. Non-card tools already
// render their complete result (inline or hyperlinked, see renderToolOutcome)
// so only card tools need the dedicated uncapped path.
func (m *tuiModel) renderToolOutcomeFull(toolName string, input map[string]any, resultText string, isErr bool) string {
	if m.rendersCard(toolName) {
		if full := renderToolFull(toolName, input, resultText, isErr, m.width); full != "" {
			return full
		}
	}
	if isErr {
		// renderToolOutcome truncates a non-card tool's error to one 100-rune
		// line for the status row (errText) — fine live, but it would defeat
		// /transcript's whole point of showing the full text uncapped.
		status := tui.RenderToolStatus(toolName, summariseInput(input), true, "")
		if trimmed := strings.TrimSpace(sanitizeForPrompt(resultText)); trimmed != "" {
			for _, line := range strings.Split(trimmed, "\n") {
				status += "\n" + hintStyle.Render("  "+line)
			}
		}
		return status
	}
	return m.renderToolOutcome(toolName, input, resultText, isErr, 0)
}

// toolCallRecord is one completed tool_use/tool_result pair pulled from
// history for /transcript's re-print.
type toolCallRecord struct {
	name   string
	input  map[string]any
	result string
	isErr  bool
}

// recentToolCalls returns the last n completed tool calls from history in
// chronological order, pairing each tool_use with its tool_result the same
// way replayHistoryLines does. Calls still awaiting a result (mid-flight) are
// skipped. Returns fewer than n if history doesn't have that many.
func (m *tuiModel) recentToolCalls(n int) []toolCallRecord {
	msgs := m.a.History.Snapshot()
	results := map[string]agent.ContentBlock{}
	for _, msg := range msgs {
		for _, b := range msg.Blocks {
			if b.Type == "tool_result" {
				results[b.ToolUseID] = b
			}
		}
	}
	var all []toolCallRecord
	for _, msg := range msgs {
		if msg.Role != agent.RoleAssistant {
			continue
		}
		for _, b := range msg.Blocks {
			if b.Type != "tool_use" {
				continue
			}
			res, ok := results[b.ID]
			if !ok {
				continue
			}
			all = append(all, toolCallRecord{
				name:   b.Name,
				input:  b.Input,
				result: agent.StripRemindersForDisplay(res.Result),
				isErr:  res.IsError,
			})
		}
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all
}

// replayMaxTurns caps how many of a resumed session's most recent turns are
// replayed into scrollback; older turns collapse into a single omission line.
// Without a cap a few-hundred-turn session floods the terminal on resume,
// before the user can even type (#1097).
const replayMaxTurns = 20

// replayHistoryLines renders a resumed session's prior turns into scrollback
// lines exactly as they appeared live: user prompts and assistant replies
// verbatim (markdown unless --plain), tool calls through the same
// renderToolOutcome path as the live done/error events (rich cards, status
// lines, full error text). Returns nil for a fresh session (empty history).
func (m *tuiModel) replayHistoryLines() []string {
	msgs, omitted := recentTurns(m.a.History.Snapshot(), replayMaxTurns)
	if len(msgs) == 0 {
		return nil
	}
	// Pair each tool_use with its tool_result (by ID) so the collapsed line can
	// show the call's outcome.
	results := map[string]agent.ContentBlock{}
	for _, msg := range msgs {
		for _, b := range msg.Blocks {
			if b.Type == "tool_result" {
				results[b.ToolUseID] = b
			}
		}
	}

	var out []string
	if omitted > 0 {
		word := "turn"
		if omitted != 1 {
			word = "turns"
		}
		out = append(out, hintStyle.Render(fmt.Sprintf("… earlier history omitted (%d %s)", omitted, word)))
	}
	for _, msg := range msgs {
		switch msg.Role {
		case agent.RoleUser:
			// Skip pure tool_result carriers — only real typed prompts echo.
			if t := userMessageText(msg); strings.TrimSpace(t) != "" {
				out = append(out, userEchoStyle.Render("> ")+t)
			}
		case agent.RoleAssistant:
			if len(msg.Blocks) == 0 {
				if s := m.renderReplayText(msg.Content); s != "" {
					out = append(out, s)
				}
				continue
			}
			for _, b := range msg.Blocks {
				switch b.Type {
				case "text":
					if s := m.renderReplayText(b.Text); s != "" {
						out = append(out, s)
					}
				case "tool_use":
					res := results[b.ID]
					out = append(out, m.renderToolOutcome(
						b.Name, b.Input,
						agent.StripRemindersForDisplay(res.Result), res.IsError, 0))
				}
			}
		}
	}
	return out
}

// recentTurns splits msgs into turns — a RoleUser message plus everything
// up to (not including) the next one — and returns only the last max turns,
// plus how many leading turns were dropped. Any messages before the first
// RoleUser message (there shouldn't be any in practice) are kept attached to
// the first turn rather than silently dropped. max <= 0 disables the cap.
func recentTurns(msgs []agent.Message, max int) ([]agent.Message, int) {
	if max <= 0 || len(msgs) == 0 {
		return msgs, 0
	}
	var bounds []int
	for i, msg := range msgs {
		if msg.Role == agent.RoleUser {
			bounds = append(bounds, i)
		}
	}
	if len(bounds) <= max {
		return msgs, 0
	}
	start := bounds[len(bounds)-max]
	return msgs[start:], len(bounds) - max
}

// renderReplayText styles one block of restored assistant/user prose: markdown
// unless --plain. Empty input renders to "".
func (m *tuiModel) renderReplayText(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	if m.cfg.plain {
		return strings.TrimRight(s, "\n")
	}
	return m.md.render(s, m.width)
}

// userMessageText returns a user message's human-typed text, ignoring
// tool_result carriers, non-text (e.g. image) blocks, and any
// model-facing <system-reminder> spans the harness injected into the turn.
func userMessageText(msg agent.Message) string {
	var s string
	if len(msg.Blocks) == 0 {
		s = msg.Content
	} else {
		var b strings.Builder
		for _, blk := range msg.Blocks {
			if blk.Type == "text" {
				b.WriteString(blk.Text)
			}
		}
		s = b.String()
	}
	return strings.TrimSpace(agent.StripSystemReminders(s))
}

// flushSprint ends the answer hand-off sprint, if one is active, and releases
// the buffered answer text into the live partial so the prose resumes
// streaming. Idempotent — safe to call on any event or turn boundary.
func (m *tuiModel) flushSprint() {
	if !m.answerSprint {
		return
	}
	m.answerSprint = false
	buf := m.sprintBuf.String()
	m.sprintBuf.Reset()
	if buf != "" {
		m.appendText(buf)
	}
}

// appendText buffers a streamed text delta into m.partial. The partial is
// rendered live in View() so the user sees tokens arrive in real time. When a
// complete block boundary is found (blank line outside a code fence), that
// prefix is promoted to the scrollback via printlnBlock/flushPrints so it
// becomes part of the terminal's native scrollback history.
func (m *tuiModel) appendText(text string) {
	m.partial.WriteString(text)

	if m.cfg.plain {
		buf := m.partial.String()
		idx := strings.LastIndexByte(buf, '\n')
		if idx < 0 {
			return
		}
		complete := buf[:idx]
		m.partial.Reset()
		m.partial.WriteString(buf[idx+1:])
		if m.assistantFirstBlock {
			complete = assistantPrefixStyle.Render("◆ ") + complete
			m.assistantFirstBlock = false
		}
		m.println(complete)
		return
	}

	commit, rest := splitCommittableMarkdown(m.partial.String())
	if commit == "" {
		return
	}
	m.partial.Reset()
	m.partial.WriteString(rest)
	rendered := m.md.render(commit, m.width)
	if m.assistantFirstBlock {
		rendered = injectAssistantPrefix(rendered, assistantPrefixStyle.Render("◆ "))
		m.assistantFirstBlock = false
	}
	m.printlnBlock(rendered)
}

// flushText commits whatever assistant text is still buffered (the final or
// pre-tool block), rendered through glamour unless --plain. Returns nil when
// nothing is pending.
func (m *tuiModel) flushText() tea.Cmd {
	s, ok := m.flushTextString()
	if !ok {
		return nil
	}
	m.printlnBlock(s)
	return nil
}

// flushTextString returns the buffered assistant text (rendered through glamour
// unless --plain) and true when there was pending text. Used internally when
// multiple lines must be emitted in strict order.
func (m *tuiModel) flushTextString() (string, bool) {
	p := m.partial.String()
	if p == "" {
		return "", false
	}
	m.partial.Reset()
	if m.cfg.plain {
		if m.assistantFirstBlock {
			p = assistantPrefixStyle.Render("◆ ") + p
			m.assistantFirstBlock = false
		}
		return p, true
	}
	rendered := m.md.render(p, m.width)
	if m.assistantFirstBlock {
		rendered = injectAssistantPrefix(rendered, assistantPrefixStyle.Render("◆ "))
		m.assistantFirstBlock = false
	}
	return rendered, true
}

// commitToolLine flushes any in-progress text to the scrollback, then appends
// the tool line/card.
func (m *tuiModel) commitToolLine(line string) {
	if s, ok := m.flushTextString(); ok {
		m.printlnBlock(s)
	}
	m.printlnBlock(line)
}

// println queues a line for output to the terminal scrollback via
// tea.Println, emitted by flushPrints at the end of each Update cycle.
// In inline mode, committed lines scroll naturally above the live View() area.
func (m *tuiModel) println(line string) {
	m.printlnBuf = append(m.printlnBuf, line)
	m.lastPrintBlank = line == ""
}

// printlnBlock queues a block-level item (card, message echo, markdown block,
// notice) preceded by one blank separator line, so blocks breathe instead of
// stacking edge-to-edge (Claude Code style). Plain mode keeps the dense
// line-oriented layout, matching the headless renderer.
func (m *tuiModel) printlnBlock(line string) {
	if line == "" {
		return
	}
	if !m.cfg.plain && !m.lastPrintBlank {
		m.println("")
	}
	m.println(line)
}

// flushPrints returns a Cmd that prints all queued lines to the terminal
// in order, above the live View() area.
func (m *tuiModel) flushPrints() tea.Cmd {
	if len(m.printlnBuf) == 0 {
		return nil
	}
	cmds := make([]tea.Cmd, len(m.printlnBuf))
	for i, line := range m.printlnBuf {
		cmds[i] = tea.Println(line)
	}
	m.printlnBuf = nil
	return tea.Sequence(cmds...)
}

func (m *tuiModel) handleTurnFinished(err error) (tea.Model, tea.Cmd) {
	m.flushSprint() // safety net: never strand held answer text on interrupt
	m.turnRunning = false
	m.cancelTurn = nil
	m.running = nil // clear any live tool indicator (e.g. on interrupt)

	// Any steer messages that weren't drained via EventSteerInjected during
	// the turn (e.g. typed after the last loop iteration) are printed now so
	// they don't vanish from the transcript.
	for _, s := range m.pendingSteer {
		m.printlnBlock(userEchoStyle.Render("> ") + s)
	}
	m.pendingSteer = nil

	// Auto-save (history is well-formed even after an interrupt).
	if !m.cfg.noSave {
		m.cfg.session.SyncFrom(m.a.History)
		_ = m.cfg.session.Save()
	}

	// An aborted or errored turn parks the goal-continuation loop: an
	// interrupt means the user said stop, and chaining onto a persistent
	// error is unbounded paid retries. The zero-progress audit can't catch
	// either (partial replies were already billed). A rate-limited
	// continuation turn parks harder — usage_limited persists until
	// /goal resume.
	if err != nil && m.goalsWired() {
		if m.cfg.session.GoalContinuationPending() && agent.IsRateLimitErr(err) {
			if g, gerr := m.cfg.session.SetGoalStatus(agent.GoalUsageLimited); gerr == nil {
				m.printlnBlock(noticeStyle.Render("● Goal usage limited (provider rate limit) — /goal resume to retry"))
				m.goalLastStatus = g.Status
			}
		}
		m.cfg.session.SuppressGoalContinuation()
	}

	// Drain any inbox messages that weren't consumed during the turn and
	// run them as the next turn, ahead of explicitly-queued items. Injected
	// model-facing spans (<system-reminder>, <goal_context>) are not user
	// speech — strip them from the echo, and skip lines that were nothing else.
	if items := m.a.Inbox.Drain(); len(items) > 0 {
		it := pendingFromInbox(items)
		for _, line := range strings.Split(it.text, "\n\n") {
			if visible := strings.TrimSpace(agent.StripSystemReminders(line)); visible != "" {
				m.printlnBlock(userEchoStyle.Render("> ") + visible)
			}
		}
		m.queue = append([]pendingItem{it}, m.queue...)
	}

	// Dequeue the next pending turn, if any.
	if len(m.queue) > 0 {
		next := m.queue[0]
		m.queue = m.queue[1:]
		// A slash command queued mid-turn is dispatched now that we're idle.
		if strings.HasPrefix(next.text, "/") && len(next.blocks) == 0 {
			model, cmd := m.dispatchSlash(next.text)
			if cmd == nil {
				return model, m.flushPrints()
			}
			return model, tea.Sequence(m.flushPrints(), cmd)
		}
		return m, m.startQueued(next)
	}

	// Idle with nothing queued: an active goal keeps going. The session owns
	// the policy (status, zero-progress suppression); user input always won
	// above by reaching the queue/inbox branches first.
	if err == nil {
		if prompt, ok := m.goalContinuationKick(); ok {
			return m, tea.Sequence(m.flushPrints(), m.startTurnEcho(prompt, ""))
		}
	}
	return m, nil
}
