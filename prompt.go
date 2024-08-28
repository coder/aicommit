package aicommit

import (
	"fmt"
	"io"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/sashabaranov/go-openai"
	"github.com/tiktoken-go/tokenizer"
)

func countTokens(msgs ...openai.ChatCompletionMessage) int {
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

func ellipse(s string, maxTokens int) string {
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

func BuildPrompt(log io.Writer, dir string, maxTokens int) ([]openai.ChatCompletionMessage, error) {
	resp := []openai.ChatCompletionMessage{
		{
			Role: openai.ChatMessageRoleSystem,
			Content: "You are a helpful assistant that generates commit messages for git diffs." +
				"Generate nothing but the commit message. Do not include any other text.",
		},
	}

	repo, err := git.PlainOpen(dir)
	if err != nil {
		return nil, fmt.Errorf("open repo: %w", err)
	}

	// Get the working directory diff
	targetDiff, err := generateDiff(repo, "")
	if err != nil {
		return nil, fmt.Errorf("generate working directory diff: %w", err)
	}

	if targetDiff == "" {
		return nil, fmt.Errorf("no changes detected in the working directory")
	}

	targetDiff = ellipse(targetDiff, 8192)

	targetDiffNumTokens := countTokens(
		openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleUser,
			Content: targetDiff,
		},
	)

	// Get the HEAD reference
	head, err := repo.Head()
	if err != nil {
		// No commits yet
		fmt.Fprintln(log, "no commits yet")
		resp = append(resp, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleUser,
			Content: targetDiff,
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
		commits = append(commits, commit)
	}

	var tokensUsed int
	// Process the commits (you can modify this part based on your needs)
	for _, commit := range commits {
		diff, err := generateDiff(repo, commit.Hash.String())
		if err != nil {
			return nil, fmt.Errorf("generate diff: %w", err)
		}

		const maxDiffLength = 8192
		msgs := []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleUser,
				Content: ellipse(diff, maxDiffLength),
			},
			{
				Role:    openai.ChatMessageRoleAssistant,
				Content: commit.Message,
			},
		}
		tok := countTokens(msgs...)

		if tok+tokensUsed+targetDiffNumTokens > maxTokens {
			break
		}

		tokensUsed += tok
		resp = append(resp, msgs...)
	}

	resp = append(resp, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: targetDiff,
	})

	return resp, nil
}

func generateDiff(repo *git.Repository, refName string) (string, error) {
	if refName == "" {
		// Handle working directory changes
		worktree, err := repo.Worktree()
		if err != nil {
			return "", fmt.Errorf("failed to get worktree: %w", err)
		}

		status, err := worktree.Status()
		if err != nil {
			return "", fmt.Errorf("failed to get worktree status: %w", err)
		}

		var builder strings.Builder
		for path, fileStatus := range status {
			if fileStatus.Staging != git.Unmodified || fileStatus.Worktree != git.Unmodified {
				file, err := worktree.Filesystem.Open(path)
				if err != nil {
					return "", fmt.Errorf("failed to open file %s: %w", path, err)
				}
				defer file.Close()

				content, err := io.ReadAll(file)
				if err != nil {
					return "", fmt.Errorf("failed to read file %s: %w", path, err)
				}

				builder.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", path, path))
				builder.WriteString(fmt.Sprintf("--- a/%s\n", path))
				builder.WriteString(fmt.Sprintf("+++ b/%s\n", path))
				builder.WriteString(string(content))
			}
		}
		return builder.String(), nil
	}

	// Resolve the reference
	ref, err := repo.Reference(plumbing.ReferenceName(refName), true)
	if err != nil {
		return "", fmt.Errorf("failed to resolve reference %q: %w", refName, err)
	}

	// Get the commit object for the reference
	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return "", fmt.Errorf("failed to get commit object: %w", err)
	}

	// Get the parent commit
	parent, err := commit.Parent(0)
	if err != nil {
		return "", fmt.Errorf("failed to get parent commit: %w", err)
	}

	// Get the trees for the current commit and its parent
	currentTree, err := commit.Tree()
	if err != nil {
		return "", fmt.Errorf("failed to get current tree: %w", err)
	}

	parentTree, err := parent.Tree()
	if err != nil {
		return "", fmt.Errorf("failed to get parent tree: %w", err)
	}

	// Calculate the diff
	diff, err := object.DiffTree(parentTree, currentTree)
	if err != nil {
		return "", fmt.Errorf("failed to calculate diff: %w", err)
	}

	// Build the canonical diff string
	var builder strings.Builder
	for _, change := range diff {
		patch, err := change.Patch()
		if err != nil {
			return "", fmt.Errorf("failed to get patch: %w", err)
		}
		builder.WriteString(patch.String())
	}

	return builder.String(), nil
}
