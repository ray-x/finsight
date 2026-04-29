package llm

import (
	"strings"
	"testing"
)

// TestSevenRoleInstructionReturnsNonEmpty validates the instruction is populated.
func TestSevenRoleInstructionReturnsNonEmpty(t *testing.T) {
	instr := SevenRoleInstruction()
	if len(instr) == 0 {
		t.Fatal("SevenRoleInstruction() returned empty string")
	}
}

// TestSevenRoleInstructionIncludesAllRoles validates all 7 roles are documented.
func TestSevenRoleInstructionIncludesAllRoles(t *testing.T) {
	instr := SevenRoleInstruction()
	roles := []string{
		"Market Analyst",
		"Fundamental Analyst",
		"Technical Analyst",
		"Risk Analyst",
		"Sentiment/News Analyst",
		"Strategy Analyst",
		"Portfolio Manager",
	}
	for _, role := range roles {
		if !strings.Contains(instr, role) {
			t.Fatalf("missing role %q in instruction", role)
		}
	}
}

// TestSevenRoleInstructionIncludesAllSections validates all 12 report sections are specified.
func TestSevenRoleInstructionIncludesAllSections(t *testing.T) {
	instr := SevenRoleInstruction()
	sections := []string{
		"## Market",
		"## Fundamentals",
		"## Technicals",
		"## Risk",
		"## Sentiment & News",
		"## Thesis",
		"## Catalysts",
		"## Risks",
		"## Valuation",
		"## What Changed Since Last Report",
		"## Factor Vote",
		"## Final Recommendation",
	}
	for _, section := range sections {
		if !strings.Contains(instr, section) {
			t.Fatalf("missing section %q in instruction", section)
		}
	}
}

// TestSevenRoleInstructionFactorVoteSchemaIncludesStrategy validates the Factor Vote JSON includes strategy score.
func TestSevenRoleInstructionFactorVoteSchemaIncludesStrategy(t *testing.T) {
	instr := SevenRoleInstruction()

	// Validate all required JSON keys are mentioned in the instruction
	requiredKeys := []string{
		"\"market\"",
		"\"fundamental\"",
		"\"technical\"",
		"\"risk\"",
		"\"sentiment_news\"",
		"\"strategy\"",
		"\"weighted_total\"",
		"\"verdict\"",
	}
	for _, key := range requiredKeys {
		if !strings.Contains(instr, key) {
			t.Fatalf("Factor Vote JSON schema missing key: %s", key)
		}
	}

	// Validate strategy field structure is documented
	if !strings.Contains(instr, "\"strategy\": {\"score\"") && !strings.Contains(instr, "\"strategy\":{\"score\"") {
		t.Fatal("strategy field structure not properly documented in instruction")
	}
}

// TestSevenRoleInstructionWeightingIncludesStrategy validates strategy weighting in synthesis rules.
func TestSevenRoleInstructionWeightingIncludesStrategy(t *testing.T) {
	instr := SevenRoleInstruction()
	if !strings.Contains(instr, "Strategy fit 15%") && !strings.Contains(instr, "Strategy 15%") {
		t.Fatal("instruction does not mention strategy weighting in Portfolio Manager section")
	}
}

// TestKnowledgeMemoryInstructionReturnsNonEmpty validates memory workflow is defined.
func TestKnowledgeMemoryInstructionReturnsNonEmpty(t *testing.T) {
	instr := KnowledgeMemoryInstruction()
	if len(instr) == 0 {
		t.Fatal("KnowledgeMemoryInstruction() returned empty string")
	}
}

// TestKnowledgeMemoryInstructionIncludesSearchAndStore validates key workflow steps.
func TestKnowledgeMemoryInstructionIncludesSearchAndStore(t *testing.T) {
	instr := KnowledgeMemoryInstruction()
	expectedSteps := []string{
		"search_llm_reports",
		"store_llm_report",
		"Factor Vote JSON",
	}
	for _, step := range expectedSteps {
		if !strings.Contains(instr, step) {
			t.Fatalf("knowledge memory instruction missing key step %q", step)
		}
	}
}
