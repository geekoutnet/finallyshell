package app

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"finallyshell/internal/ai"
	"finallyshell/locales"
)

// A conversation's context is budgeted (AISettings.ContextTokens): the
// history a question sends fits in it, and once the conversation grows
// past three quarters of it, its earlier part is summarized by the model,
// with one more request, the last three questions kept as they are. The
// summary stands for those messages from then on; a notice in the chat
// marks where it ends. A summary that fails leaves the question to go on
// with the history that fits.

// aiCompactKeep is how many of the last questions, with what followed
// each, are never summarized.
const aiCompactKeep = 3

// aiContextChoices are the budgets Options › AI offers, and their tokens.
var (
	aiContextChoices = []string{"32K", "64K", "128K", "200K"}
	aiContextTokens  = []int{32000, 64000, 128000, 200000}
)

// aiContextChoice is the choice for tokens, the default's for 0.
func aiContextChoice(tokens int) string {
	if i := slices.Index(aiContextTokens, ai.ContextBudget(tokens)); i >= 0 {
		return aiContextChoices[i]
	}
	return aiContextChoices[1]
}

// aiContextTokensOf is the setting for choice: 0 for the default.
func aiContextTokensOf(choice string) int {
	i := slices.Index(aiContextChoices, choice)
	if i < 0 || aiContextTokens[i] == ai.DefaultContextTokens {
		return 0
	}
	return aiContextTokens[i]
}

// aiHistoryTokens is about how many tokens the whole history of chat not
// yet summarized takes, with its summary.
func aiHistoryTokens(chat *desktopAIChat) int {
	n := ai.EstimateTokens(chat.Summary)
	for _, m := range chat.Messages {
		if m.Compacted || m.private || m.preview != "" || m.Role == "notice" {
			continue
		}
		n += 4 + ai.EstimateTokens(m.Text) + ai.EstimateTokens(m.output)/4
	}
	return n
}

// aiCompactCut is where chat's history to summarize ends: at the first of
// its last aiCompactKeep questions; 0 when there is nothing before them.
func aiCompactCut(chat *desktopAIChat) int {
	seen := 0
	for i := len(chat.Messages) - 1; i >= 0; i-- {
		m := chat.Messages[i]
		if m.Compacted {
			break
		}
		if m.Role == "user" {
			seen++
			if seen == aiCompactKeep {
				// Anything before it not yet summarized?
				for j := i - 1; j >= 0 && !chat.Messages[j].Compacted; j-- {
					if chat.Messages[j].Role != "notice" {
						return i
					}
				}
				return 0
			}
		}
	}
	return 0
}

// aiCompactTranscript is the part of chat before cut not yet summarized,
// as the summary request reads it.
func aiCompactTranscript(client ai.Client, chat *desktopAIChat, cut int) string {
	var b strings.Builder
	for _, m := range chat.Messages[:cut] {
		if m.Compacted || m.private || m.preview != "" || m.Role == "notice" || m.Text == "" {
			continue
		}
		switch m.Role {
		case "user":
			b.WriteString("User: ")
		case "assistant":
			b.WriteString("Assistant: ")
		case "command":
			b.WriteString("Command: ")
		}
		b.WriteString(ai.Limit(client.SafeText(m.Text), 8<<10))
		if m.Role == "command" {
			switch {
			case m.Exit != nil:
				fmt.Fprintf(&b, " (exit code %d)", *m.Exit)
			case m.Status == aiCmdTimedOut:
				b.WriteString(" (stopped at its time limit)")
			case m.Status == aiCmdRefused:
				b.WriteString(" (refused by the user)")
			}
			if m.output != "" {
				b.WriteString("\n" + ai.TrimOutput(client.SafeText(m.output), 1024))
			}
		}
		b.WriteString("\n\n")
	}
	return b.String()
}

// aiCompact summarizes chat before cut, the earlier summary with it, then
// calls then, summarized or not.
func (a *App) aiCompact(ctx context.Context, client ai.Client, chat *desktopAIChat, cut, budget int, then func()) {
	transcript := aiCompactTranscript(client, chat, cut)
	before := ai.EstimateTokens(chat.Summary) + ai.EstimateTokens(transcript)
	old := slices.Clone(chat.Messages[:cut])
	messages := []ai.Message{{Role: "system", Content: ai.CompactPrompt}}
	if chat.Summary != "" {
		messages = append(messages, ai.Message{Role: "user", Content: "[Summary of the conversation before]\n" + client.SafeText(chat.Summary)})
	}
	// What the request can hold: the transcript's start and end.
	messages = append(messages, ai.Message{Role: "user", Content: "[Conversation to summarize]\n" + ai.TrimOutput(transcript, budget*2)})
	a.aiStream(ctx, client, messages, 16<<10, func(text string, done bool, err error) {
		if !done {
			return
		}
		if summary := strings.TrimSpace(text); err == nil && summary != "" {
			first := len(chat.Messages)
			for i, m := range chat.Messages {
				if slices.Contains(old, m) {
					m.Compacted = true
				} else if first == len(chat.Messages) {
					first = i
				}
			}
			chat.Summary = summary
			notice := &desktopAIMessage{Role: "notice", Text: locales.AiContextCompacted(strconv.Itoa(before), strconv.Itoa(ai.EstimateTokens(summary)))}
			chat.Messages = slices.Insert(chat.Messages, first, notice)
			a.aiSaveChats()
		}
		then()
	})
}
