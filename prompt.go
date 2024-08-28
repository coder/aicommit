package aicommit

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/sashabaranov/go-openai"
	"github.com/tiktoken-go/tokenizer"
)

func CountTokens(msgs ...openai.ChatCompletionMessage) int {
	enc, err := tokenizer.Get(tokenizer.Cl100kBase)
	if err != nil {
		panic("oh oh")
	}

	var tokens int
	for _, msg := range msgs {
		ts, _, _ := enc.Encode(msg.Content)
		tokens += len(ts)

		for _, call := range msg.ToolCalls {
			ts, _, _ = enc.Encode(call.Function.Arguments)
			tokens += len(ts)
		}
	}
	return tokens
}

// Ellipse returns a string that is truncated to the maximum number of tokens.
func Ellipse(s string, maxTokens int) string {
	enc, err := tokenizer.Get(tokenizer.Cl100kBase)
	if err != nil {
		panic("failed to get tokenizer")
	}

	tokens, _, _ := enc.Encode(s)
	if len(tokens) <= maxTokens {
		return s
	}

	// Decode the truncated tokens back to a string
	truncated, _ := enc.Decode(tokens[:maxTokens])
	return truncated + "..."
}

func reverseSlice[S ~[]E, E any](s S) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func BuildPrompt(log io.Writer, dir string,
	commitHash string,
	maxTokens int,
) ([]openai.ChatCompletionMessage, error) {
	resp := []openai.ChatCompletionMessage{
		{
			Role: openai.ChatMessageRoleSystem,
			Content: "You are a helpful assistant that generates commit messages for git diffs." +
				"Generate nothing but the commit message. Do not include any other text." +
				"Commit messages should have a maximum column width of 100 characters." +
				"Extended descriptions go on a new line. Follow the style of the existing commit messages.",
		},
	}

	repo, err := git.PlainOpen(dir)
	if err != nil {
		return nil, fmt.Errorf("open repo: %w", err)
	}

	var buf bytes.Buffer
	// Get the working directory diff
	if err := generateDiff(&buf, dir, commitHash); err != nil {
		return nil, fmt.Errorf("generate working directory diff: %w", err)
	}

	if buf.Len() == 0 {
		if commitHash == "" {
			return nil, fmt.Errorf("no staged changes, nothing to commit")
		}
		return nil, fmt.Errorf("no changes detected for %q", commitHash)
	}

	const minTokens = 5000
	if maxTokens < minTokens {
		return nil, fmt.Errorf("maxTokens must be greater than %d", minTokens)
	}

	targetDiffString := Ellipse(buf.String(), maxTokens/4)

	targetDiffNumTokens := CountTokens(
		openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleUser,
			Content: targetDiffString,
		},
	)

	// Get the HEAD reference
	head, err := repo.Head()
	if err != nil {
		// No commits yet
		fmt.Fprintln(log, "no commits yet")
		resp = append(resp, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleUser,
			Content: targetDiffString,
		})
		return resp, nil
	}

	// Create a log options struct
	logOptions := &git.LogOptions{
		From:  head.Hash(),
		Order: git.LogOrderCommitterTime,
	}

	// Get the commit iterator
	commitIter, err := repo.Log(logOptions)
	if err != nil {
		return nil, fmt.Errorf("get commit iterator: %w", err)
	}
	defer commitIter.Close()

	// Collect the last 100 commits
	var commits []*object.Commit
	for i := 0; i < 100; i++ {
		commit, err := commitIter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("iterate commits: %w", err)
		}
		// Ignore if commit equals ref, because we are trying to recalculate
		// that particular commit's message.
		if commit.Hash.String() == commitHash {
			continue
		}
		commits = append(commits, commit)

	}

	// We want to reverse the commits so that the most recent commit is the
	// last or "most recent" in the chat.
	reverseSlice(commits)

	var commitMsgs []string
	for _, commit := range commits {
		commitMsgs = append(commitMsgs, Ellipse(commit.Message, 1000))
	}
	// We provide the commit messages in case the actual commit diffs are cut
	// off due to token limits.
	resp = append(resp, openai.ChatCompletionMessage{
		Role: openai.ChatMessageRoleSystem,
		Content: "Here are recent commit messages:\n" +
			strings.Join(commitMsgs, "\n-------\n"),
	})

	sysToken := CountTokens(resp...)

	var tokensUsed int
	// Process the commits (you can modify this part based on your needs)
	for _, commit := range commits {
		buf.Reset()
		if err := generateDiff(&buf, dir, commit.Hash.String()); err != nil {
			return nil, fmt.Errorf("generate diff: %w", err)
		}

		maxDiffLength := maxTokens / 10
		msgs := []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleUser,
				Content: Ellipse(buf.String(), maxDiffLength),
			},
			{
				Role:    openai.ChatMessageRoleAssistant,
				Content: commit.Message,
			},
		}
		tok := CountTokens(msgs...)

		if tok+tokensUsed+targetDiffNumTokens+sysToken > maxTokens {
			break
		}

		tokensUsed += tok
		resp = append(resp, msgs...)
	}

	resp = append(resp, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: targetDiffString,
	})

	return resp, nil
}

// generateDiff uses the git CLI to generate a diff for the given reference.
// If refName is empty, it will generate a diff of staged changes for the working directory.
func generateDiff(w io.Writer, dir string, refName string) error {
	// We don't use go-git as reaching parity with git is a pain.
	var cmd *exec.Cmd

	if refName == "" {
		// Generate diff for staged changes in the working directory
		cmd = exec.Command("git", "-C", dir, "diff", "--cached")
	} else {
		// Generate diff for the specified reference
		cmd = exec.Command("git", "-C", dir, "diff", refName+"^!", "--")
	}

	cmd.Stdout = w
	cmd.Stderr = io.Discard

	return cmd.Run()
}
