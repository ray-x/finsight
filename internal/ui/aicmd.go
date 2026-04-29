package ui

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/ray-x/finsight/internal/agent"
	"github.com/ray-x/finsight/internal/cache"
	"github.com/ray-x/finsight/internal/llm"
	"github.com/ray-x/finsight/internal/logger"
	"github.com/ray-x/finsight/internal/news"
	"github.com/ray-x/finsight/internal/yahoo"
)

// === AI command window ===
//
// The AI command window is a unified, interactive prompt editor that
// replaces the previous one-shot AI popups in watchlist / detail /
// portfolio views. Users type free-form prompts that can embed macros
// ("slash commands") which expand into structured context blocks sent
// to the LLM along with the raw prompt.
//
// Supported macros (case-insensitive):
//
//	/symbol:SYM       Quote snapshot for SYM (price, change, ranges, etc.)
//	/range:1D|1W|...  Timeframe hint (applies to subsequent /symbol)
//	/earning:SYM      Latest financials / analyst data for SYM
//	/portfolio        The user's whole portfolio table
//	/watchlist        The active watchlist group
//	/help             Inline cheatsheet (no LLM call)
//
// Example:
//
//	/summarise /symbol:NVDA in /range:1M, should I buy at 200?
//	Consider my current /portfolio for balance and risk.
//
// Output is rendered as markdown (headings, bold, tables, etc.) via
// renderMarkdown.

// AICmdStage describes which pane of the popup is active.
type AICmdStage int

const (
	aiCmdInput AICmdStage = iota
	aiCmdLoading
	aiCmdResult
)

// AICmdState is the interactive-prompt popup state.
type AICmdState struct {
	Active bool
	Stage  AICmdStage

	// Input buffer (unicode-safe via runes) and cursor position.
	Input  []rune
	Cursor int

	// Result rendering
	Result     string // raw markdown from LLM
	Err        string
	ScrollOff  int
	LastPrompt string // cache-key source

	// Autocomplete
	Suggestions    []aiSuggestion
	SuggestionSel  int
	ShowSuggestion bool

	// Prompt history (most-recent first)
	History    []string
	HistoryIdx int // -1 = live buffer

	// Loading indicator
	SpinnerFrame   int    // current spinner glyph index
	LoadingSubject string // short description of what's being analysed

	// Transient status line (e.g. "✓ copied to pbcopy"). Cleared on
	// the next keystroke.
	Toast string

	// Cursor blink state for the input prompt.
	CursorOn bool
}

type aiSuggestion struct {
	Display string
	Insert  string // text that replaces current token
	Hint    string
}

// Known macro names.
var aiMacros = []string{"/ask", "/symbol", "/range", "/earning", "/news", "/portfolio", "/watchlist", "/summarise", "/analyze", "/compare", "/help"}

// Known range tokens for /range: autocomplete (mirrors `timeframes`).
var aiRangeTokens = []string{"1D", "1W", "1M", "6M", "1Y", "3Y", "5Y", "10Y"}

// reMacro matches /name or /name:value (value ends at whitespace).
var reMacro = regexp.MustCompile(`(?i)/([a-z]+)(?::([^\s,]+))?`)

var (
	reHeadingLevel2  = regexp.MustCompile(`(?m)^##\s+`)
	reFactorVoteHead = regexp.MustCompile(`(?mi)^##\s*Factor Vote\s*$`)
	reJSONFence      = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")
)

// aiCmdResultMsg is emitted when the LLM finishes.
type aiCmdResultMsg struct {
	prompt string
	text   string
	err    error
}

// aiCmdTickMsg drives the loading spinner.
type aiCmdTickMsg struct{}

// aiCmdBlinkMsg drives the input cursor blink.
type aiCmdBlinkMsg struct{}

// aiSpinnerFrames is the animation sequence for the loading indicator.
var aiSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func aiCmdTickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return aiCmdTickMsg{} })
}

// aiCmdBlinkCmd schedules the next cursor-blink toggle. 500ms is a
// conventional terminal-cursor cadence.
func aiCmdBlinkCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return aiCmdBlinkMsg{} })
}

// === Entry points ===

// openAICmd opens the command window with a smart seed based on current
// view context. seed is pre-filled into the input; user can edit freely
// before submitting.
func (m Model) openAICmd(seed string) (tea.Model, tea.Cmd) {
	m.aicmd = AICmdState{
		Active:     true,
		Stage:      aiCmdInput,
		Input:      []rune(seed),
		Cursor:     len([]rune(seed)),
		HistoryIdx: -1,
		History:    m.aicmd.History, // preserve across opens
		CursorOn:   true,
	}
	return m, aiCmdBlinkCmd()
}

// openAICmdAuto opens the window with the given prompt and immediately
// submits it to the LLM. Used for the `a` shortcut which fires a brief
// per-symbol analysis without requiring the user to press Enter.
func (m Model) openAICmdAuto(prompt string) (tea.Model, tea.Cmd) {
	m.aicmd = AICmdState{
		Active:     true,
		Stage:      aiCmdInput,
		Input:      []rune(prompt),
		Cursor:     len([]rune(prompt)),
		HistoryIdx: -1,
		History:    m.aicmd.History,
		CursorOn:   true,
	}
	mi, cmd := m.submitAICmd()
	return mi, tea.Batch(cmd, aiCmdBlinkCmd())
}

// aiCmdAutoPrompt builds a brief per-symbol analysis prompt for the
// current view. Returns "" if there is no symbol in context.
func (m Model) aiCmdAutoPrompt() string {
	var sym, tf string
	switch m.mode {
	case viewDetail, viewWatchlist:
		if m.selected < len(m.items) {
			sym = m.items[m.selected].Symbol
			tf = m.currentTimeframe().Label
		}
	case viewPortfolio:
		if len(m.portfolioItems) > 0 && m.portfolioSelected < len(m.portfolioItems) {
			sym = m.portfolioItems[m.portfolioSelected].Symbol
		}
	}
	if sym == "" {
		return ""
	}
	parts := []string{"/analyze /symbol:" + sym}
	if tf != "" {
		parts = append(parts, "in /range:"+tf)
	}
	parts = append(parts, "and /earning:"+sym+".")
	parts = append(parts, "Include recent /news:"+sym+" to explain short-term moves.")
	parts = append(parts, "Provide a brief analysis covering price action, valuation, financial health, and a clear verdict.")
	return strings.Join(parts, " ")
}

// aiCmdSeed returns a context-aware starter prompt for each view. It
// does NOT execute; the user can edit before pressing Enter.
func (m Model) aiCmdSeed() string {
	switch m.mode {
	case viewDetail:
		if m.selected < len(m.items) {
			sym := m.items[m.selected].Symbol
			tf := m.currentTimeframe().Label
			return fmt.Sprintf("/analyze /symbol:%s in /range:%s", sym, tf)
		}
	case viewPortfolio:
		if len(m.portfolioItems) > 0 && m.portfolioSelected < len(m.portfolioItems) {
			sym := m.portfolioItems[m.portfolioSelected].Symbol
			return fmt.Sprintf("/analyze /symbol:%s using my /portfolio", sym)
		}
		return "Review my /portfolio"
	case viewWatchlist:
		return "/summarise /watchlist"
	}
	return ""
}

// === Key handling ===

func (m Model) handleAICmdKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Suggestion dropdown takes priority for navigation keys when open.
	if m.aicmd.ShowSuggestion && len(m.aicmd.Suggestions) > 0 {
		switch msg.String() {
		case "down", "ctrl+n":
			m.aicmd.SuggestionSel = (m.aicmd.SuggestionSel + 1) % len(m.aicmd.Suggestions)
			return m, nil
		case "up", "ctrl+p":
			m.aicmd.SuggestionSel = (m.aicmd.SuggestionSel - 1 + len(m.aicmd.Suggestions)) % len(m.aicmd.Suggestions)
			return m, nil
		case "tab", "right":
			m.applyAICmdSuggestion()
			return m, nil
		case "esc":
			m.aicmd.ShowSuggestion = false
			return m, nil
		}
	}

	s := msg.String()
	// Any keystroke clears a lingering toast.
	m.aicmd.Toast = ""
	switch s {
	case "esc":
		m.aicmd = AICmdState{History: m.aicmd.History}
		return m, nil
	case "enter":
		return m.submitAICmd()
	case "ctrl+l":
		m.aicmd.Input = nil
		m.aicmd.Cursor = 0
		m.aicmd.ShowSuggestion = false
		m.aicmd.Stage = aiCmdInput
		m.aicmd.Result = ""
		m.aicmd.Err = ""
		return m, nil
	case "ctrl+r":
		if m.aicmd.LastPrompt != "" {
			return m, m.runAICmd(m.aicmd.LastPrompt, true)
		}
		return m, nil
	case "ctrl+u":
		// delete to start of line
		m.aicmd.Input = m.aicmd.Input[m.aicmd.Cursor:]
		m.aicmd.Cursor = 0
		m.recomputeAISuggestions()
		return m, nil
	case "ctrl+w":
		m.aiCmdDeleteWord()
		m.recomputeAISuggestions()
		return m, nil
	case "ctrl+a", "home":
		m.aicmd.Cursor = 0
		return m, nil
	case "ctrl+e", "end":
		m.aicmd.Cursor = len(m.aicmd.Input)
		return m, nil
	case "left":
		if m.aicmd.Cursor > 0 {
			m.aicmd.Cursor--
		}
		return m, nil
	case "right":
		if m.aicmd.Cursor < len(m.aicmd.Input) {
			m.aicmd.Cursor++
		}
		return m, nil
	case "up":
		// Up always navigates prompt history. If the result view is
		// showing, switch back to input mode first so the recalled
		// prompt is editable. Use PgUp/j/k to scroll the result.
		if m.aicmd.Stage == aiCmdResult {
			m.aicmd.Stage = aiCmdInput
			m.aicmd.ScrollOff = 0
		}
		if len(m.aicmd.History) > 0 {
			idx := m.aicmd.HistoryIdx + 1
			if idx >= len(m.aicmd.History) {
				idx = len(m.aicmd.History) - 1
			}
			m.aicmd.HistoryIdx = idx
			m.aicmd.Input = []rune(m.aicmd.History[idx])
			m.aicmd.Cursor = len(m.aicmd.Input)
		}
		return m, nil
	case "down":
		// Down always navigates prompt history forward.
		if m.aicmd.Stage == aiCmdResult {
			m.aicmd.Stage = aiCmdInput
			m.aicmd.ScrollOff = 0
		}
		if m.aicmd.HistoryIdx >= 0 {
			m.aicmd.HistoryIdx--
			if m.aicmd.HistoryIdx < 0 {
				m.aicmd.Input = nil
			} else {
				m.aicmd.Input = []rune(m.aicmd.History[m.aicmd.HistoryIdx])
			}
			m.aicmd.Cursor = len(m.aicmd.Input)
		}
		return m, nil
	case "k":
		// Vim-style scroll-up while the result is showing. In input
		// mode, fall through so "k" can be typed as a character.
		if m.aicmd.Stage == aiCmdResult {
			if m.aicmd.ScrollOff > 0 {
				m.aicmd.ScrollOff--
			}
			return m, nil
		}
	case "j":
		if m.aicmd.Stage == aiCmdResult {
			m.aicmd.ScrollOff++
			return m, nil
		}
	case "y":
		// Copy the current AI result to the OS clipboard. Only active
		// while the result is showing so "y" can still be typed as a
		// character while composing a prompt.
		if m.aicmd.Stage == aiCmdResult && m.aicmd.Result != "" {
			if via, err := copyToClipboard(m.aicmd.Result); err == nil {
				n := len([]rune(m.aicmd.Result))
				m.aicmd.Toast = fmt.Sprintf("✓ copied %d chars to clipboard (%s)", n, via)
			} else {
				m.aicmd.Toast = "⚠ clipboard copy failed: " + err.Error()
			}
			return m, nil
		}
	case "pgup":
		if m.aicmd.ScrollOff > 10 {
			m.aicmd.ScrollOff -= 10
		} else {
			m.aicmd.ScrollOff = 0
		}
		return m, nil
	case "pgdown":
		m.aicmd.ScrollOff += 10
		return m, nil
	case "tab":
		m.applyAICmdSuggestion()
		return m, nil
	case "backspace":
		if m.aicmd.Cursor > 0 {
			m.aicmd.Input = append(m.aicmd.Input[:m.aicmd.Cursor-1], m.aicmd.Input[m.aicmd.Cursor:]...)
			m.aicmd.Cursor--
			m.recomputeAISuggestions()
		}
		return m, nil
	case "delete":
		if m.aicmd.Cursor < len(m.aicmd.Input) {
			m.aicmd.Input = append(m.aicmd.Input[:m.aicmd.Cursor], m.aicmd.Input[m.aicmd.Cursor+1:]...)
			m.recomputeAISuggestions()
		}
		return m, nil
	}

	// Regular rune input
	if len(msg.Runes) > 0 {
		// Typing after a result transitions back to the input stage so
		// up/down navigates prompt history (rather than scrolling the
		// result) and ghost-text renders correctly.
		if m.aicmd.Stage == aiCmdResult {
			m.aicmd.Stage = aiCmdInput
			m.aicmd.ScrollOff = 0
		}
		for _, r := range msg.Runes {
			m.aicmd.Input = append(m.aicmd.Input[:m.aicmd.Cursor], append([]rune{r}, m.aicmd.Input[m.aicmd.Cursor:]...)...)
			m.aicmd.Cursor++
		}
		m.recomputeAISuggestions()
	}
	return m, nil
}

func (m *Model) aiCmdDeleteWord() {
	if m.aicmd.Cursor == 0 {
		return
	}
	i := m.aicmd.Cursor
	// skip trailing spaces
	for i > 0 && m.aicmd.Input[i-1] == ' ' {
		i--
	}
	// delete word
	for i > 0 && m.aicmd.Input[i-1] != ' ' {
		i--
	}
	m.aicmd.Input = append(m.aicmd.Input[:i], m.aicmd.Input[m.aicmd.Cursor:]...)
	m.aicmd.Cursor = i
}

// === Autocomplete ===

// currentToken returns the word under the cursor and its start position.
func (m Model) currentAIToken() (string, int) {
	i := m.aicmd.Cursor
	for i > 0 && m.aicmd.Input[i-1] != ' ' {
		i--
	}
	return string(m.aicmd.Input[i:m.aicmd.Cursor]), i
}

func (m *Model) recomputeAISuggestions() {
	tok, _ := m.currentAIToken()
	if tok == "" || !strings.HasPrefix(tok, "/") {
		m.aicmd.Suggestions = nil
		m.aicmd.ShowSuggestion = false
		return
	}

	lower := strings.ToLower(tok)
	var sugs []aiSuggestion

	// Detect `/macro:partial` vs `/partial`
	if colon := strings.Index(lower, ":"); colon > 0 {
		macro := lower[:colon]
		partial := strings.ToUpper(strings.TrimSpace(tok[colon+1:]))
		switch macro {
		case "/symbol", "/earning":
			for _, s := range m.aiSymbolCandidates() {
				if partial == "" || strings.HasPrefix(strings.ToUpper(s.Symbol), partial) {
					sugs = append(sugs, aiSuggestion{
						Display: fmt.Sprintf("%s  %s", s.Symbol, s.Name),
						Insert:  macro + ":" + s.Symbol,
						Hint:    s.Name,
					})
				}
				if len(sugs) >= 8 {
					break
				}
			}
		case "/range":
			for _, r := range aiRangeTokens {
				if partial == "" || strings.HasPrefix(r, partial) {
					sugs = append(sugs, aiSuggestion{Display: r, Insert: macro + ":" + r})
				}
			}
		}
	} else {
		for _, mac := range aiMacros {
			if strings.HasPrefix(mac, lower) {
				sugs = append(sugs, aiSuggestion{Display: mac, Insert: mac, Hint: aiMacroHint(mac)})
			}
		}
	}

	m.aicmd.Suggestions = sugs
	if m.aicmd.SuggestionSel >= len(sugs) {
		m.aicmd.SuggestionSel = 0
	}
	m.aicmd.ShowSuggestion = len(sugs) > 0
}

func aiMacroHint(name string) string {
	switch name {
	case "/symbol":
		return "insert symbol quote context"
	case "/range":
		return "timeframe (1D..5Y)"
	case "/earning":
		return "latest earnings/financials"
	case "/portfolio":
		return "whole portfolio table"
	case "/watchlist":
		return "current watchlist group"
	case "/summarise", "/analyze", "/compare":
		return "prompt verb (optional)"
	case "/help":
		return "show macro cheatsheet"
	}
	return ""
}

type aiSymCandidate struct {
	Symbol string
	Name   string
}

// aiSymbolCandidates returns de-duplicated symbols from the current
// watchlist group and the user's portfolio.
func (m Model) aiSymbolCandidates() []aiSymCandidate {
	seen := map[string]bool{}
	var out []aiSymCandidate
	for _, it := range m.items {
		if seen[it.Symbol] {
			continue
		}
		seen[it.Symbol] = true
		out = append(out, aiSymCandidate{Symbol: it.Symbol, Name: it.Name})
	}
	if m.portfolio != nil {
		for _, p := range m.portfolio.Positions {
			if seen[p.Symbol] {
				continue
			}
			seen[p.Symbol] = true
			out = append(out, aiSymCandidate{Symbol: p.Symbol, Name: p.Symbol})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out
}

func (m *Model) applyAICmdSuggestion() {
	if !m.aicmd.ShowSuggestion || len(m.aicmd.Suggestions) == 0 {
		return
	}
	sug := m.aicmd.Suggestions[m.aicmd.SuggestionSel]
	insert := sug.Insert
	// In detail view, auto-attach the current symbol when the user picks
	// `/symbol` or `/earning` so they don't have to type the ticker.
	if sym := m.aiCmdContextSymbol(); sym != "" {
		switch insert {
		case "/symbol", "/earning", "/news":
			insert = insert + ":" + sym
		}
	}
	_, start := m.currentAIToken()
	// Replace token with insert text
	newInput := append([]rune(nil), m.aicmd.Input[:start]...)
	newInput = append(newInput, []rune(insert)...)
	newInput = append(newInput, m.aicmd.Input[m.aicmd.Cursor:]...)
	m.aicmd.Input = newInput
	m.aicmd.Cursor = start + len([]rune(insert))
	m.aicmd.ShowSuggestion = false
}

// aiCmdContextSymbol returns the current detail-view symbol, or "" if
// the user is not on a single-symbol view. Used to auto-expand bare
// `/symbol` and `/earning` macros.
func (m Model) aiCmdContextSymbol() string {
	if m.mode == viewDetail && m.selected < len(m.items) {
		return m.items[m.selected].Symbol
	}
	return ""
}

// aiCmdAutoFillContext rewrites bare `/symbol` and `/earning` macros
// (those without an arg) to use the current detail-view symbol when
// available. Returns the input unchanged if no context symbol exists.
func (m Model) aiCmdAutoFillContext(raw string) string {
	sym := m.aiCmdContextSymbol()
	if sym == "" {
		return raw
	}
	return reMacro.ReplaceAllStringFunc(raw, func(tok string) string {
		sub := reMacro.FindStringSubmatch(tok)
		if len(sub) < 3 {
			return tok
		}
		name := strings.ToLower(sub[1])
		if sub[2] != "" {
			return tok
		}
		if name == "symbol" || name == "earning" || name == "news" {
			return "/" + name + ":" + sym
		}
		return tok
	})
}

// === Submit / run ===

// aiRequireArgMacros lists macros that must be followed by `:value`.
var aiRequireArgMacros = map[string]string{
	"symbol":  "symbol ticker (e.g. /symbol:NVDA)",
	"earning": "symbol ticker (e.g. /earning:AAPL)",
	"news":    "symbol ticker (e.g. /news:NVDA)",
	"range":   "timeframe (e.g. /range:1M; one of 1D/1W/1M/6M/1Y/3Y/5Y/10Y)",
}

// aiCmdValidateMacros returns a non-empty error string if any macro in
// the prompt is missing its required argument.
func aiCmdValidateMacros(raw string) string {
	for _, match := range reMacro.FindAllStringSubmatch(raw, -1) {
		name := strings.ToLower(match[1])
		arg := match[2]
		hint, needs := aiRequireArgMacros[name]
		if !needs {
			continue
		}
		if strings.TrimSpace(arg) == "" {
			return fmt.Sprintf("`/%s` needs a value: %s", name, hint)
		}
	}
	return ""
}

func (m Model) submitAICmd() (tea.Model, tea.Cmd) {
	raw := strings.TrimSpace(string(m.aicmd.Input))
	if raw == "" {
		return m, nil
	}
	logger.Log("aicmd submit: %q", raw)
	// /help short-circuit
	if strings.EqualFold(raw, "/help") {
		m.aicmd.Stage = aiCmdResult
		m.aicmd.Result = aiCmdHelpMarkdown()
		m.aicmd.ScrollOff = 0
		return m, nil
	}

	// /ask <question> — explicit agent path. Also the implicit default
	// for prompts that contain no macros (a bare natural-language
	// question like "why did NVDA drop this week?"). Users who embed
	// macros (/symbol, /news, /portfolio, …) keep the deterministic
	// expansion path below.
	askQ, askOK := stripAskPrefix(raw)
	if !askOK && !containsMacro(raw) {
		askQ, askOK = raw, true
	}
	if askOK {
		if askQ == "" {
			m.aicmd.Stage = aiCmdResult
			m.aicmd.Err = "Usage: /ask <question>"
			return m, nil
		}
		// Push to history (dedup consecutive duplicates)
		if len(m.aicmd.History) == 0 || m.aicmd.History[0] != raw {
			m.aicmd.History = append([]string{raw}, m.aicmd.History...)
			if len(m.aicmd.History) > aiCmdHistoryMax {
				m.aicmd.History = m.aicmd.History[:aiCmdHistoryMax]
			}
			persistAICmdHistory(m.aicmd.History)
		}
		m.aicmd.HistoryIdx = -1
		m.aicmd.Stage = aiCmdLoading
		m.aicmd.LastPrompt = raw
		m.aicmd.Result = ""
		m.aicmd.Err = ""
		m.aicmd.ScrollOff = 0
		m.aicmd.SpinnerFrame = 0
		m.aicmd.LoadingSubject = "agent · " + truncateSubject(askQ)
		return m, tea.Batch(m.runAgentCmd(raw, askQ), aiCmdTickCmd())
	}

	// Auto-expand bare /symbol and /earning to the current detail symbol
	// before validating, so users can type just `/symbol` on a detail page.
	if filled := m.aiCmdAutoFillContext(raw); filled != raw {
		logger.Log("aicmd auto-fill: %q -> %q", raw, filled)
		raw = filled
		m.aicmd.Input = []rune(raw)
		m.aicmd.Cursor = len(m.aicmd.Input)
	}

	// Validate macro arguments before we touch the LLM.
	if vErr := aiCmdValidateMacros(raw); vErr != "" {
		logger.Log("aicmd validation: %s", vErr)
		m.aicmd.Stage = aiCmdResult
		m.aicmd.Err = vErr
		m.aicmd.Result = ""
		m.aicmd.ScrollOff = 0
		return m, nil
	}

	// Push to history (dedup consecutive duplicates)
	if len(m.aicmd.History) == 0 || m.aicmd.History[0] != raw {
		m.aicmd.History = append([]string{raw}, m.aicmd.History...)
		if len(m.aicmd.History) > aiCmdHistoryMax {
			m.aicmd.History = m.aicmd.History[:aiCmdHistoryMax]
		}
		persistAICmdHistory(m.aicmd.History)
	}
	m.aicmd.HistoryIdx = -1
	m.aicmd.Stage = aiCmdLoading
	m.aicmd.LastPrompt = raw
	m.aicmd.Result = ""
	m.aicmd.Err = ""
	m.aicmd.ScrollOff = 0
	m.aicmd.SpinnerFrame = 0
	m.aicmd.LoadingSubject = m.aiCmdSubject(raw)
	return m, tea.Batch(m.runAICmd(raw, false), aiCmdTickCmd())
}

// aiCmdSubject returns a short label (e.g. "NVDA", "watchlist",
// "portfolio") describing what the user asked about. It scans the
// prompt for macros and falls back to the current view context.
func (m Model) aiCmdSubject(raw string) string {
	lower := strings.ToLower(raw)
	// Prefer explicit /symbol:XYZ or /earning:XYZ in the prompt.
	for _, match := range reMacro.FindAllStringSubmatch(raw, -1) {
		name := strings.ToLower(match[1])
		val := strings.ToUpper(strings.TrimSpace(match[2]))
		switch name {
		case "symbol", "earning":
			if val != "" {
				return val
			}
		case "portfolio":
			return "portfolio"
		case "watchlist":
			return "watchlist"
		}
	}
	_ = lower
	// Fall back to the current view context.
	switch m.mode {
	case viewDetail:
		if m.selected < len(m.items) {
			return m.items[m.selected].Symbol
		}
	case viewPortfolio:
		return "portfolio"
	case viewWatchlist:
		return "watchlist"
	}
	return "your prompt"
}

// runAICmd expands macros, builds the final LLM prompt, and (unless
// cached) dispatches to the LLM. Missing earnings data is fetched
// inline (synchronously within the goroutine) so /earning:SYM works
// even when the user has never opened that symbol's detail view.
func (m Model) runAICmd(raw string, force bool) tea.Cmd {
	if m.llmClient == nil {
		return func() tea.Msg {
			return aiCmdResultMsg{prompt: raw, err: fmt.Errorf("LLM is not configured (set llm.endpoint and llm.model in config)")}
		}
	}

	expanded, contextBlock := m.expandAICmd(raw)

	// Identify `/earning:SYM` macros whose data is missing locally so
	// we can fetch it synchronously inside the goroutine below.
	missingEarnings := m.aiCmdMissingEarnings(raw)
	if len(missingEarnings) > 0 {
		logger.Log("aicmd: fetching missing financials for %v", missingEarnings)
	}
	missingNews := m.aiCmdMissingNews(raw)
	if len(missingNews) > 0 {
		logger.Log("aicmd: fetching missing news for %v", missingNews)
	}

	userPrompt := expanded
	if contextBlock != "" {
		userPrompt = expanded + "\n\n## Context\n" + contextBlock
	}
	userPrompt += "\n\nRespond in markdown. Use tables when comparing data. Keep it concise and data-driven."

	cacheKey := aiCmdCacheKey(raw)
	if !force && m.cache != nil {
		if cached := m.cache.GetText(cacheKey, "ai-cmd"); cached != "" {
			logger.Log("aicmd: using cached result for %q", raw)
			return func() tea.Msg { return aiCmdResultMsg{prompt: raw, text: cached} }
		}
	}
	if force && m.cache != nil {
		m.cache.DeleteText(cacheKey, "ai-cmd")
	}

	client := m.llmClient
	cacheRef := m.cache
	yahooClient := m.client
	newsProvider := m.news
	systemPrompt := aiCmdSystemPrompt + todayBlock() + m.cfg.Investor.SystemPromptBlock()
	companyNames := make(map[string]string, len(m.items))
	for i := range m.items {
		companyNames[strings.ToUpper(m.items[i].Symbol)] = m.items[i].Name
	}
	return func() tea.Msg {
		finalPrompt := userPrompt
		extraCtx := ""
		if len(missingEarnings) > 0 && yahooClient != nil {
			var extra strings.Builder
			for _, sym := range missingEarnings {
				fin, err := yahooClient.GetFinancials(sym)
				if err != nil || fin == nil {
					logger.Log("aicmd: fetch financials %s failed: %v", sym, err)
					extra.WriteString(fmt.Sprintf("\n\n### Earnings: %s\n_Fetch failed: %v_\n", sym, err))
					continue
				}
				if cacheRef != nil {
					cacheRef.PutFinancials(sym, fin)
				}
				logger.Log("aicmd: fetched financials for %s", sym)
				extra.WriteString("\n\n" + formatEarningsContext(sym, fin))
			}
			extraCtx += extra.String()
		}
		if len(missingNews) > 0 && newsProvider != nil {
			var extra strings.Builder
			since := time.Now().AddDate(0, 0, -14)
			for _, sym := range missingNews {
				items, err := newsProvider.GetCompanyNews(sym, since, 10)
				if err != nil {
					logger.Log("aicmd: fetch news %s failed: %v", sym, err)
					extra.WriteString(fmt.Sprintf("\n\n### News: %s\n_Fetch failed: %v_\n", sym, err))
					continue
				}
				if cacheRef != nil {
					cacheRef.PutNews(sym, items)
				}
				logger.Log("aicmd: fetched %d news items for %s", len(items), sym)
				extra.WriteString("\n\n" + formatNewsContext(sym, companyNames[sym], items))
			}
			extraCtx += extra.String()
		}
		if extraCtx != "" {
			finalPrompt = expanded + "\n\n## Context\n" + contextBlock + extraCtx +
				"\n\nRespond in markdown. Use tables when comparing data. Keep it concise and data-driven."
		}
		ctx := context.Background()
		logger.Log("aicmd: dispatching to LLM (prompt %d chars, context %d chars)", len(finalPrompt), len(contextBlock))
		text, err := client.Chat(ctx, systemPrompt, finalPrompt)
		if err == nil && text != "" && cacheRef != nil {
			cacheRef.PutText(cacheKey, "ai-cmd", text)
		}
		if err != nil {
			logger.Log("aicmd: LLM error: %v", err)
		} else {
			logger.Log("aicmd: LLM returned %d chars", len(text))
		}
		return aiCmdResultMsg{prompt: raw, text: text, err: err}
	}
}

// === Agent (/ask) path ===

// stripAskPrefix returns the question part of an `/ask …` prompt.
// The prefix may be at the start of the string only. Returns ok=false
// if the prompt does not start with `/ask`.
func stripAskPrefix(raw string) (string, bool) {
	trim := strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(trim), "/ask") {
		return "", false
	}
	rest := trim[len("/ask"):]
	if rest == "" {
		return "", true
	}
	if rest[0] != ' ' && rest[0] != ':' && rest[0] != '\t' {
		return "", false
	}
	return strings.TrimSpace(strings.TrimLeft(rest, " :\t")), true
}

func truncateSubject(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 40 {
		return s
	}
	return s[:37] + "…"
}

// containsMacro reports whether raw embeds any known macro
// (`/symbol`, `/range`, `/earning`, `/news`, `/portfolio`,
// `/watchlist`, `/summarise`, `/analyze`, `/compare`). Used to decide
// whether to route a bare prompt through the agent loop or the
// deterministic expansion path.
func containsMacro(raw string) bool {
	for _, match := range reMacro.FindAllStringSubmatch(raw, -1) {
		name := strings.ToLower(match[1])
		switch name {
		case "symbol", "range", "earning", "news",
			"portfolio", "watchlist", "summarise", "analyze", "compare":
			return true
		}
	}
	return false
}

// todayBlock returns a short system-prompt suffix that anchors the model
// to the current date so phrases like "Q1 2026" or "last week" resolve
// correctly. Using time.Now() makes this implicitly up-to-date.
func todayBlock() string {
	return "\n\nCurrent date: " + time.Now().UTC().Format("2006-01-02") + "."
}

// agentSystemPrompt tells the model how to use the tool catalogue.
// Intentionally short: long tool-use instructions hurt more than help.
const agentSystemPrompt = `You are Finsight, a terminal-based financial analyst.

You have tools you can call to fetch live market data:
- get_quote(symbols): current price + intraday change for up to 5 tickers.
- get_news(symbol, days?, limit?): filtered recent headlines.
- get_earnings(symbol, quarters?): fundamentals, growth, margins, analyst consensus.
  The eps_history and quarterly arrays are NEWEST-FIRST. The guidance
  array is FORWARD-LOOKING and covers current/next quarter and
  current/next year (EPS avg/low/high, revenue avg/low/high, YoY growth,
  estimate dispersion). next_earnings_date is the upcoming report date.
  latest_eps_verdict is "beat", "meet", or "miss" vs analyst consensus.
  Use latest_reported_quarter and data_as_of to anchor recency.
- get_guidance(symbol): SEC 8-K (US) or 6-K (foreign) press release with
  the COMPANY'S OWN forward guidance. Call this when the user asks about
  management's outlook, guidance ranges, or whether actuals beat the
  company's own targets. NOT the same as analyst consensus from get_earnings.
- get_technicals(symbol, range?, interval?): algorithmic-trading indicator
  stack — SMA/EMA (9/26/59/120, 20/50/200) with 9-vs-26 and 50-vs-200 cross
  state, RSI(14) with overbought/oversold, Bollinger (20,2) bands + %B,
  MACD (12/26/9) with signal-cross state, Stochastic (14,1,3) %K/%D, and
  classic pivot-point levels (P, R1-R3, S1-S3). Returns latest values plus
  a "signals" array of plain-English bullets. Use for questions about
  momentum, overbought/oversold, entry/exit levels, cross signals, or
  chart patterns. Defaults: range=6mo, interval=1d.

Guidelines:
- Always call get_earnings when the user asks about a specific quarter
  (e.g. "Q1 2026", "last earnings"), beats/misses, or fundamentals.
  Do not claim data is unavailable without calling the tool first.
- Prefer get_quote first when the user asks about price action.
- Call get_technicals for any question about indicators, signals, RSI,
  MACD, moving averages, Bollinger, pivots, or technical setup.
- Call multiple independent tools in the SAME turn when you can (parallel).
- Stop calling tools once you can answer. Do not loop.
- Answer in markdown. Use tables when comparing. Be concise and data-driven.
- If a tool returns an error, acknowledge it and proceed with what you have.`

// runAgentCmd runs the agentic retrieval loop. Unlike runAICmd it does
// not expand macros; the model decides what data to pull via tool
// calls. Results are cached by prompt hash so repeated /ask queries
// skip the roundtrip(s).
func buildAgentSystemPrompt(useMultiAgent bool, knowledgePrompt, investorBlock string) string {
	systemPrompt := agentSystemPrompt + todayBlock() + investorBlock
	if !useMultiAgent {
		// Single-agent mode uses the seven-role synthesis instruction.
		systemPrompt += llm.SevenRoleInstruction() + knowledgePrompt
	} else {
		// Multi-agent mode has per-role prompts; only append knowledge memory instruction.
		systemPrompt += knowledgePrompt
	}
	return systemPrompt
}

func (m Model) runAgentCmd(raw, question string) tea.Cmd {
	if m.llmClient == nil {
		return func() tea.Msg {
			return aiCmdResultMsg{prompt: raw, err: fmt.Errorf("LLM is not configured (set llm.endpoint and llm.model in config)")}
		}
	}
	cacheKey := aiCmdCacheKey(raw)
	if m.cache != nil {
		if cached := m.cache.GetText(cacheKey, "ai-cmd"); cached != "" {
			logger.Log("aicmd agent: using cached result for %q", raw)
			return func() tea.Msg { return aiCmdResultMsg{prompt: raw, text: cached} }
		}
	}
	client := m.llmClient
	cacheRef := m.cache
	var knowledgePrompt string
	deps := agent.Deps{Yahoo: m.client, News: m.news, Edgar: m.edgarClient}
	if sqliteCache, ok := m.cache.(*cache.SQLiteCache); ok {
		deps.KnowledgeDB = sqliteCache.KnowledgeDB()
		if deps.KnowledgeDB != nil {
			knowledgePrompt = llm.KnowledgeMemoryInstruction()
		}
	}
	tools := agent.DefaultTools(deps)
	useMultiAgent := m.cfg.LLM.UseMultiAgent
	systemPrompt := buildAgentSystemPrompt(useMultiAgent, knowledgePrompt, m.cfg.Investor.SystemPromptBlock())
	
	return func() tea.Msg {
		// Add timeout to prevent indefinite LLM calls (5 minute max)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		var text string
		var err error
		
		if useMultiAgent {
			// Multi-agent orchestration path
			logger.Log("aicmd agent: running multi-agent orchestration with %d tools for %q", len(tools), question)
			analyses, err := agent.OrchestrateMultiAgentAnalysis(ctx, client, question, tools, agent.MultiAgentConfig{
				MaxStepsPerRole: 6,
				PerToolCallCap:  4,
				ParallelRoles:   true,
				OnRoleComplete: func(analysis agent.RoleAnalysis) {
					if analysis.Error != nil {
						logger.Log("aicmd agent: role %s failed: %v", analysis.Role, analysis.Error)
					} else {
						logger.Log("aicmd agent: role %s complete (score: %.2f, confidence: %.2f)", analysis.Role, analysis.Score, analysis.Confidence)
					}
				},
			})
			if err == nil && len(analyses) > 0 {
				text = formatMultiAgentResults(analyses, question)
			}
		} else {
			// Single-agent path (original behavior)
			logger.Log("aicmd agent: running single-agent with %d tools for %q", len(tools), question)
			var trace agent.Trace
			text, trace, err = agent.Run(ctx, client, systemPrompt, question, tools, agent.Options{
				MaxSteps: 6,
				OnStep: func(s agent.Step) {
					if s.Err != nil {
						logger.Log("aicmd agent: tool %s failed: %v", s.ToolName, s.Err)
					} else {
						logger.Log("aicmd agent: tool %s ok (%d chars)", s.ToolName, len(s.Result))
					}
				},
			})
			if err != nil {
				// Fall back to plain Chat if the provider refused tools.
				if err == llm.ErrToolsUnsupported || strings.Contains(err.Error(), "tool calling not supported") {
					logger.Log("aicmd agent: provider lacks tool support, falling back to plain Chat")
					text, err = client.Chat(ctx, systemPrompt, question)
				}
			}
			if err == nil && text != "" {
				text = appendAgentTrace(text, trace)
			}
		}
		
		if err == nil && text != "" {
			if vErr := validateFactorVoteJSON(text); vErr != nil {
				logger.Log("aicmd agent: factor vote invalid (%v); attempting repair", vErr)
				repaired, rErr := repairFactorVoteSection(ctx, client, systemPrompt, question, text)
				if rErr != nil {
					logger.Log("aicmd agent: factor vote repair failed: %v", rErr)
				} else {
					text = repaired
				}
			}
			if cacheRef != nil {
				cacheRef.PutText(cacheKey, "ai-cmd", text)
			}
		}
		if err != nil {
			logger.Log("aicmd agent: error: %v", err)
		} else {
			logger.Log("aicmd agent: returned %d chars", len(text))
		}
		return aiCmdResultMsg{prompt: raw, text: text, err: err}
	}
}

// appendAgentTrace tacks a small "tools used" footer onto the final
// answer so users can see what the agent pulled.
func appendAgentTrace(answer string, trace agent.Trace) string {
	if len(trace.Steps) == 0 {
		return answer
	}
	var sb strings.Builder
	sb.WriteString(answer)
	sb.WriteString("\n\n---\n")
	sb.WriteString("_Tools used: ")
	for i, s := range trace.Steps {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("`")
		sb.WriteString(s.ToolName)
		if sym, ok := s.Args["symbol"].(string); ok && sym != "" {
			sb.WriteString(":")
			sb.WriteString(strings.ToUpper(sym))
		} else if syms, ok := s.Args["symbols"].([]any); ok && len(syms) > 0 {
			sb.WriteString(":")
			for j, v := range syms {
				if j > 0 {
					sb.WriteString(",")
				}
				if str, ok := v.(string); ok {
					sb.WriteString(strings.ToUpper(str))
				}
			}
		}
		if s.Err != nil {
			sb.WriteString(" ⚠")
		}
		sb.WriteString("`")
	}
	sb.WriteString("_")
	return sb.String()
}

// formatMultiAgentResults converts multi-agent RoleAnalysis results into
// a markdown report with a Factor Vote JSON section compatible with
// validateFactorVoteJSON. Each role's analysis is rendered as a section,
// followed by a weighted summary.
func formatMultiAgentResults(analyses []agent.RoleAnalysis, question string) string {
	var sb strings.Builder
	sb.WriteString("## Multi-Agent Analysis\n\n")
	sb.WriteString("**Question:** ")
	sb.WriteString(question)
	sb.WriteString("\n\n")

	// Build a map for easy lookup and organize by role
	roleMap := make(map[agent.RoleType]*agent.RoleAnalysis)
	var roleOrder []agent.RoleType
	for i := range analyses {
		roleMap[analyses[i].Role] = &analyses[i]
		// Track order of non-Portfolio roles (Portfolio is the synthesis)
		if analyses[i].Role != agent.RolePortfolio {
			roleOrder = append(roleOrder, analyses[i].Role)
		}
	}

	// Render individual role analyses
	for _, role := range roleOrder {
		analysis := roleMap[role]
		if analysis == nil {
			continue
		}
		sb.WriteString("### ")
		sb.WriteString(string(role))
		sb.WriteString("\n\n")
		sb.WriteString(analysis.Analysis)
		sb.WriteString("\n\n")
		sb.WriteString("**Score:** ")
		sb.WriteString(fmt.Sprintf("%.2f", analysis.Score))
		sb.WriteString(" | **Confidence:** ")
		sb.WriteString(fmt.Sprintf("%.2f", analysis.Confidence))
		sb.WriteString(" | **Verdict:** ")
		sb.WriteString(analysis.Verdict)
		sb.WriteString("\n\n")
	}

	// Portfolio/Synthesis section
	if portfolio := roleMap[agent.RolePortfolio]; portfolio != nil {
		sb.WriteString("### Portfolio Manager (Synthesis)\n\n")
		sb.WriteString(portfolio.Analysis)
		sb.WriteString("\n\n")
		sb.WriteString("**Weighted Score:** ")
		sb.WriteString(fmt.Sprintf("%.2f", portfolio.Score))
		sb.WriteString(" | **Verdict:** ")
		sb.WriteString(portfolio.Verdict)
		sb.WriteString("\n\n")
	}

	// Build Factor Vote JSON section (required for validation)
	sb.WriteString("## Factor Vote\n\n")
	sb.WriteString("```json\n")
	factorVote := map[string]any{
		"market":           extractRoleScore(roleMap, agent.RoleMarket),
		"fundamental":      extractRoleScore(roleMap, agent.RoleFundamental),
		"technical":        extractRoleScore(roleMap, agent.RoleTechnical),
		"risk":             extractRoleScore(roleMap, agent.RoleRisk),
		"sentiment_news":   extractRoleScore(roleMap, agent.RoleSentiment),
		"strategy":         extractRoleScore(roleMap, agent.RoleStrategy),
		"weighted_total":   extractRoleScore(roleMap, agent.RolePortfolio),
		"verdict":          extractRoleVerdict(roleMap, agent.RolePortfolio),
	}
	b, err := json.Marshal(factorVote)
	if err != nil {
		logger.Log("error: serialize factor vote: %v; using empty JSON", err)
		sb.WriteString("{}")
	} else {
		sb.WriteString(string(b))
	}
	sb.WriteString("\n```\n")

	return sb.String()
}

// extractRoleScore returns the score for a specific role, or 0.0 if not found.
func extractRoleScore(roleMap map[agent.RoleType]*agent.RoleAnalysis, role agent.RoleType) float64 {
	if analysis := roleMap[role]; analysis != nil {
		return analysis.Score
	}
	return 0.0
}

// extractRoleVerdict returns the verdict for a specific role, or "Neutral" if not found.
func extractRoleVerdict(roleMap map[agent.RoleType]*agent.RoleAnalysis, role agent.RoleType) string {
	if analysis := roleMap[role]; analysis != nil && analysis.Verdict != "" {
		return analysis.Verdict
	}
	return "Neutral"
}

func validateFactorVoteJSON(report string) error {
	section, found := extractSectionByHeading(report, reFactorVoteHead)
	if !found {
		return fmt.Errorf("missing Factor Vote section")
	}
	jsonBlock, ok := extractJSONBlock(section)
	if !ok {
		return fmt.Errorf("missing JSON fenced block in Factor Vote section")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(jsonBlock), &payload); err != nil {
		return fmt.Errorf("invalid Factor Vote JSON: %w", err)
	}
	for _, key := range []string{"market", "fundamental", "technical", "risk", "sentiment_news", "strategy", "weighted_total", "verdict"} {
		if _, ok := payload[key]; !ok {
			return fmt.Errorf("Factor Vote JSON missing key %q", key)
		}
	}
	return nil
}

func extractSectionByHeading(text string, heading *regexp.Regexp) (string, bool) {
	headLoc := heading.FindStringIndex(text)
	if headLoc == nil {
		return "", false
	}
	start := headLoc[0]
	rest := text[headLoc[1]:]
	next := reHeadingLevel2.FindStringIndex(rest)
	if next == nil {
		return strings.TrimSpace(text[start:]), true
	}
	end := headLoc[1] + next[0]
	return strings.TrimSpace(text[start:end]), true
}

func extractJSONBlock(text string) (string, bool) {
	m := reJSONFence.FindStringSubmatch(text)
	if len(m) < 2 {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

func repairFactorVoteSection(ctx context.Context, client *llm.Client, systemPrompt, question, report string) (string, error) {
	repairPrompt := fmt.Sprintf(`You are repairing a markdown analysis report.

Task:
- Return ONLY a corrected "## Factor Vote" section.
- Include one fenced JSON block (with json tag) containing these keys:
  market, fundamental, technical, risk, sentiment_news, weighted_total, verdict.
- Keep scores in [-2, 2] and confidence in [0, 1].
- Use only information already present in the report.

Original question:
%s

Report:
%s`, question, report)

	repair, err := client.Chat(ctx, systemPrompt, repairPrompt)
	if err != nil {
		return "", err
	}
	section, found := extractSectionByHeading(repair, reFactorVoteHead)
	if !found {
		if strings.Contains(strings.ToLower(repair), "factor vote") {
			section = strings.TrimSpace(repair)
		} else {
			section = "## Factor Vote\n\n" + strings.TrimSpace(repair)
		}
	}
	if err := validateFactorVoteJSON(section); err != nil {
		return "", fmt.Errorf("repair output is still invalid: %w", err)
	}
	return replaceOrAppendFactorVoteSection(report, section), nil
}

func replaceOrAppendFactorVoteSection(report, factorSection string) string {
	factorSection = strings.TrimSpace(factorSection)
	if factorSection == "" {
		return report
	}
	start, end, found := factorVoteSectionBounds(report)
	if !found {
		return strings.TrimRight(report, "\n") + "\n\n" + factorSection
	}
	return strings.TrimRight(report[:start], "\n") + "\n\n" + factorSection + "\n\n" + strings.TrimLeft(report[end:], "\n")
}

func factorVoteSectionBounds(report string) (int, int, bool) {
	headLoc := reFactorVoteHead.FindStringIndex(report)
	if headLoc == nil {
		return 0, 0, false
	}
	start := headLoc[0]
	rest := report[headLoc[1]:]
	next := reHeadingLevel2.FindStringIndex(rest)
	if next == nil {
		return start, len(report), true
	}
	end := headLoc[1] + next[0]
	return start, end, true
}

// aiCmdMissingEarnings returns the symbols referenced by `/earning:SYM`
// macros that do not have local `Financials` data loaded. Used to
// decide whether a background fetch is needed before prompting the LLM.
func (m Model) aiCmdMissingEarnings(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, match := range reMacro.FindAllStringSubmatch(raw, -1) {
		if strings.ToLower(match[1]) != "earning" {
			continue
		}
		arg := strings.ToUpper(strings.TrimSpace(match[2]))
		if arg == "" || seen[arg] {
			continue
		}
		seen[arg] = true
		hasLocal := false
		for _, it := range m.items {
			if strings.EqualFold(it.Symbol, arg) && it.Financials != nil {
				hasLocal = true
				break
			}
		}
		if !hasLocal {
			out = append(out, arg)
		}
	}
	return out
}

// aiCmdMissingNews returns the symbols referenced by `/news:SYM` macros
// that have no fresh local or cached news. Used so the LLM flow can
// fetch headlines on demand without requiring a prior visit to the
// detail view.
func (m Model) aiCmdMissingNews(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, match := range reMacro.FindAllStringSubmatch(raw, -1) {
		if strings.ToLower(match[1]) != "news" {
			continue
		}
		arg := strings.ToUpper(strings.TrimSpace(match[2]))
		if arg == "" || seen[arg] {
			continue
		}
		seen[arg] = true
		hasLocal := false
		for _, it := range m.items {
			if strings.EqualFold(it.Symbol, arg) && len(it.News) > 0 {
				hasLocal = true
				break
			}
		}
		if !hasLocal && m.cache != nil {
			if cached := m.cache.GetNews(arg); len(cached) > 0 {
				hasLocal = true
			}
		}
		if !hasLocal {
			out = append(out, arg)
		}
	}
	return out
}

// formatEarningsContext formats a freshly-fetched FinancialData block
// the same way aiCtxEarnings does for locally-loaded data.
func formatEarningsContext(sym string, f *yahoo.FinancialData) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Earnings: %s\n", sym))
	if f == nil {
		sb.WriteString("_No data available._\n")
		return sb.String()
	}
	if f.ProfitMargin != 0 {
		sb.WriteString(fmt.Sprintf("- Margins: profit %.2f%% · operating %.2f%% · gross %.2f%%\n", f.ProfitMargin*100, f.OperatingMargins*100, f.GrossMargins*100))
	}
	if f.RevenueGrowth != 0 || f.EarningsGrowth != 0 {
		sb.WriteString(fmt.Sprintf("- Growth: revenue %+.2f%% · earnings %+.2f%%\n", f.RevenueGrowth*100, f.EarningsGrowth*100))
	}
	if f.DebtToEquity > 0 || f.CurrentRatio > 0 {
		sb.WriteString(fmt.Sprintf("- Balance: debt/equity %.2f · current ratio %.2f · FCF %s\n", f.DebtToEquity, f.CurrentRatio, formatSignedMoney(f.FreeCashflow)))
	}
	if f.TargetMeanPrice > 0 {
		sb.WriteString(fmt.Sprintf("- Analysts: %d covering · target %.2f (range %.2f – %.2f) · rating %s\n",
			f.NumberOfAnalysts, f.TargetMeanPrice, f.TargetLowPrice, f.TargetHighPrice, f.RecommendationKey))
	}
	return sb.String()
}

// formatNewsContext renders a list of news headlines as a markdown
// context block. Used by both the locally-loaded aiCtxNews path and the
// auto-fetch path inside runAICmd. Items are passed through news.Filter
// first to drop stale/off-topic/blocklisted entries and cap the size
// fed to the LLM.
func formatNewsContext(sym, companyName string, items []news.Item) string {
	items = news.Filter(items, news.FilterOpts{Symbol: sym, CompanyName: companyName})
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### News: %s\n", sym))
	if len(items) == 0 {
		sb.WriteString("_No recent headlines found._\n")
		return sb.String()
	}
	now := time.Now()
	for _, it := range items {
		age := ""
		if !it.PublishedAt.IsZero() {
			d := now.Sub(it.PublishedAt)
			switch {
			case d < time.Hour:
				age = fmt.Sprintf("%dm ago", int(d.Minutes()))
			case d < 24*time.Hour:
				age = fmt.Sprintf("%dh ago", int(d.Hours()))
			default:
				age = fmt.Sprintf("%dd ago", int(d.Hours()/24))
			}
		}
		pub := it.Publisher
		if pub == "" {
			pub = "unknown"
		}
		sb.WriteString(fmt.Sprintf("- **%s** · %s · %s\n", it.Title, pub, age))
	}
	return sb.String()
}

func (m Model) handleAICmdResult(msg aiCmdResultMsg) (tea.Model, tea.Cmd) {
	m.aicmd.Stage = aiCmdResult
	if msg.err != nil {
		m.aicmd.Err = msg.err.Error()
		m.aicmd.Result = ""
	} else {
		m.aicmd.Result = msg.text
		m.aicmd.Err = ""
	}
	// Clear the input so the user can type a follow-up prompt without
	// first wiping the previous one. History still has the original.
	m.aicmd.Input = nil
	m.aicmd.Cursor = 0
	m.aicmd.HistoryIdx = -1
	m.aicmd.ShowSuggestion = false
	return m, nil
}

// handleAICmdTick advances the spinner animation while loading.
func (m Model) handleAICmdTick() (tea.Model, tea.Cmd) {
	if !m.aicmd.Active || m.aicmd.Stage != aiCmdLoading {
		return m, nil
	}
	m.aicmd.SpinnerFrame = (m.aicmd.SpinnerFrame + 1) % len(aiSpinnerFrames)
	return m, aiCmdTickCmd()
}

// handleAICmdBlink toggles the input cursor. Keeps rescheduling while
// the window is active so blinking resumes automatically after a
// submit → result → edit cycle without a re-kick at every Stage
// transition. Stops once the window closes.
func (m Model) handleAICmdBlink() (tea.Model, tea.Cmd) {
	if !m.aicmd.Active {
		m.aicmd.CursorOn = true
		return m, nil
	}
	if m.aicmd.Stage == aiCmdInput {
		m.aicmd.CursorOn = !m.aicmd.CursorOn
	} else {
		m.aicmd.CursorOn = true
	}
	return m, aiCmdBlinkCmd()
}

// aiCmdHistoryMax is the upper bound on entries kept in memory + on disk.
const aiCmdHistoryMax = 200

// aiCmdHistoryPath returns the filesystem path where prompt history is
// persisted. Honours $XDG_STATE_HOME when set (Linux convention) and
// falls back to ~/.config/finsight/history. Returns "" when the home
// directory cannot be resolved — callers treat that as "persistence off".
func aiCmdHistoryPath() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "finsight", "history")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "finsight", "history")
}

// loadAICmdHistory reads the persisted prompt history from disk. Entries
// are stored newest-first, one per line. Silently returns nil on any
// error so a missing/corrupt file never blocks startup.
func loadAICmdHistory() []string {
	path := aiCmdHistoryPath()
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	scanner := bufio.NewScanner(f)
	// Allow long prompts (default scanner cap is 64KB; 1MB is plenty).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\n")
		if line == "" {
			continue
		}
		out = append(out, line)
		if len(out) >= aiCmdHistoryMax {
			break
		}
	}
	return out
}

// persistAICmdHistory writes the given history (newest-first) to disk
// atomically. Errors are logged but never propagated — history is a
// convenience, never critical.
func persistAICmdHistory(history []string) {
	path := aiCmdHistoryPath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		logger.Log("aicmd history: mkdir %s: %v", filepath.Dir(path), err)
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".history-*")
	if err != nil {
		logger.Log("aicmd history: createtemp: %v", err)
		return
	}
	limit := len(history)
	if limit > aiCmdHistoryMax {
		limit = aiCmdHistoryMax
	}
	w := bufio.NewWriter(tmp)
	for i := 0; i < limit; i++ {
		// Skip any entries containing newlines defensively; our input
		// layer strips them but be safe.
		line := strings.ReplaceAll(history[i], "\n", " ")
		_, _ = w.WriteString(line)
		_ = w.WriteByte('\n')
	}
	_ = w.Flush()
	_ = tmp.Close()
	if err := os.Rename(tmp.Name(), path); err != nil {
		logger.Log("aicmd history: rename: %v", err)
		_ = os.Remove(tmp.Name())
	}
}

const aiCmdSystemPrompt = `You are Finsight, a terminal-based financial analysis assistant.
Guidelines:
1. Ground every statement in the provided Context data. Cite concrete numbers.
2. Be concrete and concise. Use markdown with headings, bold, bullet lists, and tables.
3. When recommending actions, state specific price levels and risk factors.
4. Acknowledge uncertainty when the data is insufficient.
5. This is informational analysis, not financial advice.`

func aiCmdCacheKey(raw string) string {
	h := sha1.Sum([]byte(strings.ToLower(strings.TrimSpace(raw))))
	return hex.EncodeToString(h[:])
}

// === Macro expansion ===

// expandAICmd scans `raw` for macros and returns:
//   - the visible prompt (with macros preserved as human-readable tokens
//     so the model still sees the user's intent)
//   - a consolidated context block (markdown) covering every macro that
//     maps to actual data.
func (m Model) expandAICmd(raw string) (visible, ctx string) {
	var ctxParts []string
	seen := map[string]bool{} // dedup by key

	// Default range for /symbol without explicit /range
	currentRange := m.currentTimeframe().Label
	lastRange := currentRange

	// Pre-scan for the first /range:X so it applies to all preceding
	// /symbol macros too (common phrasing: "/symbol:NVDA in /range:1M").
	if pre := reMacro.FindAllStringSubmatch(raw, -1); len(pre) > 0 {
		for _, mm := range pre {
			if strings.ToLower(mm[1]) == "range" && len(mm) > 2 && mm[2] != "" {
				lastRange = strings.ToUpper(mm[2])
				break
			}
		}
	}

	// Walk all macros
	matches := reMacro.FindAllStringSubmatchIndex(raw, -1)
	for _, match := range matches {
		full := raw[match[0]:match[1]]
		name := strings.ToLower(raw[match[2]:match[3]])
		var arg string
		if match[4] >= 0 {
			arg = raw[match[4]:match[5]]
		}
		switch name {
		case "range":
			if arg != "" {
				lastRange = strings.ToUpper(arg)
			}
			_ = full
		case "symbol":
			if arg == "" {
				continue
			}
			sym := strings.ToUpper(arg)
			k := "symbol:" + sym + ":" + lastRange
			if seen[k] {
				continue
			}
			seen[k] = true
			ctxParts = append(ctxParts, m.aiCtxSymbol(sym, lastRange))
		case "earning":
			if arg == "" {
				continue
			}
			sym := strings.ToUpper(arg)
			k := "earning:" + sym
			if seen[k] {
				continue
			}
			seen[k] = true
			ctxParts = append(ctxParts, m.aiCtxEarnings(sym))
		case "news":
			if arg == "" {
				continue
			}
			sym := strings.ToUpper(arg)
			k := "news:" + sym
			if seen[k] {
				continue
			}
			seen[k] = true
			ctxParts = append(ctxParts, m.aiCtxNews(sym))
		case "portfolio":
			if seen["portfolio"] {
				continue
			}
			seen["portfolio"] = true
			ctxParts = append(ctxParts, m.aiCtxPortfolio())
		case "watchlist":
			if seen["watchlist"] {
				continue
			}
			seen["watchlist"] = true
			ctxParts = append(ctxParts, m.aiCtxWatchlist())
		}
	}

	// Implicit context: detail view always includes the focused symbol
	if m.mode == viewDetail && m.selected < len(m.items) {
		sym := m.items[m.selected].Symbol
		if !seen["symbol:"+sym+":"+lastRange] && !seen["symbol:"+sym+":"+currentRange] {
			ctxParts = append(ctxParts, m.aiCtxSymbol(sym, currentRange))
		}
	}

	visible = raw
	ctx = strings.Join(ctxParts, "\n\n")
	return
}

func (m Model) aiCtxSymbol(sym, rangeLabel string) string {
	// Find quote in m.items or portfolioItems
	var it *WatchlistItem
	for i := range m.items {
		if strings.EqualFold(m.items[i].Symbol, sym) {
			it = &m.items[i]
			break
		}
	}
	if it == nil {
		for i := range m.portfolioItems {
			if strings.EqualFold(m.portfolioItems[i].Symbol, sym) {
				it = &m.portfolioItems[i].WatchlistItem
				break
			}
		}
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Symbol: %s", sym))
	if rangeLabel != "" {
		sb.WriteString(fmt.Sprintf(" (range %s)", rangeLabel))
	}
	sb.WriteString("\n")
	if it == nil || it.Quote == nil {
		sb.WriteString("_No quote data loaded in the current session._\n")
		return sb.String()
	}
	q := it.Quote
	if it.Name != "" {
		sb.WriteString(fmt.Sprintf("- Name: %s\n", it.Name))
	}
	sb.WriteString(fmt.Sprintf("- Price: %.2f %s (Δ %+.2f / %+.2f%% today)\n", q.Price, q.Currency, q.Change, q.ChangePercent))
	if q.Open > 0 || q.DayLow > 0 || q.DayHigh > 0 {
		sb.WriteString(fmt.Sprintf("- Day: open %.2f · low %.2f · high %.2f · prev close %.2f\n", q.Open, q.DayLow, q.DayHigh, q.PreviousClose))
	}
	if q.FiftyTwoWeekHigh > 0 || q.FiftyTwoWeekLow > 0 {
		sb.WriteString(fmt.Sprintf("- 52W range: %.2f – %.2f\n", q.FiftyTwoWeekLow, q.FiftyTwoWeekHigh))
	}
	if q.Volume > 0 {
		sb.WriteString(fmt.Sprintf("- Volume: %d (avg %d)\n", q.Volume, q.AvgVolume))
	}
	if q.MarketCap > 0 {
		sb.WriteString(fmt.Sprintf("- Market cap: %s\n", formatMarketCap(q.MarketCap)))
	}
	if q.PE > 0 || q.ForwardPE > 0 {
		sb.WriteString(fmt.Sprintf("- Valuation: PE %.2f · Fwd PE %.2f · PEG %.2f · EPS %.2f · Beta %.2f\n",
			q.PE, q.ForwardPE, q.PEG, q.EPS, q.Beta))
	}
	if q.DividendYield > 0 {
		sb.WriteString(fmt.Sprintf("- Dividend yield: %.2f%%\n", q.DividendYield))
	}
	// Chart summary
	if it.ChartData != nil && len(it.ChartData.Closes) > 0 {
		cd := it.ChartData
		first := cd.Closes[0]
		last := cd.Closes[len(cd.Closes)-1]
		min, max := first, first
		for _, c := range cd.Closes {
			if c < min {
				min = c
			}
			if c > max {
				max = c
			}
		}
		pct := 0.0
		if first > 0 {
			pct = ((last - first) / first) * 100
		}
		sb.WriteString(fmt.Sprintf("- Chart (%s): start %.2f · end %.2f · low %.2f · high %.2f · change %+.2f%%\n",
			rangeLabel, first, last, min, max, pct))
	}
	return sb.String()
}

func (m Model) aiCtxEarnings(sym string) string {
	var it *WatchlistItem
	for i := range m.items {
		if strings.EqualFold(m.items[i].Symbol, sym) {
			it = &m.items[i]
			break
		}
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Earnings: %s\n", sym))
	if it == nil || it.Financials == nil {
		sb.WriteString("_Earnings data not loaded. Open the symbol's detail view and press `m` or `e` to fetch._\n")
		return sb.String()
	}
	f := it.Financials
	if f.ProfitMargin != 0 {
		sb.WriteString(fmt.Sprintf("- Margins: profit %.2f%% · operating %.2f%% · gross %.2f%%\n", f.ProfitMargin*100, f.OperatingMargins*100, f.GrossMargins*100))
	}
	if f.RevenueGrowth != 0 || f.EarningsGrowth != 0 {
		sb.WriteString(fmt.Sprintf("- Growth: revenue %+.2f%% · earnings %+.2f%%\n", f.RevenueGrowth*100, f.EarningsGrowth*100))
	}
	if f.DebtToEquity > 0 || f.CurrentRatio > 0 {
		sb.WriteString(fmt.Sprintf("- Balance: debt/equity %.2f · current ratio %.2f · FCF %s\n", f.DebtToEquity, f.CurrentRatio, formatSignedMoney(f.FreeCashflow)))
	}
	if f.TargetMeanPrice > 0 {
		sb.WriteString(fmt.Sprintf("- Analysts: %d covering · target %.2f (range %.2f – %.2f) · rating %s\n",
			f.NumberOfAnalysts, f.TargetMeanPrice, f.TargetLowPrice, f.TargetHighPrice, f.RecommendationKey))
	}
	return sb.String()
}

// aiCtxNews returns a markdown block of recent headlines for `sym`
// using locally attached items first, then the fresh cache. A
// placeholder is returned if neither is available; runAICmd's
// auto-fetch path will populate fresh context before dispatching to
// the LLM.
func (m Model) aiCtxNews(sym string) string {
	var items []news.Item
	for i := range m.items {
		if strings.EqualFold(m.items[i].Symbol, sym) && len(m.items[i].News) > 0 {
			items = m.items[i].News
			break
		}
	}
	if len(items) == 0 && m.cache != nil {
		items = m.cache.GetNews(sym)
	}
	if len(items) == 0 {
		return fmt.Sprintf("### News: %s\n_Headlines not loaded yet._\n", sym)
	}
	return formatNewsContext(sym, m.companyNameFor(sym), items)
}

// companyNameFor returns the display name for a watchlist symbol, or
// empty string if unknown. Used to boost news-relevance filtering.
func (m Model) companyNameFor(sym string) string {
	for i := range m.items {
		if strings.EqualFold(m.items[i].Symbol, sym) {
			return m.items[i].Name
		}
	}
	return ""
}

func (m Model) aiCtxPortfolio() string {
	snaps := m.portfolioSnapshots()
	if len(snaps) == 0 {
		return "### Portfolio\n_No positions configured._\n"
	}
	return "### Portfolio\n" + llm.BuildPortfolioContext(snaps)
}

func (m Model) aiCtxWatchlist() string {
	if len(m.items) == 0 {
		return "### Watchlist\n_Empty._\n"
	}
	var sb strings.Builder
	groupName := "Default"
	if m.activeGroup < len(m.cfg.Watchlists) {
		groupName = m.cfg.Watchlists[m.activeGroup].Name
	}
	sb.WriteString(fmt.Sprintf("### Watchlist: %s\n", groupName))
	sb.WriteString("| Symbol | Name | Price | Change % | Volume |\n")
	sb.WriteString("|---|---|---:|---:|---:|\n")
	for _, it := range m.items {
		price, chg := "—", "—"
		vol := "—"
		if it.Quote != nil {
			price = fmt.Sprintf("%.2f", it.Quote.Price)
			chg = fmt.Sprintf("%+.2f%%", it.Quote.ChangePercent)
			if it.Quote.Volume > 0 {
				vol = fmt.Sprintf("%d", it.Quote.Volume)
			}
		}
		name := it.Name
		if name == "" {
			name = it.Symbol
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n", it.Symbol, name, price, chg, vol))
	}
	return sb.String()
}

// formatSignedMoney is a small helper mirroring llm.formatMoney for int64
// cash values. Kept local to avoid exporting llm helpers.
func formatSignedMoney(v int64) string {
	f := float64(v)
	neg := f < 0
	if neg {
		f = -f
	}
	var out string
	switch {
	case f >= 1e9:
		out = fmt.Sprintf("$%.2fB", f/1e9)
	case f >= 1e6:
		out = fmt.Sprintf("$%.2fM", f/1e6)
	case f >= 1e3:
		out = fmt.Sprintf("$%.2fK", f/1e3)
	default:
		out = fmt.Sprintf("$%.0f", f)
	}
	if neg {
		return "-" + out
	}
	return out
}

// === Rendering ===

func aiCmdHelpMarkdown() string {
	return `## AI Command — Macros

Type a free-form prompt. Embed any of these macros to inject structured context:

| Macro | Effect |
|---|---|
| ` + "`/symbol:SYM`" + ` | Quote snapshot for SYM (price, ranges, valuation) |
| ` + "`/range:1M`" + ` | Timeframe hint; applies to following ` + "`/symbol`" + ` |
| ` + "`/earning:SYM`" + ` | Latest financials / analyst targets |
| ` + "`/news:SYM`" + ` | Recent headlines (auto-fetched, ~2 week window) |
| ` + "`/portfolio`" + ` | Your portfolio table (weights, P/L) |
| ` + "`/watchlist`" + ` | Current watchlist group |
| ` + "`/help`" + ` | Show this cheatsheet (no LLM call) |

### Agent mode (default)

Plain natural-language questions (no macros) automatically route to the agentic loop — the model picks which tools to call (` + "`get_quote`" + `, ` + "`get_news`" + `, ` + "`get_earnings`" + `, ` + "`get_guidance`" + `, ` + "`get_technicals`" + `) based on your question, in parallel when possible. The ` + "`/ask`" + ` prefix is optional and forces agent mode even if macros are present.

### Example

` + "`" + `/summarise /symbol:NVDA in /range:1M, should I buy at 200? Consider my /portfolio for balance and risk.` + "`" + `

### Keys

- **Enter** send · **Esc** close · **Tab** accept suggestion
- **↑/↓** (in input) history · **↑/↓** (in result) scroll
- **Ctrl+L** clear · **Ctrl+R** regenerate · **Ctrl+W** delete word
`
}

func (m Model) renderAICmdPopup() string {
	// Title line (shown just above the input prompt so it sits with
	// the user's typing area rather than floating over the results).
	title := helpKeyStyle.Render("  ◆ Finsight AI Analyst")
	var ctxHint string
	switch m.mode {
	case viewDetail:
		if m.selected < len(m.items) {
			ctxHint = fmt.Sprintf(" — context: %s (%s)", m.items[m.selected].Symbol, m.currentTimeframe().Label)
		}
	case viewPortfolio:
		ctxHint = " — context: portfolio"
	case viewWatchlist:
		ctxHint = " — context: watchlist"
	}
	titleLine := title + nameStyle.Render(ctxHint)

	// Top banner labels the conversation area so the popup reads like a
	// chat transcript. It changes to reflect whatever pane is active.
	var topLabel string
	switch m.aicmd.Stage {
	case aiCmdLoading:
		topLabel = "  ▸ Analysing…"
	case aiCmdResult:
		if m.aicmd.Err != "" {
			topLabel = "  ▸ Error"
		} else {
			topLabel = "  ▸ Analysis"
		}
	default:
		if m.aicmd.Result != "" {
			topLabel = "  ▸ Analysis"
		} else {
			topLabel = "  ▸ Conversation"
		}
	}
	topBanner := helpKeyStyle.Render(topLabel)

	// Input field
	inputW := m.width - 14
	if inputW < 40 {
		inputW = 40
	}
	inputLine := m.renderAICmdInput(inputW)

	// Suggestions (shown below the input while typing)
	var sugBlock string
	if m.aicmd.ShowSuggestion && m.aicmd.Stage != aiCmdLoading {
		sugBlock = m.renderAICmdSuggestions()
	}

	// Result / loading / status body
	maxW := m.width - 12
	if maxW < 40 {
		maxW = 40
	}
	var body string
	switch m.aicmd.Stage {
	case aiCmdInput:
		if m.aicmd.Result != "" {
			// Keep prior result visible above a fresh input line so the
			// user can compose a follow-up prompt.
			body = m.renderScrolledMarkdown(m.aicmd.Result, maxW)
		} else if len(m.aicmd.Input) == 0 {
			body = nameStyle.Render("  Type your prompt. Press / for macro suggestions, Enter to send, /help for docs.")
		} else {
			body = nameStyle.Render("  Press Enter to send.")
		}
	case aiCmdLoading:
		body = m.renderAICmdSpinner()
	case aiCmdResult:
		if m.aicmd.Err != "" {
			body = errorStyle.Render("  Error: " + m.aicmd.Err)
		} else {
			body = m.renderScrolledMarkdown(m.aicmd.Result, maxW)
		}
	}

	// Footer
	footerKeys := []string{
		helpKeyStyle.Render("Enter") + " send",
		helpKeyStyle.Render("Esc") + " close",
		helpKeyStyle.Render("Tab") + " accept",
		helpKeyStyle.Render("↑↓") + " history",
		helpKeyStyle.Render("PgUp/PgDn·j/k") + " scroll",
		helpKeyStyle.Render("y") + " copy",
		helpKeyStyle.Render("Ctrl+L") + " clear",
		helpKeyStyle.Render("Ctrl+R") + " regen",
	}
	footer := helpStyle.Render("  " + strings.Join(footerKeys, "  ·  "))

	// Layout: top banner + body on top, then the title sits just above
	// the input prompt (chat-style), suggestion dropdown under the
	// input, footer.
	parts := []string{topBanner, "", body, "", titleLine, inputLine}
	if sugBlock != "" {
		parts = append(parts, sugBlock)
	}
	if m.aicmd.Toast != "" {
		parts = append(parts, "", nameStyle.Render("  "+m.aicmd.Toast))
	}
	parts = append(parts, "", footer)
	return strings.Join(parts, "\n")
}

// renderAICmdSpinner shows "{vendor} {model} is analysing {subject}".
func (m Model) renderAICmdSpinner() string {
	frame := aiSpinnerFrames[m.aicmd.SpinnerFrame%len(aiSpinnerFrames)]
	vendor := "LLM"
	model := ""
	if m.llmClient != nil {
		switch m.llmClient.Provider() {
		case llm.ProviderOpenAI:
			vendor = "OpenAI"
		case llm.ProviderCopilot:
			vendor = "GitHub Copilot"
		case llm.ProviderVertex:
			vendor = "Vertex AI"
		default:
			vendor = string(m.llmClient.Provider())
		}
		model = m.llmClient.Model()
	}
	subject := m.aicmd.LoadingSubject
	if subject == "" {
		subject = "your prompt"
	}
	vendorStr := vendor
	if model != "" {
		vendorStr = vendor + " " + model
	}
	spin := helpKeyStyle.Render(frame)
	vendorRender := helpKeyStyle.Render(vendorStr)
	subjectRender := helpKeyStyle.Render(subject)
	return "  " + spin + " " + nameStyle.Render(vendorRender+" is analysing "+subjectRender+"...")
}

func (m Model) renderAICmdInput(width int) string {
	// Tri-color chevron prompt (red / yellow / green), matching the
	// finsight mark. Replaces the prior single-color `Ask ❯`.
	chevR := lipgloss.NewStyle().Foreground(colorRed).Render("❯")
	chevY := lipgloss.NewStyle().Foreground(colorYellow).Render("❯")
	chevG := lipgloss.NewStyle().Foreground(colorGreen).Render("❯")
	prompt := "  " + helpKeyStyle.Render("Ask") + " " + chevR + chevY + chevG + " "
	text := string(m.aicmd.Input)
	// Blinking cursor glyph. Reserves the same visual width when off
	// so surrounding text doesn't shift.
	cursor := "▊"
	if !m.aicmd.CursorOn {
		cursor = " "
	}
	if m.aicmd.Cursor > len(m.aicmd.Input) {
		m.aicmd.Cursor = len(m.aicmd.Input)
	}
	pre := string(m.aicmd.Input[:m.aicmd.Cursor])
	post := string(m.aicmd.Input[m.aicmd.Cursor:])

	// Ghost-text preview: when a suggestion is selected, show the
	// portion that would still be inserted after the cursor as dim
	// text so the user sees what Tab will fill in. The current token
	// under the cursor is replaced by the suggestion in preview, so
	// anything typed so far is trimmed from the ghost.
	ghost := m.aiCmdGhostText()
	ghostRender := ""
	if ghost != "" && post == "" {
		ghostRender = nameStyle.Faint(true).Render(ghost)
	}

	rendered := aiHighlightMacros(pre) + cursor + ghostRender + aiHighlightMacros(post)

	// Truncate visually from the left if too long so cursor stays visible
	if ansi.StringWidth(rendered) > width {
		// Crude: keep last `width` cells
		rendered = "…" + ansi.Truncate(rendered, width-1, "")
		_ = text
	}
	return prompt + rendered
}

// aiCmdGhostText returns the suffix of the currently-selected
// suggestion that has not yet been typed. Empty when no dropdown is
// visible or the user has typed beyond the suggestion.
func (m Model) aiCmdGhostText() string {
	if !m.aicmd.ShowSuggestion || len(m.aicmd.Suggestions) == 0 {
		return ""
	}
	sel := m.aicmd.SuggestionSel
	if sel < 0 || sel >= len(m.aicmd.Suggestions) {
		return ""
	}
	tok, _ := m.currentAIToken()
	insert := m.aicmd.Suggestions[sel].Insert
	if !strings.HasPrefix(strings.ToLower(insert), strings.ToLower(tok)) {
		return ""
	}
	return insert[len(tok):]
}

// aiHighlightMacros colours /macro tokens in the input display.
func aiHighlightMacros(s string) string {
	return reMacro.ReplaceAllStringFunc(s, func(m string) string {
		return mdCodeStyle.Render(m)
	})
}

func (m Model) renderAICmdSuggestions() string {
	const windowSize = 5
	total := len(m.aicmd.Suggestions)
	maxN := windowSize
	if total < maxN {
		maxN = total
	}
	// Scroll window so the selected item stays visible.
	start := 0
	if m.aicmd.SuggestionSel >= windowSize {
		start = m.aicmd.SuggestionSel - windowSize + 1
	}
	if start+maxN > total {
		start = total - maxN
	}
	if start < 0 {
		start = 0
	}
	var lines []string
	// Compact rendering: no top/bottom decorative borders so the whole
	// list always fits inside the popup; the selected row is marked
	// with `❯` plus its row highlight.
	for i := start; i < start+maxN; i++ {
		s := m.aicmd.Suggestions[i]
		marker := "  "
		if i == m.aicmd.SuggestionSel {
			marker = helpKeyStyle.Render(" ❯")
		}
		line := fmt.Sprintf("%s %s", marker, s.Display)
		if s.Hint != "" && s.Hint != s.Display {
			line += nameStyle.Render("  — " + s.Hint)
		}
		if i == m.aicmd.SuggestionSel {
			line = selectedRowStyle.Render(line)
		} else {
			line = cellStyle.Render(line)
		}
		if total > windowSize && i == start+maxN-1 {
			line += nameStyle.Render(fmt.Sprintf("  (%d/%d)", m.aicmd.SuggestionSel+1, total))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderScrolledMarkdown(md string, width int) string {
	rendered := renderMarkdown(md, width)
	lines := strings.Split(rendered, "\n")
	// Reserve space for popup chrome + title/input/footer; reserve more
	// when the suggestion dropdown is open so the selected row never
	// gets clipped off the bottom of the visible popup area.
	reserved := 16
	if m.aicmd.ShowSuggestion {
		reserved += 6
	}
	maxVisible := m.height - reserved
	if maxVisible < 6 {
		maxVisible = 6
	}
	start := m.aicmd.ScrollOff
	if start > len(lines)-maxVisible {
		start = len(lines) - maxVisible
	}
	if start < 0 {
		start = 0
	}
	end := start + maxVisible
	if end > len(lines) {
		end = len(lines)
	}
	visible := lines[start:end]
	return strings.Join(visible, "\n") + "\n" + lipgloss.NewStyle().Foreground(colorDim).Render(fmt.Sprintf("  [lines %d-%d / %d]", start+1, end, len(lines)))
}
