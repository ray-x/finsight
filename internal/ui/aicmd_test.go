package ui

import (
	"math"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ray-x/finsight/internal/config"
	"github.com/ray-x/finsight/internal/llm"
	"github.com/ray-x/finsight/internal/agent"
	"github.com/ray-x/finsight/internal/portfolio"
	"github.com/ray-x/finsight/internal/yahoo"
)

func newTestAICmdModel() Model {
	cfg := &config.Config{
		RefreshInterval: 900,
		ChartStyle:      "candlestick_dotted",
		Watchlists: []config.WatchlistGroup{
			{Name: "Tech", Symbols: []config.WatchItem{
				{Symbol: "NVDA", Name: "NVIDIA"},
				{Symbol: "AAPL", Name: "Apple"},
			}},
		},
	}
	nvdaQ := &yahoo.Quote{Symbol: "NVDA", Price: 900, PreviousClose: 880, Open: 885, DayHigh: 905, DayLow: 882, ChangePercent: 2.27, Change: 20, Volume: 50_000_000}
	aaplQ := &yahoo.Quote{Symbol: "AAPL", Price: 180, PreviousClose: 175, Open: 176, DayHigh: 181, DayLow: 175, ChangePercent: 2.86, Change: 5, Volume: 60_000_000}
	items := []WatchlistItem{
		{Symbol: "NVDA", Name: "NVIDIA", Quote: nvdaQ},
		{Symbol: "AAPL", Name: "Apple", Quote: aaplQ},
	}
	pf := &portfolio.File{Positions: []portfolio.Position{
		{Symbol: "NVDA", Position: 5, OpenPrice: 700},
	}}
	m := Model{
		cfg:          cfg,
		items:        items,
		mode:         viewWatchlist,
		width:        140,
		height:       40,
		relatedFocus: -1,
		portfolio:    pf,
	}
	m.rebuildPortfolioItems()
	for i := range m.portfolioItems {
		if m.portfolioItems[i].Symbol == "NVDA" {
			m.portfolioItems[i].Quote = nvdaQ
		}
	}
	return m
}

func TestAICmdExpandSymbolMacro(t *testing.T) {
	m := newTestAICmdModel()
	_, ctx := m.expandAICmd("/analyze /symbol:NVDA in /range:1M")
	if !strings.Contains(ctx, "### Symbol: NVDA") {
		t.Fatalf("missing symbol header: %q", ctx)
	}
	if !strings.Contains(ctx, "range 1M") {
		t.Fatalf("range not propagated: %q", ctx)
	}
	if !strings.Contains(ctx, "Price: 900.00") {
		t.Fatalf("price data missing: %q", ctx)
	}
}

func TestAICmdExpandPortfolioMacro(t *testing.T) {
	m := newTestAICmdModel()
	_, ctx := m.expandAICmd("Review my /portfolio")
	if !strings.Contains(ctx, "### Portfolio") {
		t.Fatalf("missing portfolio header: %q", ctx)
	}
	if !strings.Contains(ctx, "NVDA") {
		t.Fatalf("NVDA not in portfolio ctx: %q", ctx)
	}
}

func TestAICmdExpandWatchlistMacro(t *testing.T) {
	m := newTestAICmdModel()
	_, ctx := m.expandAICmd("/summarise /watchlist")
	if !strings.Contains(ctx, "### Watchlist: Tech") {
		t.Fatalf("missing watchlist header: %q", ctx)
	}
	// table should include both symbols
	for _, s := range []string{"NVDA", "AAPL"} {
		if !strings.Contains(ctx, s) {
			t.Fatalf("%s missing from watchlist ctx", s)
		}
	}
}

func TestAICmdDeduplicatesMacros(t *testing.T) {
	m := newTestAICmdModel()
	_, ctx := m.expandAICmd("/symbol:NVDA /symbol:NVDA /portfolio /portfolio")
	if strings.Count(ctx, "### Symbol: NVDA") != 1 {
		t.Fatalf("expected NVDA once, got: %q", ctx)
	}
	if strings.Count(ctx, "### Portfolio") != 1 {
		t.Fatalf("expected /portfolio once")
	}
}

func TestAICmdUnknownSymbolGracefullyHandled(t *testing.T) {
	m := newTestAICmdModel()
	_, ctx := m.expandAICmd("/symbol:ZZZZ")
	if !strings.Contains(ctx, "No quote data loaded") {
		t.Fatalf("expected missing-data note, got: %q", ctx)
	}
}

func TestAICmdSuggestionsForMacroPrefix(t *testing.T) {
	m := newTestAICmdModel()
	m.aicmd = AICmdState{Active: true, Input: []rune("/sy"), Cursor: 3}
	m.recomputeAISuggestions()
	if !m.aicmd.ShowSuggestion {
		t.Fatalf("expected suggestions to appear")
	}
	found := false
	for _, s := range m.aicmd.Suggestions {
		if s.Insert == "/symbol" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("/symbol suggestion missing, got %+v", m.aicmd.Suggestions)
	}
}

func TestAICmdSuggestionsForSymbolArg(t *testing.T) {
	m := newTestAICmdModel()
	m.aicmd = AICmdState{Active: true, Input: []rune("/symbol:N"), Cursor: len("/symbol:N")}
	m.recomputeAISuggestions()
	if !m.aicmd.ShowSuggestion {
		t.Fatalf("expected suggestions for /symbol:N")
	}
	found := false
	for _, s := range m.aicmd.Suggestions {
		if strings.HasPrefix(s.Insert, "/symbol:NVDA") {
			found = true
		}
	}
	if !found {
		t.Fatalf("NVDA not suggested: %+v", m.aicmd.Suggestions)
	}
}

func TestAICmdApplySuggestionReplacesToken(t *testing.T) {
	m := newTestAICmdModel()
	m.aicmd = AICmdState{Active: true, Input: []rune("tell me about /sy"), Cursor: len("tell me about /sy")}
	m.recomputeAISuggestions()
	// Select /symbol suggestion explicitly
	for i, s := range m.aicmd.Suggestions {
		if s.Insert == "/symbol" {
			m.aicmd.SuggestionSel = i
			break
		}
	}
	m.applyAICmdSuggestion()
	got := string(m.aicmd.Input)
	if !strings.HasSuffix(got, "/symbol") {
		t.Fatalf("after apply want suffix /symbol, got %q", got)
	}
}

func TestAICmdSeedPerView(t *testing.T) {
	m := newTestAICmdModel()
	if got := m.aiCmdSeed(); !strings.Contains(got, "/watchlist") {
		t.Fatalf("watchlist seed: %q", got)
	}
	m.mode = viewPortfolio
	if got := m.aiCmdSeed(); !strings.Contains(got, "/portfolio") {
		t.Fatalf("portfolio seed: %q", got)
	}
	m.mode = viewDetail
	m.selected = 0
	if got := m.aiCmdSeed(); !strings.Contains(got, "/symbol:NVDA") {
		t.Fatalf("detail seed: %q", got)
	}
}

func TestAICmdAutoPromptPerView(t *testing.T) {
	m := newTestAICmdModel()

	// Watchlist: selected row provides symbol + timeframe.
	m.selected = 0
	p := m.aiCmdAutoPrompt()
	if !strings.Contains(p, "/symbol:NVDA") {
		t.Fatalf("watchlist auto-prompt missing symbol: %q", p)
	}
	if !strings.Contains(p, "/earning:NVDA") {
		t.Fatalf("watchlist auto-prompt missing earning: %q", p)
	}
	if !strings.Contains(strings.ToLower(p), "verdict") {
		t.Fatalf("auto-prompt should ask for verdict: %q", p)
	}

	// Detail view: same source, but must still return the symbol.
	m.mode = viewDetail
	if got := m.aiCmdAutoPrompt(); !strings.Contains(got, "/symbol:NVDA") {
		t.Fatalf("detail auto-prompt: %q", got)
	}

	// Portfolio view: selection drives the symbol.
	m.mode = viewPortfolio
	m.portfolioSelected = 0
	if got := m.aiCmdAutoPrompt(); !strings.Contains(got, "/symbol:NVDA") {
		t.Fatalf("portfolio auto-prompt: %q", got)
	}

	// Empty context -> empty string.
	m2 := Model{cfg: &config.Config{}, mode: viewWatchlist}
	if got := m2.aiCmdAutoPrompt(); got != "" {
		t.Fatalf("empty-context auto-prompt should be \"\", got %q", got)
	}
}

func TestAICmdSubjectFromMacros(t *testing.T) {
	m := newTestAICmdModel()
	if got := m.aiCmdSubject("/analyze /symbol:NVDA"); got != "NVDA" {
		t.Fatalf("symbol macro: %q", got)
	}
	if got := m.aiCmdSubject("review /portfolio"); got != "portfolio" {
		t.Fatalf("portfolio macro: %q", got)
	}
	if got := m.aiCmdSubject("/summarise /watchlist"); got != "watchlist" {
		t.Fatalf("watchlist macro: %q", got)
	}
	if got := m.aiCmdSubject("/earning:AAPL"); got != "AAPL" {
		t.Fatalf("earning macro: %q", got)
	}
	// Falls back to view context when no macro is present.
	m.mode = viewDetail
	m.selected = 1 // AAPL
	if got := m.aiCmdSubject("hi"); got != "AAPL" {
		t.Fatalf("detail fallback: %q", got)
	}
	m.mode = viewPortfolio
	if got := m.aiCmdSubject("hi"); got != "portfolio" {
		t.Fatalf("portfolio fallback: %q", got)
	}
	m.mode = viewWatchlist
	if got := m.aiCmdSubject("hi"); got != "watchlist" {
		t.Fatalf("watchlist fallback: %q", got)
	}
}

func TestAICmdSubmitHelpShortCircuit(t *testing.T) {
	m := newTestAICmdModel()
	m.aicmd = AICmdState{Active: true, Input: []rune("/help"), Cursor: 5}
	mi, cmd := m.submitAICmd()
	mm := mi.(Model)
	if cmd != nil {
		t.Fatalf("/help should not dispatch a cmd")
	}
	if mm.aicmd.Stage != aiCmdResult {
		t.Fatalf("/help should land in result stage, got %v", mm.aicmd.Stage)
	}
	if !strings.Contains(mm.aicmd.Result, "AI Command") {
		t.Fatalf("/help result missing cheatsheet: %q", mm.aicmd.Result)
	}
}

func TestAICmdSubmitEmptyIsNoop(t *testing.T) {
	m := newTestAICmdModel()
	m.aicmd = AICmdState{Active: true}
	mi, cmd := m.submitAICmd()
	mm := mi.(Model)
	if cmd != nil {
		t.Fatalf("empty submit should produce no cmd")
	}
	if mm.aicmd.Stage == aiCmdLoading {
		t.Fatalf("empty submit must not enter loading stage")
	}
}

func TestAICmdSubmitWithoutLLMYieldsError(t *testing.T) {
	m := newTestAICmdModel()
	m.aicmd = AICmdState{Active: true, Input: []rune("/symbol:NVDA"), Cursor: 12}
	mi, cmd := m.submitAICmd()
	if cmd == nil {
		t.Fatalf("submit should return a cmd")
	}
	mm := mi.(Model)
	if mm.aicmd.Stage != aiCmdLoading {
		t.Fatalf("stage should be loading until cmd resolves, got %v", mm.aicmd.Stage)
	}
	if mm.aicmd.LoadingSubject != "NVDA" {
		t.Fatalf("LoadingSubject: want NVDA, got %q", mm.aicmd.LoadingSubject)
	}
	if mm.aicmd.LastPrompt != "/symbol:NVDA" {
		t.Fatalf("LastPrompt: %q", mm.aicmd.LastPrompt)
	}
	if len(mm.aicmd.History) == 0 || mm.aicmd.History[0] != "/symbol:NVDA" {
		t.Fatalf("history should be populated: %+v", mm.aicmd.History)
	}
	// Without a configured llmClient the runAICmd closure returns an error msg.
	// submitAICmd batches the run cmd with the spinner tick cmd; execute
	// all sub-commands and look for the result.
	var msgs []tea.Msg
	switch v := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range v {
			if c != nil {
				msgs = append(msgs, c())
			}
		}
	default:
		msgs = append(msgs, v)
	}
	var res aiCmdResultMsg
	var found bool
	for _, msg := range msgs {
		if r, ok := msg.(aiCmdResultMsg); ok {
			res = r
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("want aiCmdResultMsg in batch, got %+v", msgs)
	}
	if res.err == nil {
		t.Fatalf("expected error because llmClient is nil")
	}
}

func TestAICmdHandleResultClearsInput(t *testing.T) {
	m := newTestAICmdModel()
	m.aicmd = AICmdState{
		Active: true, Stage: aiCmdLoading,
		Input: []rune("/symbol:NVDA"), Cursor: 12, HistoryIdx: 2,
	}
	mi, _ := m.handleAICmdResult(aiCmdResultMsg{prompt: "/symbol:NVDA", text: "## Analysis"})
	mm := mi.(Model)
	if mm.aicmd.Stage != aiCmdResult {
		t.Fatalf("stage should be result: %v", mm.aicmd.Stage)
	}
	if mm.aicmd.Result != "## Analysis" {
		t.Fatalf("result: %q", mm.aicmd.Result)
	}
	if len(mm.aicmd.Input) != 0 || mm.aicmd.Cursor != 0 {
		t.Fatalf("input should be cleared for follow-ups")
	}
	if mm.aicmd.HistoryIdx != -1 {
		t.Fatalf("HistoryIdx should reset to -1")
	}
}

func TestAICmdHandleResultError(t *testing.T) {
	m := newTestAICmdModel()
	m.aicmd = AICmdState{Active: true, Stage: aiCmdLoading}
	mi, _ := m.handleAICmdResult(aiCmdResultMsg{err: errTest})
	mm := mi.(Model)
	if mm.aicmd.Err == "" {
		t.Fatalf("Err should be populated")
	}
	if mm.aicmd.Result != "" {
		t.Fatalf("Result should be empty on error")
	}
}

func TestAICmdTickAdvancesSpinnerOnlyWhileLoading(t *testing.T) {
	m := newTestAICmdModel()
	// Not loading -> no-op, no follow-up cmd.
	m.aicmd = AICmdState{Active: true, Stage: aiCmdInput, SpinnerFrame: 3}
	mi, cmd := m.handleAICmdTick()
	mm := mi.(Model)
	if cmd != nil {
		t.Fatalf("tick should not reschedule when not loading")
	}
	if mm.aicmd.SpinnerFrame != 3 {
		t.Fatalf("spinner should not advance when not loading")
	}

	// Loading -> advances frame and reschedules.
	m.aicmd = AICmdState{Active: true, Stage: aiCmdLoading, SpinnerFrame: 0}
	mi, cmd = m.handleAICmdTick()
	mm = mi.(Model)
	if cmd == nil {
		t.Fatalf("tick should reschedule while loading")
	}
	if mm.aicmd.SpinnerFrame != 1 {
		t.Fatalf("spinner should advance to 1, got %d", mm.aicmd.SpinnerFrame)
	}

	// Wraps around at the end of the frame list.
	m.aicmd = AICmdState{Active: true, Stage: aiCmdLoading, SpinnerFrame: len(aiSpinnerFrames) - 1}
	mi, _ = m.handleAICmdTick()
	mm = mi.(Model)
	if mm.aicmd.SpinnerFrame != 0 {
		t.Fatalf("spinner should wrap to 0, got %d", mm.aicmd.SpinnerFrame)
	}
}

func TestAICmdSuggestionsWindowOfFive(t *testing.T) {
	m := newTestAICmdModel()
	// `/s` should match /symbol and /summarise (at minimum).
	m.aicmd = AICmdState{Active: true, Input: []rune("/s"), Cursor: 2}
	m.recomputeAISuggestions()
	if !m.aicmd.ShowSuggestion {
		t.Fatalf("expected suggestions for /s")
	}
	seen := map[string]bool{}
	for _, s := range m.aicmd.Suggestions {
		seen[s.Insert] = true
	}
	for _, want := range []string{"/symbol", "/summarise"} {
		if !seen[want] {
			t.Fatalf("%q missing from /s suggestions: %+v", want, m.aicmd.Suggestions)
		}
	}
	// All matches must start with `/s` (prefix filter).
	for _, s := range m.aicmd.Suggestions {
		if !strings.HasPrefix(s.Insert, "/s") {
			t.Fatalf("non-matching suggestion leaked: %q", s.Insert)
		}
	}

	// Dropdown renders at most 5 rows (compact; no decorative borders).
	out := m.renderAICmdSuggestions()
	rows := strings.Count(out, "\n") + 1
	if rows > 5 {
		t.Fatalf("dropdown should render at most 5 items, got %d lines:\n%s", rows, out)
	}
}

func TestAICmdGhostTextForSelectedSuggestion(t *testing.T) {
	m := newTestAICmdModel()
	m.aicmd = AICmdState{Active: true, Input: []rune("/sy"), Cursor: 3}
	m.recomputeAISuggestions()
	// Pick /symbol explicitly
	for i, s := range m.aicmd.Suggestions {
		if s.Insert == "/symbol" {
			m.aicmd.SuggestionSel = i
			break
		}
	}
	if got := m.aiCmdGhostText(); got != "mbol" {
		t.Fatalf("ghost text for /sy -> /symbol should be %q, got %q", "mbol", got)
	}

	// When the current token already matches the suggestion fully, no ghost.
	m.aicmd.Input = []rune("/symbol")
	m.aicmd.Cursor = len(m.aicmd.Input)
	m.recomputeAISuggestions()
	for i, s := range m.aicmd.Suggestions {
		if s.Insert == "/symbol" {
			m.aicmd.SuggestionSel = i
			break
		}
	}
	if got := m.aiCmdGhostText(); got != "" {
		t.Fatalf("ghost text when fully typed should be empty, got %q", got)
	}

	// No dropdown -> empty.
	m.aicmd.ShowSuggestion = false
	if got := m.aiCmdGhostText(); got != "" {
		t.Fatalf("ghost text without dropdown should be empty, got %q", got)
	}
}

func TestValidateFactorVoteJSON_OK(t *testing.T) {
	report := "## Thesis\n" +
		"Bullish skew.\n\n" +
		"## Factor Vote\n\n" +
		"```json\n" +
		"{\n" +
		"  \"market\": {\"score\": 0.5, \"confidence\": 0.8},\n" +
		"  \"fundamental\": {\"score\": 1.2, \"confidence\": 0.7},\n" +
		"  \"technical\": {\"score\": 0.9, \"confidence\": 0.6},\n" +
		"  \"risk\": {\"score\": -0.4, \"confidence\": 0.9},\n" +
		"  \"sentiment_news\": {\"score\": 0.3, \"confidence\": 0.5},\n" +
		"  \"strategy\": {\"score\": 0.7, \"confidence\": 0.8},\n" +
		"  \"weighted_total\": 0.62,\n" +
		"  \"verdict\": \"Bullish\"\n" +
		"}\n" +
		"```\n\n" +
		"## Final Recommendation\n" +
		"Watch pullbacks."
	if err := validateFactorVoteJSON(report); err != nil {
		t.Fatalf("expected valid factor vote JSON, got error: %v", err)
	}
}

func TestValidateFactorVoteJSON_MissingOrInvalid(t *testing.T) {
	missing := "## Thesis\nNo factor vote section."
	if err := validateFactorVoteJSON(missing); err == nil {
		t.Fatalf("expected error for missing Factor Vote section")
	}

	invalid := "## Factor Vote\n\n" +
		"```json\n" +
		"{\"market\": {\"score\": 1}}\n" +
		"```\n"
	if err := validateFactorVoteJSON(invalid); err == nil {
		t.Fatalf("expected error for incomplete Factor Vote JSON")
	}
}

func TestReplaceOrAppendFactorVoteSection(t *testing.T) {
	orig := "## Thesis\n" +
		"Text.\n\n" +
		"## Factor Vote\n\n" +
		"```json\n" +
		"{\"market\": {\"score\": 0, \"confidence\": 0}, \"fundamental\": {\"score\": 0, \"confidence\": 0}, \"technical\": {\"score\": 0, \"confidence\": 0}, \"risk\": {\"score\": 0, \"confidence\": 0}, \"sentiment_news\": {\"score\": 0, \"confidence\": 0}, \"strategy\": {\"score\": 0, \"confidence\": 0}, \"weighted_total\": 0, \"verdict\": \"Neutral\"}\n" +
		"```\n\n" +
		"## Final Recommendation\n" +
		"Old."
	repl := "## Factor Vote\n\n" +
		"```json\n" +
		"{\"market\": {\"score\": 1, \"confidence\": 1}, \"fundamental\": {\"score\": 1, \"confidence\": 1}, \"technical\": {\"score\": 1, \"confidence\": 1}, \"risk\": {\"score\": -1, \"confidence\": 1}, \"sentiment_news\": {\"score\": 0, \"confidence\": 1}, \"strategy\": {\"score\": 0.5, \"confidence\": 0.9}, \"weighted_total\": 0.8, \"verdict\": \"Bullish\"}\n" +
		"```\n"
	got := replaceOrAppendFactorVoteSection(orig, repl)
	if strings.Count(got, "## Factor Vote") != 1 {
		t.Fatalf("expected exactly one Factor Vote section after replace, got: %q", got)
	}
	if !strings.Contains(got, "\"weighted_total\": 0.8") {
		t.Fatalf("replacement content missing: %q", got)
	}

	noSection := "## Thesis\nText only."
	appended := replaceOrAppendFactorVoteSection(noSection, repl)
	if !strings.Contains(appended, "## Factor Vote") {
		t.Fatalf("expected Factor Vote section appended: %q", appended)
	}
}

var errTest = stubErr("boom")

type stubErr string

func (s stubErr) Error() string { return string(s) }

func TestAICmdValidateMacrosRejectsMissingArg(t *testing.T) {
	cases := []struct {
		in      string
		wantSub string
	}{
		{"/symbol", "/symbol"},
		{"/symbol:", "/symbol"},
		{"/earning what now", "/earning"},
		{"hello /range", "/range"},
	}
	for _, c := range cases {
		if got := aiCmdValidateMacros(c.in); got == "" {
			t.Errorf("expected error for %q, got empty", c.in)
		} else if !strings.Contains(got, c.wantSub) {
			t.Errorf("expected error for %q to mention %q, got %q", c.in, c.wantSub, got)
		}
	}
}

func TestAICmdValidateMacrosAcceptsArgs(t *testing.T) {
	cases := []string{
		"/symbol:NVDA",
		"/range:1M",
		"/earning:AAPL vs /earning:MSFT",
		"what happened today",
	}
	for _, c := range cases {
		if got := aiCmdValidateMacros(c); got != "" {
			t.Errorf("unexpected error for %q: %s", c, got)
		}
	}
}

func TestAICmdSubmitSurfacesValidationError(t *testing.T) {
	m := newTestAICmdModel()
	m.aicmd = AICmdState{Active: true, Input: []rune("/symbol"), Cursor: 7}
	updated, cmd := m.submitAICmd()
	if cmd != nil {
		t.Fatalf("expected no command for validation failure, got one")
	}
	um := updated.(Model)
	if um.aicmd.Err == "" {
		t.Fatalf("expected Err to be set")
	}
	if um.aicmd.Stage != aiCmdResult {
		t.Fatalf("expected Stage aiCmdResult, got %v", um.aicmd.Stage)
	}
}

func TestAICmdMissingEarningsDetection(t *testing.T) {
	m := newTestAICmdModel()
	// No Financials loaded on any item yet.
	got := m.aiCmdMissingEarnings("/earning:NVDA and /earning:AAPL")
	if len(got) != 2 {
		t.Fatalf("expected 2 missing symbols, got %v", got)
	}
	// Simulate NVDA Financials already loaded.
	m.items[0].Financials = &yahoo.FinancialData{}
	got = m.aiCmdMissingEarnings("/earning:NVDA and /earning:AAPL")
	if len(got) != 1 || got[0] != "AAPL" {
		t.Fatalf("expected only AAPL missing, got %v", got)
	}
}

func TestAICmdTypingAfterResultSwitchesToInputStage(t *testing.T) {
	m := newTestAICmdModel()
	m.aicmd = AICmdState{Active: true, Stage: aiCmdResult, Result: "previous", ScrollOff: 3}
	updated, _ := m.handleAICmdKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	um := updated.(Model)
	if um.aicmd.Stage != aiCmdInput {
		t.Fatalf("expected Stage to reset to aiCmdInput, got %v", um.aicmd.Stage)
	}
	if um.aicmd.ScrollOff != 0 {
		t.Fatalf("expected ScrollOff to reset to 0, got %d", um.aicmd.ScrollOff)
	}
	if string(um.aicmd.Input) != "h" {
		t.Fatalf("expected Input %q, got %q", "h", string(um.aicmd.Input))
	}
}

func TestAICmdAutoFillContextOnlyInDetail(t *testing.T) {
	m := newTestAICmdModel()
	// Watchlist mode: bare /symbol stays bare.
	if got := m.aiCmdAutoFillContext("/symbol"); got != "/symbol" {
		t.Fatalf("watchlist should not auto-fill, got %q", got)
	}
	// Detail mode on NVDA (selected=0): bare /symbol becomes /symbol:NVDA.
	m.mode = viewDetail
	m.selected = 0
	cases := map[string]string{
		"/symbol":              "/symbol:NVDA",
		"/earning and /symbol": "/earning:NVDA and /symbol:NVDA",
		"/symbol:AAPL":         "/symbol:AAPL", // already filled, untouched
		"/range":               "/range",       // not auto-filled
	}
	for in, want := range cases {
		if got := m.aiCmdAutoFillContext(in); got != want {
			t.Errorf("autofill(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAICmdSubmitAutoFillsDetailSymbol(t *testing.T) {
	m := newTestAICmdModel()
	m.mode = viewDetail
	m.selected = 1 // AAPL
	m.llmClient = nil
	m.aicmd = AICmdState{Active: true, Input: []rune("/analyze /symbol and /earning"), Cursor: 28}
	updated, _ := m.submitAICmd()
	um := updated.(Model)
	got := string(um.aicmd.Input)
	want := "/analyze /symbol:AAPL and /earning:AAPL"
	if got != want {
		t.Fatalf("submit auto-fill input = %q, want %q", got, want)
	}
}

func TestAICmdSuggestionInsertAttachesDetailSymbol(t *testing.T) {
	m := newTestAICmdModel()
	m.mode = viewDetail
	m.selected = 0 // NVDA
	m.aicmd = AICmdState{Active: true, Input: []rune("/sym"), Cursor: 4}
	m.recomputeAISuggestions()
	for i, s := range m.aicmd.Suggestions {
		if s.Insert == "/symbol" {
			m.aicmd.SuggestionSel = i
			break
		}
	}
	m.applyAICmdSuggestion()
	if got := string(m.aicmd.Input); got != "/symbol:NVDA" {
		t.Fatalf("suggestion insert = %q, want %q", got, "/symbol:NVDA")
	}
}

func TestAICmdNewsMacroValidation(t *testing.T) {
	if got := aiCmdValidateMacros("/news"); got == "" || !strings.Contains(got, "/news") {
		t.Fatalf("expected /news validation error, got %q", got)
	}
	if got := aiCmdValidateMacros("/news:NVDA"); got != "" {
		t.Fatalf("unexpected error for /news:NVDA: %s", got)
	}
}

func TestAICmdNewsAutoFillInDetail(t *testing.T) {
	m := newTestAICmdModel()
	m.mode = viewDetail
	m.selected = 0
	got := m.aiCmdAutoFillContext("/news")
	if got != "/news:NVDA" {
		t.Fatalf("autofill(%q) = %q, want %q", "/news", got, "/news:NVDA")
	}
}

func TestAICmdMissingNewsDetection(t *testing.T) {
	m := newTestAICmdModel()
	got := m.aiCmdMissingNews("/news:NVDA and /news:AAPL")
	if len(got) != 2 {
		t.Fatalf("expected 2 missing news symbols, got %v", got)
	}
}

func TestStripAskPrefix(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"/ask why is NVDA up", "why is NVDA up", true},
		{"  /ask   hello  ", "hello", true},
		{"/ASK question", "question", true},
		{"/ask", "", true},
		{"/ask:why", "why", true},
		{"/asking something", "", false},
		{"not an ask", "", false},
	}
	for _, c := range cases {
		got, ok := stripAskPrefix(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("stripAskPrefix(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestContainsMacro(t *testing.T) {
	cases := map[string]bool{
		"why did NVDA drop this week?":       false,
		"compare TSLA and F":                 false,
		"/symbol:NVDA what's happening":      true,
		"summarise /portfolio":               true,
		"check /news:AAPL":                   true,
		"/range:1M for NVDA":                 true,
		"nothing to see here":                false,
		"/ask why is NVDA up":                false,
		"path/to/file is not a macro":        false,
	}
	for in, want := range cases {
		if got := containsMacro(in); got != want {
			t.Errorf("containsMacro(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestBuildAgentSystemPrompt_ModeRouting(t *testing.T) {
	knowledge := "[knowledge-block]"
	investor := "[investor-block]"

	single := buildAgentSystemPrompt(false, knowledge, investor)
	if !strings.Contains(single, llm.SevenRoleInstruction()) {
		t.Fatal("single-agent prompt should include SevenRoleInstruction")
	}
	if !strings.Contains(single, knowledge) {
		t.Fatal("single-agent prompt should include knowledge block")
	}
	if !strings.Contains(single, investor) {
		t.Fatal("single-agent prompt should include investor block")
	}

	multi := buildAgentSystemPrompt(true, knowledge, investor)
	if strings.Contains(multi, llm.SevenRoleInstruction()) {
		t.Fatal("multi-agent prompt should not include SevenRoleInstruction")
	}
	if !strings.Contains(multi, knowledge) {
		t.Fatal("multi-agent prompt should include knowledge block")
	}
	if !strings.Contains(multi, investor) {
		t.Fatal("multi-agent prompt should include investor block")
	}
}

func TestFormatMultiAgentResults_JSONMarshalFailureFallback(t *testing.T) {
	analyses := []agent.RoleAnalysis{
		{Role: agent.RoleMarket, Analysis: "market", Score: math.NaN(), Confidence: 0.8, Verdict: "Bullish"},
		{Role: agent.RolePortfolio, Analysis: "portfolio", Score: 0.4, Confidence: 0.7, Verdict: "Neutral"},
	}

	report := formatMultiAgentResults(analyses, "test question")
	if !strings.Contains(report, "## Factor Vote") {
		t.Fatalf("expected Factor Vote section, got: %q", report)
	}
	if !strings.Contains(report, "```json\n{}\n```") {
		t.Fatalf("expected marshal failure fallback JSON block, got: %q", report)
	}
}
