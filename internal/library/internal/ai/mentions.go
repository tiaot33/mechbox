package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const judgeTimeout = 2 * time.Minute

type MentionCandidate struct {
	Name, Description string
	Lines             []string
}

const judgePrompt = "You read numbered items taken from the Markdown files of an AI agent skill. " +
	"Each item names another skill, gives its description, and quotes up to 3 lines where that name appears as a single word. " +
	"For each item decide whether the lines refer to that skill: they tell the agent to use, run, load, read or hand off to it, " +
	"or otherwise mean the skill itself. Answer no when the word only has its ordinary meaning, " +
	"such as \"review this change\" or \"write a spec\" for skills named review or spec. " +
	"Reply with a JSON array of the numbers of the items that refer to the skill, such as [1, 3], or [] when none do. " +
	"Reply with the array only."

func JudgeMentions(ctx context.Context, settings Settings, candidates []MentionCandidate) ([]bool, error) {
	if settings.Model == "" {
		return nil, ErrNotConfigured
	}
	answer, err := call(ctx, judgeTimeout, settings, judgePrompt, judgeRequest(candidates))
	if err != nil {
		return nil, err
	}
	return parseJudgments(answer, len(candidates))
}

func judgeRequest(candidates []MentionCandidate) string {
	var b strings.Builder
	for i, candidate := range candidates {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "Item %d\nSkill name: %s\nSkill description: %s\nLines:\n", i+1, candidate.Name, candidate.Description)
		for _, line := range candidate.Lines {
			b.WriteString("> " + line + "\n")
		}
	}
	return b.String()
}

func parseJudgments(answer string, n int) ([]bool, error) {
	start, end := strings.Index(answer, "["), strings.LastIndex(answer, "]")
	if start < 0 || end < start {
		return nil, fmt.Errorf("%w: the answer is not a json array of item numbers: %q", ErrRejected, answer)
	}
	var numbers []int
	if err := json.Unmarshal([]byte(answer[start:end+1]), &numbers); err != nil {
		return nil, fmt.Errorf("%w: the answer is not a json array of item numbers: %q", ErrRejected, answer)
	}
	judgments := make([]bool, n)
	for _, number := range numbers {
		if number < 1 || number > n {
			return nil, fmt.Errorf("%w: the answer names item %d, but there are %d", ErrRejected, number, n)
		}
		judgments[number-1] = true
	}
	return judgments, nil
}
