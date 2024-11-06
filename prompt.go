package aicommit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func findGitRoot(dir string) (string, error) {
	dir = filepath.Clean(dir)
	for {
		_, err := os.Stat(filepath.Join(dir, ".git"))
		if err == nil {
			return dir, nil
		}
		if os.IsNotExist(err) {
			if dir == "/" {
				return "", fmt.Errorf("not a git repository")
			}
			dir = filepath.Dir(dir)
		} else {
			return "", fmt.Errorf("failed to stat .git: %w", err)
		}
	}
}

const styleGuideFilename = "COMMITS.md"
const defaultUserStyleGuide = `
1. Limit the subject line to 50 characters.
2. Use the imperative mood in the subject line.
3. Capitalize the subject line such as "Fix Issue 886" and don't end it with a period.
4. The subject line should summarize the main change concisely.
5. Only include a body if absolutely necessary for complex changes.
6. If a body is needed, separate it from the subject with a blank line.
7. Wrap the body at 72 characters.
8. In the body, explain the why, not the what (the diff shows the what).
9. Use bullet points in the body only for truly distinct changes.
10. Be extremely concise. Assume the reader can understand the diff.
11. Never repeat information between the subject and body.
12. Do not repeat commit messages from previous commits.
13. Prioritize clarity and brevity over completeness.
14. Adhere to the repository's commit style if it exists.
`

// findRepoStyleGuide searches for "COMMITS.md" in the repository root of dir
// and returns its contents.
func findRepoStyleGuide(dir string) (string, error) {
	root, err := findGitRoot(dir)
	if err != nil {
		return "", fmt.Errorf("find git root: %w", err)
	}

	styleGuide, err := os.ReadFile(filepath.Join(root, styleGuideFilename))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read style guide: %w", err)
	}
	return strings.TrimSpace(string(styleGuide)), nil
}

func findUserStyleGuide() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find user home dir: %w", err)
	}
	styleGuide, err := os.ReadFile(filepath.Join(home, styleGuideFilename))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read user style guide: %w", err)
	}
	return strings.TrimSpace(string(styleGuide)), nil
}

func BuildPrompt(
	log io.Writer,
	dir string,
	commitHash string,
	amend bool,
	maxTokens int,
) ([]openai.ChatCompletionMessage, error) {
	resp := []openai.ChatCompletionMessage{
		{
			Role: openai.ChatMessageRoleSystem,
			Content: strings.Join([]string{
				"You are a tool called `aicommit` that generates high quality commit messages for git diffs.",
				"Generate only the commit message, without any additional text.",
			}, "\n"),
		},
	}

	gitRoot, err := findGitRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("find git root: %w", err)
	}

	repo, err := git.PlainOpen(gitRoot)
	if err != nil {
		return nil, fmt.Errorf("open repo %q: %w", dir, err)
	}

	var buf bytes.Buffer
	// Get the working directory diff
	if err := generateDiff(&buf, dir, commitHash, amend); err != nil {
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

	targetDiffString := buf.String()

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

	// Collect the last N commits
	var commits []*object.Commit
	for i := 0; i < 300; i++ {
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
		Content: "Here are recent commit messages in the same repository:\n" +
			mustJSON(commitMsgs),
	},
	)

	// Add style guide after commit messages so it takes priority.
	repoStyleGuide, err := findRepoStyleGuide(dir)
	if err != nil {
		return nil, fmt.Errorf("find style guide: %w", err)
	}
	if repoStyleGuide != "" {
		resp = append(resp, openai.ChatCompletionMessage{
			Role: openai.ChatMessageRoleSystem,
			Content: "This repository has a style guide. Follow it even when " +
				"it diverges from the norm.\n" + repoStyleGuide,
		})
	} else {
		userStyleGuide, err := findUserStyleGuide()
		if err != nil {
			return nil, fmt.Errorf("find user style guide: %w", err)
		}
		if userStyleGuide == "" {
			userStyleGuide = defaultUserStyleGuide
		}
		resp = append(resp, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: "This user has a preferred style guide:\n" + userStyleGuide,
		})
	}

	resp = append(resp, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: Ellipse(targetDiffString, maxTokens-CountTokens(resp...)),
	})

	return resp, nil
}

// generateDiff uses the git CLI to generate a diff for the given reference.
// If refName is empty, it will generate a diff of staged changes for the working directory.
func generateDiff(w io.Writer, dir string, refName string, amend bool) error {
	// Use the git CLI instead of go-git for more accurate and complete diff generation
	cmd := exec.Command("git", "-C", dir, "diff")

	if refName == "" {
		// Case 1: No specific commit reference provided
		// Generate diff for staged changes in the working directory
		cmd.Args = append(cmd.Args, "--cached")
	} else {
		// Case 2: A specific commit reference is provided
		if amend {
			// Case 2a: Amending the specified commit
			// Show diff of the commit being amended plus any staged changes
			cmd.Args = append(cmd.Args, "--cached", refName+"^")
		} else {
			// Case 2b: Show changes introduced by the specific commit
			cmd.Args = append(cmd.Args, refName+"^", refName)
		}
	}

	var errBuf bytes.Buffer
	cmd.Stdout = w
	cmd.Stderr = &errBuf

	// Run the git command and return any execution errors
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("running %s %s: %w\n%s",
			cmd.Args[0], strings.Join(cmd.Args[1:], " "), err, errBuf.String())
	}

	return nil
}
