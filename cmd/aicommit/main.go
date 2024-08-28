package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/coder/aicommit"
	"github.com/coder/pretty"
	"github.com/coder/serpent"
	"github.com/muesli/termenv"
	"github.com/sashabaranov/go-openai"
)

var colorProfile = termenv.ColorProfile()

func errorf(format string, args ...any) {
	c := pretty.FgColor(colorProfile.Color("#ff0000"))
	pretty.Fprintf(os.Stderr, c, "err: "+format, args...)
}

var isDebug = os.Getenv("AICOMMIT_DEBUG") != ""

func debugf(format string, args ...any) {
	if !isDebug {
		return
	}
	// Gray
	c := pretty.FgColor(colorProfile.Color("#808080"))
	pretty.Fprintf(os.Stderr, c, format, args...)
}

func hasUnstagedChanges() (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return false, err
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if len(line) >= 2 {
			// Check if the second character is not space
			// This covers cases like 'MM', ' M', and other unstaged scenarios
			if line[1] != ' ' {
				return true, nil
			}
		}
	}

	return false, nil
}

func getLastCommitHash() (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func run(inv *serpent.Invocation, opts runOptions) error {
	workdir, err := os.Getwd()
	if err != nil {
		return err
	}

	ref := ""
	if opts.amend {
		lastCommitHash, err := getLastCommitHash()
		if err != nil {
			return err
		}
		ref = lastCommitHash
	}

	msgs, err := aicommit.BuildPrompt(inv.Stdout, workdir, ref, 64000)
	if err != nil {
		return err
	}

	ctx := inv.Context()

	stream, err := opts.client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model:    openai.GPT4o,
		Stream:   true,
		Messages: msgs,
	})
	if err != nil {
		return err
	}
	defer stream.Close()

	msg := &strings.Builder{}

	// Sky blue color
	color := pretty.FgColor(colorProfile.Color("#2FA8FF"))

	for {
		resp, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		c := resp.Choices[0].Delta.Content
		msg.WriteString(c)
		pretty.Fprintf(inv.Stdout, color, "%s", c)
	}

	if opts.dryRun {
		return nil
	}

	inv.Stdout.Write([]byte("\n"))

	unstaged, err := hasUnstagedChanges()
	if err != nil {
		return err
	}

	if unstaged && !opts.amend {
		return errors.New("unstaged changes detected, please stage changes before committing")
	}

	inv.Stdout.Write([]byte("\n"))
	cmd := exec.Command("git", "commit", "-m", msg.String())
	if opts.amend {
		cmd.Args = append(cmd.Args, "--amend")
	}
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

type runOptions struct {
	client *openai.Client
	dryRun bool
	amend  bool
}

func main() {
	var (
		opts      runOptions
		openAIKey string
	)
	cmd := &serpent.Command{
		Use:   "aicommit",
		Short: "aicommit is a tool for generating commit messages",
		Handler: func(inv *serpent.Invocation) error {
			client := openai.NewClient(openAIKey)
			opts.client = client
			return run(inv, opts)
		},
		Options: []serpent.Option{
			{
				Name:        "openai-key",
				Description: "The OpenAI API key to use.",
				Env:         "OPENAI_API_KEY",
				Value:       serpent.StringOf(&openAIKey),
				Required:    true,
			},
			{
				Name:        "dry-run",
				Flag:        "dry",
				Description: "Dry run the command.",
				Value:       serpent.BoolOf(&opts.dryRun),
			},
			{
				Name:          "amend",
				Flag:          "amend",
				FlagShorthand: "a",
				Description:   "Amend the last commit.",
				Value:         serpent.BoolOf(&opts.amend),
			},
		},
	}

	err := cmd.Invoke().WithOS().Run()
	if err != nil {
		var unknownCmdErr *serpent.UnknownSubcommandError
		if errors.As(err, &unknownCmdErr) {
			// Unknown command is printed by the help function for some reason.
			os.Exit(1)
		}
		var runCommandErr *serpent.RunCommandError
		if errors.As(err, &runCommandErr) {
			errorf("%s\n", runCommandErr.Err)
			os.Exit(1)
		}

		errorf("%s\n", err)
		os.Exit(1)
	}
}
